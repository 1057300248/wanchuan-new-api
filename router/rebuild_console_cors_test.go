package router

import (
	"embed"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// CORS permits browser reads; it must never replace the session origin/auth
// guards, including when a CORS-only origin is deliberately configured.
func TestRebuildConsoleCORSPreservesSessionOriginGuard(t *testing.T) {
	t.Setenv("CONSOLE_CORS_ALLOW_ORIGINS", "https://console.example,https://cors-only.example")
	useRedirectFrontend(t)
	oldDB, oldRedis, oldSecret := model.DB, common.RedisEnabled, common.SessionSecret
	oldSecure, oldTrusted := common.SessionCookieSecure, common.SessionCookieTrustedURLs
	oldLimiter, oldDBType := common.GlobalApiRateLimitEnable, common.MainDatabaseType()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}))
	model.DB, common.RedisEnabled, common.SessionSecret = db, false, "rebuild-cors-synthetic-secret"
	common.SessionCookieSecure, common.SessionCookieTrustedURLs = true, []string{"https://console.example"}
	common.GlobalApiRateLimitEnable = false
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		model.DB, common.RedisEnabled, common.SessionSecret = oldDB, oldRedis, oldSecret
		common.SessionCookieSecure, common.SessionCookieTrustedURLs = oldSecure, oldTrusted
		common.GlobalApiRateLimitEnable = oldLimiter
		common.SetMainDatabaseType(oldDBType)
		sqlDB, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Close())
	})
	user := &model.User{Username: "cors-fixture", Password: "unused", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1}
	require.NoError(t, db.Create(user).Error)
	bundle, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "synthetic-cors")
	require.NoError(t, err)
	engine := gin.New()
	require.NoError(t, setRouterWithRegistry(engine, WebAssets{BuildFS: embed.FS{}}, newChuanRouteRegistry(t)))
	for _, tc := range []struct {
		name, origin string
		cookie       bool
		status       int
		credentials  bool
	}{
		{"allowed_with_valid_cookie", "https://console.example", true, http.StatusOK, true},
		{"allowed_without_cookie_is_still_unauthorized", "https://console.example", false, http.StatusUnauthorized, true},
		{"cors_does_not_grant_session_trust", "https://cors-only.example", true, http.StatusForbidden, true},
		{"foreign_cookie_is_forbidden", "https://foreign.example", true, http.StatusForbidden, false},
		{"lookalike_origin_is_not_allowed", "https://console.example.evil.invalid", true, http.StatusForbidden, false},
		{"opaque_origin_is_not_allowed", "null", true, http.StatusForbidden, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "https://api.example/api/user/auth/session", nil)
			req.Header.Set("Origin", tc.origin)
			if tc.cookie {
				req.AddCookie(&http.Cookie{Name: service.RefreshCookieName, Value: bundle.RefreshToken})
			}
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, req)
			assert.Equal(t, tc.status, recorder.Code)
			if tc.credentials {
				assert.Equal(t, tc.origin, recorder.Header().Get("Access-Control-Allow-Origin"))
				assert.Equal(t, "true", recorder.Header().Get("Access-Control-Allow-Credentials"))
			} else {
				assert.Equal(t, "*", recorder.Header().Get("Access-Control-Allow-Origin"))
				assert.Empty(t, recorder.Header().Get("Access-Control-Allow-Credentials"))
				assert.NotContains(t, recorder.Body.String(), user.Username)
			}
		})
	}
}
