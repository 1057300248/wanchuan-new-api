package model

import (
	"context"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func refundDeadlineFixture(t *testing.T, tokenHeld int) BillingFinalization {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "refund-deadline.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	oldDB, oldRedis := DB, common.RedisEnabled
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	DB, common.RedisEnabled = db, false
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	t.Cleanup(func() { DB, common.RedisEnabled = oldDB, oldRedis; common.SetDatabaseTypes(oldMain, oldLog) })
	require.NoError(t, db.AutoMigrate(&User{}, &Token{}, &BalanceLedgerOperation{}, &BalanceLedger{}, &BillingFinalization{}))
	require.NoError(t, db.Create(&User{Id: 93001, Username: "refund-deadline", AffCode: "refund-deadline", Quota: 1000, QuotaPaid: 1000}).Error)
	_, err = DebitQuota(LedgerEntry{UserId: 93001, Delta: 100, Reason: LedgerReasonBillingPreConsume, ReferenceType: "relay", ReferenceId: "refund-deadline", RequestId: "refund-deadline", IdempotencyKey: "billing:preconsume:refund-deadline"}, AccountPaid)
	require.NoError(t, err)
	r := BillingFinalization{RequestID: "refund-deadline", UserID: 93001, Source: "wallet", SourceHeld: 100, PreConsumed: 100, Paid: 100}
	if tokenHeld > 0 {
		token := Token{Id: 93002, UserId: r.UserID, Key: "synthetic-deadline-token", Status: common.TokenStatusDisabled, RemainQuota: 1000 - tokenHeld, UsedQuota: tokenHeld}
		require.NoError(t, db.Create(&token).Error)
		r.TokenID, r.TokenHeld, r.TokenFingerprint = token.Id, tokenHeld, BillingTokenFingerprint(token.Key)
	}
	_, err = PersistBillingRefund(context.Background(), r)
	require.NoError(t, err)
	return r
}

func useStalledRefundRedis(t *testing.T) {
	t.Helper()
	oldClient, oldEnabled := common.RDB, common.RedisEnabled
	// A deterministic owned in-memory socket accepts bytes but never replies.
	// Disabled read/write timeouts reproduce the supported explicit Redis config;
	// only the propagated operation deadline can release this command.
	client := redis.NewClient(&redis.Options{
		ReadTimeout: -1, WriteTimeout: -1, MaxRetries: -1,
		ContextTimeoutEnabled: true, DisableIdentity: true, Protocol: 2,
		Dialer: func(context.Context, string, string) (net.Conn, error) {
			clientConn, serverConn := net.Pipe()
			go func() { _, _ = io.Copy(io.Discard, serverConn); _ = serverConn.Close() }()
			return clientConn, nil
		},
	})
	common.RDB, common.RedisEnabled = client, true
	t.Cleanup(func() { require.NoError(t, client.Close()); common.RDB, common.RedisEnabled = oldClient, oldEnabled })
}

func TestBillingRefundDeadlineIncludesTokenFenceAndUserCache(t *testing.T) {
	for _, tokenHeld := range []int{0, 100} {
		name := "postcommit_user_cache"
		if tokenHeld > 0 {
			name = "precommit_token_fence"
		}
		t.Run(name, func(t *testing.T) {
			r := refundDeadlineFixture(t, tokenHeld)
			useStalledRefundRedis(t)
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer cancel()
			done := make(chan error, 1)
			joined := false
			go func() { done <- RecoverBillingRefund(ctx, r.RequestID) }()
			defer func() {
				cancel()
				if !joined {
					_ = common.RDB.Close()
					<-done // Do not restore shared DB/cache globals while recovery is alive.
				}
			}()
			var err error
			select {
			case err = <-done:
				joined = true
			case <-time.After(2 * time.Second):
				t.Fatal("refund ignored its operation deadline in Redis")
			}
			common.RedisEnabled = false
			var stored BillingFinalization
			require.NoError(t, DB.First(&stored, "request_id = ?", r.RequestID).Error)
			var user User
			require.NoError(t, DB.First(&user, r.UserID).Error)
			if tokenHeld == 0 {
				require.NoError(t, err, "postcommit cache failure is not a financial rollback")
				assert.Equal(t, BillingRefunded, stored.State)
				assert.Equal(t, 1000, user.Quota)
			} else {
				require.Error(t, err)
				assert.Equal(t, BillingRefundPending, stored.State)
				assert.Equal(t, 900, user.Quota, "token fence timeout rolls the financial transaction back")
				var token Token
				require.NoError(t, DB.First(&token, r.TokenID).Error)
				assert.Equal(t, 900, token.RemainQuota)
			}
			// The same exact identity remains safe after either timeout boundary.
			require.NoError(t, RecoverBillingRefund(context.Background(), r.RequestID))
			require.NoError(t, RecoverBillingRefund(context.Background(), r.RequestID))
			require.NoError(t, DB.First(&user, r.UserID).Error)
			assert.Equal(t, 1000, user.Quota)
			if tokenHeld > 0 {
				var token Token
				require.NoError(t, DB.First(&token, r.TokenID).Error)
				assert.Equal(t, 1000, token.RemainQuota)
				assert.Equal(t, common.TokenStatusDisabled, token.Status, "refund must not revive authentication")
			}
		})
	}
	t.Run("expired_deadline_before_notification", testBillingRefundExpiredDeadlineBeforeNotification)
}

// This synthetic context isolates the interval after an absolute deadline but
// before its cancellation notification is delivered. It is not a substitute
// for the real socket/deadline tests above, nor a claim about timer scheduling.
type refundDelayedDeadlineContext struct {
	context.Context
	deadline time.Time
}

func (c refundDelayedDeadlineContext) Deadline() (time.Time, bool) { return c.deadline, true }

func testBillingRefundExpiredDeadlineBeforeNotification(t *testing.T) {
	r := refundDeadlineFixture(t, 100)
	useStalledRefundRedis(t)
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := refundDelayedDeadlineContext{Context: base, deadline: time.Now().Add(-time.Second)}
	require.NoError(t, ctx.Err(), "synthetic cancellation notification has not been delivered")
	done := make(chan error, 1)
	joined := false
	go func() { done <- RecoverBillingRefund(ctx, r.RequestID) }()
	defer func() {
		cancel()
		if !joined {
			_ = common.RDB.Close()
			<-done // Join before restoring shared DB/cache globals.
		}
	}()
	select {
	case err := <-done:
		joined = true
		assert.ErrorIs(t, err, context.DeadlineExceeded, "an elapsed operation budget cannot be treated as a live Redis failure")
	case <-time.After(2 * time.Second):
		t.Fatal("synthetic deadline recovery ignored its watchdog")
	}
	require.NoError(t, ctx.Err(), "rollback must not depend on notification delivery")
	common.RedisEnabled = false
	var owner BillingFinalization
	var user User
	var token Token
	require.NoError(t, DB.First(&owner, "request_id = ?", r.RequestID).Error)
	require.NoError(t, DB.First(&user, r.UserID).Error)
	require.NoError(t, DB.First(&token, r.TokenID).Error)
	assert.Equal(t, BillingRefundPending, owner.State)
	assert.Equal(t, r.UserID, owner.UserID)
	assert.Equal(t, r.TokenID, owner.TokenID)
	assert.Equal(t, r.TokenFingerprint, owner.TokenFingerprint)
	assert.Equal(t, r.SourceHeld, owner.SourceHeld)
	assert.Equal(t, r.TokenHeld, owner.TokenHeld)
	assert.Equal(t, r.Shares(), owner.Shares())
	assert.Equal(t, 900, user.Quota)
	assert.Equal(t, 900, user.QuotaPaid)
	assert.Equal(t, 900, token.RemainQuota)
	assert.Equal(t, 100, token.UsedQuota)
	assert.Equal(t, common.TokenStatusDisabled, token.Status)
	var operations, entries int64
	key := "billing:refund:" + r.RequestID
	require.NoError(t, DB.Model(&BalanceLedgerOperation{}).Where("idempotency_key = ?", key).Count(&operations).Error)
	require.NoError(t, DB.Model(&BalanceLedger{}).Where("idempotency_key = ?", key).Count(&entries).Error)
	assert.Zero(t, operations, "source credit and its idempotency owner must roll back together")
	assert.Zero(t, entries)

	require.NoError(t, RecoverBillingRefund(context.Background(), r.RequestID))
	require.NoError(t, RecoverBillingRefund(context.Background(), r.RequestID))
	require.NoError(t, DB.First(&owner, "request_id = ?", r.RequestID).Error)
	require.NoError(t, DB.First(&user, r.UserID).Error)
	require.NoError(t, DB.First(&token, r.TokenID).Error)
	assert.Equal(t, BillingRefunded, owner.State)
	assert.Equal(t, 1000, user.Quota)
	assert.Equal(t, 1000, user.QuotaPaid)
	assert.Equal(t, 1000, token.RemainQuota)
	assert.Zero(t, token.UsedQuota)
	assert.Equal(t, common.TokenStatusDisabled, token.Status)
	require.NoError(t, DB.Model(&BalanceLedgerOperation{}).Where("idempotency_key = ?", key).Count(&operations).Error)
	require.NoError(t, DB.Model(&BalanceLedger{}).Where("idempotency_key = ?", key).Count(&entries).Error)
	assert.EqualValues(t, 1, operations)
	assert.EqualValues(t, 1, entries)
}

func TestBillingRefundContextPreservesTokenFence(t *testing.T) {
	r := refundDeadlineFixture(t, 100)
	server := useUserCacheMiniRedis(t)
	var oldToken Token
	require.NoError(t, DB.First(&oldToken, r.TokenID).Error)
	_, err := cacheInitToken(oldToken)
	require.NoError(t, err)
	require.True(t, server.Exists(getTokenCacheKey(oldToken.Key)))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, RecoverBillingRefund(ctx, r.RequestID))
	assert.True(t, server.Exists(getTokenCacheFenceKey(oldToken.Key)))
	assert.False(t, server.Exists(getTokenCacheKey(oldToken.Key)))
	published, err := cacheInitToken(oldToken)
	require.NoError(t, err)
	assert.Zero(t, published, "pre-refund snapshot cannot repopulate a fenced token cache")
	var token Token
	require.NoError(t, DB.First(&token, r.TokenID).Error)
	assert.Equal(t, 1000, token.RemainQuota)
	assert.Equal(t, common.TokenStatusDisabled, token.Status)
}

func TestBillingRefundReplayInvalidatesUserCache(t *testing.T) {
	r := refundDeadlineFixture(t, 0)
	require.NoError(t, RecoverBillingRefund(context.Background(), r.RequestID))
	server := useUserCacheMiniRedis(t)
	server.HSet(getUserCacheKey(r.UserID), "Quota", "900")
	require.True(t, server.Exists(getUserCacheKey(r.UserID)))
	require.NoError(t, RecoverBillingRefund(context.Background(), r.RequestID))
	assert.False(t, server.Exists(getUserCacheKey(r.UserID)), "terminal replay repairs cache invalidation after a lost commit ACK or failed cache tail")
	var user User
	require.NoError(t, DB.First(&user, r.UserID).Error)
	assert.Equal(t, 1000, user.Quota)
}

func TestBillingRefundRedisErrorRetainsLegacyLiveContextPolicy(t *testing.T) {
	r := refundDeadlineFixture(t, 100)
	server := useUserCacheMiniRedis(t)
	server.SetError("ERR synthetic Redis unavailable")
	// Historically a Redis command error is logged, while the authoritative
	// database transaction continues. Context propagation does not change that
	// policy; an expired transaction context is a different, rollback case.
	require.NoError(t, RecoverBillingRefund(context.Background(), r.RequestID))
	var owner BillingFinalization
	require.NoError(t, DB.First(&owner, "request_id = ?", r.RequestID).Error)
	assert.Equal(t, BillingRefunded, owner.State)
	var token Token
	require.NoError(t, DB.First(&token, r.TokenID).Error)
	assert.Equal(t, 1000, token.RemainQuota)
	assert.Equal(t, common.TokenStatusDisabled, token.Status)
}
