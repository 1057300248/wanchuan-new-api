package model

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// This fixture exercises the ledger primitives independently, not registration,
// payment callbacks, service funding owners, or subscription receipt activation.
func setupLedgerCapabilityState(t *testing.T) {
	t.Helper()
	truncateTables(t)
	require.NoError(t, migrateRequiredWalletSchema(DB))
	for _, table := range []string{"balance_ledger", "balance_ledger_operations", "users", "tokens"} {
		require.NoError(t, DB.Exec("DELETE FROM "+table).Error)
	}
	oldRedis, oldBatch := common.RedisEnabled, common.BatchUpdateEnabled
	common.RedisEnabled, common.BatchUpdateEnabled = false, false
	t.Cleanup(func() {
		defer func() { common.RedisEnabled, common.BatchUpdateEnabled = oldRedis, oldBatch }()
		require.NoError(t, DB.Exec("DELETE FROM balance_ledger").Error)
		require.NoError(t, DB.Exec("DELETE FROM balance_ledger_operations").Error)
	})
}

func newLedgerCapabilityUser(t *testing.T, id, paid, bonus, reward int) User {
	t.Helper()
	user := User{Id: id, Username: fmt.Sprintf("capability-%d", id), AffCode: fmt.Sprintf("cap%d", id),
		Quota: paid + bonus + reward, QuotaPaid: paid, QuotaBonus: bonus, QuotaReward: reward,
		Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&user).Error)
	return user
}

func assertLedgerCapabilityBalances(t *testing.T, id, paid, bonus, reward int) {
	t.Helper()
	var user User
	require.NoError(t, DB.First(&user, id).Error)
	require.Equal(t, paid, user.QuotaPaid)
	require.Equal(t, bonus, user.QuotaBonus)
	require.Equal(t, reward, user.QuotaReward)
	require.Equal(t, paid+bonus+reward, user.Quota)
}

func assertLedgerCapabilityCounts(t *testing.T, operations, rows int64) {
	t.Helper()
	var count int64
	require.NoError(t, DB.Model(&BalanceLedgerOperation{}).Count(&count).Error)
	require.Equal(t, operations, count)
	require.NoError(t, DB.Model(&BalanceLedger{}).Count(&count).Error)
	require.Equal(t, rows, count)
}

func TestLedgerCapabilityDoesNotActivateMigrationOrHooks(t *testing.T) {
	// Isolate the schema-only boundary from the now-active production host registration.
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "schema-only.db")), &gorm.Config{})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	previous := DB
	DB = db
	t.Cleanup(func() { DB = previous; require.NoError(t, pool.Close()) })
	require.NoError(t, db.AutoMigrate(&User{}, &Token{}))
	setupLedgerCapabilityState(t)
	// The existing schema guard can run repeatedly without registering the new
	// receipt type or inferring paid credit from an old quota-only row.
	user := User{Id: 81001, Username: "capability-legacy", AffCode: "caplegacy", Quota: 91}
	require.NoError(t, DB.Create(&user).Error)
	require.NoError(t, DB.Model(&user).Update("display_name", "metadata only").Error)
	for range 2 {
		require.NoError(t, migrateRequiredWalletSchema(DB))
	}
	var after User
	require.NoError(t, DB.First(&after, user.Id).Error)
	require.Equal(t, 91, after.Quota)
	require.Zero(t, after.QuotaPaid)
	require.Zero(t, after.QuotaBonus)
	require.Zero(t, after.QuotaReward)
	require.False(t, DB.Migrator().HasTable(&BillingFinalization{}))
	assertLedgerCapabilityCounts(t, 0, 0)
}

func TestLedgerCapabilityOperationsAndIdempotency(t *testing.T) {
	setupLedgerCapabilityState(t)
	user := newLedgerCapabilityUser(t, 81002, 100, 60, 40)
	entry := LedgerEntry{UserId: user.Id, Delta: 150, Reason: LedgerReasonBillingPreConsume,
		ReferenceType: "relay", ReferenceId: "cap-operations", RequestId: "cap-operations",
		IdempotencyKey: "billing:preconsume:cap-operations"}
	order := []LedgerAccount{AccountBonus, AccountReward, AccountPaid}
	result, err := DebitQuota(entry, order...)
	require.NoError(t, err)
	require.False(t, result.Replayed)
	require.Len(t, result.Rows, 3)
	require.Equal(t, 200, result.TotalBefore)
	require.Equal(t, 50, result.TotalAfter)
	assertLedgerCapabilityBalances(t, user.Id, 50, 0, 0)
	replayed, err := DebitQuota(entry, order...)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	require.Equal(t, result.Rows, replayed.Rows)
	entry.Delta--
	_, err = DebitQuota(entry, order...)
	require.ErrorIs(t, err, ErrLedgerIdempotencyConflict)
	assertLedgerCapabilityCounts(t, 1, 3)
	assertLedgerCapabilityBalances(t, user.Id, 50, 0, 0)

	_, err = TransferQuota(LedgerEntry{UserId: user.Id, Account: AccountPaid, Delta: 20,
		Reason: LedgerReasonSystem, IdempotencyKey: "cap-transfer"}, AccountReward)
	require.NoError(t, err)
	_, err = OverrideQuota(LedgerEntry{UserId: user.Id, Account: AccountBonus, Delta: 10,
		Reason: LedgerReasonAdminOverride, IdempotencyKey: "cap-override"})
	require.NoError(t, err)
	_, err = CreditQuota(LedgerEntry{UserId: user.Id, Account: AccountPaid, Delta: 40,
		Reason: LedgerReasonTopup, IdempotencyKey: "cap-credit"})
	require.NoError(t, err)
	assertLedgerCapabilityBalances(t, user.Id, 70, 10, 20)
	assertLedgerCapabilityCounts(t, 4, 7)

	_, err = CreditQuota(LedgerEntry{UserId: user.Id, Account: AccountPaid, Delta: common.MaxWalletQuota,
		Reason: LedgerReasonTopup, IdempotencyKey: "cap-over-limit"})
	require.ErrorIs(t, err, ErrWalletQuotaLimitExceeded)
	assertLedgerCapabilityBalances(t, user.Id, 70, 10, 20)
	assertLedgerCapabilityCounts(t, 4, 7)
}

func TestLedgerCapabilitySourceAndTokenRollback(t *testing.T) {
	setupLedgerCapabilityState(t)
	user := newLedgerCapabilityUser(t, 81003, 100, 0, 0)
	token := Token{UserId: user.Id, Key: "capability-rollback", RemainQuota: 100}
	require.NoError(t, DB.Create(&token).Error)
	rollback := errors.New("caller rollback")
	err := DB.Transaction(func(tx *gorm.DB) error {
		_, err := DebitQuotaTx(tx, LedgerEntry{UserId: user.Id, Delta: 25, Reason: LedgerReasonBillingPreConsume,
			IdempotencyKey: "cap-rollback"})
		if err != nil {
			return err
		}
		if err := ApplyTokenQuotaDeltaTx(tx, token.Id, token.Key, -25); err != nil {
			return err
		}
		return rollback
	})
	require.ErrorIs(t, err, rollback)
	assertLedgerCapabilityBalances(t, user.Id, 100, 0, 0)
	assertLedgerCapabilityCounts(t, 0, 0)
	var after Token
	require.NoError(t, DB.First(&after, token.Id).Error)
	require.Equal(t, 100, after.RemainQuota)
	require.Zero(t, after.UsedQuota)
}

func TestLedgerCapabilityDetailInsertFailureRollsBack(t *testing.T) {
	setupLedgerCapabilityState(t)
	user := newLedgerCapabilityUser(t, 81004, 100, 0, 0)
	insertFailure := errors.New("injected ledger detail failure")
	callback := "capability_ledger_detail_failure"
	hits := 0
	require.NoError(t, DB.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.ModelType == reflect.TypeFor[BalanceLedger]() {
			hits++
			tx.AddError(insertFailure)
		}
	}))
	t.Cleanup(func() { require.NoError(t, DB.Callback().Create().Remove(callback)) })
	_, err := CreditQuota(LedgerEntry{UserId: user.Id, Account: AccountPaid, Delta: 10,
		Reason: LedgerReasonTopup, IdempotencyKey: "cap-insert-fail"})
	require.ErrorIs(t, err, insertFailure)
	require.Equal(t, 1, hits)
	assertLedgerCapabilityBalances(t, user.Id, 100, 0, 0)
	assertLedgerCapabilityCounts(t, 0, 0)
}

func TestLedgerCapabilityRefundUsesOriginalAccounts(t *testing.T) {
	setupLedgerCapabilityState(t)
	user := newLedgerCapabilityUser(t, 81005, 100, 60, 40)
	requestID := "cap-refund"
	_, err := DebitQuota(LedgerEntry{UserId: user.Id, Delta: 150, Reason: LedgerReasonBillingPreConsume,
		ReferenceType: "relay", ReferenceId: requestID, RequestId: requestID,
		IdempotencyKey: "billing:preconsume:" + requestID}, AccountBonus, AccountReward, AccountPaid)
	require.NoError(t, err)
	partial := LedgerEntry{UserId: user.Id, Reason: LedgerReasonBillingSettle, ReferenceType: "relay",
		ReferenceId: requestID, RequestId: requestID, IdempotencyKey: "billing:settle_refund:" + requestID + ":60"}
	var result *LedgerResult
	var outstanding []LedgerShare
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		var err error
		result, outstanding, err = CreditWalletRefundTx(tx, partial, 60)
		return err
	}))
	require.False(t, result.Replayed)
	require.Equal(t, []LedgerShare{{AccountBonus, 36}, {AccountReward, 24}, {AccountPaid, 30}}, outstanding)
	assertLedgerCapabilityBalances(t, user.Id, 70, 24, 16)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		var err error
		result, outstanding, err = CreditWalletRefundTx(tx, partial, 60)
		return err
	}))
	require.True(t, result.Replayed)
	assertLedgerCapabilityBalances(t, user.Id, 70, 24, 16)
	full := LedgerEntry{UserId: user.Id, Reason: LedgerReasonBillingRefund, ReferenceType: "relay",
		ReferenceId: requestID, RequestId: requestID, IdempotencyKey: "billing:refund:" + requestID}
	for range 2 {
		require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
			var err error
			result, outstanding, err = CreditWalletRefundTx(tx, full, 0)
			return err
		}))
		require.Empty(t, outstanding)
		assertLedgerCapabilityBalances(t, user.Id, 100, 60, 40)
	}
	require.True(t, result.Replayed)
	assertLedgerCapabilityCounts(t, 3, 9)
}

func TestLedgerCapabilityIndependentDebitsAreNotRefundPrincipal(t *testing.T) {
	for _, purpose := range []string{"violation_fee", "realtime"} {
		t.Run(purpose, func(t *testing.T) {
			setupLedgerCapabilityState(t)
			user := newLedgerCapabilityUser(t, 81009, 100, 0, 0)
			requestID := "cap-independent-" + purpose
			_, err := DebitQuota(LedgerEntry{UserId: user.Id, Delta: 40, Reason: LedgerReasonBillingPreConsume,
				ReferenceType: "relay", ReferenceId: requestID, RequestId: requestID,
				IdempotencyKey: "billing:preconsume:" + requestID})
			require.NoError(t, err)
			fee := LedgerEntry{UserId: user.Id, Delta: 10, Reason: LedgerReasonBillingSettle,
				ReferenceType: "relay", ReferenceId: requestID, RequestId: requestID,
				IdempotencyKey: "billing:settle:" + requestID + ":10"}
			if purpose == "violation_fee" {
				fee.Metadata = `{"independent_fee":"violation_fee"}`
			} else {
				fee.IdempotencyKey = "billing:settle:" + requestID + ":wss:1:10"
			}
			_, err = DebitQuota(fee)
			require.NoError(t, err)
			require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
				receipt, err := WalletRequestReceiptTx(tx, user.Id, requestID)
				if err != nil {
					return err
				}
				require.Equal(t, []LedgerShare{{AccountPaid, 40}}, receipt)
				_, _, err = CreditWalletRefundTx(tx, LedgerEntry{UserId: user.Id, Reason: LedgerReasonBillingRefund,
					ReferenceType: "relay", ReferenceId: requestID, RequestId: requestID,
					IdempotencyKey: "billing:refund:" + requestID}, 0)
				return err
			}))
			assertLedgerCapabilityBalances(t, user.Id, 90, 0, 0)
			assertLedgerCapabilityCounts(t, 3, 3)
			require.NoError(t, DB.Model(&BalanceLedger{}).Where("idempotency_key = ?", fee.IdempotencyKey).
				Update("request_id", "different-owner").Error)
			err = DB.Transaction(func(tx *gorm.DB) error {
				_, err := WalletRequestReceiptTx(tx, user.Id, requestID)
				return err
			})
			require.Error(t, err)
			code, class := BillingRefundErrorCode(err)
			require.Equal(t, "source_evidence", code)
			require.Equal(t, billingRefundReview, class)
			assertLedgerCapabilityBalances(t, user.Id, 90, 0, 0)
			assertLedgerCapabilityCounts(t, 3, 3)
		})
	}
}

func TestLedgerCapabilityRejectsUnknownRefundEvidence(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		t.Run(fmt.Sprintf("malformed_%t", malformed), func(t *testing.T) {
			setupLedgerCapabilityState(t)
			user := newLedgerCapabilityUser(t, 81006, 100, 0, 0)
			requestID := "cap-unknown"
			if malformed {
				_, err := DebitQuota(LedgerEntry{UserId: user.Id, Delta: 25, Reason: LedgerReasonBillingPreConsume,
					ReferenceType: "relay", ReferenceId: requestID, RequestId: requestID,
					IdempotencyKey: "billing:preconsume:" + requestID})
				require.NoError(t, err)
				require.NoError(t, DB.Model(&BalanceLedger{}).Where("user_id = ?", user.Id).Update("request_id", "different-owner").Error)
			}
			err := DB.Transaction(func(tx *gorm.DB) error {
				_, _, err := CreditWalletRefundTx(tx, LedgerEntry{UserId: user.Id, Reason: LedgerReasonBillingRefund,
					ReferenceType: "relay", ReferenceId: requestID, RequestId: requestID,
					IdempotencyKey: "billing:refund:" + requestID}, 0)
				return err
			})
			require.ErrorIs(t, err, ErrMissingWalletDebitReceipt)
			if malformed {
				assertLedgerCapabilityBalances(t, user.Id, 75, 0, 0)
				assertLedgerCapabilityCounts(t, 1, 1)
			} else {
				assertLedgerCapabilityBalances(t, user.Id, 100, 0, 0)
				assertLedgerCapabilityCounts(t, 0, 0)
			}
		})
	}
}

func TestTokenCapabilityBounds(t *testing.T) {
	setupLedgerCapabilityState(t)
	for _, tc := range []struct {
		name                string
		remain, used, delta int
	}{
		{"remain_overflow", math.MaxInt - 50, 100, 100},
		{"remain_underflow", math.MinInt + 50, 0, -100},
		{"used_overflow", 1000, math.MaxInt - 50, -100},
		{"used_underflow", 1000, math.MinInt + 50, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token := Token{UserId: 81007, Key: "cap-" + tc.name, RemainQuota: tc.remain, UsedQuota: tc.used}
			require.NoError(t, DB.Create(&token).Error)
			err := DB.Transaction(func(tx *gorm.DB) error { return ApplyTokenQuotaDeltaTx(tx, token.Id, token.Key, tc.delta) })
			require.Error(t, err)
			code, class := BillingRefundErrorCode(err)
			require.Equal(t, "token_counter_bounds", code)
			require.Equal(t, billingRefundReview, class)
			var after Token
			require.NoError(t, DB.First(&after, token.Id).Error)
			require.Equal(t, tc.remain, after.RemainQuota)
			require.Equal(t, tc.used, after.UsedQuota)
		})
	}
	t.Run("exact_boundary", func(t *testing.T) {
		token := Token{UserId: 81007, Key: "cap-exact-boundary", RemainQuota: math.MaxInt - 100, UsedQuota: 100}
		require.NoError(t, DB.Create(&token).Error)
		require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return ApplyTokenQuotaDeltaTx(tx, token.Id, token.Key, 100) }))
		var after Token
		require.NoError(t, DB.First(&after, token.Id).Error)
		require.Equal(t, math.MaxInt, after.RemainQuota)
		require.Zero(t, after.UsedQuota)
	})
}

func TestTokenCapabilityOriginalIdentityAndDeletion(t *testing.T) {
	setupLedgerCapabilityState(t)
	token := Token{UserId: 81008, Key: "cap-original-token", RemainQuota: 100}
	require.NoError(t, DB.Create(&token).Error)
	require.Error(t, DB.Transaction(func(tx *gorm.DB) error {
		return ApplyTokenQuotaDeltaTx(tx, token.Id, "different-key", -10)
	}))
	for _, deleted := range []bool{false, true} {
		t.Run(fmt.Sprintf("deleted_%t", deleted), func(t *testing.T) {
			if deleted {
				require.NoError(t, DB.Delete(&token).Error)
			}
			for _, identity := range []struct {
				user int
				key  string
			}{{token.UserId + 1, token.Key}, {token.UserId, "different-key"}} {
				require.Error(t, DB.Transaction(func(tx *gorm.DB) error {
					return SettleTokenQuotaDeltaTx(tx, token.Id, identity.user, identity.key, -10)
				}))
			}
			callback := fmt.Sprintf("cap_token_update_%t", deleted)
			var statement string
			hits := 0
			require.NoError(t, DB.Callback().Update().After("gorm:update").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Schema != nil && tx.Statement.Schema.ModelType == reflect.TypeFor[Token]() {
					hits++
					statement = tx.Statement.SQL.String()
				}
			}))
			t.Cleanup(func() { require.NoError(t, DB.Callback().Update().Remove(callback)) })
			require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
				return SettleTokenQuotaDeltaTx(tx, token.Id, token.UserId, token.Key, -10)
			}))
			require.Equal(t, 1, hits)
			require.NotEmpty(t, statement)
			require.NotContains(t, strings.ToUpper(statement), " FROM ")
			var after Token
			require.NoError(t, DB.Unscoped().First(&after, token.Id).Error)
			expectedUsed := 10
			if deleted {
				expectedUsed = 20
				require.ErrorIs(t, DB.Transaction(func(tx *gorm.DB) error {
					return ApplyTokenQuotaDeltaTx(tx, token.Id, token.Key, -10)
				}), gorm.ErrRecordNotFound)
			}
			require.Equal(t, 100-expectedUsed, after.RemainQuota)
			require.Equal(t, expectedUsed, after.UsedQuota)
			require.Equal(t, deleted, after.DeletedAt.Valid)
		})
	}
	require.NoError(t, DB.Unscoped().Delete(&token).Error)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return SettleTokenQuotaDeltaTx(tx, token.Id, token.UserId, token.Key, -10)
	}))
	var count int64
	require.NoError(t, DB.Unscoped().Model(&Token{}).Where("id = ?", token.Id).Count(&count).Error)
	require.Zero(t, count, "settlement never recreates a physically removed credential")
}

func TestDBTimestampCapabilityUsesTransactionConnection(t *testing.T) {
	// TestMain limits SQLite to one connection. Bound the global handle too so
	// an accidental fallback cannot deadlock this regression test indefinitely.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	originalDB := DB
	DB = DB.WithContext(ctx)
	t.Cleanup(func() { DB = originalDB })
	sqlDB, err := DB.DB()
	require.NoError(t, err)
	require.Equal(t, 1, sqlDB.Stats().MaxOpenConnections)
	queries := 0
	callback := "capability_transaction_clock"
	require.NoError(t, DB.Callback().Row().Before("gorm:row").Register(callback, func(query *gorm.DB) {
		if strings.Contains(query.Statement.SQL.String(), "strftime") {
			queries++
		}
	}))
	t.Cleanup(func() { require.NoError(t, DB.Callback().Row().Remove(callback)) })
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		ts := GetDBTimestamp(tx)
		require.NoError(t, ctx.Err(), "application-time fallback after pool timeout is not success")
		require.Positive(t, ts)
		return nil
	}))
	require.Equal(t, 1, queries, "observe a real database clock query")
}
