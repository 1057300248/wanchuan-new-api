package wallet

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupWalletTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousRedisEnabled := common.RedisEnabled
	previousMainDatabaseType, previousLogDatabaseType := common.MainDatabaseType(), common.LogDatabaseType()
	common.RedisEnabled = false
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB, model.LOG_DB = db, db
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.BalanceLedgerOperation{},
		&model.BalanceLedger{},
		&model.AffiliateReward{},
	))

	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled = previousRedisEnabled
		common.SetDatabaseTypes(previousMainDatabaseType, previousLogDatabaseType)
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func performWalletRequest(t *testing.T, method, path, body string, userId, role int) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("id", userId)
	c.Set("role", role)
	return c, recorder
}

func TestGetWalletSummaryAndLedger(t *testing.T) {
	db := setupWalletTestDB(t)
	user := model.User{
		Username: "wallet-summary-user", Password: "password", AffCode: "ws-aff-1", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default",
		Quota: 1500, QuotaPaid: 1000, QuotaBonus: 300, QuotaAffiliate: 200,
	}
	require.NoError(t, db.Create(&user).Error)
	other := model.User{
		Username: "wallet-summary-other", Password: "password", AffCode: "ws-aff-2", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: 9000, QuotaPaid: 9000,
	}
	require.NoError(t, db.Create(&other).Error)

	now := common.GetTimestamp()
	require.NoError(t, db.Create(&model.BalanceLedgerOperation{
		IdempotencyKey: "wallet-test-op-1", UserId: user.Id, Operation: "credit",
		OperationSignature: "s1", TotalBefore: 500, TotalAfter: 1000, CreatedAt: now,
	}).Error)
	require.NoError(t, db.Create(&model.BalanceLedger{
		OperationId: 1, UserId: user.Id, Account: "paid", Delta: 500,
		BalanceBefore: 500, BalanceAfter: 1000, TotalBefore: 500, TotalAfter: 1000,
		Reason: "topup", ReferenceType: "topup", ReferenceId: "TP-1",
		IdempotencyKey: "wallet-test-op-1", CreatedAt: now,
	}).Error)
	require.NoError(t, db.Create(&model.BalanceLedgerOperation{
		IdempotencyKey: "wallet-test-op-legacy-affiliate", UserId: user.Id, Operation: "credit",
		OperationSignature: "legacy", TotalBefore: 1000, TotalAfter: 1200, CreatedAt: now,
	}).Error)
	require.NoError(t, db.Create(&model.BalanceLedger{
		OperationId: 2, UserId: user.Id, Account: "affiliate", Delta: 200,
		BalanceBefore: 0, BalanceAfter: 200, TotalBefore: 1000, TotalAfter: 1200,
		Reason: "legacy_referral", ReferenceType: "legacy", ReferenceId: "AFF-OLD",
		IdempotencyKey: "wallet-test-op-legacy-affiliate", CreatedAt: now,
	}).Error)
	require.NoError(t, db.Create(&model.BalanceLedgerOperation{
		IdempotencyKey: "wallet-test-op-2", UserId: other.Id, Operation: "credit",
		OperationSignature: "s2", TotalBefore: 0, TotalAfter: 9000, CreatedAt: now,
	}).Error)
	require.NoError(t, db.Create(&model.BalanceLedger{
		OperationId: 3, UserId: other.Id, Account: "paid", Delta: 9000,
		BalanceBefore: 0, BalanceAfter: 9000, TotalBefore: 0, TotalAfter: 9000,
		Reason: "topup", ReferenceType: "topup", ReferenceId: "TP-2",
		IdempotencyKey: "wallet-test-op-2", CreatedAt: now,
	}).Error)

	c, recorder := performWalletRequest(t, http.MethodGet, "/api/user/wallet", "", user.Id, common.RoleCommonUser)
	GetWalletSummary(c)
	require.Equal(t, http.StatusOK, recorder.Code)
	body := recorder.Body.String()
	assert.Contains(t, body, `"total":1300`)
	assert.Contains(t, body, `"paid":1000`)
	assert.Contains(t, body, `"bonus":300`)
	assert.NotContains(t, body, `"affiliate"`)
	assert.Contains(t, body, `"referral_bonus_rate":0.05`)

	c, recorder = performWalletRequest(t, http.MethodGet, "/api/user/wallet/ledger?p=1&page_size=10", "", user.Id, common.RoleCommonUser)
	GetWalletLedger(c)
	require.Equal(t, http.StatusOK, recorder.Code)
	body = recorder.Body.String()
	assert.Contains(t, body, `"total":1`)
	assert.Contains(t, body, `"reference":"TP-1"`)
	assert.NotContains(t, body, "AFF-OLD")
	assert.NotContains(t, body, "TP-2")

	c, recorder = performWalletRequest(t, http.MethodGet, "/api/user/wallet/ledger?account=affiliate", "", user.Id, common.RoleCommonUser)
	GetWalletLedger(c)
	assert.Contains(t, recorder.Body.String(), `"success":false`)
	assert.Contains(t, recorder.Body.String(), "account")
}

func TestGetReferralOverviewUsesBonusVocabulary(t *testing.T) {
	db := setupWalletTestDB(t)
	user := model.User{
		Username: "referral-overview-user", Password: "password", AffCode: "bonus-code", AffCount: 2,
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default",
	}
	require.NoError(t, db.Create(&user).Error)
	now := common.GetTimestamp()
	require.NoError(t, db.Create(&model.AffiliateReward{
		UserId: user.Id, SourceUserId: user.Id + 1, EligiblePaidQuota: 1000, RewardQuota: 200,
		Status: string(model.AffiliateRewardPending), UnlockAt: now + 86400,
		ReferenceType: "relay", ReferenceId: "REQ-overview", IdempotencyKey: "affiliate_reward:relay:REQ-overview",
	}).Error)

	c, recorder := performWalletRequest(t, http.MethodGet, "/api/user/referral/overview", "", user.Id, common.RoleCommonUser)
	GetReferralOverview(c)
	require.Equal(t, http.StatusOK, recorder.Code)
	body := recorder.Body.String()
	assert.Contains(t, body, `"invite_count":2`)
	assert.Contains(t, body, `"eligible_paid_quota":1000`)
	assert.Contains(t, body, `"pending_bonus_quota":200`)
	assert.Contains(t, body, `"credited_bonus_quota":0`)
	assert.Contains(t, body, `"total_bonus_quota":200`)
	assert.NotContains(t, body, "withdraw")
}
