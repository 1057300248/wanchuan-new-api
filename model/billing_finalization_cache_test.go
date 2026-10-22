package model

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Commit really reaches the database before cancellation or a lost-ACK error is
// injected. A callback on the last UPDATE would only prove precommit behavior.
type billingCacheCommitPool struct {
	*sql.DB
	afterCommit func() error
}

func (p billingCacheCommitPool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	tx, err := p.DB.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &billingCacheCommitTx{Tx: tx, afterCommit: p.afterCommit}, nil
}

type billingCacheCommitTx struct {
	*sql.Tx
	afterCommit func() error
}

func (tx billingCacheCommitTx) Commit() error {
	if err := tx.Tx.Commit(); err != nil {
		return err
	}
	return tx.afterCommit()
}

func observeBillingCacheCommit(t *testing.T, afterCommit func() error) func() {
	t.Helper()
	sqlDB, err := DB.DB()
	require.NoError(t, err)
	previousPool := DB.Statement.ConnPool
	DB.Statement.ConnPool = billingCacheCommitPool{DB: sqlDB, afterCommit: afterCommit}
	restore := func() { DB.Statement.ConnPool = previousPool }
	t.Cleanup(restore)
	return restore
}

type billingCacheDeleteHook struct {
	userID    int
	failures  int
	attempts  int
	ctxErrors []error
	started   []time.Time
	deadlines []time.Time
}

func (h *billingCacheDeleteHook) DialHook(next redis.DialHook) redis.DialHook {
	return next
}

func (h *billingCacheDeleteHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func (h *billingCacheDeleteHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		args := cmd.Args()
		if cmd.Name() != "del" || len(args) != 2 || args[1] != getUserCacheKey(h.userID) {
			return next(ctx, cmd)
		}
		h.attempts++
		h.ctxErrors = append(h.ctxErrors, ctx.Err())
		h.started = append(h.started, time.Now())
		deadline, _ := ctx.Deadline()
		h.deadlines = append(h.deadlines, deadline)
		if h.attempts <= h.failures {
			return errors.New("ERR synthetic token=do-not-log-cache-secret")
		}
		return next(ctx, cmd)
	}
}

func assertBillingCacheDeadlines(t *testing.T, hook *billingCacheDeleteHook) {
	t.Helper()
	require.NotEmpty(t, hook.ctxErrors)
	for index, ctxErr := range hook.ctxErrors {
		assert.NoError(t, ctxErr, "cache attempt must not inherit the canceled transaction context")
		deadline := hook.deadlines[index]
		assert.False(t, deadline.IsZero(), "every DEL has an independent finite deadline")
		assert.False(t, deadline.After(hook.started[index].Add(200*time.Millisecond)), "a single cache attempt has at most 200 ms")
		assert.False(t, deadline.After(hook.started[0].Add(500*time.Millisecond)), "retries share one overall 500 ms budget")
	}
}

func assertBillingCacheRefundFinal(t *testing.T, r BillingFinalization) BillingFinalization {
	t.Helper()
	var owner BillingFinalization
	var user User
	var token Token
	require.NoError(t, DB.First(&owner, "request_id = ?", r.RequestID).Error)
	require.NoError(t, DB.First(&user, r.UserID).Error)
	require.NoError(t, DB.First(&token, r.TokenID).Error)
	assert.Equal(t, BillingRefunded, owner.State)
	assert.Equal(t, r.UserID, owner.UserID)
	assert.Equal(t, r.TokenID, owner.TokenID)
	assert.Equal(t, r.TokenFingerprint, owner.TokenFingerprint)
	assert.Equal(t, r.SourceHeld, owner.SourceHeld)
	assert.Equal(t, r.TokenHeld, owner.TokenHeld)
	assert.Equal(t, r.Shares(), owner.Shares())
	assert.Zero(t, owner.Attempts, "a failed cache tail must not create financial retry history")
	assert.Zero(t, owner.NextRetryAt)
	assert.Equal(t, 1000, user.Quota)
	assert.Equal(t, 1000, user.QuotaPaid)
	assert.Equal(t, 1000, token.RemainQuota)
	assert.Zero(t, token.UsedQuota)
	assert.Equal(t, common.TokenStatusDisabled, token.Status)
	var operations, entries int64
	key := "billing:refund:" + r.RequestID
	require.NoError(t, DB.Model(&BalanceLedgerOperation{}).Where("idempotency_key = ?", key).Count(&operations).Error)
	require.NoError(t, DB.Model(&BalanceLedger{}).Where("idempotency_key = ?", key).Count(&entries).Error)
	assert.EqualValues(t, 1, operations)
	assert.EqualValues(t, 1, entries)
	return owner
}

// Native opt-ins must point at fresh, dedicated disposable databases; do not
// reuse the databases owned by the other finalization matrix suites.
func TestBillingFinalizationCacheDatabaseMatrix(t *testing.T) {
	for _, dialect := range []struct {
		name common.DatabaseType
		env  string
	}{
		{common.DatabaseTypeSQLite, ""},
		{common.DatabaseTypeMySQL, "TEST_FINALIZATION_CACHE_MYSQL_DSN"},
		{common.DatabaseTypePostgreSQL, "TEST_FINALIZATION_CACHE_POSTGRES_DSN"},
	} {
		t.Run(string(dialect.name), func(t *testing.T) {
			dsn := os.Getenv(dialect.env)
			if dialect.env != "" && dsn == "" {
				t.Skip(dialect.env + " not configured")
			}
			var driver gorm.Dialector = sqlite.Open(filepath.Join(t.TempDir(), "cache-finalization.db"))
			if dialect.name == common.DatabaseTypeMySQL {
				driver = mysql.Open(dsn)
			} else if dialect.name == common.DatabaseTypePostgreSQL {
				driver = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
			}
			db, err := gorm.Open(driver, &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			tables, err := db.Migrator().GetTables()
			require.NoError(t, err)
			require.Empty(t, tables, "dedicated cache-finalization database must be empty")
			oldDB, oldRedis := DB, common.RedisEnabled
			oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
			DB, common.RedisEnabled = db, false
			common.SetDatabaseTypes(dialect.name, dialect.name)
			t.Cleanup(func() { DB, common.RedisEnabled = oldDB, oldRedis; common.SetDatabaseTypes(oldMain, oldLog) })
			require.NoError(t, db.AutoMigrate(&User{}, &Token{}, &BalanceLedgerOperation{}, &BalanceLedger{}, &BillingFinalization{}))

			for index, scenario := range []string{"canceled_after_commit", "deadline_after_commit", "lost_commit_ack", "transient_delete_failure", "exhausted_then_terminal_replay"} {
				t.Run(scenario, func(t *testing.T) {
					id := 94000 + index
					requestID := fmt.Sprintf("cache-finalization-%d", id)
					key := fmt.Sprintf("synthetic-cache-token-%d", id)
					require.NoError(t, DB.Create(&User{Id: id, Username: requestID, AffCode: requestID, Quota: 1000, QuotaPaid: 1000}).Error)
					require.NoError(t, DB.Create(&Token{Id: id, UserId: id, Key: key, Status: common.TokenStatusDisabled, RemainQuota: 900, UsedQuota: 100}).Error)
					_, err := DebitQuota(LedgerEntry{UserId: id, Delta: 100, Reason: LedgerReasonBillingPreConsume, ReferenceType: "relay", ReferenceId: requestID, RequestId: requestID, IdempotencyKey: "billing:preconsume:" + requestID}, AccountPaid)
					require.NoError(t, err)
					r := BillingFinalization{RequestID: requestID, UserID: id, Source: "wallet", SourceHeld: 100, PreConsumed: 100, Paid: 100, TokenID: id, TokenHeld: 100, TokenFingerprint: BillingTokenFingerprint(key)}
					_, err = PersistBillingRefund(context.Background(), r)
					require.NoError(t, err)
					server := useUserCacheMiniRedis(t)
					server.HSet(getUserCacheKey(id), "Quota", "900")
					hook := &billingCacheDeleteHook{userID: id}
					common.RDB.AddHook(hook)
					if scenario == "transient_delete_failure" {
						hook.failures = 1
					} else if scenario == "exhausted_then_terminal_replay" {
						hook.failures = 2
					}

					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					if scenario == "deadline_after_commit" {
						ctx, cancel = context.WithTimeout(context.Background(), time.Second)
						defer cancel()
					}
					lostACK := errors.New("synthetic lost commit acknowledgement")
					committed := false
					restorePool := observeBillingCacheCommit(t, func() error {
						committed = true
						if scenario == "deadline_after_commit" {
							<-ctx.Done()
						} else if scenario == "canceled_after_commit" || scenario == "lost_commit_ack" {
							cancel()
						}
						if scenario == "lost_commit_ack" {
							return lostACK
						}
						return nil
					})
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
					err = RecoverBillingRefund(ctx, r.RequestID)
					restorePool()
					require.True(t, committed, "cancellation is injected only after an actual successful database COMMIT")
					if scenario == "lost_commit_ack" {
						assert.ErrorIs(t, err, lostACK, "cache compensation must preserve the uncertain financial error")
					} else {
						require.NoError(t, err, "a cache failure must not report a committed refund as rolled back")
					}
					if scenario == "deadline_after_commit" {
						assert.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
					} else if scenario == "canceled_after_commit" || scenario == "lost_commit_ack" {
						assert.ErrorIs(t, ctx.Err(), context.Canceled)
					}
					assertBillingCacheDeadlines(t, hook)
					wantAttempts := 1
					if hook.failures > 0 {
						wantAttempts = 2
					}
					assert.Equal(t, wantAttempts, hook.attempts)
					owner := assertBillingCacheRefundFinal(t, r)
					if scenario == "exhausted_then_terminal_replay" {
						assert.True(t, server.Exists(getUserCacheKey(id)), "finite best-effort retries are not a durable cache journal")
						common.LogWriterMu.Lock()
						logText := logs.String()
						common.LogWriterMu.Unlock()
						assert.Contains(t, logText, "billing user cache invalidation exhausted:")
						assert.Contains(t, logText, "attempts=2")
						assert.Contains(t, logText, "cache_only=true")
						assert.NotContains(t, logText, "do-not-log-cache-secret")
						completed, err := RecoverPendingBillingRefunds(context.Background(), 10)
						require.NoError(t, err)
						assert.Zero(t, completed, "cache failure cannot requeue terminal money")
						assert.True(t, server.Exists(getUserCacheKey(id)))
					} else {
						assert.False(t, server.Exists(getUserCacheKey(id)))
					}
					require.NoError(t, RecoverBillingRefund(context.Background(), r.RequestID))
					assert.False(t, server.Exists(getUserCacheKey(id)), "terminal replay can repair cache-only exhaustion")
					assert.Equal(t, owner, assertBillingCacheRefundFinal(t, r), "cache-only replay must preserve the immutable financial receipt and outcome")
				})
			}
		})
	}
}

func TestBillingFinalizationCacheStalledRedisIsBounded(t *testing.T) {
	r := refundDeadlineFixture(t, 0)
	oldClient, oldEnabled := common.RDB, common.RedisEnabled
	var sockets sync.WaitGroup
	var socketsMu sync.Mutex
	var serverSockets []net.Conn
	socketsClosed := false
	closeSockets := func() {
		socketsMu.Lock()
		socketsClosed = true
		for _, conn := range serverSockets {
			_ = conn.Close()
		}
		socketsMu.Unlock()
	}
	client := redis.NewClient(&redis.Options{
		ReadTimeout: -1, WriteTimeout: -1, MaxRetries: -1,
		ContextTimeoutEnabled: true, DisableIdentity: true, Protocol: 2,
		Dialer: func(context.Context, string, string) (net.Conn, error) {
			clientConn, serverConn := net.Pipe()
			socketsMu.Lock()
			if socketsClosed {
				socketsMu.Unlock()
				_ = clientConn.Close()
				_ = serverConn.Close()
				return nil, net.ErrClosed
			}
			serverSockets = append(serverSockets, serverConn)
			sockets.Go(func() { _, _ = io.Copy(io.Discard, serverConn); _ = serverConn.Close() })
			socketsMu.Unlock()
			return clientConn, nil
		},
	})
	common.RDB, common.RedisEnabled = client, true
	t.Cleanup(func() {
		closeErr := client.Close()
		// Failed Redis handshakes need not retain the peer in the client pool;
		// this fixture owns both ends and must close every peer before joining.
		closeSockets()
		sockets.Wait()
		common.RDB, common.RedisEnabled = oldClient, oldEnabled
		assert.NoError(t, closeErr)
	})
	hook := &billingCacheDeleteHook{userID: r.UserID}
	common.RDB.AddHook(hook)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	committed := false
	restorePool := observeBillingCacheCommit(t, func() error { committed = true; cancel(); return nil })
	done := make(chan error, 1)
	joined := false
	started := time.Now()
	go func() { done <- RecoverBillingRefund(ctx, r.RequestID) }()
	defer func() {
		if !joined {
			closeSockets()
			_ = common.RDB.Close()
			<-done
		}
	}()
	select {
	case err := <-done:
		joined = true
		require.NoError(t, err, "stalled postcommit Redis must not undo financial success")
		t.Logf("stalled postcommit Redis returned after %s", time.Since(started))
	case <-time.After(2 * time.Second):
		t.Fatal("postcommit cache invalidation exceeded its independent bounded budget")
	}
	restorePool()
	assert.True(t, committed)
	assert.ErrorIs(t, ctx.Err(), context.Canceled)
	assert.Equal(t, 2, hook.attempts)
	assertBillingCacheDeadlines(t, hook)
	var owner BillingFinalization
	var user User
	require.NoError(t, DB.First(&owner, "request_id = ?", r.RequestID).Error)
	require.NoError(t, DB.First(&user, r.UserID).Error)
	assert.Equal(t, BillingRefunded, owner.State)
	assert.Zero(t, owner.Attempts)
	assert.Zero(t, owner.NextRetryAt)
	assert.Equal(t, 1000, user.Quota)
}
