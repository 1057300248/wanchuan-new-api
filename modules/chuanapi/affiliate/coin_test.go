package affiliate_test

import (
	"errors"
	"fmt"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	service "github.com/QuantumNous/new-api/modules/chuanapi/affiliate"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// This fixture opens a fresh in-memory SQLite database, not SQL_DSN or production.
func referralDatabase(t *testing.T) *gorm.DB {
	t.Helper()

	var driver gorm.Dialector = sqlite.Open(":memory:")
	dialect, dsn := os.Getenv("REFERRAL_TEST_DIALECT"), os.Getenv("REFERRAL_TEST_DSN")
	if dialect != "" && dialect != "sqlite" {
		require.Contains(t, dsn, "127.0.0.1", "integration tests require loopback databases")
		require.Contains(t, dsn, "referral_test", "integration tests require a dedicated database")
		switch dialect {
		case "mysql":
			driver = mysql.Open(dsn)
		case "postgres":
			driver = postgres.Open(dsn)
		default:
			t.Fatalf("unknown dialect %q", dialect)
		}
	}
	db, err := gorm.Open(driver, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	connection, err := db.DB()
	require.NoError(t, err)
	connection.SetMaxOpenConns(1)
	var databaseVersion string
	versionQuery := "SELECT sqlite_version()"
	if dialect == "mysql" || dialect == "postgres" {
		versionQuery = "SELECT VERSION()"
		connection.SetMaxOpenConns(8)
	}
	require.NoError(t, db.Raw(versionQuery).Scan(&databaseVersion).Error)
	t.Logf("isolated database version: %s", databaseVersion)
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	previousRedis := common.RedisEnabled
	model.DB, model.LOG_DB = db, db
	databaseType := common.DatabaseTypeSQLite
	if dialect == "mysql" {
		databaseType = common.DatabaseTypeMySQL
	}
	if dialect == "postgres" {
		databaseType = common.DatabaseTypePostgreSQL
	}
	common.SetDatabaseTypes(databaseType, databaseType)
	common.RedisEnabled = false
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMainType, previousLogType)
		common.RedisEnabled = previousRedis
		require.NoError(t, connection.Close())
	})
	require.NoError(t, db.AutoMigrate(
		&model.User{}, &model.TopUp{}, &model.ReferralCoinReward{},
		&model.ReferralCoinLedger{}, &model.BalanceLedgerOperation{}, &model.BalanceLedger{}, &model.AffiliateReward{}, &model.Log{},
	))

	for _, table := range []any{&model.ReferralCoinLedger{}, &model.ReferralCoinReward{}, &model.BalanceLedger{}, &model.BalanceLedgerOperation{}, &model.TopUp{}, &model.AffiliateReward{}, &model.User{}} {
		require.NoError(t, db.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(table).Error)
	}
	return db
}

func coinHolder(t *testing.T, db *gorm.DB, name string, microcoins int) model.User {
	t.Helper()
	user := model.User{Username: name, AffCode: name, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&model.ReferralCoinLedger{
		UserId: user.Id, Delta: microcoins, Reason: "referral_commission",
		ReferenceType: "audit_fixture", ReferenceId: name,
		IdempotencyKey: "fixture:" + name,
	}).Error)
	return user
}

func TestReferralWholeCoinRedemptionAndRetry(t *testing.T) {
	db := referralDatabase(t)
	user := coinHolder(t, db, "whole-coin", common.ReferralCoinScale)
	require.NoError(t, service.RedeemReferralCoin(user.Id, common.ReferralCoinScale, "whole-coin-redemption"))
	require.NoError(t, service.RedeemReferralCoin(user.Id, common.ReferralCoinScale, "whole-coin-redemption"))
	require.NoError(t, db.First(&user, user.Id).Error)
	assert.Equal(t, int(common.QuotaPerUnit), user.QuotaBonus)
	assert.Equal(t, user.QuotaBonus, user.Quota)
	balance, err := model.ReferralCoinBalance(user.Id)
	require.NoError(t, err)
	assert.Zero(t, balance)
}

func TestReferralRedemptionPreservesValueWhenSplit(t *testing.T) {
	db := referralDatabase(t)
	require.Equal(t, float64(500000), common.QuotaPerUnit)
	bulk := coinHolder(t, db, "bulk", 2)
	split := coinHolder(t, db, "split", 2)
	require.NoError(t, service.RedeemReferralCoin(bulk.Id, 2, "bulk-two-microcoins"))
	for _, requestID := range []string{"split-first-microcoin", "split-second-microcoin"} {
		require.ErrorIs(t, service.RedeemReferralCoin(split.Id, 1, requestID), common.ErrReferralCoinAmount)
	}
	balance, err := model.ReferralCoinBalance(split.Id)
	require.NoError(t, err)
	assert.Equal(t, 2, balance, "rejected fractional quota must leave the coins available")
	require.NoError(t, service.RedeemReferralCoin(split.Id, 2, "split-exact-redemption"))
	require.NoError(t, db.First(&bulk, bulk.Id).Error)
	require.NoError(t, db.First(&split, split.Id).Error)
	t.Logf("same starting value: 2 microcoins; bulk quota=%d, split quota=%d", bulk.QuotaBonus, split.QuotaBonus)
	assert.Equal(t, 1, bulk.QuotaBonus)
	assert.Equal(t, bulk.QuotaBonus, split.QuotaBonus, "splitting a redemption must not create extra wallet credit")
}

func TestReferralRedemptionRequestIDIsBoundToUser(t *testing.T) {
	db := referralDatabase(t)
	first := coinHolder(t, db, "first-user", common.ReferralCoinScale)
	second := coinHolder(t, db, "second-user", common.ReferralCoinScale)
	require.NoError(t, service.RedeemReferralCoin(first.Id, common.ReferralCoinScale, "client-request-0001"))
	err := service.RedeemReferralCoin(second.Id, common.ReferralCoinScale, "client-request-0001")
	if err != nil {
		assert.ErrorIs(t, err, model.ErrLedgerIdempotencyConflict)
		return
	}
	require.NoError(t, db.First(&second, second.Id).Error)
	balance, err := model.ReferralCoinBalance(second.Id)
	require.NoError(t, err)
	t.Logf("second user's API result=success; bonus=%d; unspent microcoins=%d", second.QuotaBonus, balance)
	assert.Equal(t, int(common.QuotaPerUnit), second.QuotaBonus, "a successful response must refer to this user's own redemption")
	assert.Zero(t, balance)
}

func TestReferralCoinBalanceKeepsSupportedWalletRange(t *testing.T) {
	db := referralDatabase(t)
	user := coinHolder(t, db, "large-balance", 3_000_000_000)
	balance, err := model.ReferralCoinBalance(user.Id)
	require.NoError(t, err)
	t.Logf("stored microcoins=%d; returned microcoins=%d", 3_000_000_000, balance)
	assert.Equal(t, 3_000_000_000, balance, "wallet-range balances must not silently saturate at the single-request int32 limit")
}

func TestReferralTopupMaturityBaseline(t *testing.T) {
	db := referralDatabase(t)
	oldEnabled, oldRate, oldDays := common.ReferralCoinEnabled, common.ReferralCoinRate, common.ReferralCoinFreezeDays
	common.ReferralCoinEnabled, common.ReferralCoinRate, common.ReferralCoinFreezeDays = true, 0.01, 0
	t.Cleanup(func() {
		common.ReferralCoinEnabled, common.ReferralCoinRate, common.ReferralCoinFreezeDays = oldEnabled, oldRate, oldDays
	})
	inviter := model.User{Username: "inviter", AffCode: "inviter", Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&inviter).Error)
	invitee := model.User{Username: "invitee", AffCode: "invitee", InviterId: inviter.Id, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&invitee).Error)
	order := model.TopUp{UserId: invitee.Id, TradeNo: "settled-order", Status: common.TopUpStatusSuccess}
	require.NoError(t, db.Create(&order).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return model.RecordReferralCoinForTopupTx(tx, invitee.Id, int(common.QuotaPerUnit*100), order.TradeNo)
	}))
	for range 2 {
		_, err := model.MatureReferralCoinRewards(inviter.Id)
		require.NoError(t, err)
	}
	balance, err := model.ReferralCoinBalance(inviter.Id)
	require.NoError(t, err)
	assert.Equal(t, common.ReferralCoinScale, balance)
	var count int64
	require.NoError(t, db.Model(&model.ReferralCoinLedger{}).Where("user_id = ? AND reason = ?", inviter.Id, "referral_commission").Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestReferralRedemptionRejectsChangedAmountAndRollsBackFailedCredit(t *testing.T) {
	db := referralDatabase(t)
	user := coinHolder(t, db, "amount-binding", 2*common.ReferralCoinScale)
	require.NoError(t, service.RedeemReferralCoin(user.Id, common.ReferralCoinScale, "amount-binding-request"))
	require.ErrorIs(t, service.RedeemReferralCoin(user.Id, 2*common.ReferralCoinScale, "amount-binding-request"), model.ErrLedgerIdempotencyConflict)
	require.ErrorIs(t, model.RedeemReferralCoins(user.Id, common.ReferralCoinScale, 1, "fake-quota-request"), common.ErrReferralCoinAmount)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", user.Id).Updates(map[string]any{"quota": common.MaxWalletQuota, "quota_bonus": common.MaxWalletQuota}).Error)
	require.Error(t, service.RedeemReferralCoin(user.Id, common.ReferralCoinScale, "full-wallet-request"))
	balance, err := model.ReferralCoinBalance(user.Id)
	require.NoError(t, err)
	assert.Equal(t, common.ReferralCoinScale, balance)
	var ledgerCount int64
	require.NoError(t, db.Model(&model.ReferralCoinLedger{}).Where("reference_id = ?", "full-wallet-request").Count(&ledgerCount).Error)
	assert.Zero(t, ledgerCount)
}

func TestReferralRedemptionChecksBothLedgerOwners(t *testing.T) {
	db := referralDatabase(t)
	first := coinHolder(t, db, "bound-user", common.ReferralCoinScale)
	second := coinHolder(t, db, "other-user", common.ReferralCoinScale)
	require.NoError(t, service.RedeemReferralCoin(first.Id, common.ReferralCoinScale, "owner-binding-request"))
	require.NoError(t, db.Model(&model.BalanceLedger{}).Where("user_id = ? AND reference_id = ?", first.Id, "owner-binding-request").Update("user_id", second.Id).Error)
	require.ErrorIs(t, service.RedeemReferralCoin(first.Id, common.ReferralCoinScale, "owner-binding-request"), model.ErrLedgerIdempotencyConflict)
}

func TestReferralLegacyRedemptionRetryRemainsUserBound(t *testing.T) {
	db := referralDatabase(t)
	first := coinHolder(t, db, "legacy-user", common.ReferralCoinScale)
	second := coinHolder(t, db, "new-user", common.ReferralCoinScale)
	const requestID = "legacy-key-request"
	require.NoError(t, service.RedeemReferralCoin(first.Id, common.ReferralCoinScale, requestID))
	scoped := fmt.Sprintf("referral_coin:redeem:%d:%s", first.Id, requestID)
	legacy := "referral_coin:redeem:" + requestID
	require.NoError(t, db.Model(&model.ReferralCoinLedger{}).Where("idempotency_key = ?", scoped).Update("idempotency_key", legacy).Error)
	require.NoError(t, db.Model(&model.BalanceLedger{}).Where("idempotency_key = ?", scoped+":bonus").Update("idempotency_key", legacy+":bonus").Error)
	require.NoError(t, service.RedeemReferralCoin(first.Id, common.ReferralCoinScale, requestID))
	require.NoError(t, service.RedeemReferralCoin(second.Id, common.ReferralCoinScale, requestID))
	require.NoError(t, db.First(&first, first.Id).Error)
	require.NoError(t, db.First(&second, second.Id).Error)
	assert.Equal(t, int(common.QuotaPerUnit), first.QuotaBonus)
	assert.Equal(t, first.QuotaBonus, second.QuotaBonus)
}

func TestReferralDisabledAccrualStillAllowsExistingCoinRedemption(t *testing.T) {
	db := referralDatabase(t)
	old := common.ReferralCoinEnabled
	common.ReferralCoinEnabled = false
	t.Cleanup(func() { common.ReferralCoinEnabled = old })
	user := coinHolder(t, db, "disabled-accrual", common.ReferralCoinScale)
	require.NoError(t, service.RedeemReferralCoin(user.Id, common.ReferralCoinScale, "disabled-accrual-request"))
	balance, err := model.ReferralCoinBalance(user.Id)
	require.NoError(t, err)
	assert.Zero(t, balance)
}

func TestReferralBalanceOutOfRangeIsAnExplicitError(t *testing.T) {
	for _, balance := range []int{-1, common.MaxWalletQuota + 1} {
		t.Run(fmt.Sprint(balance), func(t *testing.T) {
			db := referralDatabase(t)
			user := coinHolder(t, db, "invalid-balance", balance)
			_, err := model.ReferralCoinBalance(user.Id)
			require.ErrorIs(t, err, model.ErrReferralCoinBalanceRange)
		})
	}
}

func TestReferralFrozenCancelledAndDueRewards(t *testing.T) {
	for _, tc := range []struct {
		name         string
		unlockOffset int64
		sourceStatus int
		topupStatus  string
		expected     string
		balance      int
	}{
		{"frozen", 86400, common.UserStatusEnabled, common.TopUpStatusSuccess, model.ReferralCoinPending, 0},
		{"due", -1, common.UserStatusEnabled, common.TopUpStatusSuccess, model.ReferralCoinCredited, common.ReferralCoinScale},
		{"disabled-source", -1, common.UserStatusDisabled, common.TopUpStatusSuccess, model.ReferralCoinCancelled, 0},
		{"unsettled-payment", -1, common.UserStatusEnabled, common.TopUpStatusPending, model.ReferralCoinCancelled, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := referralDatabase(t)
			inviter := model.User{Username: "mature-inviter", AffCode: "mature-inviter", Status: common.UserStatusEnabled}
			require.NoError(t, db.Create(&inviter).Error)
			source := model.User{Username: "mature-source", AffCode: "mature-source", Status: tc.sourceStatus, InviterId: inviter.Id}
			require.NoError(t, db.Create(&source).Error)
			require.NoError(t, db.Create(&model.TopUp{UserId: source.Id, TradeNo: "maturity-order", Status: tc.topupStatus}).Error)
			reward := model.ReferralCoinReward{UserId: inviter.Id, SourceUserId: source.Id, PaidAmount: 50000000, CoinAmount: common.ReferralCoinScale, Status: model.ReferralCoinPending, UnlockAt: common.GetTimestamp() + tc.unlockOffset, ReferenceType: "topup", ReferenceId: "maturity-order", IdempotencyKey: "maturity-reward"}
			require.NoError(t, model.CreateReferralCoinReward(&reward))
			for range 2 {
				_, err := model.MatureReferralCoinRewards(inviter.Id)
				require.NoError(t, err)
			}
			require.NoError(t, db.First(&reward, reward.Id).Error)
			assert.Equal(t, tc.expected, reward.Status)
			balance, err := model.ReferralCoinBalance(inviter.Id)
			require.NoError(t, err)
			assert.Equal(t, tc.balance, balance)
		})
	}
}

func TestReferralConcurrentRedemptionsNeverOverspend(t *testing.T) {
	db := referralDatabase(t)
	user := coinHolder(t, db, "concurrent-user", common.ReferralCoinScale)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results <- service.RedeemReferralCoin(user.Id, common.ReferralCoinScale, fmt.Sprintf("concurrent-request-%d", i))
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			assert.True(t, errors.Is(err, model.ErrReferralCoinInsufficient) || strings.Contains(strings.ToLower(err.Error()), "locked"), "unexpected concurrent result: %v", err)
		}
	}
	assert.Equal(t, 1, successes)
	require.NoError(t, db.First(&user, user.Id).Error)
	assert.Equal(t, int(common.QuotaPerUnit), user.QuotaBonus)
	balance, err := model.ReferralCoinBalance(user.Id)
	require.NoError(t, err)
	assert.Zero(t, balance)
}

func TestReferralPaidCallbackAndCommissionCommitTogether(t *testing.T) {
	db := referralDatabase(t)
	oldEnabled, oldRate, oldDays := common.ReferralCoinEnabled, common.ReferralCoinRate, common.ReferralCoinFreezeDays
	common.ReferralCoinEnabled, common.ReferralCoinRate, common.ReferralCoinFreezeDays = true, 0.01, 7
	t.Cleanup(func() {
		common.ReferralCoinEnabled, common.ReferralCoinRate, common.ReferralCoinFreezeDays = oldEnabled, oldRate, oldDays
	})
	inviter := model.User{Username: "payment-inviter", AffCode: "payment-inviter", Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&inviter).Error)
	source := model.User{Username: "payment-source", AffCode: "payment-source", Status: common.UserStatusEnabled, InviterId: inviter.Id}
	require.NoError(t, db.Create(&source).Error)
	order := model.TopUp{UserId: source.Id, TradeNo: "payment-commission-order", PaymentProvider: model.PaymentProviderEpay, PaymentMethod: "alipay", Status: common.TopUpStatusPending, Amount: 100, Money: 100}
	require.NoError(t, db.Create(&order).Error)
	const failReward = "test:referral-reward-insert-failure"
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register(failReward, func(tx *gorm.DB) {
		if tx.Statement.Table == "referral_coin_rewards" {
			tx.AddError(errors.New("reward storage failure"))
		}
	}))
	t.Cleanup(func() { db.Callback().Create().Remove(failReward) })
	_, err := model.RechargeEpay(order.TradeNo, "alipay", "100.00", "127.0.0.1")
	require.Error(t, err)
	require.NoError(t, db.First(&source, source.Id).Error)
	assert.Zero(t, source.QuotaPaid)
	require.NoError(t, db.First(&order, order.Id).Error)
	assert.Equal(t, common.TopUpStatusPending, order.Status)
	var rewards int64
	require.NoError(t, db.Model(&model.ReferralCoinReward{}).Count(&rewards).Error)
	assert.Zero(t, rewards)
	require.NoError(t, db.Callback().Create().Remove(failReward))
	already, err := model.RechargeEpay(order.TradeNo, "alipay", "100.00", "127.0.0.1")
	require.NoError(t, err)
	assert.False(t, already)
	already, err = model.RechargeEpay(order.TradeNo, "alipay", "100.00", "127.0.0.1")
	require.NoError(t, err)
	assert.True(t, already)
	var reward model.ReferralCoinReward
	require.NoError(t, db.Where("reference_id = ?", order.TradeNo).First(&reward).Error)
	assert.Equal(t, inviter.Id, reward.UserId)
	assert.Equal(t, common.ReferralCoinScale, reward.CoinAmount)
	assert.Equal(t, model.ReferralCoinPending, reward.Status)
	assert.Equal(t, int64(7*86400), reward.UnlockAt-reward.CreatedAt)
	require.NoError(t, db.Model(&model.ReferralCoinReward{}).Count(&rewards).Error)
	assert.EqualValues(t, 1, rewards)
	require.NoError(t, db.First(&source, source.Id).Error)
	assert.Equal(t, 50000000, source.QuotaPaid)
}

func TestReferralMaturityDatabaseFailureDoesNotCancelReward(t *testing.T) {
	db := referralDatabase(t)
	owner := model.User{Username: "error-owner", AffCode: "error-owner", Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&owner).Error)
	source := model.User{Username: "error-source", AffCode: "error-source", Status: common.UserStatusEnabled, InviterId: owner.Id}
	require.NoError(t, db.Create(&source).Error)
	require.NoError(t, db.Create(&model.TopUp{UserId: source.Id, TradeNo: "error-order", Status: common.TopUpStatusSuccess}).Error)
	reward := model.ReferralCoinReward{UserId: owner.Id, SourceUserId: source.Id, CoinAmount: 1000000, Status: model.ReferralCoinPending, UnlockAt: common.GetTimestamp() - 1, ReferenceType: "topup", ReferenceId: "error-order", IdempotencyKey: "error-reward"}
	require.NoError(t, model.CreateReferralCoinReward(&reward))
	const failQuery = "test:referral-topup-read-failure"
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(failQuery, func(tx *gorm.DB) {
		if tx.Statement.Table == "top_ups" {
			tx.AddError(errors.New("temporary topup read failure"))
		}
	}))
	t.Cleanup(func() { db.Callback().Query().Remove(failQuery) })
	_, err := model.MatureReferralCoinRewards(owner.Id)
	require.Error(t, err)
	require.NoError(t, db.First(&reward, reward.Id).Error)
	assert.Equal(t, model.ReferralCoinPending, reward.Status)
	balance, err := model.ReferralCoinBalance(owner.Id)
	require.NoError(t, err)
	assert.Zero(t, balance)
	require.NoError(t, db.Callback().Query().Remove(failQuery))
	_, err = model.MatureReferralCoinRewards(owner.Id)
	require.NoError(t, err)
	balance, err = model.ReferralCoinBalance(owner.Id)
	require.NoError(t, err)
	assert.Equal(t, 1000000, balance)
}
