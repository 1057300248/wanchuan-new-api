package model

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestBillingRefundRetryClassificationAndSchedule(t *testing.T) {
	for _, tc := range []struct {
		name, code, class string
		err               error
	}{
		{"unknown_secret", "recovery_error", billingRefundRetryable, errors.New("SQL token=synthetic-do-not-store-password identity mismatch")},
		{"timeout", "deadline_exceeded", billingRefundRetryable, fmt.Errorf("wrapped: %w", context.DeadlineExceeded)},
		{"cancel", "canceled", billingRefundRetryable, context.Canceled},
		{"identity", "identity_mismatch", billingRefundReview, billingRefundReviewError("identity_mismatch", "original billing token identity mismatch")},
		{"missing", "missing_record", billingRefundReview, gorm.ErrRecordNotFound},
		{"period_unknown", "subscription_period", billingRefundReview, ErrSubscriptionPeriodUnknown},
		{"metadata", "metadata_write_failed", billingRefundRetryable, &billingRefundMetadataError{cause: errors.New("private SQL")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, class := BillingRefundErrorCode(tc.err)
			assert.Equal(t, tc.code, code)
			assert.Equal(t, tc.class, class)
			assert.NotContains(t, code+class, "synthetic")
		})
	}
	for _, tc := range []struct {
		attempts int
		delay    int64
	}{
		{math.MinInt, 30}, {0, 30}, {1, 30}, {2, 60}, {5, 480}, {7, 1920}, {8, 3600}, {math.MaxInt, 3600},
	} {
		assert.Equal(t, int64(1000)+tc.delay, billingRefundRetryAt(1000, tc.attempts, billingRefundRetryable))
	}
	assert.Equal(t, int64(4600), billingRefundRetryAt(1000, 1, billingRefundReview))
	assert.Equal(t, int64(math.MaxInt64), billingRefundRetryAt(math.MaxInt64-10, 1, billingRefundRetryable))
}

// Exact pre-operations terminal schema. It is intentionally independent of the
// current model so additive defaults and old pending rows are actually tested.
type billingFinalizationBeforeOps struct {
	RequestID          string `gorm:"type:varchar(64);primaryKey"`
	UserID             int    `gorm:"not null;index"`
	Source             string `gorm:"type:varchar(16);not null"`
	TokenID            int    `gorm:"not null"`
	TokenFingerprint   string `gorm:"type:varchar(64);not null"`
	SourceHeld         int    `gorm:"type:bigint;not null"`
	TokenHeld          int    `gorm:"type:bigint;not null"`
	PreConsumed        int    `gorm:"type:bigint;not null"`
	Paid               int    `gorm:"type:bigint;not null"`
	Bonus              int    `gorm:"type:bigint;not null"`
	Reward             int    `gorm:"type:bigint;not null"`
	SubscriptionID     int    `gorm:"not null"`
	SubscriptionPeriod int64  `gorm:"type:bigint;not null"`
	State              string `gorm:"type:varchar(24);not null;index:idx_billing_refund_queue,priority:1"`
	ActualQuota        int    `gorm:"type:bigint;not null"`
	TaskID             int64  `gorm:"not null"`
	TaskPublicID       string `gorm:"type:varchar(191);not null"`
	Attempts           int    `gorm:"not null"`
	CreatedAt          int64  `gorm:"autoCreateTime"`
	UpdatedAt          int64  `gorm:"autoUpdateTime;index:idx_billing_refund_queue,priority:2"`
}

func (billingFinalizationBeforeOps) TableName() string { return "billing_finalizations" }

// Only fresh dedicated databases are accepted; this is never a generic TEST_DSN.
func TestBillingRefundOperationsDatabaseMatrix(t *testing.T) {
	for _, dialect := range []struct {
		name common.DatabaseType
		env  string
	}{
		{common.DatabaseTypeSQLite, ""},
		{common.DatabaseTypeMySQL, "TEST_REFUND_OPS_MYSQL_DSN"},
		{common.DatabaseTypePostgreSQL, "TEST_REFUND_OPS_POSTGRES_DSN"},
	} {
		t.Run(string(dialect.name), func(t *testing.T) {
			dsn := os.Getenv(dialect.env)
			if dialect.env != "" && dsn == "" {
				t.Skip(dialect.env + " not configured")
			}
			var driver gorm.Dialector = sqlite.Open(":memory:")
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
			require.Empty(t, tables, "refusing existing database")
			oldDB, oldRedis := DB, common.RedisEnabled
			oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
			DB, common.RedisEnabled = db, false
			common.SetDatabaseTypes(dialect.name, dialect.name)
			t.Cleanup(func() { DB, common.RedisEnabled = oldDB, oldRedis; common.SetDatabaseTypes(oldMain, oldLog) })
			require.NoError(t, db.AutoMigrate(&User{}, &Token{}, &BalanceLedgerOperation{}, &BalanceLedger{}, &UserSubscription{}, &SubscriptionPreConsumeRecord{}))
			var version string
			versionSQL := "SELECT version()"
			if dialect.name == common.DatabaseTypeSQLite {
				versionSQL = "SELECT sqlite_version()"
			}
			require.NoError(t, db.Raw(versionSQL).Scan(&version).Error)
			t.Logf("refund operations matrix engine=%s version=%s", dialect.name, version)
			for _, mode := range []string{"upgrade", "fresh"} {
				t.Run(mode, func(t *testing.T) {
					var historical []billingFinalizationBeforeOps
					if mode == "upgrade" {
						require.NoError(t, db.AutoMigrate(&billingFinalizationBeforeOps{}))
						now := common.GetTimestamp()
						require.NoError(t, db.Create(&User{Id: 80002, Username: "upgrade-held", AffCode: "upgrade-held", Quota: 1000, QuotaPaid: 1000}).Error)
						require.NoError(t, db.Create(&Token{Id: 80002, UserId: 80002, Key: "synthetic-upgrade-original", RemainQuota: 900, UsedQuota: 100}).Error)
						_, err := DebitQuota(LedgerEntry{UserId: 80002, Delta: 100, Reason: LedgerReasonBillingPreConsume, ReferenceType: "relay", ReferenceId: "pre-metadata-held", RequestId: "pre-metadata-held", IdempotencyKey: "billing:preconsume:pre-metadata-held"}, AccountPaid)
						require.NoError(t, err)
						historical = []billingFinalizationBeforeOps{
							{RequestID: "pre-metadata-pending", UserID: 80001, Source: "wallet", State: BillingRefundPending, Attempts: 3, CreatedAt: now - 10, UpdatedAt: now - 5},
							{RequestID: "pre-metadata-held", UserID: 80002, Source: "wallet", TokenID: 80002, TokenFingerprint: BillingTokenFingerprint("synthetic-upgrade-original"), SourceHeld: 100, TokenHeld: 100, PreConsumed: 100, Paid: 100, State: BillingRefundPending, Attempts: 2, CreatedAt: now - 20, UpdatedAt: now - 4},
							{RequestID: "pre-metadata-task", UserID: 80003, Source: "subscription", TokenID: 80003, TokenFingerprint: BillingTokenFingerprint("synthetic-task-original"), SourceHeld: 81, TokenHeld: 73, PreConsumed: 79, SubscriptionID: 80004, SubscriptionPeriod: 9, State: BillingTaskOwned, TaskID: 87000, TaskPublicID: "original-public-destination", CreatedAt: now - 30, UpdatedAt: now - 3},
							{RequestID: "pre-metadata-settled", UserID: 80004, Source: "wallet", SourceHeld: 100, PreConsumed: 100, Paid: 60, Bonus: 30, Reward: 10, State: BillingSettled, ActualQuota: 180, CreatedAt: now - 40, UpdatedAt: now - 2},
							{RequestID: "pre-metadata-refunded", UserID: 80005, Source: "subscription", SourceHeld: 91, PreConsumed: 91, SubscriptionID: 80006, SubscriptionPeriod: 11, State: BillingRefunded, Attempts: 4, CreatedAt: now - 50, UpdatedAt: now - 1},
						}
						require.NoError(t, db.Create(&historical).Error)
					} else {
						// This table belongs only to this empty, dedicated fixture DB.
						require.NoError(t, db.Migrator().DropTable(&BillingFinalization{}))
					}
					for migration := range 2 {
						recorder := &migrationSQLRecorder{}
						require.NoError(t, db.Session(&gorm.Session{Logger: recorder}).AutoMigrate(&BillingFinalization{}))
						if migration > 0 {
							assert.Empty(t, recorder.schemaMutations(), "repeated migration must issue no schema-changing DDL")
						}
						for _, before := range historical {
							var after billingFinalizationBeforeOps
							require.NoError(t, db.First(&after, "request_id = ?", before.RequestID).Error)
							assert.Equal(t, before, after, "every original receipt field must survive each additive migration")
						}
					}
					indexes, err := db.Migrator().GetIndexes(&BillingFinalization{})
					require.NoError(t, err)
					var dueColumns []string
					for _, index := range indexes {
						if index.Name() == "idx_billing_refund_due" {
							dueColumns = index.Columns()
						}
					}
					if dialect.name == common.DatabaseTypePostgreSQL {
						dueColumns = nil
						require.NoError(t, db.Raw(`SELECT pg_get_indexdef(i.indexrelid, n, true)
FROM pg_catalog.pg_index i JOIN pg_catalog.pg_class idx ON idx.oid = i.indexrelid
CROSS JOIN generate_series(1, i.indnatts) AS n
WHERE i.indrelid = to_regclass(?) AND idx.relname = ? ORDER BY n`, "billing_finalizations", "idx_billing_refund_due").Scan(&dueColumns).Error)
					}
					assert.Equal(t, []string{"state", "next_retry_at", "request_id"}, dueColumns)
					require.True(t, db.Migrator().HasIndex(&BillingFinalization{}, "idx_billing_refund_due"))
					require.True(t, db.Migrator().HasIndex(&BillingFinalization{}, "idx_billing_refund_queue"))
					if mode == "upgrade" {
						var old BillingFinalization
						require.NoError(t, db.First(&old, "request_id = ?", "pre-metadata-pending").Error)
						require.Equal(t, 3, old.Attempts)
						require.Zero(t, old.NextRetryAt)
						require.Zero(t, old.EscalatedAt)
						require.Empty(t, old.LastErrorCode)
						require.NoError(t, db.Create(&User{Id: 80001, Username: "upgrade-pending", AffCode: "upgrade-pending"}).Error)
						n, err := RecoverPendingBillingRefunds(context.Background(), 100)
						require.NoError(t, err)
						require.Equal(t, 2, n, "zero-held and real nonzero old pending rows with default next_retry_at=0 remain eligible")
						require.Error(t, db.Create(&old).Error, "original owner uniqueness survives repeated migration")
					}
					serial := 0
					seed := func(t *testing.T, request string) BillingFinalization {
						t.Helper()
						serial++
						id := 81000 + serial
						if mode == "fresh" {
							id += 1000
						}
						require.NoError(t, db.Create(&User{Id: id, Username: request, AffCode: request, Quota: 1000, QuotaPaid: 1000}).Error)
						key := fmt.Sprintf("synthetic-ops-%d", id)
						require.NoError(t, db.Create(&Token{Id: id, UserId: id, Key: key, RemainQuota: 900, UsedQuota: 100}).Error)
						_, err := DebitQuota(LedgerEntry{UserId: id, Delta: 100, Reason: LedgerReasonBillingPreConsume, ReferenceType: "relay", ReferenceId: request, RequestId: request, IdempotencyKey: "billing:preconsume:" + request}, AccountPaid)
						require.NoError(t, err)
						r := BillingFinalization{RequestID: request, UserID: id, Source: "wallet", TokenID: id, TokenFingerprint: BillingTokenFingerprint(key), SourceHeld: 100, TokenHeld: 100, PreConsumed: 100, Paid: 100}
						_, err = PersistBillingRefund(context.Background(), r)
						require.NoError(t, err)
						return r
					}
					if dialect.name != common.DatabaseTypeSQLite {
						t.Run("held_source_lock_respects_deadline", func(t *testing.T) {
							r := seed(t, mode+"-lock-deadline")
							sqlDB.SetMaxOpenConns(2)
							defer sqlDB.SetMaxOpenConns(1)
							blocker := db.Begin()
							require.NoError(t, blocker.Error)
							defer blocker.Rollback()
							var locked User
							require.NoError(t, lockForUpdate(blocker).First(&locked, r.UserID).Error)
							// Successful locking read is the synchronization barrier: no
							// sleep, hook or mock substitutes for an actual engine lock.
							ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
							defer cancel()
							done := make(chan error, 1)
							joined := false
							go func() { done <- RecoverBillingRefund(ctx, r.RequestID) }()
							defer func() {
								cancel()
								_ = blocker.Rollback().Error
								if !joined {
									<-done
								} // Join before pool/global restoration, including fatal assertions.
							}()
							select {
							case err := <-done:
								joined = true
								require.Error(t, err, "held authoritative source lock must time out without money movement")
							case <-time.After(2 * time.Second):
								t.Fatal("actual engine row-lock wait ignored recovery deadline")
							}
							require.NoError(t, blocker.Rollback().Error)
							var owner BillingFinalization
							var user User
							var token Token
							require.NoError(t, db.First(&owner, "request_id = ?", r.RequestID).Error)
							require.NoError(t, db.First(&user, r.UserID).Error)
							require.NoError(t, db.First(&token, r.TokenID).Error)
							assert.Equal(t, BillingRefundPending, owner.State)
							assert.Equal(t, 900, user.Quota)
							assert.Equal(t, 900, token.RemainQuota)
							require.NoError(t, RecoverBillingRefund(context.Background(), r.RequestID))
							require.NoError(t, RecoverBillingRefund(context.Background(), r.RequestID))
							require.NoError(t, db.First(&user, r.UserID).Error)
							require.NoError(t, db.First(&token, r.TokenID).Error)
							assert.Equal(t, 1000, user.Quota)
							assert.Equal(t, 1000, token.RemainQuota)
						})
					}
					t.Run("poison_prefix_does_not_starve_refunds", func(t *testing.T) {
						// More poison rows than the batch limit used below; money stays
						// held while later healthy authorized work advances each pass.
						poison := make([]BillingFinalization, 0, 6)
						for i := range 6 {
							r := seed(t, fmt.Sprintf("%s-a-poison-%d", mode, i))
							require.NoError(t, db.Model(&Token{}).Where("id = ?", r.TokenID).Update("key", fmt.Sprintf("replacement-%s-%d", mode, i)).Error)
							poison = append(poison, r)
						}
						healthy := seed(t, mode+"-z-healthy")
						for range 3 {
							n, err := RecoverPendingBillingRefunds(context.Background(), 2)
							require.Error(t, err)
							require.Zero(t, n)
						}
						n, err := RecoverPendingBillingRefunds(context.Background(), 2)
						require.NoError(t, err)
						require.Equal(t, 1, n)
						for _, r := range poison {
							var stored BillingFinalization
							require.NoError(t, db.First(&stored, "request_id = ?", r.RequestID).Error)
							assert.Equal(t, BillingRefundPending, stored.State)
							assert.Equal(t, 1, stored.Attempts)
							assert.Equal(t, "identity_mismatch", stored.LastErrorCode)
							assert.Equal(t, billingRefundReview, stored.LastErrorClass)
							assert.Positive(t, stored.EscalatedAt)
							assert.Equal(t, int64(3600), stored.NextRetryAt-stored.LastAttemptAt)
							var user User
							require.NoError(t, db.First(&user, r.UserID).Error)
							assert.Equal(t, 900, user.Quota)
						}
						var user User
						require.NoError(t, db.First(&user, healthy.UserID).Error)
						assert.Equal(t, 1000, user.Quota)
						// No hot loop when every remaining row is scheduled later.
						n, err = RecoverPendingBillingRefunds(context.Background(), 100)
						require.NoError(t, err)
						assert.Zero(t, n)
						// Repair the authoritative original identity, then its next
						// scheduled retry recovers exactly once, retaining history.
						r := poison[0]
						require.NoError(t, db.Model(&Token{}).Where("id = ?", r.TokenID).Update("key", fmt.Sprintf("synthetic-ops-%d", r.TokenID)).Error)
						require.NoError(t, db.Model(&BillingFinalization{}).Where("request_id = ?", r.RequestID).Update("next_retry_at", 0).Error)
						n, err = RecoverPendingBillingRefunds(context.Background(), 1)
						require.NoError(t, err)
						require.Equal(t, 1, n)
						require.NoError(t, RecoverBillingRefund(context.Background(), r.RequestID))
						user = User{}
						require.NoError(t, db.First(&user, r.UserID).Error)
						assert.Equal(t, 1000, user.Quota)
						var stored BillingFinalization
						require.NoError(t, db.First(&stored, "request_id = ?", r.RequestID).Error)
						assert.Equal(t, 1, stored.Attempts)
						assert.Positive(t, stored.EscalatedAt)
						assert.Zero(t, stored.NextRetryAt)
						// Late metadata after a concurrent/ACK-uncertain success may
						// neither resurrect pending nor change retry history.
						require.NoError(t, recordBillingRefundFailure(context.Background(), r.RequestID, errors.New("late"), common.GetTimestamp()+5000))
						var after BillingFinalization
						require.NoError(t, db.First(&after, "request_id = ?", r.RequestID).Error)
						assert.Equal(t, stored, after)
					})
					t.Run("new_intents_do_not_starve_older_due_retry", func(t *testing.T) {
						older := seed(t, mode+"-z-older-due")
						require.NoError(t, db.Model(&BillingFinalization{}).Where("request_id = ?", older.RequestID).Updates(map[string]any{"attempts": 1, "next_retry_at": common.GetTimestamp() - 30}).Error)
						newer := seed(t, mode+"-a-new-intent")
						n, err := RecoverPendingBillingRefunds(context.Background(), 1)
						require.NoError(t, err)
						require.Equal(t, 1, n)
						var oldState, newState BillingFinalization
						require.NoError(t, db.First(&oldState, "request_id = ?", older.RequestID).Error)
						require.NoError(t, db.First(&newState, "request_id = ?", newer.RequestID).Error)
						assert.Equal(t, BillingRefunded, oldState.State)
						assert.Equal(t, BillingRefundPending, newState.State)
						assert.Positive(t, newState.NextRetryAt, "new work must not jump ahead of all existing due retries with a zero sentinel")
						require.NoError(t, RecoverBillingRefund(context.Background(), newer.RequestID))
					})
					t.Run("failure_history_clock_and_metadata_failure", func(t *testing.T) {
						r := seed(t, mode+"-transient")
						now := common.GetTimestamp()
						privateError := errors.New("synthetic-password SQL connection error")
						for attempt := range 5 {
							require.NoError(t, recordBillingRefundFailure(context.Background(), r.RequestID, privateError, now+int64(attempt)))
						}
						var stored BillingFinalization
						require.NoError(t, db.First(&stored, "request_id = ?", r.RequestID).Error)
						assert.Equal(t, 5, stored.Attempts)
						assert.Equal(t, "recovery_error", stored.FirstErrorCode)
						assert.Equal(t, billingRefundRetryable, stored.FirstErrorClass)
						assert.Equal(t, now, stored.FirstFailedAt)
						assert.Equal(t, now+4, stored.EscalatedAt)
						assert.NotContains(t, fmt.Sprintf("%+v", stored), "synthetic-password")
						require.NoError(t, recordBillingRefundFailure(context.Background(), r.RequestID, context.DeadlineExceeded, now-1000))
						require.NoError(t, db.First(&stored, "request_id = ?", r.RequestID).Error)
						assert.Equal(t, now+4, stored.LastAttemptAt, "clock regression does not pull retries backwards")
						assert.Equal(t, "deadline_exceeded", stored.LastErrorCode)
						assert.Equal(t, "recovery_error", stored.FirstErrorCode)
						before := stored
						name := "ops-metadata-failure"
						require.NoError(t, db.Callback().Update().Before("gorm:update").Register(name, func(tx *gorm.DB) {
							if tx.Statement.Table == "billing_finalizations" {
								tx.AddError(errors.New("synthetic metadata failure"))
							}
						}))
						require.Error(t, recordBillingRefundFailure(context.Background(), r.RequestID, privateError, now+10))
						err := RecoverBillingRefund(context.Background(), r.RequestID)
						require.Error(t, err)
						code, class := BillingRefundErrorCode(err)
						assert.Equal(t, "metadata_write_failed", code)
						assert.Equal(t, billingRefundRetryable, class)
						require.NoError(t, db.Callback().Update().Remove(name))
						require.NoError(t, db.First(&stored, "request_id = ?", r.RequestID).Error)
						assert.Equal(t, before, stored, "metadata failure leaves exact pending authority intact")
						ctx, cancel := context.WithCancel(context.Background())
						cancel()
						n, err := RecoverPendingBillingRefunds(ctx, 100)
						require.ErrorIs(t, err, context.Canceled)
						assert.Zero(t, n)
					})
				})
			}
			if os.Getenv("TEST_REFUND_QUEUE_PRESSURE") == "1" {
				t.Run("long_pending_queue_pressure", func(t *testing.T) { testBillingRefundQueuePressure(t, db) })
			}
		})
	}
}

func TestBillingRefundDueRecheckDoesNotBypassSchedule(t *testing.T) {
	// The matrix proves actual money/queue behavior; this test exercises the
	// stale scan boundary without a timing-dependent race or waiting an hour.
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	oldDB := DB
	DB = db
	t.Cleanup(func() { DB = oldDB })
	require.NoError(t, db.AutoMigrate(&BillingFinalization{}))
	r := BillingFinalization{RequestID: "stale-scan", UserID: 1, Source: "wallet", State: BillingRefundPending, NextRetryAt: common.GetTimestamp() + 3600}
	require.NoError(t, db.Create(&r).Error)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done, err := recoverBillingRefund(ctx, r.RequestID, true)
	require.NoError(t, err)
	assert.False(t, done)
	var after BillingFinalization
	require.NoError(t, db.First(&after, "request_id = ?", r.RequestID).Error)
	assert.Equal(t, r, after, "a stale queue scan must not even consult or change source/token state")
}
