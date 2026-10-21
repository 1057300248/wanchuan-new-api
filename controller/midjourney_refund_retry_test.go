package controller

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func midjourneyRefundDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := withPaymentBindingDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.Midjourney{}, &model.Channel{}, &model.BalanceLedgerOperation{}, &model.BalanceLedger{}, &model.Log{}))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	oldMemory, oldRedis, oldBatch, oldLog := common.MemoryCacheEnabled, common.RedisEnabled, common.BatchUpdateEnabled, model.LOG_DB
	common.MemoryCacheEnabled = false
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	model.LOG_DB = db
	t.Cleanup(func() {
		common.MemoryCacheEnabled, common.RedisEnabled, common.BatchUpdateEnabled, model.LOG_DB = oldMemory, oldRedis, oldBatch, oldLog
	})
	return db
}

func TestMidjourneyFailedRefundRemainsRunnableWithoutChannel(t *testing.T) {
	db := midjourneyRefundDB(t)
	require.NoError(t, db.Create(&model.User{Id: 29501, Username: "mj-refund-retry", Quota: 900, QuotaPaid: 900, UsedQuota: 100, RequestCount: 1, Status: common.UserStatusEnabled}).Error)
	require.NoError(t, db.Create(&model.Token{Id: 29503, UserId: 29501, Key: "synthetic-mj-retry", RemainQuota: 900, UsedQuota: 100}).Error)
	task := &model.Midjourney{MjId: "mj-refund-retry", UserId: 29501, TokenId: 29503, ChannelId: 29502, Quota: 100, Status: "IN_PROGRESS", Progress: "20%", WalletDebit: []model.LedgerShare{{Account: model.AccountPaid, Amount: 100}}}
	require.NoError(t, db.Create(task).Error)
	// The channel was deleted after the task was accepted and charged.
	runMidjourneyTaskUpdateOnce(context.Background(), nil)
	require.NoError(t, db.First(task, task.Id).Error)
	require.Equal(t, "FAILURE", task.Status)
	require.Equal(t, "100%", task.Progress)
	require.True(t, model.HasUnfinishedMidjourneyTasks(), "failed-but-charged tasks keep the worker scheduled")
	require.NoError(t, db.Exec(`CREATE TRIGGER fail_mj_refund BEFORE INSERT ON balance_ledger_operations BEGIN SELECT RAISE(ABORT, 'transient MJ refund failure'); END`).Error)
	runMidjourneyTaskUpdateOnce(context.Background(), nil)
	require.NoError(t, db.First(task, task.Id).Error)
	assert.Equal(t, 100, task.Quota)
	require.True(t, model.HasUnfinishedMidjourneyTasks())
	var user model.User
	require.NoError(t, db.First(&user, 29501).Error)
	assert.Equal(t, 900, user.QuotaPaid)
	require.NoError(t, db.Exec("DROP TRIGGER fail_mj_refund").Error)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() { runMidjourneyTaskUpdateOnce(context.Background(), nil) })
	}
	wg.Wait()
	runMidjourneyTaskUpdateOnce(context.Background(), nil)
	require.NoError(t, db.First(task, task.Id).Error)
	require.NoError(t, db.First(&user, 29501).Error)
	var token model.Token
	require.NoError(t, db.First(&token, 29503).Error)
	assert.Zero(t, task.Quota)
	assert.Empty(t, task.WalletDebit)
	assert.Equal(t, 1000, user.QuotaPaid)
	assert.Zero(t, user.UsedQuota)
	assert.Equal(t, 1, user.RequestCount)
	assert.Equal(t, 1000, token.RemainQuota)
	assert.Zero(t, token.UsedQuota)
	var operations int64
	require.NoError(t, db.Model(&model.BalanceLedgerOperation{}).Count(&operations).Error)
	assert.EqualValues(t, 1, operations)
	assert.False(t, model.HasUnfinishedMidjourneyTasks())
}

func TestMidjourneyTransientChannelLookupCannotAuthorizeRefund(t *testing.T) {
	db := midjourneyRefundDB(t)
	require.NoError(t, db.Create(&model.User{Id: 29511, Username: "mj-lookup-failure", Quota: 900, QuotaPaid: 900}).Error)
	require.NoError(t, db.Create(&model.Channel{Id: 29512, Name: "existing-channel"}).Error)
	task := &model.Midjourney{UserId: 29511, MjId: "mj-lookup-failure", ChannelId: 29512, Quota: 100, Status: "IN_PROGRESS", Progress: "20%", WalletDebit: []model.LedgerShare{{Account: model.AccountPaid, Amount: 100}}}
	require.NoError(t, db.Create(task).Error)
	const hook = "test:mj-transient-lookup"
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(hook, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "Channel" {
			tx.AddError(errors.New("temporary database lookup failure"))
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Query().Remove(hook) })
	for range 2 {
		runMidjourneyTaskUpdateOnce(context.Background(), nil)
	}
	require.NoError(t, db.First(task, task.Id).Error)
	assert.Equal(t, "IN_PROGRESS", task.Status)
	assert.Equal(t, "20%", task.Progress)
	assert.Equal(t, 100, task.Quota)
	var user model.User
	require.NoError(t, db.First(&user, 29511).Error)
	assert.Equal(t, 900, user.QuotaPaid)
	var count int64
	require.NoError(t, db.Model(&model.BalanceLedgerOperation{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestMidjourneyRefundRevalidatesCommittedFailure(t *testing.T) {
	for _, timing := range []string{"before_reload", "before_receipt_cas"} {
		t.Run(timing, func(t *testing.T) {
			db := midjourneyRefundDB(t)
			require.NoError(t, db.Create(&model.User{Id: 29521, Username: "mj-stale-success", Quota: 900, QuotaPaid: 900}).Error)
			task := &model.Midjourney{UserId: 29521, MjId: "mj-stale-success", Quota: 100, Status: "FAILURE", Progress: "100%", WalletDebit: []model.LedgerShare{{Account: model.AccountPaid, Amount: 100}}}
			require.NoError(t, db.Create(task).Error)
			if timing == "before_reload" {
				require.NoError(t, db.Model(task).UpdateColumn("status", "SUCCESS").Error)
				task.Status = "FAILURE" // stale observer from the worker's original scan
				require.NoError(t, model.FailUnfinishedMidjourneyTasksByIDs([]int{task.Id}, "missing channel"))
			} else {
				// A deterministic in-transaction interleaving proves the final
				// state predicate rolls the ledger back, not just the early check.
				require.NoError(t, db.Exec(`CREATE TRIGGER advance_mj_success AFTER INSERT ON balance_ledger_operations BEGIN UPDATE midjourneys SET status = 'SUCCESS'; END`).Error)
			}
			assert.False(t, service.RefundMidjourneyQuota(context.Background(), task, "stale failure"))
			var user model.User
			require.NoError(t, db.First(&user, 29521).Error)
			assert.Equal(t, 900, user.QuotaPaid)
			require.NoError(t, db.First(task, task.Id).Error)
			assert.Equal(t, 100, task.Quota)
			if timing == "before_reload" {
				assert.Equal(t, "SUCCESS", task.Status)
			}
			var count int64
			require.NoError(t, db.Model(&model.BalanceLedgerOperation{}).Count(&count).Error)
			assert.Zero(t, count)
		})
	}
}

func TestMidjourneyEmptyUpstreamIDsHaveIndependentRefundReceipts(t *testing.T) {
	db := midjourneyRefundDB(t)
	require.NoError(t, db.Create(&model.User{Id: 29531, Username: "mj-empty-ids", Quota: 800, QuotaPaid: 800, UsedQuota: 200, RequestCount: 2}).Error)
	for range 2 {
		task := &model.Midjourney{UserId: 29531, Quota: 100, Status: "FAILURE", Progress: "100%", WalletDebit: []model.LedgerShare{{Account: model.AccountPaid, Amount: 100}}}
		require.NoError(t, db.Create(task).Error)
	}
	runMidjourneyTaskUpdateOnce(context.Background(), nil)
	runMidjourneyTaskUpdateOnce(context.Background(), nil)
	var user model.User
	require.NoError(t, db.First(&user, 29531).Error)
	assert.Equal(t, 1000, user.QuotaPaid)
	assert.Zero(t, user.UsedQuota)
	var operations, pending int64
	require.NoError(t, db.Model(&model.BalanceLedgerOperation{}).Count(&operations).Error)
	require.NoError(t, db.Model(&model.Midjourney{}).Where("quota <> 0").Count(&pending).Error)
	assert.EqualValues(t, 2, operations)
	assert.Zero(t, pending)
	assert.False(t, model.HasUnfinishedMidjourneyTasks())
}

func TestMidjourneyRefundPreservesLegacyAndUnchargedReceipts(t *testing.T) {
	for _, receipt := range []struct {
		name     string
		shares   []model.LedgerShare
		expected int
	}{
		{"legacy_null", nil, 1000}, {"uncharged_empty", []model.LedgerShare{}, 900},
	} {
		t.Run(receipt.name, func(t *testing.T) {
			db := midjourneyRefundDB(t)
			require.NoError(t, db.Create(&model.User{Id: 29541, Username: "mj-receipt-kind", Quota: 900, QuotaPaid: 900}).Error)
			task := &model.Midjourney{UserId: 29541, MjId: receipt.name, Quota: 100, Status: "FAILURE", Progress: "100%", WalletDebit: receipt.shares}
			require.NoError(t, db.Create(task).Error)
			runMidjourneyTaskUpdateOnce(context.Background(), nil)
			runMidjourneyTaskUpdateOnce(context.Background(), nil)
			var user model.User
			require.NoError(t, db.First(&user, 29541).Error)
			assert.Equal(t, receipt.expected, user.QuotaPaid)
			require.NoError(t, db.First(task, task.Id).Error)
			assert.Zero(t, task.Quota)
		})
	}
}

func TestMidjourneyCacheMissRequiresAuthoritativeAbsence(t *testing.T) {
	db := midjourneyRefundDB(t)
	require.NoError(t, db.Create(&model.User{Id: 29551, Username: "mj-cache-miss", Quota: 900, QuotaPaid: 900}).Error)
	require.NoError(t, db.Create(&model.Channel{Id: 29552, Name: "exists-on-other-node"}).Error)
	task := &model.Midjourney{UserId: 29551, MjId: "mj-cache-miss", ChannelId: 29552, Quota: 100, Status: "IN_PROGRESS", Progress: "20%", WalletDebit: []model.LedgerShare{{Account: model.AccountPaid, Amount: 100}}}
	require.NoError(t, db.Create(task).Error)
	const hook = "test:mj-snapshot-miss"
	confirmed := 0
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(hook, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "Channel" {
			if len(tx.Statement.Omits) == 0 {
				tx.AddError(model.ErrChannelCacheMiss) // the cache-only lookup is unavailable
			} else {
				confirmed++
			} // the metadata-only authoritative lookup succeeds
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Query().Remove(hook) })
	for range 2 {
		runMidjourneyTaskUpdateOnce(context.Background(), nil)
	}
	require.Equal(t, 2, confirmed)
	require.NoError(t, db.First(task, task.Id).Error)
	assert.Equal(t, "IN_PROGRESS", task.Status)
	assert.Equal(t, 100, task.Quota)
	var user model.User
	require.NoError(t, db.First(&user, 29551).Error)
	assert.Equal(t, 900, user.QuotaPaid)
}
