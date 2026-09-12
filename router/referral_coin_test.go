package router

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/extension"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestReferralCoinRoutesEnforceIdentityAndAmount(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	conn, err := db.DB()
	require.NoError(t, err)
	conn.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.ReferralCoinReward{}, &model.ReferralCoinLedger{}, &model.BalanceLedger{}, &model.BalanceLedgerOperation{}))
	oldDB, oldLog := model.DB, model.LOG_DB
	oldRedis, oldLimit, oldCritical := common.RedisEnabled, common.GlobalApiRateLimitEnable, common.CriticalRateLimitEnable
	oldSecret := common.SessionSecret
	oldSecure := common.SessionCookieSecure
	oldMain, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
	model.DB, model.LOG_DB = db, db
	common.RedisEnabled, common.GlobalApiRateLimitEnable, common.CriticalRateLimitEnable = false, false, false
	common.SessionSecret = "referral-route-fixture-only"
	common.SessionCookieSecure = true
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLog
		common.RedisEnabled, common.GlobalApiRateLimitEnable, common.CriticalRateLimitEnable = oldRedis, oldLimit, oldCritical
		common.SessionSecret = oldSecret
		common.SessionCookieSecure = oldSecure
		common.SetDatabaseTypes(oldMain, oldLogType)
		require.NoError(t, conn.Close())
	})
	owner := model.User{Username: "referral-owner", AffCode: "referral-owner", Status: common.UserStatusEnabled, Role: common.RoleCommonUser, AuthVersion: 1}
	other := model.User{Username: "referral-other", AffCode: "referral-other", Status: common.UserStatusEnabled, Role: common.RoleCommonUser, AuthVersion: 1}
	require.NoError(t, db.Create(&owner).Error)
	require.NoError(t, db.Create(&other).Error)
	for _, user := range []model.User{owner, other} {
		require.NoError(t, db.Create(&model.ReferralCoinLedger{UserId: user.Id, Delta: 2 * common.ReferralCoinScale, Reason: "fixture", ReferenceType: "fixture", ReferenceId: user.Username, IdempotencyKey: user.Username}).Error)
	}
	first, err := service.CreateLoginSession(owner.Id, "password", "127.0.0.1", "fixture")
	require.NoError(t, err)
	second, err := service.CreateLoginSession(other.Id, "password", "127.0.0.1", "fixture")
	require.NoError(t, err)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	scopes := SetApiRouter(engine)
	require.NoError(t, newChuanRouteRegistry(t).ApplyRoutesWithScopes(scopes, extension.PriorityBeforePlugin))

	for _, tc := range []struct {
		name, token, origin string
		cookie              bool
		coins               int
		status              int
	}{
		{"anonymous", "", "", false, 1000000, http.StatusUnauthorized},
		{"fractional quota", first.AccessToken, "", false, 1, http.StatusBadRequest},
		{"above wallet range", first.AccessToken, "", false, common.MaxWalletQuota + 1, http.StatusBadRequest},
		{"foreign cookie alone grants no redemption access", "", "https://foreign.example", true, 1000000, http.StatusUnauthorized},
		{"same origin cookie alone grants no redemption access", "", "https://console.example", true, 1000000, http.StatusUnauthorized},
		{"first owner", first.AccessToken, "", false, 1000000, http.StatusOK},
		{"first owner retry", first.AccessToken, "", false, 1000000, http.StatusOK},
		{"second owner same key", second.AccessToken, "", false, 1000000, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := common.Marshal(map[string]any{"coins": tc.coins, "request_id": "route-request-0001", "user_id": other.Id})
			require.NoError(t, err)
			request := httptest.NewRequest(http.MethodPost, "https://console.example/api/user/referral/coins/redeem", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Origin", "https://console.example")
			if tc.token != "" {
				request.Header.Set("Authorization", "Bearer "+tc.token)
			}
			if tc.cookie {
				request.AddCookie(&http.Cookie{Name: service.RefreshCookieName, Value: first.RefreshToken})
				request.Header.Set("Origin", tc.origin)
			}
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			require.Equal(t, tc.status, response.Code)
			var result struct {
				Success bool `json:"success"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
			assert.Equal(t, tc.status == http.StatusOK, result.Success)
		})
	}
	for _, user := range []model.User{owner, other} {
		require.NoError(t, db.First(&user, user.Id).Error)
		assert.Equal(t, int(common.QuotaPerUnit), user.QuotaBonus)
		balance, err := model.ReferralCoinBalance(user.Id)
		require.NoError(t, err)
		assert.Equal(t, common.ReferralCoinScale, balance)
	}
	require.NoError(t, db.Model(&model.UserSession{}).Where("sid = ?", first.Session.SID).Update("expires_at", common.GetTimestamp()-1).Error)
	request := httptest.NewRequest(http.MethodGet, "https://console.example/api/user/referral/coins", nil)
	request.Header.Set("Origin", "https://console.example")
	request.Header.Set("Authorization", "Bearer "+first.AccessToken)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	assert.Equal(t, http.StatusUnauthorized, response.Code)
}
