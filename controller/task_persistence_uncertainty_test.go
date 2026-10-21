package controller

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	walletmodule "github.com/QuantumNous/new-api/modules/chuanapi/wallet"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type submissionACKOutagePool struct {
	gorm.ConnPool
	mode          atomic.Int32
	unavailable   atomic.Bool
	blocked       atomic.Int32
	refundAttempt chan struct{}
}

func (p *submissionACKOutagePool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	if p.unavailable.Load() {
		if p.blocked.Add(1) == 2 {
			// The first failure is the task readback. The second is the
			// existing Refund decision, whether persisted inline or queued.
			close(p.refundAttempt)
		}
		return nil, errors.New("synthetic database unavailable")
	}
	tx, err := p.ConnPool.(gorm.TxBeginner).BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &submissionACKOutageTx{Tx: tx, pool: p}, nil
}

type submissionACKOutageTx struct {
	*sql.Tx
	pool *submissionACKOutagePool
}

func (tx *submissionACKOutageTx) Commit() error {
	mode := tx.pool.mode.Swap(0)
	if mode == 1 {
		if err := tx.Tx.Rollback(); err != nil {
			return err
		}
	} else if err := tx.Tx.Commit(); err != nil {
		return err
	}
	if mode != 0 {
		tx.pool.unavailable.Store(true)
		return errors.New("synthetic lost COMMIT ACK with private-database-diagnostic")
	}
	return nil
}

func TestTaskSubmissionUncertainPersistenceClientContracts(t *testing.T) {
	registry := moneyport.NewFactoryRegistry()
	require.NoError(t, service.RegisterHostFundingFactories(registry))
	require.NoError(t, walletmodule.RegisterFundingFactory(registry))
	if err := service.ConfigureFundingFactoryRegistry(registry); err != nil {
		require.Contains(t, err.Error(), "already configured")
	}
	plugin, err := pluginruntime.CompilePlugin(`
export const meta={apiVersion:1,key:"uncertain-client",name:"Uncertain client",version:"1.0.0",author:{name:"Test"},models:["plugin-model"],fetchMode:"per_task",routes:[{method:"POST",path:"/vendor/jobs",type:"submit",decode:"decode",render:"created"}]};
export const native={decode:function(ctx){return {kind:"submit",model:"plugin-model",requestBody:{}};},created:function(ctx,task){return {id:task.task_id};},error:function(ctx,error){return {code:error.code,message:error.message,retryable:error.retryable};}};
export function buildSubmitRequest(){return {}} export function parseSubmitResponse(){return {taskId:"upstream"}} export function buildQueryRequest(){return {}} export function parseTaskResult(){return {status:"SUCCESS"}}
`, pluginruntime.Options{})
	require.NoError(t, err)
	for _, mode := range []int32{1, 2} {
		for _, surface := range []string{"native", "native_plugin", "images", "responses", "video"} {
			t.Run(map[int32]string{1: "rolled_back", 2: "committed"}[mode]+"/"+surface, func(t *testing.T) {
				db, dialect := openTaskDialectDatabase(t, &model.User{}, &model.Channel{}, &model.Task{}, &model.Token{}, &model.Log{}, &model.Model{}, &model.BalanceLedger{}, &model.BalanceLedgerOperation{})
				oldDB, oldLogDB := model.DB, model.LOG_DB
				oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
				oldRedis, oldMemory, oldBatch, oldConsume := common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled
				model.DB, model.LOG_DB = db, db
				common.SetDatabaseTypes(dialect, dialect)
				common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled = false, false, false, false
				oldTrust := operation_setting.GetQuotaSetting().TrustQuotaUSD
				operation_setting.GetQuotaSetting().TrustQuotaUSD = 0
				t.Cleanup(func() {
					model.DB, model.LOG_DB = oldDB, oldLogDB
					common.SetDatabaseTypes(oldMain, oldLog)
					common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled = oldRedis, oldMemory, oldBatch, oldConsume
					operation_setting.GetQuotaSetting().TrustQuotaUSD = oldTrust
					model.InvalidateModelBonusEligibleCache()
				})
				user := model.User{Username: "uncertain-client", AffCode: "uncertain-client", Quota: 1000, QuotaPaid: 1000}
				require.NoError(t, db.Create(&user).Error)
				var sends atomic.Int32
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					sends.Add(1)
					w.WriteHeader(http.StatusAccepted)
				}))
				t.Cleanup(upstream.Close)
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/vendor/jobs", strings.NewReader(`{}`))
				info := taskSubmissionRelayInfo(nil)
				info.UserId = user.Id
				info.UserGroup = "default"
				info.IsPlayground = true
				info.UserSetting.BillingPreference = "wallet_only"
				info.RequestId = common.NewRequestId()
				info.PublicTaskID = model.GenerateTaskID()
				info.ChannelMeta.ApiKey = "synthetic-private-channel-key"
				c.Set(common.RequestIdKey, info.RequestId)
				session, apiErr := service.NewBillingSession(c, info, 100)
				require.Nil(t, apiErr)
				info.Billing = session
				pool := &submissionACKOutagePool{ConnPool: db.ConnPool, refundAttempt: make(chan struct{})}
				pool.mode.Store(mode)
				wrapped := db.Session(&gorm.Session{NewDB: true, Context: context.Background()})
				config := *wrapped.Config
				wrapped.Config = &config
				wrapped.Config.ConnPool, wrapped.Statement.ConnPool = pool, pool
				model.DB = wrapped
				var logs bytes.Buffer
				common.LogWriterMu.Lock()
				oldWriter := gin.DefaultErrorWriter
				gin.DefaultErrorWriter = &logs
				common.LogWriterMu.Unlock()
				t.Cleanup(func() {
					common.LogWriterMu.Lock()
					gin.DefaultErrorWriter = oldWriter
					common.LogWriterMu.Unlock()
				})
				t.Cleanup(func() {
					// Keep this SAME DB wrapper installed until the original
					// refund callback finishes, including on a failed assertion.
					pool.mode.Store(0)
					pool.unavailable.Store(false)
					session.Refund(c)
					require.Eventually(t, func() bool { return !session.NeedsRefund() }, 5*time.Second, time.Millisecond)
				})
				outcome, taskErr := executeTaskSubmissionWith(c, info, func(c *gin.Context, _ *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
					request, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, upstream.URL, nil)
					require.NoError(t, err)
					response, err := upstream.Client().Do(request)
					require.NoError(t, err)
					require.NoError(t, response.Body.Close())
					return &relay.TaskSubmitResult{Platform: constant.TaskPlatform("uncertain-client"), UpstreamTaskID: "synthetic-private-upstream-id", TaskData: []byte(`{"secret":"synthetic-private-payload"}`), Quota: 180}, nil
				})
				assert.Nil(t, outcome)
				require.NotNil(t, taskErr)
				assert.EqualValues(t, 1, sends.Load())
				select {
				case <-pool.refundAttempt:
				case <-time.After(5 * time.Second):
					t.Fatal("existing deferred refund never attempted its intent persistence")
				}
				assert.True(t, session.NeedsRefund())
				assert.ErrorIs(t, session.Reserve(200), model.ErrBillingFinalized, "existing deferred Refund closes the session")
				assert.False(t, taskErr.NoRetry)
				assert.True(t, taskErr.LocalError)
				assert.Equal(t, "task_insert_failed", taskErr.Code)
				assert.Equal(t, "failed to persist task", taskErr.Message)
				assert.Equal(t, http.StatusInternalServerError, taskErr.StatusCode)
				assert.False(t, c.Writer.Written())
				common.LogWriterMu.Lock()
				logSnapshot := logs.String()
				common.LogWriterMu.Unlock()
				assert.Contains(t, logSnapshot, "task_persistence_uncertain")
				assert.Contains(t, logSnapshot, info.PublicTaskID)
				assert.Contains(t, logSnapshot, info.RequestId)
				for _, private := range []string{"synthetic-private-upstream-id", "synthetic-private-channel-key", "synthetic-private-payload", "private-database-diagnostic"} {
					assert.NotContains(t, logSnapshot, private)
				}
				baselineRecorder := httptest.NewRecorder()
				baseline, _ := gin.CreateTestContext(baselineRecorder)
				baseline.Request = c.Request.Clone(c.Request.Context())
				baseline.Set(common.RequestIdKey, info.RequestId)
				baselineError := service.TaskErrorWrapperLocal(errors.New("failed to persist task"), "task_insert_failed", http.StatusInternalServerError)
				assert.Equal(t, decideTaskRetry(c, baselineError, 3), decideTaskRetry(c, taskErr, 3), "classification must not change retry policy")
				if surface == "native_plugin" {
					for _, context := range []*gin.Context{c, baseline} {
						context.Set(pluginruntime.ContextKeyPinnedRoute, pluginruntime.PinnedRoute{Plugin: plugin, Route: plugin.Meta.Routes[0]})
						context.Set(pluginruntime.ContextKeyRouteRequest, pluginruntime.RouteRequestContext{Path: "/vendor/jobs", Method: http.MethodPost, Body: map[string]any{"kind": "json", "value": map[string]any{}}})
					}
				}
				if surface == "video" {
					for _, context := range []*gin.Context{c, baseline} {
						context.Set(pluginruntime.ContextKeyPinnedEndpoint, pluginruntime.PinnedEndpoint{Protocol: "openai_video", Operation: pluginruntime.HostProtocolOperation{Name: "create"}})
					}
				}
				present := respondTaskSubmissionError
				if surface == "images" {
					present = respondTaskPluginImageError
				} else if surface == "responses" {
					present = respondPluginProtocolSubmissionError
				}
				present(baseline, baselineError)
				present(c, taskErr)
				assert.Equal(t, baselineRecorder.Code, recorder.Code)
				assert.Equal(t, http.StatusInternalServerError, recorder.Code)
				assert.Equal(t, info.PublicTaskID, recorder.Header().Get("X-New-API-Task-ID"))
				assert.Equal(t, info.RequestId, recorder.Header().Get("X-New-API-Task-Request-ID"))
				assert.Equal(t, "uncertain", recorder.Header().Get("X-New-API-Task-Persistence"))
				assert.Empty(t, recorder.Header().Get("X-New-API-Task-Accepted"))
				if surface == "native" || surface == "video" {
					var body dto.TaskError
					require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &body))
					assert.Equal(t, baselineError.Code, body.Code)
					assert.Equal(t, baselineError.Message, body.Message)
					assert.Equal(t, map[string]any{"code": "task_persistence_uncertain", "persistence": "uncertain", "request_id": info.RequestId, "task_id": info.PublicTaskID}, body.Data)
				} else {
					assert.JSONEq(t, baselineRecorder.Body.String(), recorder.Body.String(), "protocol/native bodies are unchanged")
				}
				for _, private := range []string{"synthetic-private-upstream-id", "synthetic-private-channel-key", "synthetic-private-payload", "private-database-diagnostic", "fingerprint"} {
					assert.NotContains(t, recorder.Body.String(), private)
				}
				pool.unavailable.Store(false)
				var taskCount int64
				require.NoError(t, db.Model(&model.Task{}).Count(&taskCount).Error)
				expectedTasks, expectedBalance := int64(0), 1000
				if mode == 2 {
					expectedTasks, expectedBalance = 1, 900
				}
				assert.Equal(t, expectedTasks, taskCount)
				session.Refund(c)
				require.Eventually(t, func() bool { return !session.NeedsRefund() }, 3*time.Second, time.Millisecond)
				var stored model.User
				require.NoError(t, db.First(&stored, user.Id).Error)
				assert.Equal(t, expectedBalance, stored.Quota)
				assert.EqualValues(t, 1, sends.Load(), "restoring DB and refund reconciliation must not send upstream again")
			})
		}
	}
}
