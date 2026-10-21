package service

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// A real child exits without running session, dispatcher or SQL cleanup. The
// parent opens only the committed SQLite file: no session or retry memory can
// cross this boundary. It must distinguish proved intent from an orphan hold.
func TestBillingRefundIntentProcessExit(t *testing.T) {
	for _, mode := range []string{"before_intent", "after_retry_commits_intent"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "refund-restart.db")
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBillingRefundIntentProcessExitHelper$", "-test.count=1")
			cmd.Env = append(os.Environ(), "TEST_REFUND_INTENT_CHILD_MODE="+mode, "TEST_REFUND_INTENT_CHILD_DB="+path)
			output, err := cmd.CombinedOutput()
			require.NoError(t, err, "child must reach the intended exit boundary: %s", output)
			db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			original := model.DB
			model.DB = db
			t.Cleanup(func() { model.DB = original })
			assertUserBalances(t, 31201, 0, 900, 0)
			var token model.Token
			require.NoError(t, db.First(&token, 31202).Error)
			require.Equal(t, 930, token.RemainQuota)
			var records []model.BillingFinalization
			require.NoError(t, db.Find(&records).Error)
			if mode == "before_intent" {
				require.Empty(t, records)
				completed, err := model.RecoverPendingBillingRefunds(context.Background(), 100)
				require.NoError(t, err)
				require.Zero(t, completed, "a restarted worker cannot invent the lost refund decision")
				assertUserBalances(t, 31201, 0, 900, 0)
				require.NoError(t, db.First(&token, 31202).Error)
				require.Equal(t, 930, token.RemainQuota)
				return
			}
			require.Len(t, records, 1)
			require.Equal(t, model.BillingRefundPending, records[0].State)
			require.Equal(t, 100, records[0].SourceHeld)
			require.Equal(t, 70, records[0].TokenHeld)
			// The retry committed the exact original decision before the process
			// exited. Recover only this proved pending owner, twice for replay.
			require.NoError(t, model.RecoverBillingRefund(context.Background(), records[0].RequestID))
			require.NoError(t, model.RecoverBillingRefund(context.Background(), records[0].RequestID))
			assertUserBalances(t, 31201, 0, 1000, 0)
			require.NoError(t, db.First(&token, 31202).Error)
			require.Equal(t, 1000, token.RemainQuota)
		})
	}
}

func TestBillingRefundIntentProcessExitHelper(t *testing.T) {
	mode := os.Getenv("TEST_REFUND_INTENT_CHILD_MODE")
	if mode == "" {
		t.Skip("invoked only by the process-exit regression test")
	}
	require.Contains(t, []string{"before_intent", "after_retry_commits_intent"}, mode)
	db, err := gorm.Open(sqlite.Open(os.Getenv("TEST_REFUND_INTENT_CHILD_DB")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.BalanceLedgerOperation{}, &model.BalanceLedger{}, &model.BillingFinalization{}))
	model.DB = db
	s := legacyRefundSession(t, "process-exit-known-refund", 100, 70)
	var first sync.Once
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("process_exit_refund_fault", func(tx *gorm.DB) {
		if tx.Statement.Table == "billing_finalizations" {
			first.Do(func() { tx.AddError(errors.New("synthetic first intent write failure")) })
		}
		if tx.Statement.Table == "users" && mode == "after_retry_commits_intent" {
			tx.AddError(errors.New("synthetic source unavailable after durable intent"))
		}
	}))
	var retrying <-chan struct{}
	if mode == "before_intent" {
		retrying, _ = pauseRefundIntentRetry(t)
	}
	useRefundDispatcher(t, 2, 1, time.Second)
	s.Refund(testGinContext(t)) // no second Refund, in either child
	if mode == "before_intent" {
		awaitRefundSignal(t, retrying)
	} else {
		waitRefundCallback(t, s)
	}
	require.True(t, s.NeedsRefund())
	os.Exit(0) // deliberately bypass every in-memory retry/drain/fixture cleanup
}
