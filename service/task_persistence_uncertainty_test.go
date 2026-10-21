package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

var errTaskCommitACKLost = errors.New("synthetic COMMIT ACK lost with private database diagnostic")

// The actual SQL COMMIT/ROLLBACK finishes before every subsequent DB operation
// is made unavailable. This tests lost ACK + unavailable readback, not merely a
// before-COMMIT callback error. Recovery switches connectivity back on explicitly.
type taskPersistenceOutagePool struct {
	gorm.ConnPool
	mode        atomic.Int32
	unavailable atomic.Bool
	begins      atomic.Int32
	execs       atomic.Int32
}

func (p *taskPersistenceOutagePool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	p.begins.Add(1)
	if p.unavailable.Load() {
		return nil, errors.New("synthetic database unavailable")
	}
	tx, err := p.ConnPool.(gorm.TxBeginner).BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &taskPersistenceOutageTx{Tx: tx, pool: p}, nil
}

func (p *taskPersistenceOutagePool) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	p.execs.Add(1)
	if p.unavailable.Load() {
		return nil, errors.New("synthetic database unavailable")
	}
	return p.ConnPool.ExecContext(ctx, query, args...)
}

func (p *taskPersistenceOutagePool) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if p.unavailable.Load() {
		return nil, errors.New("synthetic database unavailable")
	}
	return p.ConnPool.QueryContext(ctx, query, args...)
}

type taskPersistenceOutageTx struct {
	*sql.Tx
	pool *taskPersistenceOutagePool
}

func (tx *taskPersistenceOutageTx) Commit() error {
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
		return errTaskCommitACKLost
	}
	return nil
}

func taskPersistenceOutage(t *testing.T, mode int32) *taskPersistenceOutagePool {
	t.Helper()
	original := model.DB
	pool := &taskPersistenceOutagePool{ConnPool: original.ConnPool}
	pool.mode.Store(mode)
	wrapped := original.Session(&gorm.Session{NewDB: true, Context: context.Background()})
	config := *wrapped.Config
	wrapped.Config = &config
	wrapped.Config.ConnPool = pool
	wrapped.Statement.ConnPool = pool
	model.DB = wrapped
	t.Cleanup(func() { model.DB = original })
	return pool
}

func TestTaskPersistenceUncertainCommitAndReadbackOutage(t *testing.T) {
	for _, mode := range []int32{1, 2} {
		t.Run(map[int32]string{1: "rolled_back", 2: "committed"}[mode], func(t *testing.T) {
			s := legacyRefundSession(t, "uncertain-insert-request", 100, 100)
			s.relayInfo.Billing = s
			task := &model.Task{TaskID: "task_uncertain_destination", UserId: 31201, Status: model.TaskStatusSubmitted, PrivateData: model.TaskPrivateData{RequestId: s.relayInfo.RequestId, BillingSource: "wallet", TokenId: 31202, WalletDebit: s.WalletDebitShares(), Key: "synthetic-private-upstream-key", UpstreamTaskID: "synthetic-private-upstream-id"}, Data: []byte(`{"private":"inline-payload"}`)}
			PrepareSubmittedTaskBilling(s.relayInfo, task, 180)
			pool := taskPersistenceOutage(t, mode)
			err := PersistTaskWithBillingOwner(context.Background(), s.relayInfo, task, "data")
			var uncertain *TaskPersistenceUncertainError
			require.ErrorAs(t, err, &uncertain)
			assert.ErrorIs(t, err, errTaskCommitACKLost)
			assert.EqualValues(t, 2, pool.begins.Load(), "one INSERT and one failed readback; no resubmission")
			assert.False(t, s.taskOwned, "unresolved evidence is not an acceptance flag")
			assert.Equal(t, TaskPersistenceCorrelation{Code: "task_persistence_uncertain", Persistence: "uncertain", RequestID: s.relayInfo.RequestId, TaskID: task.TaskID}, uncertain.Correlation())
			for _, format := range []string{"%s", "%v", "%+v", "%#v", "%q"} {
				message := fmt.Sprintf(format, uncertain)
				assert.Contains(t, message, task.TaskID)
				assert.Contains(t, message, s.relayInfo.RequestId)
				assert.NotContains(t, message, "synthetic-original-refund-token")
				assert.NotContains(t, message, uncertain.receipt.TokenFingerprint)
				assert.NotContains(t, message, "private database diagnostic")
			}
			encodedError, err := common.Marshal(uncertain)
			require.NoError(t, err)
			assert.JSONEq(t, `{}`, string(encodedError), "private receipt and raw cause are never serialized")
			evidence, err := uncertain.Reconcile(context.Background())
			require.Error(t, err)
			assert.Empty(t, evidence, "DB unavailability is not an absent observation")
			pool.unavailable.Store(false)
			begins, execs := pool.begins.Load(), pool.execs.Load()
			evidence, err = uncertain.Reconcile(context.Background())
			require.NoError(t, err)
			assert.Equal(t, begins, pool.begins.Load(), "reconciliation must not claim a terminal owner")
			assert.Equal(t, execs, pool.execs.Load(), "reconciliation is SELECT-only")
			assert.Equal(t, s.relayInfo.RequestId, evidence.RequestID)
			assert.Equal(t, task.TaskID, evidence.TaskID)
			expectedObservation := "not_observed"
			expectedTasks := int64(0)
			if mode == 2 {
				expectedObservation, expectedTasks = "committed", 1
				assert.Equal(t, model.BillingTaskOwned, evidence.BillingState)
				assert.Equal(t, model.TaskStatus(model.TaskStatusSubmitted), evidence.TaskStatus)
			}
			assert.Equal(t, expectedObservation, evidence.Observation)
			var count int64
			require.NoError(t, model.DB.Model(&model.Task{}).Count(&count).Error)
			assert.Equal(t, expectedTasks, count)
			require.NoError(t, model.DB.Model(&model.BillingFinalization{}).Count(&count).Error)
			assert.Equal(t, expectedTasks, count)
			assertUserBalances(t, 31201, 0, 900, 0)
			var token model.Token
			require.NoError(t, model.DB.First(&token, 31202).Error)
			assert.Equal(t, 900, token.RemainQuota)
			encoded, err := common.Marshal(evidence)
			require.NoError(t, err)
			assert.NotContains(t, string(encoded), "private")
			assert.NotContains(t, string(encoded), "fingerprint")
			// The existing Refund decision is deliberately unchanged. The same
			// original task owner blocks a duplicate refund if COMMIT succeeded.
			s.Refund(testGinContext(t))
			waitRefundCallback(t, s)
			expectedBalance := 1000
			if mode == 2 {
				expectedBalance = 900
			}
			assertUserBalances(t, 31201, 0, expectedBalance, 0)
			require.NoError(t, model.DB.First(&token, 31202).Error)
			assert.Equal(t, expectedBalance, token.RemainQuota)
		})
	}
}

func TestTaskPersistenceReconciliationRequiresOriginalIdentity(t *testing.T) {
	s := legacyRefundSession(t, "reconcile-original-identity", 100, 100)
	s.relayInfo.Billing = s
	task := &model.Task{TaskID: "task_original_destination", UserId: 31201, PrivateData: model.TaskPrivateData{RequestId: s.relayInfo.RequestId, BillingSource: "wallet", TokenId: 31202, WalletDebit: s.WalletDebitShares()}}
	PrepareSubmittedTaskBilling(s.relayInfo, task, 180)
	require.NoError(t, PersistTaskWithBillingOwner(context.Background(), s.relayInfo, task))
	receipt := *s.terminalReceipt
	for _, tc := range []struct {
		name   string
		mutate func(*model.BillingFinalization)
	}{
		{"user", func(r *model.BillingFinalization) { r.UserID++ }},
		{"token", func(r *model.BillingFinalization) { r.TokenID++ }},
		{"fingerprint", func(r *model.BillingFinalization) { r.TokenFingerprint = strings.Repeat("a", 64) }},
		{"held", func(r *model.BillingFinalization) { r.SourceHeld++; r.Paid++ }},
		{"token_held", func(r *model.BillingFinalization) { r.TokenHeld++ }},
		{"preconsume", func(r *model.BillingFinalization) { r.PreConsumed++ }},
		{"source_period", func(r *model.BillingFinalization) {
			r.Source = "subscription"
			r.Paid = 0
			r.SubscriptionID = 71
			r.SubscriptionPeriod = 3
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := receipt
			tc.mutate(&changed)
			evidence, err := model.InspectBillingTaskPersistence(context.Background(), changed, task.TaskID)
			require.Error(t, err)
			assert.Empty(t, evidence, "mismatched identity discloses no task/owner")
		})
	}
	evidence, err := model.InspectBillingTaskPersistence(context.Background(), receipt, "task_another_destination")
	require.Error(t, err)
	assert.Empty(t, evidence)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	evidence, err = model.InspectBillingTaskPersistence(ctx, receipt, task.TaskID)
	require.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, evidence)
	_, err = model.InspectBillingTaskPersistence(nil, receipt, task.TaskID)
	require.Error(t, err)
	t.Run("subscription_period", func(t *testing.T) {
		subReceipt := model.BillingFinalization{RequestID: "reconcile-subscription-period", UserID: 31201, Source: "subscription", SourceHeld: 100, PreConsumed: 100, SubscriptionID: 77, SubscriptionPeriod: 3, State: model.BillingTaskOwned, TaskPublicID: "task_subscription_destination"}
		subTask := model.Task{TaskID: subReceipt.TaskPublicID, UserId: subReceipt.UserID, PrivateData: model.TaskPrivateData{RequestId: subReceipt.RequestID, BillingSource: "subscription", SubscriptionId: 77, SubscriptionQuotaPeriod: 3}}
		require.NoError(t, model.DB.Create(&subTask).Error)
		subReceipt.TaskID = subTask.ID
		require.NoError(t, model.DB.Create(&subReceipt).Error)
		evidence, err := model.InspectBillingTaskPersistence(context.Background(), subReceipt, subTask.TaskID)
		require.NoError(t, err)
		assert.Equal(t, "committed", evidence.Observation)
		subReceipt.SubscriptionPeriod++
		evidence, err = model.InspectBillingTaskPersistence(context.Background(), subReceipt, subTask.TaskID)
		require.Error(t, err)
		assert.Empty(t, evidence)
	})
	t.Run("task_identity", func(t *testing.T) {
		original := task.PrivateData
		changed := original
		changed.TokenId++
		require.NoError(t, model.DB.Model(task).Update("private_data", changed).Error)
		evidence, err := model.InspectBillingTaskPersistence(context.Background(), receipt, task.TaskID)
		require.Error(t, err)
		assert.Empty(t, evidence)
		require.NoError(t, model.DB.Model(task).Update("private_data", original).Error)
	})
	t.Run("unsafe_status", func(t *testing.T) {
		require.NoError(t, model.DB.Model(task).Update("status", "private-status").Error)
		evidence, err := model.InspectBillingTaskPersistence(context.Background(), receipt, task.TaskID)
		require.NoError(t, err)
		assert.Equal(t, model.TaskStatus(model.TaskStatusUnknown), evidence.TaskStatus)
	})
	t.Run("missing_destination", func(t *testing.T) {
		require.NoError(t, model.DB.Delete(task).Error)
		evidence, err := model.InspectBillingTaskPersistence(context.Background(), receipt, task.TaskID)
		require.NoError(t, err)
		assert.Equal(t, "task_not_observed", evidence.Observation)
	})
	t.Run("other_terminal_owner", func(t *testing.T) {
		require.NoError(t, model.DB.Model(&model.BillingFinalization{}).Where("request_id = ?", receipt.RequestID).Update("state", model.BillingRefunded).Error)
		evidence, err := model.InspectBillingTaskPersistence(context.Background(), receipt, task.TaskID)
		require.NoError(t, err)
		assert.Equal(t, "other_terminal_owner", evidence.Observation)
		assert.Equal(t, model.BillingRefunded, evidence.BillingState)
	})
}

func TestTaskPersistenceCorrelationExcludesUnsafeIdentifiers(t *testing.T) {
	err := &TaskPersistenceUncertainError{receipt: model.BillingFinalization{RequestID: "request\r\ninjected"}, taskPublicID: strings.Repeat("x", 192)}
	correlation := err.Correlation()
	assert.Empty(t, correlation.RequestID)
	assert.Empty(t, correlation.TaskID)
	assert.NotContains(t, err.Error(), "injected")
}
