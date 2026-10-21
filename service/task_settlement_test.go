package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func pendingSettlementFailure(t *testing.T, table string) func() {
	t.Helper()
	var enabled atomic.Bool
	enabled.Store(true)
	name := "test:pending:" + t.Name()
	callback := func(tx *gorm.DB) {
		if !enabled.Load() || tx.Statement.Table != table {
			return
		}
		if table == "tasks" {
			clearing := false
			for _, field := range tx.Statement.Selects {
				clearing = clearing || field == "settlement_pending"
			}
			if !clearing {
				return
			}
		}
		tx.AddError(errors.New("forced pending settlement transaction failure"))
	}
	if table == "balance_ledger" {
		require.NoError(t, model.DB.Callback().Create().After("gorm:create").Register(name, callback))
		t.Cleanup(func() { require.NoError(t, model.DB.Callback().Create().Remove(name)) })
	} else {
		require.NoError(t, model.DB.Callback().Update().After("gorm:update").Register(name, callback))
		t.Cleanup(func() { require.NoError(t, model.DB.Callback().Update().Remove(name)) })
	}
	return func() { enabled.Store(false) }
}

func runDurableTaskSettlementCases(t *testing.T) {
	t.Run("batch_enabled_submission_reservation", func(t *testing.T) { runTaskBatchReservationCases(t, nil) })
	t.Run("submission_saturation_audit_survives_recovery", func(t *testing.T) {
		oldExport := common.DataExportEnabled
		common.DataExportEnabled = false
		t.Cleanup(func() { common.DataExportEnabled = oldExport })
		user, token, channel := seedReceiptActors(t, 0, 0)
		require.NoError(t, model.DB.Model(user).Updates(map[string]any{"quota_paid": common.MaxQuota + 1000, "quota": common.MaxQuota + 1000}).Error)
		require.NoError(t, model.DB.Model(token).Update("remain_quota", common.MaxQuota+2000).Error)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
		info := &relaycommon.RelayInfo{
			TaskRelayInfo: &relaycommon.TaskRelayInfo{}, ForcePreConsume: true,
			ChannelMeta: &relaycommon.ChannelMeta{ChannelId: channel.Id},
			UserId:      user.Id, TokenId: token.Id, TokenKey: token.Key,
			OriginModelName: "test-model", RequestId: fmt.Sprintf("submit-clamp-%d", user.Id),
			UsingGroup: "default", UserGroup: "default",
			UserSetting: dto.UserSetting{BillingPreference: "wallet_only"},
		}
		require.Nil(t, PreConsumeBilling(c, 100, info))
		actualQuota, clamp := common.QuotaFromFloatChecked(math.Inf(1))
		require.NotNil(t, clamp)
		info.QuotaClamp = clamp
		task := makeTask(user.Id, channel.Id, 100, token.Id, BillingSourceWallet, 0)
		task.TaskID = info.RequestId
		task.PrivateData.WalletDebit = WalletSessionDebitShares(info)
		PrepareSubmittedTaskBilling(info, task, actualQuota)
		require.NoError(t, model.DB.Create(task).Error)
		var reloaded model.Task
		require.NoError(t, model.DB.First(&reloaded, task.ID).Error)
		require.NoError(t, RecoverTaskSettlement(t.Context(), &reloaded))
		var logs []model.Log
		require.NoError(t, model.LOG_DB.Where("user_id = ?", user.Id).Find(&logs).Error)
		require.Len(t, logs, 1)
		var other map[string]any
		require.NoError(t, common.UnmarshalJsonStr(logs[0].Other, &other))
		admin, ok := other["admin_info"].(map[string]any)
		require.True(t, ok, "recovered submit log must retain the saturation audit")
		marker, ok := admin["quota_saturation"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "+Inf", marker["original"])
		assert.Equal(t, float64(common.MaxQuota), marker["clamped"])
		assert.Equal(t, string(common.QuotaClampOverflow), marker["kind"])
	})
	for _, table := range []string{"tokens", "tasks", "channels", "balance_ledger"} {
		t.Run("terminal_pending_reload_after_"+table+"_failure", func(t *testing.T) {
			user, token, channel := seedReceiptActors(t, 100, 40)
			task := seedReceiptTask(t, user, token, channel, 100)
			task.Status = model.TaskStatusInProgress
			require.NoError(t, model.DB.Model(task).Update("status", task.Status).Error)
			restore := pendingSettlementFailure(t, table)
			err := ApplyTaskPollResult(t.Context(), &mockAdaptor{adjustReturn: 300}, task, &relaycommon.TaskInfo{Status: "SUCCESS"}, []byte(`{"done":true}`))
			require.Error(t, err)
			var persisted model.Task
			require.NoError(t, model.DB.First(&persisted, task.ID).Error)
			assert.Equal(t, model.TaskStatus(model.TaskStatusSuccess), persisted.Status)
			require.True(t, persisted.SettlementPending)
			require.NotNil(t, persisted.PrivateData.PendingSettlement)
			assert.Equal(t, 300, persisted.PrivateData.PendingSettlement.Quota)
			assert.Equal(t, 100, persisted.Quota)
			assert.True(t, model.HasUnfinishedSyncTasks(), "terminal financial work keeps scheduler awake")
			assertUserBalances(t, user.Id, 0, 1000, 40)
			assert.Equal(t, 1900, getTokenRemainQuota(t, token.Id))
			used, requests := getUserUsageAccounting(t, user.Id)
			assert.Equal(t, 100, used)
			assert.Equal(t, 1, requests)
			assert.EqualValues(t, 100, getChannelUsedQuota(t, channel.Id))
			assert.Zero(t, countReceiptLogs(t, user.Id))
			restore()
			// Discard all observer state. Recovery has no adaptor, no provider
			// request and no old BillingSession, just the durable record.
			oldFactory := GetTaskAdaptorFunc
			GetTaskAdaptorFunc = nil
			RunTaskPollingOnce(context.Background(), nil)
			GetTaskAdaptorFunc = oldFactory
			require.NoError(t, model.DB.First(&persisted, task.ID).Error)
			assert.False(t, persisted.SettlementPending)
			assert.Nil(t, persisted.PrivateData.PendingSettlement)
			assert.Equal(t, 300, persisted.Quota)
			assertUserBalances(t, user.Id, 0, 840, 0)
			assert.Equal(t, 1700, getTokenRemainQuota(t, token.Id))
			assert.Equal(t, 300, getTokenUsedQuota(t, token.Id))
			used, requests = getUserUsageAccounting(t, user.Id)
			assert.Equal(t, 300, used)
			assert.Equal(t, 1, requests)
			assert.EqualValues(t, 300, getChannelUsedQuota(t, channel.Id))
			require.NoError(t, RecoverTaskSettlement(t.Context(), task), "stale observer is an idempotent replay")
			assert.EqualValues(t, 1, countReceiptLogs(t, user.Id))
			var operations int64
			require.NoError(t, model.DB.Model(&model.BalanceLedgerOperation{}).Where("idempotency_key = ?", fmt.Sprintf("task:terminal:%d", task.ID)).Count(&operations).Error)
			assert.EqualValues(t, 1, operations)
		})
	}

	t.Run("concurrent_poll_query_single_terminal_refund", func(t *testing.T) {
		user, token, channel := seedReceiptActors(t, 100, 40)
		task := seedReceiptTask(t, user, token, channel, 300)
		task.Status = model.TaskStatusInProgress
		require.NoError(t, model.DB.Model(task).Update("status", task.Status).Error)
		observers := []model.Task{*task, *task}
		start := make(chan struct{})
		errs := make([]error, 2)
		var wg sync.WaitGroup
		for i := range observers {
			wg.Go(func() {
				<-start
				errs[i] = ApplyTaskPollResult(context.Background(), &mockAdaptor{}, &observers[i], relaycommon.FailTaskInfo("provider rejected job"), []byte(`{"done":true,"failed":true}`))
			})
		}
		close(start)
		wg.Wait()
		for _, err := range errs {
			require.NoError(t, err)
		}
		assertUserBalances(t, user.Id, 100, 1000, 40)
		assert.Equal(t, 2000, getTokenRemainQuota(t, token.Id))
		assert.Zero(t, getTokenUsedQuota(t, token.Id))
		used, requests := getUserUsageAccounting(t, user.Id)
		assert.Zero(t, used)
		assert.Equal(t, 1, requests)
		assert.Zero(t, getChannelUsedQuota(t, channel.Id))
		assert.EqualValues(t, 1, countReceiptLogs(t, user.Id))
		var persisted model.Task
		require.NoError(t, model.DB.First(&persisted, task.ID).Error)
		assert.False(t, persisted.SettlementPending)
		assert.Zero(t, persisted.Quota)
		assert.Empty(t, persisted.PrivateData.WalletDebit)
	})

	t.Run("pending_submit_shortage_then_failure_refunds_held_amount", func(t *testing.T) {
		user, token, channel := seedReceiptActors(t, 100, 40)
		task := seedReceiptTask(t, user, token, channel, 300)
		task.Status, task.Progress = model.TaskStatusInProgress, "50%"
		task.SettlementPending = true
		task.PrivateData.PendingSettlement = &model.TaskSettlement{Phase: "submit", Quota: 1800}
		require.NoError(t, model.DB.Save(task).Error)
		seedChargedAccounting(t, user.Id, channel.Id, 0, 0, 0)
		require.Error(t, RecoverTaskSettlement(t.Context(), task))
		require.NoError(t, ApplyTaskPollResult(t.Context(), &mockAdaptor{}, task, &relaycommon.TaskInfo{Status: "IN_PROGRESS", Progress: "70%", PluginState: []byte(`{"next":"poll-step-2"}`)}, []byte(`{"progress":70}`)))
		var intermediate model.Task
		require.NoError(t, model.DB.First(&intermediate, task.ID).Error)
		assert.Equal(t, "70%", intermediate.Progress)
		assert.JSONEq(t, `{"next":"poll-step-2"}`, string(intermediate.PrivateData.PluginState))
		require.NotNil(t, intermediate.PrivateData.PendingSettlement)
		assert.Equal(t, 1800, intermediate.PrivateData.PendingSettlement.Quota, "progress preserves the frozen quote")
		adaptor := &pendingFailureAdaptor{}
		require.NoError(t, updateVideoSingleTask(t.Context(), adaptor, channel, task.TaskID, map[string]*model.Task{task.TaskID: task}))
		assert.Equal(t, 1, adaptor.calls, "pending top-up must not prevent upstream failure discovery")
		assertUserBalances(t, user.Id, 100, 1000, 40)
		assert.Equal(t, 2000, getTokenRemainQuota(t, token.Id))
		assert.Zero(t, getTokenUsedQuota(t, token.Id))
		used, requests := getUserUsageAccounting(t, user.Id)
		assert.Zero(t, used)
		assert.Equal(t, 1, requests)
		assert.False(t, task.SettlementPending)
		assert.Zero(t, task.Quota)
		assert.Equal(t, model.TaskStatus(model.TaskStatusFailure), task.Status)
		require.NoError(t, RecoverTaskSettlement(t.Context(), task))
		assert.EqualValues(t, 1, countReceiptLogs(t, user.Id))
	})

	t.Run("submit_partial_refund_and_terminal_refund_have_distinct_receipts", func(t *testing.T) {
		user, token, channel := seedReceiptActors(t, 100, 40)
		task := seedReceiptTask(t, user, token, channel, 300)
		task.Status = model.TaskStatusInProgress
		task.SettlementPending = true
		task.PrivateData.PendingSettlement = &model.TaskSettlement{Phase: "submit", Quota: 100}
		require.NoError(t, model.DB.Save(task).Error)
		seedChargedAccounting(t, user.Id, channel.Id, 0, 0, 0)
		require.NoError(t, RecoverTaskSettlement(t.Context(), task))
		assert.Equal(t, 100, task.Quota)
		require.NoError(t, ApplyTaskPollResult(t.Context(), &mockAdaptor{}, task, relaycommon.FailTaskInfo("failed"), []byte(`{}`)))
		assertUserBalances(t, user.Id, 100, 1000, 40)
		assert.Equal(t, 2000, getTokenRemainQuota(t, token.Id))
		assert.Zero(t, getTokenUsedQuota(t, token.Id))
		used, requests := getUserUsageAccounting(t, user.Id)
		assert.Zero(t, used)
		assert.Equal(t, 1, requests)
		assert.Zero(t, getChannelUsedQuota(t, channel.Id))
	})

	t.Run("stale_nonterminal_observer_cannot_resurrect_submit_intent", func(t *testing.T) {
		user, token, channel := seedReceiptActors(t, 100, 40)
		task := seedReceiptTask(t, user, token, channel, 100)
		task.Status = model.TaskStatusInProgress
		task.SettlementPending = true
		task.PrivateData.PendingSettlement = &model.TaskSettlement{Phase: "submit", Quota: 300}
		require.NoError(t, model.DB.Save(task).Error)
		seedChargedAccounting(t, user.Id, channel.Id, 0, 0, 0)
		stale := *task
		require.NoError(t, RecoverTaskSettlement(t.Context(), task))
		stale.Progress = "70%"
		won, err := stale.UpdateWithStatus(model.TaskStatusInProgress)
		require.NoError(t, err)
		assert.True(t, won)
		assert.False(t, stale.SettlementPending)
		assert.Nil(t, stale.PrivateData.PendingSettlement)
		assert.Equal(t, 300, stale.Quota)
		require.NoError(t, RecoverTaskSettlement(t.Context(), &stale))
		assertUserBalances(t, user.Id, 0, 840, 0)
		assert.Equal(t, 1700, getTokenRemainQuota(t, token.Id))
		used, requests := getUserUsageAccounting(t, user.Id)
		assert.Equal(t, 300, used)
		assert.Equal(t, 1, requests)
		assert.EqualValues(t, 1, countReceiptLogs(t, user.Id))
	})

	t.Run("subscription_pending_token_failure_recovers_once", func(t *testing.T) {
		user, token, channel := seedReceiptActors(t, 0, 0)
		sub := model.UserSubscription{UserId: user.Id, AmountTotal: 5000, AmountUsed: 100, QuotaPeriod: 1, Status: "active"}
		require.NoError(t, model.DB.Create(&sub).Error)
		require.NoError(t, model.DB.Model(token).Updates(map[string]any{"remain_quota": 1900, "used_quota": 100}).Error)
		task := makeTask(user.Id, channel.Id, 100, token.Id, BillingSourceSubscription, sub.Id)
		task.TaskID = fmt.Sprintf("pending-sub-%d", user.Id)
		require.NoError(t, model.DB.Create(task).Error)
		seedChargedAccounting(t, user.Id, channel.Id, token.Id, 100, 1)
		restore := pendingSettlementFailure(t, "tokens")
		require.Error(t, ApplyTaskPollResult(t.Context(), &mockAdaptor{adjustReturn: 300}, task, &relaycommon.TaskInfo{Status: "SUCCESS"}, []byte(`{}`)))
		require.NoError(t, model.DB.First(&sub, sub.Id).Error)
		assert.EqualValues(t, 100, sub.AmountUsed)
		restore()
		var reloaded model.Task
		require.NoError(t, model.DB.First(&reloaded, task.ID).Error)
		require.NoError(t, RecoverTaskSettlement(t.Context(), &reloaded))
		require.NoError(t, RecoverTaskSettlement(t.Context(), task))
		require.NoError(t, model.DB.First(&sub, sub.Id).Error)
		assert.EqualValues(t, 300, sub.AmountUsed)
		assert.Equal(t, 1700, getTokenRemainQuota(t, token.Id))
		assert.Equal(t, 300, getTokenUsedQuota(t, token.Id))
		assert.EqualValues(t, 1, countReceiptLogs(t, user.Id))
	})

	t.Run("pending_queue_rotates_failed_oldest", func(t *testing.T) {
		user, token, channel := seedReceiptActors(t, 0, 0)
		first := seedReceiptTask(t, user, token, channel, 100)
		first.SettlementPending = true
		first.PrivateData.PendingSettlement = &model.TaskSettlement{Phase: "terminal", Quota: 2000}
		require.NoError(t, model.DB.Save(first).Error)
		second := *first
		second.ID = 0
		second.TaskID += "-second"
		require.NoError(t, model.DB.Create(&second).Error)
		rows, err := model.GetPendingTaskSettlements(t.Context(), 1)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		assert.Equal(t, first.ID, rows[0].ID)
		require.Error(t, RecoverTaskSettlement(t.Context(), rows[0]))
		rows, err = model.GetPendingTaskSettlements(t.Context(), 1)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		assert.Equal(t, second.ID, rows[0].ID)
	})
}

type pendingFailureAdaptor struct{ calls int }

func (a *pendingFailureAdaptor) Init(*relaycommon.RelayInfo) {}
func (a *pendingFailureAdaptor) FetchTask(string, string, *model.Task, string) (*http.Response, error) {
	a.calls++
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"done":true}`))}, nil
}
func (*pendingFailureAdaptor) ParseTaskResult(*model.Task, *http.Response, []byte) (*relaycommon.TaskInfo, error) {
	return relaycommon.FailTaskInfo("upstream failed"), nil
}
func (*pendingFailureAdaptor) AdjustBillingOnComplete(*model.Task, *relaycommon.TaskInfo) int {
	return 0
}

// Reopen the file-backed primary database with a new GORM handle, discarding
// callbacks and all in-memory session/observer state before financial recovery.
func TestTaskSettlementSQLiteRestartRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "task-restart.db")
	open := func() *gorm.DB {
		db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
		require.NoError(t, err)
		sqlDB, err := db.DB()
		require.NoError(t, err)
		sqlDB.SetMaxOpenConns(1)
		return db
	}
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	model.DB = open()
	model.LOG_DB = model.DB
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		sqlDB, err := model.DB.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Close())
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.SetDatabaseTypes(oldMain, oldLog)
	})
	require.NoError(t, model.DB.AutoMigrate(&model.User{}, &model.Token{}, &model.Channel{}, &model.Model{}, &model.Task{}, &model.Log{}, &model.Midjourney{}, &model.UserSubscription{}, &model.BalanceLedger{}, &model.BalanceLedgerOperation{}))
	user, token, channel := seedReceiptActors(t, 100, 40)
	task := seedReceiptTask(t, user, token, channel, 100)
	task.Status = model.TaskStatusInProgress
	require.NoError(t, model.DB.Model(task).Update("status", task.Status).Error)
	restore := pendingSettlementFailure(t, "tokens")
	require.Error(t, ApplyTaskPollResult(t.Context(), &mockAdaptor{adjustReturn: 300}, task, &relaycommon.TaskInfo{Status: "SUCCESS"}, []byte(`{"done":true}`)))
	restore()
	sqlDB, err := model.DB.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	model.DB = open()
	model.LOG_DB = model.DB
	rows, err := model.GetPendingTaskSettlements(t.Context(), 100)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, task.ID, rows[0].ID)
	require.NoError(t, RecoverTaskSettlement(t.Context(), rows[0]))
	require.NoError(t, RecoverTaskSettlement(t.Context(), rows[0]))
	assertUserBalances(t, user.Id, 0, 840, 0)
	assert.Equal(t, 1700, getTokenRemainQuota(t, token.Id))
	assert.Equal(t, 300, getTokenUsedQuota(t, token.Id))
	used, requests := getUserUsageAccounting(t, user.Id)
	assert.Equal(t, 300, used)
	assert.Equal(t, 1, requests)
	assert.EqualValues(t, 300, getChannelUsedQuota(t, channel.Id))
	assert.EqualValues(t, 1, countReceiptLogs(t, user.Id))
	rows, err = model.GetPendingTaskSettlements(t.Context(), 100)
	require.NoError(t, err)
	assert.Empty(t, rows)
}

// A persisted task must never treat an in-memory token batch as a durable
// reservation. Reopening the DB without flushing is the crash recovery boundary;
// a later flush must not duplicate any task debit or refund either.
func TestTaskBillingBatchReservationRestartRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "task-batch-restart.db")
	open := func() *gorm.DB {
		db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
		require.NoError(t, err)
		sqlDB, err := db.DB()
		require.NoError(t, err)
		sqlDB.SetMaxOpenConns(1)
		return db
	}
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	model.DB = open()
	model.LOG_DB = model.DB
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		sqlDB, err := model.DB.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Close())
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.SetDatabaseTypes(oldMain, oldLog)
	})
	require.NoError(t, model.DB.AutoMigrate(&model.User{}, &model.Token{}, &model.Channel{}, &model.Ability{}, &model.Model{}, &model.Vendor{}, &model.Task{}, &model.Log{}, &model.Midjourney{}, &model.UserSubscription{}, &model.BalanceLedger{}, &model.BalanceLedgerOperation{}))
	runTaskBatchReservationCases(t, func() {
		sqlDB, err := model.DB.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Close())
		model.DB = open()
		model.LOG_DB = model.DB
	})
}

func runTaskBatchReservationCases(t *testing.T, reopen func()) {
	for _, tc := range []struct {
		name      string
		redis     bool
		unlimited bool
		mixed     bool
	}{
		{name: "limited_without_redis"},
		{name: "limited_with_redis", redis: true},
		{name: "unlimited_without_redis", unlimited: true},
		{name: "unlimited_with_redis", redis: true, unlimited: true},
		{name: "limited_with_ordinary_request", redis: true, mixed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldRedis, oldBatch, oldExport, oldRDB := common.RedisEnabled, common.BatchUpdateEnabled, common.DataExportEnabled, common.RDB
			common.RedisEnabled, common.BatchUpdateEnabled = tc.redis, true
			common.DataExportEnabled = false
			if tc.redis {
				addr := os.Getenv("TEST_TASK_REDIS_ADDR")
				if addr == "" {
					addr = miniredis.RunT(t).Addr()
				}
				common.RDB = redis.NewClient(&redis.Options{Addr: addr, MaxRetries: -1})
				require.NoError(t, common.RDB.Ping(t.Context()).Err())
				if os.Getenv("TEST_TASK_REDIS_ADDR") != "" {
					version, err := common.RDB.Info(t.Context(), "server").Result()
					require.NoError(t, err)
					t.Logf("real Redis: %s", version)
				}
			}
			t.Cleanup(func() {
				if tc.redis {
					require.NoError(t, common.RDB.Close())
				}
				common.RedisEnabled, common.BatchUpdateEnabled, common.DataExportEnabled, common.RDB = oldRedis, oldBatch, oldExport, oldRDB
			})
			user, token, channel := seedReceiptActors(t, 0, 0)
			t.Cleanup(model.FlushRuntimeData)
			require.NoError(t, model.DB.Model(token).Update("unlimited_quota", tc.unlimited).Error)
			if tc.redis {
				token.UnlimitedQuota = tc.unlimited
				require.NoError(t, common.RedisHSetObj("token:"+common.GenerateHMAC(token.Key), token, time.Minute))
				t.Cleanup(func() {
					require.NoError(t, common.RDB.Del(context.Background(), "token:"+common.GenerateHMAC(token.Key), "token:fence:"+common.GenerateHMAC(token.Key), fmt.Sprintf("user:%d", user.Id)).Err())
				})
			}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
			if tc.mixed {
				require.NoError(t, PreConsumeTokenQuota(&relaycommon.RelayInfo{TokenId: token.Id, TokenKey: token.Key}, 20))
				assert.Equal(t, 1980, getTokenRemainQuota(t, token.Id), "ordinary token holds must be durable even when statistics batching is enabled")
			}
			info := &relaycommon.RelayInfo{
				TaskRelayInfo: &relaycommon.TaskRelayInfo{}, ForcePreConsume: true,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelId: channel.Id},
				UserId:      user.Id, TokenId: token.Id, TokenKey: token.Key, TokenUnlimited: tc.unlimited,
				OriginModelName: "test-model", RequestId: "batch-task-" + tc.name,
				UsingGroup: "default", UserGroup: "default",
				UserSetting: dto.UserSetting{BillingPreference: "wallet_only"},
			}
			require.Nil(t, PreConsumeBilling(c, 100, info))
			require.Nil(t, PreConsumeBilling(c, 200, info), "retry reservation must also be durable before upstream work")
			wantHeld, wantFinal, wantUsed := 1800, 1920, 80
			if tc.mixed {
				wantHeld, wantFinal, wantUsed = 1780, 1900, 100
			}
			assert.Equal(t, wantHeld, getTokenRemainQuota(t, token.Id), "both task token reservations must reach the DB without a batch flush")
			require.NotNil(t, PreConsumeBilling(c, 1200, info), "a retry exceeding the wallet must restore its provisional token debit")
			assert.Equal(t, wantHeld, getTokenRemainQuota(t, token.Id), "failed funding cannot leave a durable debit with a batched refund")
			assert.Equal(t, 200, info.Billing.GetPreConsumedQuota())
			task := makeTask(user.Id, channel.Id, 200, token.Id, BillingSourceWallet, 0)
			task.TaskID = "batch-task-" + tc.name
			task.PrivateData.WalletDebit = WalletSessionDebitShares(info)
			PrepareSubmittedTaskBilling(info, task, 80)
			require.NoError(t, model.DB.Create(task).Error)
			if reopen != nil {
				reopen()
			}
			if tc.redis && !tc.mixed {
				require.NoError(t, common.RDB.Del(t.Context(), "token:"+common.GenerateHMAC(token.Key), "token:fence:"+common.GenerateHMAC(token.Key)).Err())
			}
			var reloaded model.Task
			require.NoError(t, model.DB.First(&reloaded, task.ID).Error)
			require.NoError(t, RecoverTaskSettlement(t.Context(), &reloaded))
			require.NoError(t, RecoverTaskSettlement(t.Context(), &reloaded))
			assert.Equal(t, wantFinal, getTokenRemainQuota(t, token.Id), "recovery must reconcile the committed reserve, not a lost token batch")
			assert.Equal(t, wantUsed, getTokenUsedQuota(t, token.Id))
			assertUserBalances(t, user.Id, 0, 920, 0)
			used, requests := getUserUsageAccounting(t, user.Id)
			assert.Equal(t, 80, used)
			assert.Equal(t, 1, requests)
			if tc.mixed {
				// Settlement may invalidate Redis. Rehydration/fallback must
				// still include the unrelated ordinary request's token hold.
				err := PreConsumeTokenQuota(&relaycommon.RelayInfo{TokenId: token.Id, TokenKey: token.Key}, 1901)
				assert.Error(t, err, "task settlement must not discard an older ordinary request's reservation")
			}
			model.FlushRuntimeData()
			assert.Equal(t, wantFinal, getTokenRemainQuota(t, token.Id), "no token debit remains queued for a duplicate batch flush")
			assert.Equal(t, wantUsed, getTokenUsedQuota(t, token.Id))
		})
	}
}
