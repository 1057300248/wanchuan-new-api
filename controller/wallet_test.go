package controller

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
		&model.AffiliateWithdrawal{},
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
	if body == "" {
		body = ""
	}
	c.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("id", userId)
	c.Set("role", role)
	// 提取 :id 路由参数（测试未挂载真实路由树）
	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(path, "/"), "/"), "/")
	for i, part := range parts {
		if part == "withdrawals" && i+1 < len(parts) {
			c.Params = append(c.Params, gin.Param{Key: "id", Value: parts[i+1]})
			break
		}
	}
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
		Status: common.UserStatusEnabled, Group: "default",
		Quota: 9000, QuotaPaid: 9000,
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
	// 他人流水：不得出现在本人账本查询结果中
	require.NoError(t, db.Create(&model.BalanceLedgerOperation{
		IdempotencyKey: "wallet-test-op-2", UserId: other.Id, Operation: "credit",
		OperationSignature: "s2", TotalBefore: 0, TotalAfter: 9000, CreatedAt: now,
	}).Error)
	require.NoError(t, db.Create(&model.BalanceLedger{
		OperationId: 2, UserId: other.Id, Account: "paid", Delta: 9000,
		BalanceBefore: 0, BalanceAfter: 9000, TotalBefore: 0, TotalAfter: 9000,
		Reason: "topup", ReferenceType: "topup", ReferenceId: "TP-2",
		IdempotencyKey: "wallet-test-op-2", CreatedAt: now,
	}).Error)

	// 钱包汇总
	c, recorder := performWalletRequest(t, http.MethodGet, "/api/user/wallet", "", user.Id, common.RoleCommonUser)
	GetWalletSummary(c)
	require.Equal(t, http.StatusOK, recorder.Code)
	body := recorder.Body.String()
	assert.Contains(t, body, `"total":1500`)
	assert.Contains(t, body, `"paid":1000`)
	assert.Contains(t, body, `"bonus":300`)
	assert.Contains(t, body, `"affiliate":200`)
	assert.Contains(t, body, `"affiliate_reward_rate":0.2`)

	// 账本分页：仅本人流水，account 过滤生效
	c, recorder = performWalletRequest(t, http.MethodGet, "/api/user/wallet/ledger?p=1&page_size=10", "", user.Id, common.RoleCommonUser)
	GetWalletLedger(c)
	require.Equal(t, http.StatusOK, recorder.Code)
	body = recorder.Body.String()
	assert.Contains(t, body, `"total":1`)
	assert.Contains(t, body, `"before":500`)
	assert.Contains(t, body, `"after":1000`)
	assert.Contains(t, body, `"delta":500`)
	assert.Contains(t, body, `"reference":"TP-1"`)
	assert.Contains(t, body, `"reason":"topup"`)
	assert.NotContains(t, body, "TP-2")

	c, recorder = performWalletRequest(t, http.MethodGet, "/api/user/wallet/ledger?account=bonus", "", user.Id, common.RoleCommonUser)
	GetWalletLedger(c)
	assert.Contains(t, recorder.Body.String(), `"total":0`)

	// 非法账户过滤：拒绝
	c, recorder = performWalletRequest(t, http.MethodGet, "/api/user/wallet/ledger?account=hacker", "", user.Id, common.RoleCommonUser)
	GetWalletLedger(c)
	assert.NotContains(t, recorder.Body.String(), `"success":true`)
}

func TestPostReferralWithdrawalFlow(t *testing.T) {
	db := setupWalletTestDB(t)
	user := model.User{
		Username: "wallet-withdraw-user", Password: "password", AffCode: "ww-aff-1", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default",
		Quota: 800, QuotaAffiliate: 800,
	}
	require.NoError(t, db.Create(&user).Error)

	previousEnabled := common.AffiliateRewardEnabled
	previousMin := common.AffiliateMinWithdrawQuota
	common.AffiliateRewardEnabled = true
	common.AffiliateMinWithdrawQuota = 100
	t.Cleanup(func() {
		common.AffiliateRewardEnabled = previousEnabled
		common.AffiliateMinWithdrawQuota = previousMin
	})

	// 低于最低限额
	c, recorder := performWalletRequest(t, http.MethodPost, "/api/user/referral/withdrawals", `{"amount":99,"request_id":"WD-CTRL-1"}`, user.Id, common.RoleCommonUser)
	PostReferralWithdrawal(c)
	assert.NotContains(t, recorder.Body.String(), `"success":true`)

	// 正常申请
	c, recorder = performWalletRequest(t, http.MethodPost, "/api/user/referral/withdrawals", `{"amount":300,"request_id":"WD-CTRL-1"}`, user.Id, common.RoleCommonUser)
	PostReferralWithdrawal(c)
	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":true`)
	assert.Contains(t, recorder.Body.String(), `"replayed":false`)

	var stored model.AffiliateWithdrawal
	require.NoError(t, db.Where("request_id = ?", "WD-CTRL-1").First(&stored).Error)
	assert.Equal(t, string(model.WithdrawalPending), stored.Status)

	var updated model.User
	require.NoError(t, db.First(&updated, user.Id).Error)
	assert.Equal(t, 500, updated.QuotaAffiliate)

	// 同 request_id 重试：幂等重放
	c, recorder = performWalletRequest(t, http.MethodPost, "/api/user/referral/withdrawals", `{"amount":300,"request_id":"WD-CTRL-1"}`, user.Id, common.RoleCommonUser)
	PostReferralWithdrawal(c)
	assert.Contains(t, recorder.Body.String(), `"replayed":true`)
	require.NoError(t, db.First(&updated, user.Id).Error)
	assert.Equal(t, 500, updated.QuotaAffiliate, "重放不得重复扣减")

	// 功能开关关闭：不允许新提现，历史记录仍可查询
	common.AffiliateRewardEnabled = false
	c, recorder = performWalletRequest(t, http.MethodPost, "/api/user/referral/withdrawals", `{"amount":300,"request_id":"WD-CTRL-2"}`, user.Id, common.RoleCommonUser)
	PostReferralWithdrawal(c)
	assert.NotContains(t, recorder.Body.String(), `"success":true`)
	c, recorder = performWalletRequest(t, http.MethodGet, "/api/user/referral/withdrawals", "", user.Id, common.RoleCommonUser)
	GetReferralWithdrawals(c)
	assert.Contains(t, recorder.Body.String(), `"total":1`)
}

func TestAdminWithdrawalApprovalLifecycle(t *testing.T) {
	db := setupWalletTestDB(t)
	user := model.User{
		Username: "wallet-admin-withdraw", Password: "password", AffCode: "wa-aff-1", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default",
		Quota: 500, QuotaAffiliate: 500,
	}
	require.NoError(t, db.Create(&user).Error)
	// 通过真实申请路径创建：冻结扣减 300，余额 500 → 200
	previousEnabled := common.AffiliateRewardEnabled
	previousMin := common.AffiliateMinWithdrawQuota
	common.AffiliateRewardEnabled = true
	common.AffiliateMinWithdrawQuota = 100
	t.Cleanup(func() {
		common.AffiliateRewardEnabled = previousEnabled
		common.AffiliateMinWithdrawQuota = previousMin
	})
	withdrawal, _, err := model.CreateAffiliateWithdrawal(user.Id, 300, "WD-ADMIN-1")
	require.NoError(t, err)

	// 拒绝：原路退回 affiliate 并保留审计状态
	c, recorder := performWalletRequest(t, http.MethodPost, fmt.Sprintf("/api/user/referral/withdrawals/%d/reject", withdrawal.Id), `{"reason":"资料不符"}`, 9999, common.RoleRootUser)
	AdminRejectWithdrawal(c)
	assert.Contains(t, recorder.Body.String(), `"success":true`)
	var updated model.User
	require.NoError(t, db.First(&updated, user.Id).Error)
	assert.Equal(t, 500, updated.QuotaAffiliate)
	var rejected model.AffiliateWithdrawal
	require.NoError(t, db.First(&rejected, withdrawal.Id).Error)
	assert.Equal(t, string(model.WithdrawalRejected), rejected.Status)
	assert.Equal(t, "资料不符", rejected.RejectReason)

	// 审批被拒绝的申请：必须报错（资金已退回）
	c, recorder = performWalletRequest(t, http.MethodPost, fmt.Sprintf("/api/user/referral/withdrawals/%d/approve", withdrawal.Id), "", 9999, common.RoleRootUser)
	AdminApproveWithdrawal(c)
	assert.NotContains(t, recorder.Body.String(), `"success":true`)

	// 新申请 → 审批 → 支付
	withdrawal2 := model.AffiliateWithdrawal{
		UserId: user.Id, Amount: 200, Status: string(model.WithdrawalPending),
		RequestId: "WD-ADMIN-2", CreatedAt: common.GetTimestamp(),
	}
	require.NoError(t, db.Create(&withdrawal2).Error)
	c, recorder = performWalletRequest(t, http.MethodPost, fmt.Sprintf("/api/user/referral/withdrawals/%d/approve", withdrawal2.Id), "", 9999, common.RoleRootUser)
	AdminApproveWithdrawal(c)
	assert.Contains(t, recorder.Body.String(), `"success":true`)
	c, recorder = performWalletRequest(t, http.MethodGet, "/api/user/referral/withdrawals?status=approved", "", 9999, common.RoleRootUser)
	AdminListWithdrawals(c)
	assert.Contains(t, recorder.Body.String(), `"total":1`)

	// 已审批的申请不允许拒绝（资金已冻结，走人工支付）
	c, recorder = performWalletRequest(t, http.MethodPost, fmt.Sprintf("/api/user/referral/withdrawals/%d/reject", withdrawal2.Id), `{"reason":"late"}`, 9999, common.RoleRootUser)
	AdminRejectWithdrawal(c)
	assert.NotContains(t, recorder.Body.String(), `"success":true`)

	c, recorder = performWalletRequest(t, http.MethodPost, fmt.Sprintf("/api/user/referral/withdrawals/%d/pay", withdrawal2.Id), "", 9999, common.RoleRootUser)
	AdminPayWithdrawal(c)
	assert.Contains(t, recorder.Body.String(), `"success":true`)
	var paid model.AffiliateWithdrawal
	require.NoError(t, db.First(&paid, withdrawal2.Id).Error)
	assert.Equal(t, string(model.WithdrawalPaid), paid.Status)
}
