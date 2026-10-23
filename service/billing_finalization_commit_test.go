package service

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"
	"testing"

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
	if runRefundFixtureChild(t) {
		return
	}
	for _, mode := range []int32{1, 2} {
		t.Run(map[int32]string{1: "rollback", 2: "lost_ack"}[mode], func(t *testing.T) {
			t.Run("settlement", func(t *testing.T) {
				s := currentRefundSession(t, "settle-commit-boundary", 100)
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
		})
	}
}
