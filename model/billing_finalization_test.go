package model

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// These opt-ins MUST name fresh, dedicated, disposable databases, never the
// service matrix's databases or generic TEST_* DSNs. No test drops a database.
func TestBillingFinalizationDatabaseMatrix(t *testing.T) {
	for _, dialect := range []struct {
		name common.DatabaseType
		env  string
	}{
		{common.DatabaseTypeSQLite, ""}, {common.DatabaseTypeMySQL, "TEST_FINALIZATION_MODEL_MYSQL_DSN"}, {common.DatabaseTypePostgreSQL, "TEST_FINALIZATION_MODEL_POSTGRES_DSN"},
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
			sqlDB.SetMaxOpenConns(8)
			if dialect.name == common.DatabaseTypeSQLite {
				sqlDB.SetMaxOpenConns(1)
			}
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			tables, err := db.Migrator().GetTables()
			require.NoError(t, err)
			require.Empty(t, tables, "dedicated database must be empty; refusing existing data")
			oldDB, oldType, oldLogType := DB, common.MainDatabaseType(), common.LogDatabaseType()
			DB = db
			common.SetDatabaseTypes(dialect.name, dialect.name)
			t.Cleanup(func() { DB = oldDB; common.SetDatabaseTypes(oldType, oldLogType) })
			// Representative previous-schema database, then additive migration.
			require.NoError(t, db.AutoMigrate(&Option{}, &User{}, &Token{}, &BalanceLedgerOperation{}, &BalanceLedger{}, &UserSubscription{}, &SubscriptionPreConsumeRecord{}, &Task{}))
			require.False(t, db.Migrator().HasTable(&BillingFinalization{}))
			require.NoError(t, db.AutoMigrate(&BillingFinalization{}))
			serial := 0
			seed := func(t *testing.T, sourceHeld, tokenHeld int) BillingFinalization {
				serial++
				id := 41000 + serial
				request := fmt.Sprintf("terminal-%d", id)
				key := fmt.Sprintf("synthetic-terminal-token-%d", id)
				require.NoError(t, db.Create(&User{Id: id, Username: request, AffCode: request, Quota: 1000, QuotaPaid: 500, QuotaBonus: 300, QuotaReward: 200}).Error)
				require.NoError(t, db.Create(&Token{Id: id, UserId: id, Key: key, RemainQuota: 1000 - tokenHeld, UsedQuota: tokenHeld}).Error)
				result := &LedgerResult{}
				if sourceHeld > 0 {
					var err error
					result, err = DebitQuota(LedgerEntry{UserId: id, Delta: sourceHeld, Reason: LedgerReasonBillingPreConsume, ReferenceType: "relay", ReferenceId: request, RequestId: request, IdempotencyKey: "billing:preconsume:" + request}, []LedgerAccount{AccountPaid, AccountBonus, AccountReward}...)
					require.NoError(t, err)
				}
				r := BillingFinalization{RequestID: request, UserID: id, Source: "wallet", TokenID: id, TokenFingerprint: BillingTokenFingerprint(key), SourceHeld: sourceHeld, TokenHeld: tokenHeld, PreConsumed: sourceHeld}
				for _, row := range result.Rows {
					switch LedgerAccount(row.Account) {
					case AccountPaid:
						r.Paid -= row.Delta
					case AccountBonus:
						r.Bonus -= row.Delta
					case AccountReward:
						r.Reward -= row.Delta
					}
				}
				return r
			}
			balances := func(t *testing.T, r BillingFinalization, wallet, tokenQuota int) {
				t.Helper()
				var u User
				var token Token
				require.NoError(t, db.First(&u, r.UserID).Error)
				require.NoError(t, db.Unscoped().First(&token, r.TokenID).Error)
				require.Equal(t, wallet, u.Quota)
				require.Equal(t, tokenQuota, token.RemainQuota)
			}
			pending := func(t *testing.T, r BillingFinalization) {
				t.Helper()
				state, err := PersistBillingRefund(context.Background(), r)
				require.NoError(t, err)
				require.Equal(t, BillingRefundPending, state)
			}
			t.Run("atomic_failure_stages", func(t *testing.T) {
				for _, stage := range []string{"source", "operation", "ledger", "token", "terminal"} {
					t.Run(stage, func(t *testing.T) {
						r := seed(t, 600, 570)
						pending(t, r)
						name := "terminal_fail_" + stage
						callback := func(tx *gorm.DB) {
							match := stage == "source" && tx.Statement.Table == "users" || stage == "operation" && tx.Statement.Table == "balance_ledger_operations" || stage == "ledger" && tx.Statement.Table == "balance_ledger" || stage == "token" && tx.Statement.Table == "tokens" || stage == "terminal" && tx.Statement.Table == "billing_finalizations"
							if match {
								tx.AddError(errors.New("synthetic " + stage + " failure"))
							}
						}
						require.NoError(t, db.Callback().Update().Before("gorm:update").Register(name, callback))
						require.NoError(t, db.Callback().Create().Before("gorm:create").Register(name, callback))
						require.Error(t, RecoverBillingRefund(context.Background(), r.RequestID))
						balances(t, r, 400, 430)
						var stored BillingFinalization
						require.NoError(t, db.First(&stored, "request_id = ?", r.RequestID).Error)
						require.Equal(t, BillingRefundPending, stored.State)
						require.NoError(t, db.Callback().Update().Remove(name))
						require.NoError(t, db.Callback().Create().Remove(name))
						// New invocation has no live session/funding instance: restart recovery.
						require.NoError(t, RecoverBillingRefund(context.Background(), r.RequestID))
						require.NoError(t, RecoverBillingRefund(context.Background(), r.RequestID))
						balances(t, r, 1000, 1000)
						var u User
						require.NoError(t, db.First(&u, r.UserID).Error)
						require.Equal(t, 500, u.QuotaPaid)
						require.Equal(t, 300, u.QuotaBonus)
						require.Equal(t, 200, u.QuotaReward)
					})
				}
			})
			t.Run("original_token_identity", func(t *testing.T) {
				for _, mode := range []string{"soft_deleted", "hard_deleted", "replacement_key", "replacement_owner", "unlimited", "no_token"} {
					t.Run(mode, func(t *testing.T) {
						r := seed(t, 100, 100)
						if mode == "no_token" {
							r.TokenID = 0
							r.TokenHeld = 0
							r.TokenFingerprint = ""
						}
						pending(t, r)
						if mode == "no_token" {
							var stored BillingFinalization
							require.NoError(t, db.First(&stored, "request_id = ?", r.RequestID).Error)
							require.Equal(t, "", stored.TokenFingerprint, "empty identity must not acquire fixed-width padding")
							require.True(t, sameBillingIdentity(r, stored))
							pending(t, r)
						}
						switch mode {
						case "soft_deleted":
							require.NoError(t, db.Delete(&Token{}, r.TokenID).Error)
						case "hard_deleted":
							require.NoError(t, db.Unscoped().Delete(&Token{}, r.TokenID).Error)
						case "replacement_key":
							require.NoError(t, db.Model(&Token{}).Where("id = ?", r.TokenID).Update("key", "replacement").Error)
						case "replacement_owner":
							require.NoError(t, db.Model(&Token{}).Where("id = ?", r.TokenID).Update("user_id", r.UserID+100000).Error)
						case "unlimited":
							require.NoError(t, db.Model(&Token{}).Where("id = ?", r.TokenID).Update("unlimited_quota", true).Error)
						}
						err := RecoverBillingRefund(context.Background(), r.RequestID)
						if mode == "replacement_key" || mode == "replacement_owner" {
							require.ErrorContains(t, err, "identity mismatch")
							balances(t, r, 900, 900)
							return
						}
						require.NoError(t, err)
						var u User
						require.NoError(t, db.First(&u, r.UserID).Error)
						require.Equal(t, 1000, u.Quota)
						if mode == "no_token" {
							require.NoError(t, RecoverBillingRefund(context.Background(), r.RequestID))
							state, err := PersistBillingRefund(context.Background(), r)
							require.NoError(t, err)
							require.Equal(t, BillingRefunded, state)
							var untouched Token
							require.NoError(t, db.First(&untouched, r.UserID).Error)
							require.Equal(t, 900, untouched.RemainQuota)
							require.Equal(t, 100, untouched.UsedQuota)
						}
						if mode == "hard_deleted" || mode == "no_token" {
							return
						}
						balances(t, r, 1000, 1000)
						if mode == "soft_deleted" {
							var token Token
							require.NoError(t, db.Unscoped().First(&token, r.TokenID).Error)
							require.True(t, token.DeletedAt.Valid)
						}
					})
				}
			})
			t.Run("receipt_mismatch_and_legacy_replay", func(t *testing.T) {
				r := seed(t, 100, 100)
				bad := r
				bad.Paid = 99
				bad.SourceHeld = 99
				_, err := PersistBillingRefund(context.Background(), bad)
				require.Error(t, err)
				// An old source-only refund is NOT proof a token/owner committed.
				_, err = CreditQuotaShares(LedgerEntry{UserId: r.UserID, Delta: 100, Reason: LedgerReasonBillingRefund, ReferenceType: "relay", ReferenceId: r.RequestID, RequestId: r.RequestID, IdempotencyKey: "billing:refund:" + r.RequestID}, r.Shares())
				require.NoError(t, err)
				_, err = PersistBillingRefund(context.Background(), r)
				require.Error(t, err)
				balances(t, r, 1000, 900)
			})
			t.Run("near_miss_realtime_key_rejected", func(t *testing.T) {
				for _, suffix := range []string{"wss:01:20", "wss:+1:20", "wss:0:20", "wss:1:21", "wss:18446744073709551616:20"} {
					t.Run(suffix, func(t *testing.T) {
						r := seed(t, 100, 100)
						_, err := DebitQuota(LedgerEntry{UserId: r.UserID, Delta: 20, Reason: LedgerReasonBillingSettle, ReferenceType: "relay", ReferenceId: r.RequestID, RequestId: r.RequestID, IdempotencyKey: "billing:settle:" + r.RequestID + ":" + suffix}, AccountPaid)
						require.NoError(t, err)
						_, err = PersistBillingRefund(context.Background(), r)
						require.Error(t, err)
						balances(t, r, 880, 900)
					})
				}
			})
			t.Run("concurrent_workers_and_first_owners", func(t *testing.T) {
				r := seed(t, 100, 100)
				var wg sync.WaitGroup
				errs := make(chan error, 8)
				for range 8 {
					wg.Add(1)
					go func() {
						defer wg.Done()
						_, err := PersistBillingRefund(context.Background(), r)
						if err == nil {
							err = RecoverBillingRefund(context.Background(), r.RequestID)
						}
						errs <- err
					}()
				}
				wg.Wait()
				close(errs)
				for err := range errs {
					require.NoError(t, err)
				}
				balances(t, r, 1000, 1000)
				changed := r
				changed.TokenHeld = 99
				_, err := PersistBillingRefund(context.Background(), changed)
				require.ErrorContains(t, err, "identity mismatch")
				r = seed(t, 100, 100)
				results := make(chan error, 2)
				wg.Add(2)
				go func() { defer wg.Done(); _, err := PersistBillingRefund(context.Background(), r); results <- err }()
				go func() {
					defer wg.Done()
					_, err := SettleBillingFinalization(context.Background(), r, 100, func(tx *gorm.DB) error { return ApplyBillingTokenDeltaTx(tx, &r, 0) })
					results <- err
				}()
				wg.Wait()
				close(results)
				successes := 0
				for err := range results {
					if err == nil {
						successes++
					} else {
						require.ErrorIs(t, err, ErrBillingFinalized)
					}
				}
				require.Equal(t, 1, successes)
			})
			t.Run("task_owner_and_evolved_quota", func(t *testing.T) {
				r := seed(t, 100, 100)
				task := &Task{TaskID: "public-" + r.RequestID, UserId: r.UserID, Quota: 100, SettlementPending: true, Data: []byte(`{"inline":true}`), PrivateData: TaskPrivateData{RequestId: r.RequestID, BillingSource: r.Source, TokenId: r.TokenID, WalletDebit: r.Shares(), PendingSettlement: &TaskSettlement{Phase: "submit", Quota: 180}}}
				require.NoError(t, InsertTaskWithBillingFinalization(context.Background(), r, task, "data"))
				var stored Task
				require.NoError(t, db.First(&stored, task.ID).Error)
				require.Empty(t, stored.Data)
				require.NotEmpty(t, task.Data)
				require.NoError(t, db.Model(&Task{}).Where("id = ?", task.ID).Update("quota", 180).Error)
				require.NoError(t, ReadCommittedBillingTask(context.Background(), r, task))
				require.NoError(t, InsertTaskWithBillingFinalization(context.Background(), r, task, "data"))
				_, err := PersistBillingRefund(context.Background(), r)
				require.ErrorIs(t, err, ErrBillingFinalized)
				require.ErrorIs(t, RecoverBillingRefund(context.Background(), r.RequestID), ErrBillingFinalized)
				other := *task
				other.TaskID = "different-destination"
				require.Error(t, InsertTaskWithBillingFinalization(context.Background(), r, &other))
			})
			t.Run("task_transfer_rollback_and_refund_race", func(t *testing.T) {
				makeTask := func(r BillingFinalization) *Task {
					return &Task{TaskID: "bound-" + r.RequestID, UserId: r.UserID, Quota: r.PreConsumed, SettlementPending: true, PrivateData: TaskPrivateData{RequestId: r.RequestID, BillingSource: r.Source, TokenId: r.TokenID, WalletDebit: r.Shares(), PendingSettlement: &TaskSettlement{Phase: "submit", Quota: 180}}}
				}
				for _, stage := range []string{"insert", "owner"} {
					t.Run(stage, func(t *testing.T) {
						r := seed(t, 100, 100)
						task := makeTask(r)
						name := "fail_task_" + stage
						cb := func(tx *gorm.DB) {
							if stage == "insert" && tx.Statement.Table == "tasks" || stage == "owner" && tx.Statement.Table == "billing_finalizations" {
								tx.AddError(errors.New("task handoff failed"))
							}
						}
						require.NoError(t, db.Callback().Create().Before("gorm:create").Register(name, cb))
						require.NoError(t, db.Callback().Update().Before("gorm:update").Register(name, cb))
						require.Error(t, InsertTaskWithBillingFinalization(context.Background(), r, task))
						require.NoError(t, db.Callback().Create().Remove(name))
						require.NoError(t, db.Callback().Update().Remove(name))
						var count int64
						require.NoError(t, db.Model(&Task{}).Where("task_id = ?", task.TaskID).Count(&count).Error)
						require.Zero(t, count)
						require.NoError(t, db.Model(&BillingFinalization{}).Where("request_id = ?", r.RequestID).Count(&count).Error)
						require.Zero(t, count)
						pending(t, r)
						require.NoError(t, RecoverBillingRefund(context.Background(), r.RequestID))
						balances(t, r, 1000, 1000)
					})
				}
				r := seed(t, 100, 100)
				task := makeTask(r)
				var wg sync.WaitGroup
				results := make(chan error, 2)
				wg.Add(2)
				go func() { defer wg.Done(); _, err := PersistBillingRefund(context.Background(), r); results <- err }()
				go func() { defer wg.Done(); results <- InsertTaskWithBillingFinalization(context.Background(), r, task) }()
				wg.Wait()
				close(results)
				successes := 0
				for err := range results {
					if err == nil {
						successes++
					} else {
						require.ErrorIs(t, err, ErrBillingFinalized)
					}
				}
				require.Equal(t, 1, successes)
				var owner BillingFinalization
				require.NoError(t, db.First(&owner, "request_id = ?", r.RequestID).Error)
				if owner.State == BillingRefundPending {
					require.NoError(t, RecoverBillingRefund(context.Background(), r.RequestID))
					balances(t, r, 1000, 1000)
				} else {
					require.Equal(t, BillingTaskOwned, owner.State)
					require.ErrorIs(t, RecoverBillingRefund(context.Background(), r.RequestID), ErrBillingFinalized)
					balances(t, r, 900, 900)
				}
			})
			t.Run("subscription_original_period", func(t *testing.T) {
				for _, mode := range []string{"cumulative", "period_reset", "missing_subscription", "unknown_period", "receipt_changed"} {
					t.Run(mode, func(t *testing.T) {
						r := seed(t, 0, 200)
						r.Source = "subscription"
						r.SourceHeld = 200
						r.PreConsumed = 200
						r.SubscriptionID = r.UserID
						r.SubscriptionPeriod = 1
						sub := UserSubscription{Id: r.SubscriptionID, UserId: r.UserID, AmountTotal: 1000, AmountUsed: 700, QuotaPeriod: 1, Status: "active", EndTime: common.GetTimestamp() + 3600}
						require.NoError(t, db.Create(&sub).Error)
						record := SubscriptionPreConsumeRecord{RequestId: r.RequestID, UserId: r.UserID, UserSubscriptionId: r.SubscriptionID, PreConsumed: 100, ReservedAmount: 200, QuotaPeriod: 1, Status: "consumed"}
						require.NoError(t, db.Create(&record).Error)
						pending(t, r)
						switch mode {
						case "period_reset":
							require.NoError(t, db.Model(&sub).Updates(map[string]any{"quota_period": 2, "amount_used": 100}).Error)
						case "missing_subscription":
							require.NoError(t, db.Delete(&sub).Error)
						case "unknown_period":
							require.NoError(t, db.Model(&record).Update("quota_period", 0).Error)
						case "receipt_changed":
							require.NoError(t, db.Model(&record).Update("reserved_amount", 300).Error)
						}
						err := RecoverBillingRefund(context.Background(), r.RequestID)
						if mode == "unknown_period" || mode == "receipt_changed" {
							require.Error(t, err)
							balances(t, r, 1000, 800)
							return
						}
						require.NoError(t, err)
						require.NoError(t, RecoverBillingRefund(context.Background(), r.RequestID))
						balances(t, r, 1000, 1000)
						if mode != "missing_subscription" {
							var current UserSubscription
							require.NoError(t, db.First(&current, r.SubscriptionID).Error)
							expected := int64(500)
							if mode == "period_reset" {
								expected = 100
							}
							require.Equal(t, expected, current.AmountUsed)
						}
						require.NoError(t, db.First(&record, record.Id).Error)
						require.Equal(t, "refunded", record.Status)
					})
				}
			})
			t.Run("migration_preserves_pending_and_tombstones", func(t *testing.T) {
				r := seed(t, 100, 100)
				pending(t, r)
				empty := seed(t, 100, 0)
				empty.TokenID, empty.TokenFingerprint = 0, ""
				pending(t, empty)
				before := []BillingFinalization{}
				require.NoError(t, db.Order("request_id").Find(&before).Error)
				// Reproduce the initial fixed-width schema on this owned fixture,
				// including existing empty no-token and full SHA-256 identities.
				legacy := struct {
					TokenFingerprint string `gorm:"type:char(64);not null"`
				}{}
				require.NoError(t, db.Table("billing_finalizations").Migrator().AlterColumn(&legacy, "TokenFingerprint"))
				if dialect.name == common.DatabaseTypePostgreSQL {
					var padded string
					require.NoError(t, db.Model(&BillingFinalization{}).Select("token_fingerprint").Where("request_id = ?", empty.RequestID).Scan(&padded).Error)
					require.Equal(t, 64, len(padded), "legacy PostgreSQL CHAR identity padding reproducer")
				}
				require.NoError(t, db.AutoMigrate(&BillingFinalization{}))
				recorder := &migrationSQLRecorder{}
				require.NoError(t, db.Session(&gorm.Session{Logger: recorder}).AutoMigrate(&BillingFinalization{}))
				require.Empty(t, recorder.schemaMutations(), "second migration must not repeat schema changes")
				after := []BillingFinalization{}
				require.NoError(t, db.Order("request_id").Find(&after).Error)
				require.Equal(t, before, after)
				require.NoError(t, RecoverBillingRefund(context.Background(), r.RequestID))
				require.NoError(t, RecoverBillingRefund(context.Background(), empty.RequestID))
				require.NoError(t, RecoverBillingRefund(context.Background(), empty.RequestID))
				balances(t, r, 1000, 1000)
				empty.TokenID = empty.UserID // inspect the unrelated unchanged fixture token
				balances(t, empty, 1000, 1000)
			})
			t.Run("rollback_probe_no_permanent_owner", func(t *testing.T) {
				r := seed(t, 100, 100)
				stored, err := ProbeBillingFinalization(context.Background(), r)
				require.NoError(t, err)
				require.Nil(t, stored)
				var count int64
				require.NoError(t, db.Model(&BillingFinalization{}).Where("request_id = ?", r.RequestID).Count(&count).Error)
				require.Zero(t, count)
				malformed := r
				malformed.State = "building"
				require.NoError(t, db.Create(&malformed).Error)
				_, err = ProbeBillingFinalization(context.Background(), r)
				require.ErrorContains(t, err, "invalid persisted")
			})
		})
	}
}
