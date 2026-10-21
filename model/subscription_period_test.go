package model

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

func subscriptionPeriodDatabase(t *testing.T, kind common.DatabaseType, dsn string) *gorm.DB {
	t.Helper()
	fixtureName := fmt.Sprintf("speriod_%x", time.Now().UnixNano())
	var driver gorm.Dialector = sqlite.Open(filepath.Join(t.TempDir(), "period.db"))
	if kind == common.DatabaseTypeMySQL {
		driver = mysql.Open(dsn)
	}
	if kind == common.DatabaseTypePostgreSQL {
		// Explicit index names are schema-global on PostgreSQL. A table prefix
		// alone collides with idx_user_sub_active from production startup tests.
		admin, err := gorm.Open(postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}), &gorm.Config{})
		require.NoError(t, err)
		adminSQL, err := admin.DB()
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, adminSQL.Close()) })
		var existingIndex []string
		indexQuery := "SELECT indexdef FROM pg_catalog.pg_indexes WHERE schemaname = current_schema() AND indexname = ?"
		require.NoError(t, admin.Raw(indexQuery, "idx_user_sub_active").Scan(&existingIndex).Error)
		t.Cleanup(func() {
			var preservedIndex []string
			require.NoError(t, admin.Raw(indexQuery, "idx_user_sub_active").Scan(&preservedIndex).Error)
			assert.Equal(t, existingIndex, preservedIndex, "the pre-existing schema's index must remain untouched")
		})
		require.NoError(t, admin.Exec("CREATE SCHEMA ?", clause.Table{Name: fixtureName}).Error)
		t.Cleanup(func() {
			require.NoError(t, admin.Exec("DROP SCHEMA ? CASCADE", clause.Table{Name: fixtureName}).Error)
		})
		driver = postgres.New(postgres.Config{
			DSN: dsn, PreferSimpleProtocol: true,
			OptionOpenDB: []stdlib.OptionOpenDB{stdlib.OptionBeforeConnect(func(_ context.Context, config *pgx.ConnConfig) error {
				config.RuntimeParams["search_path"] = fixtureName
				return nil
			})},
		})
	}
	naming := schema.NamingStrategy{TablePrefix: fixtureName + "_"}
	db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: naming})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(8)
	if kind == common.DatabaseTypeSQLite {
		sqlDB.SetMaxOpenConns(1)
	}
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	if kind == common.DatabaseTypePostgreSQL {
		// Hold two physical connections simultaneously: SET on just the first
		// connection would silently send concurrent work back into public.
		func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			for range 2 {
				conn, err := sqlDB.Conn(ctx)
				require.NoError(t, err)
				defer conn.Close()
				var actualSchema string
				require.NoError(t, conn.QueryRowContext(ctx, "SELECT current_schema()").Scan(&actualSchema))
				require.Equal(t, fixtureName, actualSchema)
			}
		}()
	}
	for _, item := range []any{&SubscriptionPlan{}, &UserSubscription{}, &SubscriptionPreConsumeRecord{}, &Option{}} {
		require.False(t, db.Migrator().HasTable(item), "fixture must never reuse an existing table")
	}
	t.Cleanup(func() {
		require.NoError(t, db.Migrator().DropTable(&SubscriptionPreConsumeRecord{}, &UserSubscription{}, &SubscriptionPlan{}, &Option{}))
	})
	require.NoError(t, db.AutoMigrate(&Option{}))
	previousDB, previousKind, previousRedis := DB, common.MainDatabaseType(), common.RedisEnabled
	DB = db
	common.SetMainDatabaseType(kind)
	common.RedisEnabled = false
	t.Cleanup(func() { DB = previousDB; common.SetMainDatabaseType(previousKind); common.RedisEnabled = previousRedis })
	query := "SELECT version()"
	if kind == common.DatabaseTypeSQLite {
		query = "SELECT sqlite_version()"
	}
	var version string
	require.NoError(t, db.Raw(query).Scan(&version).Error)
	t.Logf("%s version: %s", kind, version)
	return db
}

func subscriptionPeriodSeed(t *testing.T, db *gorm.DB) (*SubscriptionPlan, *UserSubscription) {
	t.Helper()
	now := GetDBTimestamp(db)
	plan := &SubscriptionPlan{Title: "period fixture", TotalAmount: 1000, QuotaResetPeriod: SubscriptionResetCustom, QuotaResetCustomSeconds: 60}
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(plan).Error)
	InvalidateSubscriptionPlanCache(plan.Id)
	t.Cleanup(func() { InvalidateSubscriptionPlanCache(plan.Id) })
	sub := &UserSubscription{UserId: 73, PlanId: plan.Id, AmountTotal: 1000, StartTime: now - 30, EndTime: now + 3600, Status: "active", LastResetTime: now - 30, NextResetTime: now + 30}
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(sub).Error)
	return plan, sub
}

func subscriptionPeriodReset(t *testing.T, db *gorm.DB, plan *SubscriptionPlan, id int, automatic, advance bool, at int64) {
	t.Helper()
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var sub UserSubscription
		if err := lockForUpdate(tx).First(&sub, id).Error; err != nil {
			return err
		}
		if automatic {
			return maybeResetUserSubscriptionWithPlanTx(tx, &sub, plan, at)
		}
		return resetUserSubscriptionTx(tx, &sub, plan, at, advance)
	}))
}

func TestSubscriptionPeriodAccountingDatabaseMatrix(t *testing.T) {
	for _, dialect := range []struct {
		kind common.DatabaseType
		env  string
	}{{common.DatabaseTypeSQLite, ""}, {common.DatabaseTypeMySQL, "TEST_MYSQL_DSN"}, {common.DatabaseTypePostgreSQL, "TEST_POSTGRES_DSN"}} {
		t.Run(string(dialect.kind), func(t *testing.T) {
			dsn := strings.TrimSpace(os.Getenv(dialect.env))
			if dialect.env != "" && dsn == "" {
				t.Skip(dialect.env + " is not configured")
			}
			for _, mode := range []string{"same_period_settle", "same_period_upward", "same_period_zero", "same_period_refund", "quota_ceiling", "journal_identity", "preconsume_replay", "parallel_preconsume", "auto_reset_settle", "auto_reset_refund", "manual_reset_settle", "same_second_reset_refund", "old_positive_settle", "reserve_closed_period", "legacy_unknown", "deleted_source", "expired_source", "underflow", "parallel_settle", "parallel_refund", "reset_races_refund"} {
				t.Run(mode, func(t *testing.T) {
					db := subscriptionPeriodDatabase(t, dialect.kind, dsn)
					require.NoError(t, db.AutoMigrate(&SubscriptionPlan{}, &UserSubscription{}, &SubscriptionPreConsumeRecord{}))
					plan, sub := subscriptionPeriodSeed(t, db)
					receipt, err := PreConsumeUserSubscription("original", sub.UserId, "fixture", 0, 200)
					require.NoError(t, err)
					require.Positive(t, receipt.QuotaPeriod)
					initialPeriod := receipt.QuotaPeriod
					require.NoError(t, ReserveSubscriptionPreConsume("original", sub.Id, 300))
					require.NoError(t, ReserveSubscriptionPreConsume("original", sub.Id, 300))
					require.NoError(t, db.First(sub, sub.Id).Error)
					assert.EqualValues(t, 300, sub.AmountUsed)
					require.NoError(t, db.Model(sub).Updates(map[string]any{"amount_used": 400}).Error) // independent usage in the same period
					expectedUsed := int64(200)
					expectedStatus := "settled"
					expectedActual := int64(100)
					settle := func() error { return SettleSubscriptionPreConsume("original", sub.Id, 100) }
					refund := func() error { return RefundSubscriptionPreConsume("original") }
					switch mode {
					case "same_period_settle":
						require.NoError(t, settle())
						require.NoError(t, settle())
						require.Error(t, RefundSubscriptionPreConsume("original"))
						require.Error(t, SettleSubscriptionPreConsume("original", sub.Id, 101))
					case "same_period_upward":
						require.NoError(t, SettleSubscriptionPreConsume("original", sub.Id, 350))
						require.NoError(t, SettleSubscriptionPreConsume("original", sub.Id, 350))
						expectedUsed = 450
						expectedActual = 350
					case "same_period_zero":
						require.NoError(t, SettleSubscriptionPreConsume("original", sub.Id, 0))
						require.NoError(t, SettleSubscriptionPreConsume("original", sub.Id, 0))
						expectedUsed = 100
						expectedActual = 0
					case "quota_ceiling":
						require.Error(t, SettleSubscriptionPreConsume("original", sub.Id, 1000))
						require.Error(t, ReserveSubscriptionPreConsume("original", sub.Id, 1000))
						expectedUsed = 400
						expectedStatus = "consumed"
						expectedActual = 0
					case "journal_identity":
						for _, mismatch := range []struct {
							user, subscription int
							reserved           int64
						}{{sub.UserId + 1, sub.Id, 300}, {sub.UserId, sub.Id + 1, 300}, {sub.UserId, sub.Id, 200}} {
							require.Error(t, db.Transaction(func(tx *gorm.DB) error {
								return RefundSubscriptionPreConsumeForRequestTx(tx, "original", mismatch.user, mismatch.subscription, mismatch.reserved)
							}))
						}
						require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
							return RefundSubscriptionPreConsumeForRequestTx(tx, "original", sub.UserId, sub.Id, 300)
						}))
						require.Error(t, db.Transaction(func(tx *gorm.DB) error {
							return RefundSubscriptionPreConsumeForRequestTx(tx, "original", sub.UserId, sub.Id, 300)
						}))
						expectedUsed = 100
						expectedStatus = "refunded"
						expectedActual = 0
					case "preconsume_replay":
						replay, err := PreConsumeUserSubscription("original", sub.UserId, "fixture", 0, 200)
						require.NoError(t, err)
						assert.True(t, replay.Replayed)
						assert.EqualValues(t, 200, replay.PreConsumed)
						assert.EqualValues(t, 300, replay.ReservedAmount)
						_, err = PreConsumeUserSubscription("original", sub.UserId+1, "fixture", 0, 200)
						require.Error(t, err)
						require.NoError(t, settle())
						_, err = PreConsumeUserSubscription("original", sub.UserId, "fixture", 0, 200)
						require.Error(t, err)
					case "parallel_preconsume":
						if dialect.kind == common.DatabaseTypeMySQL {
							// Establish an absent consistent snapshot, then commit the source
							// from another connection before replaying through current reads.
							tx := db.Begin(&sql.TxOptions{Isolation: sql.LevelRepeatableRead})
							require.NoError(t, tx.Error)
							t.Cleanup(func() { _ = tx.Rollback().Error })
							var absent SubscriptionPreConsumeRecord
							initial := tx.Where("request_id = ?", "snapshot-new").Find(&absent)
							require.NoError(t, initial.Error)
							require.Zero(t, initial.RowsAffected)
							created, err := PreConsumeUserSubscription("snapshot-new", sub.UserId, "fixture", 0, 50)
							require.NoError(t, err)
							replay, err := PreConsumeUserSubscriptionTx(tx, "snapshot-new", sub.UserId, "fixture", 0, 50)
							require.NoError(t, err)
							require.True(t, replay.Replayed)
							assert.Equal(t, created.QuotaPeriod, replay.QuotaPeriod)
							assert.EqualValues(t, 50, replay.ReservedAmount)
							require.NoError(t, tx.Commit().Error)
							require.NoError(t, RefundSubscriptionPreConsume("snapshot-new"))
						}
						results := make(chan error, 2)
						var wg sync.WaitGroup
						for range 2 {
							wg.Go(func() {
								_, err := PreConsumeUserSubscription("parallel-new", sub.UserId, "fixture", 0, 50)
								results <- err
							})
						}
						wg.Wait()
						close(results)
						for err := range results {
							require.NoError(t, err)
						}
						expectedUsed = 450
						expectedStatus = "consumed"
						expectedActual = 0
					case "same_period_refund":
						require.NoError(t, refund())
						require.NoError(t, refund())
						expectedUsed = 100
						expectedStatus = "refunded"
						expectedActual = 0
					case "auto_reset_settle", "auto_reset_refund", "manual_reset_settle", "same_second_reset_refund", "old_positive_settle", "reserve_closed_period":
						at := sub.NextResetTime + 1
						automatic := strings.HasPrefix(mode, "auto_")
						subscriptionPeriodReset(t, db, plan, sub.Id, automatic, false, at)
						if automatic {
							subscriptionPeriodReset(t, db, plan, sub.Id, true, false, at)
						}
						if mode == "same_second_reset_refund" {
							subscriptionPeriodReset(t, db, plan, sub.Id, false, false, at)
						}
						fresh, err := PreConsumeUserSubscription("new-period", sub.UserId, "fixture", 0, 100)
						require.NoError(t, err)
						require.Greater(t, fresh.QuotaPeriod, initialPeriod)
						expectedPeriod := initialPeriod + 1
						if mode == "same_second_reset_refund" {
							expectedPeriod++
						}
						assert.Equal(t, expectedPeriod, fresh.QuotaPeriod)
						expectedUsed = 100
						switch mode {
						case "auto_reset_refund", "same_second_reset_refund":
							require.NoError(t, refund())
							require.NoError(t, refund())
							expectedStatus = "refunded"
							expectedActual = 0
						case "old_positive_settle":
							require.ErrorIs(t, SettleSubscriptionPreConsume("original", sub.Id, 301), ErrSubscriptionPeriodClosed)
							expectedStatus = "consumed"
							expectedActual = 0
						case "reserve_closed_period":
							require.ErrorIs(t, ReserveSubscriptionPreConsume("original", sub.Id, 400), ErrSubscriptionPeriodClosed)
							expectedStatus = "consumed"
							expectedActual = 0
						default:
							require.NoError(t, settle())
							require.NoError(t, settle())
						}
					case "legacy_unknown":
						require.NoError(t, db.Model(&SubscriptionPreConsumeRecord{}).Where("request_id = ?", "original").Update("quota_period", 0).Error)
						require.ErrorIs(t, settle(), ErrSubscriptionPeriodUnknown)
						require.ErrorIs(t, refund(), ErrSubscriptionPeriodUnknown)
						require.ErrorIs(t, ReserveSubscriptionPreConsume("original", sub.Id, 400), ErrSubscriptionPeriodUnknown)
						expectedUsed = 400
						expectedStatus = "consumed"
						expectedActual = 0
					case "deleted_source":
						require.NoError(t, db.Delete(sub).Error)
						require.NoError(t, refund())
						require.NoError(t, refund())
						expectedStatus = "refunded"
						expectedActual = 0
					case "expired_source":
						require.NoError(t, db.Model(sub).Update("status", "expired").Error)
						require.NoError(t, refund())
						expectedUsed = 100
						expectedStatus = "refunded"
						expectedActual = 0
					case "underflow":
						require.NoError(t, db.Model(sub).Update("amount_used", 50).Error)
						require.Error(t, settle())
						require.Error(t, refund())
						expectedUsed = 50
						expectedStatus = "consumed"
						expectedActual = 0
					case "parallel_settle", "parallel_refund":
						operation := settle
						if mode == "parallel_refund" {
							operation = refund
							expectedUsed = 100
							expectedStatus = "refunded"
							expectedActual = 0
						}
						results := make(chan error, 2)
						var wg sync.WaitGroup
						for range 2 {
							wg.Go(func() { results <- operation() })
						}
						wg.Wait()
						close(results)
						for err := range results {
							require.NoError(t, err)
						}
					case "reset_races_refund":
						results := make(chan error, 2)
						var wg sync.WaitGroup
						wg.Go(func() { results <- refund() })
						wg.Go(func() {
							results <- db.Transaction(func(tx *gorm.DB) error {
								var locked UserSubscription
								if err := lockForUpdate(tx).First(&locked, sub.Id).Error; err != nil {
									return err
								}
								return resetUserSubscriptionTx(tx, &locked, plan, sub.NextResetTime+1, false)
							})
						})
						wg.Wait()
						close(results)
						for err := range results {
							require.NoError(t, err)
						}
						expectedUsed = 0
						expectedStatus = "refunded"
						expectedActual = 0
					}
					var stored SubscriptionPreConsumeRecord
					require.NoError(t, db.Where("request_id = ?", "original").First(&stored).Error)
					assert.Equal(t, expectedStatus, stored.Status)
					assert.Equal(t, expectedActual, stored.SettledAmount)
					assert.EqualValues(t, 200, stored.PreConsumed)
					assert.EqualValues(t, 300, stored.ReservedAmount)
					if mode != "deleted_source" {
						require.NoError(t, db.First(sub, sub.Id).Error)
						assert.Equal(t, expectedUsed, sub.AmountUsed)
					}
					deleted, err := CleanupSubscriptionPreConsumeRecords(1)
					require.NoError(t, err)
					assert.Zero(t, deleted)
					var count int64
					require.NoError(t, db.Model(&SubscriptionPreConsumeRecord{}).Where("request_id = ?", "original").Count(&count).Error)
					assert.EqualValues(t, 1, count)
				})
			}
		})
	}
}

func TestSubscriptionPeriodTransactionRollback(t *testing.T) {
	db := subscriptionPeriodDatabase(t, common.DatabaseTypeSQLite, "")
	require.NoError(t, db.AutoMigrate(&SubscriptionPlan{}, &UserSubscription{}, &SubscriptionPreConsumeRecord{}))
	_, sub := subscriptionPeriodSeed(t, db)
	_, err := PreConsumeUserSubscription("rollback", sub.UserId, "fixture", 0, 200)
	require.NoError(t, err)
	failure := errors.New("synthetic outer token/journal failure")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		if _, err := SettleSubscriptionPreConsumeTx(tx, "rollback", sub.Id, 100); err != nil {
			return err
		}
		return failure
	}), failure)
	require.NoError(t, db.First(sub, sub.Id).Error)
	assert.EqualValues(t, 200, sub.AmountUsed)
	var record SubscriptionPreConsumeRecord
	require.NoError(t, db.Where("request_id = ?", "rollback").First(&record).Error)
	assert.Equal(t, "consumed", record.Status)
	require.NoError(t, SettleSubscriptionPreConsume("rollback", sub.Id, 100))
	require.NoError(t, db.First(sub, sub.Id).Error)
	assert.EqualValues(t, 100, sub.AmountUsed)
}

// Model an initial repeatable-read observation that predates a committed
// refund. Only the first unlocked query is stale; authoritative locked reads
// must not reuse that snapshot or authorize another request reservation.
func TestSubscriptionPeriodReplayRejectsStaleConsumedObservation(t *testing.T) {
	db := subscriptionPeriodDatabase(t, common.DatabaseTypeSQLite, "")
	require.NoError(t, db.AutoMigrate(&SubscriptionPlan{}, &UserSubscription{}, &SubscriptionPreConsumeRecord{}))
	_, sub := subscriptionPeriodSeed(t, db)
	_, err := PreConsumeUserSubscription("stale-replay", sub.UserId, "fixture", 0, 200)
	require.NoError(t, err)
	var stale SubscriptionPreConsumeRecord
	require.NoError(t, db.Where("request_id = ?", "stale-replay").First(&stale).Error)
	require.NoError(t, RefundSubscriptionPreConsume("stale-replay"))
	injected := false
	callback := "test:stale-subscription-reservation"
	require.NoError(t, db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if injected || tx.Statement.Table != db.NamingStrategy.TableName("SubscriptionPreConsumeRecord") {
			return
		}
		if record, ok := tx.Statement.Dest.(*SubscriptionPreConsumeRecord); ok && record.RequestId == stale.RequestId {
			*record = stale
			injected = true
		}
	}))
	t.Cleanup(func() { require.NoError(t, db.Callback().Query().Remove(callback)) })
	replay, err := PreConsumeUserSubscription("stale-replay", sub.UserId, "fixture", 0, 200)
	require.True(t, injected)
	require.ErrorContains(t, err, "already finalized")
	assert.Nil(t, replay)
	require.NoError(t, db.First(sub, sub.Id).Error)
	assert.Zero(t, sub.AmountUsed)
	var stored SubscriptionPreConsumeRecord
	require.NoError(t, db.Where("request_id = ?", "stale-replay").First(&stored).Error)
	assert.Equal(t, "refunded", stored.Status)
}

func TestSubscriptionPeriodJournalReserveSettlementValidation(t *testing.T) {
	db := subscriptionPeriodDatabase(t, common.DatabaseTypeSQLite, "")
	require.NoError(t, db.AutoMigrate(&SubscriptionPlan{}, &UserSubscription{}, &SubscriptionPreConsumeRecord{}))
	_, sub := subscriptionPeriodSeed(t, db)
	_, err := PreConsumeUserSubscription("journal-source", sub.UserId, "fixture", 0, 200)
	require.NoError(t, err)
	for _, mismatch := range []struct {
		user, subscription int
		reserved           int64
	}{{sub.UserId + 1, sub.Id, 200}, {sub.UserId, sub.Id + 1, 200}, {sub.UserId, sub.Id, 100}} {
		require.Error(t, db.Transaction(func(tx *gorm.DB) error {
			return ReserveSubscriptionPreConsumeForRequestTx(tx, "journal-source", mismatch.user, mismatch.subscription, mismatch.reserved, 300)
		}))
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return ReserveSubscriptionPreConsumeForRequestTx(tx, "journal-source", sub.UserId, sub.Id, 200, 300)
	}))
	for _, mismatch := range []struct {
		user, subscription int
		reserved           int64
	}{{sub.UserId + 1, sub.Id, 300}, {sub.UserId, sub.Id + 1, 300}, {sub.UserId, sub.Id, 200}} {
		require.Error(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := SettleSubscriptionPreConsumeForRequestTx(tx, "journal-source", mismatch.user, mismatch.subscription, mismatch.reserved, 100)
			return err
		}))
	}
	require.NoError(t, db.First(sub, sub.Id).Error)
	assert.EqualValues(t, 300, sub.AmountUsed)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		replayed, err := SettleSubscriptionPreConsumeForRequestTx(tx, "journal-source", sub.UserId, sub.Id, 300, 100)
		assert.False(t, replayed)
		return err
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		replayed, err := SettleSubscriptionPreConsumeForRequestTx(tx, "journal-source", sub.UserId, sub.Id, 300, 100)
		assert.True(t, replayed)
		return err
	}))
	require.NoError(t, db.First(sub, sub.Id).Error)
	assert.EqualValues(t, 100, sub.AmountUsed)
}

// A located receipt must be re-read only after the source lock, not through a
// second consistent snapshot which may still say it did not exist. The real
// MySQL repeatable-read schedule is also exercised by parallel_preconsume.
func TestSubscriptionPeriodReplayAfterAbsentSnapshot(t *testing.T) {
	db := subscriptionPeriodDatabase(t, common.DatabaseTypeSQLite, "")
	require.NoError(t, db.AutoMigrate(&SubscriptionPlan{}, &UserSubscription{}, &SubscriptionPreConsumeRecord{}))
	_, sub := subscriptionPeriodSeed(t, db)
	_, err := PreConsumeUserSubscription("snapshot-replay", sub.UserId, "fixture", 0, 200)
	require.NoError(t, err)
	var observed SubscriptionPreConsumeRecord
	require.NoError(t, db.Where("request_id = ?", "snapshot-replay").First(&observed).Error)
	sourceRead := false
	callback := "test:absent-subscription-snapshot"
	require.NoError(t, db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == db.NamingStrategy.TableName("UserSubscription") {
			sourceRead = true
		}
		if tx.Statement.Table == db.NamingStrategy.TableName("SubscriptionPreConsumeRecord") && !sourceRead {
			tx.RowsAffected = 0
			tx.AddError(gorm.ErrRecordNotFound)
		}
	}))
	t.Cleanup(func() { require.NoError(t, db.Callback().Query().Remove(callback)) })
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		replay, err := subscriptionReservationReplayTx(tx, &observed, sub.UserId, 200)
		if err != nil {
			return err
		}
		assert.True(t, replay.Replayed)
		assert.EqualValues(t, 200, replay.ReservedAmount)
		return nil
	}))
	require.NoError(t, db.First(sub, sub.Id).Error)
	assert.EqualValues(t, 200, sub.AmountUsed)
	var receipts int64
	require.NoError(t, db.Model(&SubscriptionPreConsumeRecord{}).Where("request_id = ?", observed.RequestId).Count(&receipts).Error)
	assert.EqualValues(t, 1, receipts)
}
