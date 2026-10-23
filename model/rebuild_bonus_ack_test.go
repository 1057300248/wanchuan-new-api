package model

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// This wrapper commits a real SQL transaction before deliberately returning an
// error. It exercises an unknown ACK without pretending a rollback took place.
type rebuildBonusLostAckPool struct {
	gorm.ConnPool
	committed atomic.Int32
}

type rebuildBonusLostAckTx struct {
	*sql.Tx
	pool *rebuildBonusLostAckPool
}

var errRebuildBonusLostAck = errors.New("injected metadata commit acknowledgement loss")

func (p *rebuildBonusLostAckPool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	tx, err := p.ConnPool.(gorm.TxBeginner).BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &rebuildBonusLostAckTx{Tx: tx, pool: p}, nil
}

func (tx *rebuildBonusLostAckTx) Commit() error {
	if err := tx.Tx.Commit(); err != nil {
		return err
	}
	tx.pool.committed.Add(1)
	return errRebuildBonusLostAck
}

func rebuildBonusLoseCommitAck(t *testing.T, db *gorm.DB, apply func() error) {
	t.Helper()
	original := DB
	wrapped := db.Session(&gorm.Session{NewDB: true, Context: context.Background()})
	config := *wrapped.Config
	wrapped.Config = &config
	pool := &rebuildBonusLostAckPool{ConnPool: db.ConnPool}
	wrapped.Config.ConnPool, wrapped.Statement.ConnPool = pool, pool
	DB = wrapped
	defer func() { DB = original }()
	require.ErrorIs(t, apply(), errRebuildBonusLostAck)
	require.EqualValues(t, 1, pool.committed.Load(), "a real COMMIT, not a pre-commit fault, must precede the error")
}

func TestBonusEligibilityCommitACKLossInvalidates(t *testing.T) {
	row := prepareRebuildBonusCache(t)
	require.True(t, IsModelBonusEligible(row.ModelName))
	row.BonusEligible = boolPtr(false)
	rebuildBonusLoseCommitAck(t, DB, row.Update)
	var stored Model
	require.NoError(t, DB.First(&stored, row.Id).Error)
	require.NotNil(t, stored.BonusEligible)
	require.False(t, *stored.BonusEligible, "revocation really committed before its acknowledgement was lost")
	require.False(t, IsModelBonusEligible(row.ModelName), "an unknown COMMIT must make the old positive cache cold without waiting for TTL")
}
