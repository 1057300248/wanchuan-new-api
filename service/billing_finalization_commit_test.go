package service

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Inject the actual SQL transaction boundary, not an error before COMMIT.
// mode 1 rolls back; mode 2 commits but loses the acknowledgement.
type terminalCommitPool struct {
	gorm.ConnPool
	mode *atomic.Int32
}

func (p *terminalCommitPool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	tx, err := p.ConnPool.(gorm.TxBeginner).BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &terminalCommitTx{Tx: tx, mode: p.mode}, nil
}

type terminalCommitTx struct {
	*sql.Tx
	mode *atomic.Int32
}

func (t *terminalCommitTx) Commit() error {
	mode := t.mode.Swap(0)
	if mode == 1 {
		if err := t.Tx.Rollback(); err != nil {
			return err
		}
		return errors.New("synthetic COMMIT rejected")
	}
	if err := t.Tx.Commit(); err != nil {
		return err
	}
	if mode == 2 {
		return errors.New("synthetic committed ACK lost")
	}
	return nil
}
func terminalCommitFault(t *testing.T, mode int32) *atomic.Int32 {
	t.Helper()
	original := model.DB
	flag := &atomic.Int32{}
	flag.Store(mode)
	wrapped := original.Session(&gorm.Session{NewDB: true, Context: context.Background()})
	config := *wrapped.Config
	wrapped.Config = &config
	pool := &terminalCommitPool{ConnPool: original.ConnPool, mode: flag}
	wrapped.Config.ConnPool = pool
	wrapped.Statement.ConnPool = pool
	model.DB = wrapped
	t.Cleanup(func() { model.DB = original })
	return flag
}

func TestPolicyPreservingTerminalCommitBoundaries(t *testing.T) {
	for _, mode := range []int32{1, 2} {
		t.Run(map[int32]string{1: "rollback", 2: "lost_ack"}[mode], func(t *testing.T) {
			t.Run("refund_intent", func(t *testing.T) {
				s := legacyRefundSession(t, "intent-commit-boundary", 100, 100)
				terminalCommitFault(t, mode)
				retrying, releaseRetry := pauseRefundIntentRetry(t)
				useRefundDispatcher(t, 2, 1, time.Second)
				s.Refund(testGinContext(t))
				awaitRefundSignal(t, retrying)
				require.True(t, s.NeedsRefund())
				require.ErrorIs(t, s.Reserve(200), model.ErrBillingFinalized)
				require.ErrorIs(t, s.Settle(200), model.ErrBillingFinalized)
				assertUserBalances(t, 31201, 0, 900, 0)
				var count int64
				require.NoError(t, model.DB.Model(&model.BillingFinalization{}).Count(&count).Error)
				if mode == 1 {
					require.Zero(t, count)
				} else {
					require.EqualValues(t, 1, count)
					var pending model.BillingFinalization
					require.NoError(t, model.DB.First(&pending, "request_id = ?", s.relayInfo.RequestId).Error)
					require.Equal(t, model.BillingRefundPending, pending.State)
				}
				releaseRetry()
				waitRefundCallback(t, s)
				require.False(t, s.NeedsRefund())
				assertUserBalances(t, 31201, 0, 1000, 0)
			})
			t.Run("settlement", func(t *testing.T) {
				s := legacyRefundSession(t, "settle-commit-boundary", 100, 100)
				terminalCommitFault(t, mode)
				require.Error(t, s.Settle(200))
				if mode == 1 {
					require.NoError(t, s.Reserve(150))
					s.Refund(testGinContext(t))
					waitRefundCallback(t, s)
					assertUserBalances(t, 31201, 0, 1000, 0)
				} else {
					require.ErrorIs(t, s.Reserve(250), model.ErrBillingFinalized)
					require.NoError(t, s.Settle(200))
					require.Error(t, s.Settle(201))
					s.Refund(testGinContext(t))
					assertUserBalances(t, 31201, 0, 800, 0)
				}
			})
			t.Run("task_insert", func(t *testing.T) {
				s := legacyRefundSession(t, "task-commit-boundary", 100, 100)
				s.relayInfo.Billing = s
				task := &model.Task{TaskID: "same-public-destination", UserId: 31201, PrivateData: model.TaskPrivateData{RequestId: s.relayInfo.RequestId, BillingSource: "wallet", TokenId: 31202, WalletDebit: s.WalletDebitShares()}, Data: []byte(`{"inline":true}`)}
				PrepareSubmittedTaskBilling(s.relayInfo, task, 180)
				terminalCommitFault(t, mode)
				err := PersistTaskWithBillingOwner(context.Background(), s.relayInfo, task, "data")
				if mode == 1 {
					require.Error(t, err)
					s.Refund(testGinContext(t))
					waitRefundCallback(t, s)
					assertUserBalances(t, 31201, 0, 1000, 0)
				} else {
					require.NoError(t, err)
					require.True(t, s.taskOwned)
					require.NotEmpty(t, task.Data)
					var stored model.Task
					require.NoError(t, model.DB.First(&stored, task.ID).Error)
					require.Empty(t, stored.Data)
					s.Refund(testGinContext(t))
					require.False(t, s.NeedsRefund())
					assertUserBalances(t, 31201, 0, 900, 0)
					require.NoError(t, model.DB.Model(&model.Task{}).Where("id = ?", task.ID).Update("quota", 180).Error)
					require.NoError(t, PersistTaskWithBillingOwner(context.Background(), s.relayInfo, task, "data"))
				}
			})
		})
	}
}

func TestPolicyPreservingRefundCommitBoundaries(t *testing.T) {
	for _, mode := range []int32{1, 2} {
		t.Run(map[int32]string{1: "rollback", 2: "lost_ack"}[mode], func(t *testing.T) {
			s := legacyRefundSession(t, "refund-financial-commit", 100, 100)
			s.mu.Lock()
			receipt, err := s.terminalReceiptLocked()
			s.mu.Unlock()
			require.NoError(t, err)
			_, err = model.PersistBillingRefund(context.Background(), receipt)
			require.NoError(t, err)
			terminalCommitFault(t, mode)
			require.Error(t, model.RecoverBillingRefund(context.Background(), receipt.RequestID))
			expected := 900
			if mode == 2 {
				expected = 1000
			}
			assertUserBalances(t, 31201, 0, expected, 0)
			var token model.Token
			require.NoError(t, model.DB.First(&token, 31202).Error)
			require.Equal(t, expected, token.RemainQuota)
			require.NoError(t, model.RecoverBillingRefund(context.Background(), receipt.RequestID))
			require.NoError(t, model.RecoverBillingRefund(context.Background(), receipt.RequestID))
			assertUserBalances(t, 31201, 0, 1000, 0)
			require.NoError(t, model.DB.First(&token, 31202).Error)
			require.Equal(t, 1000, token.RemainQuota)
		})
	}
}
