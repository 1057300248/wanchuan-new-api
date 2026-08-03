package router

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type playgroundRouteErrorPayload struct {
	Error struct {
		Message string          `json:"message"`
		Code    types.ErrorCode `json:"code"`
	} `json:"error"`
}

func setupPlaygroundRouterTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	originalIsMasterNode := common.IsMasterNode
	originalSQLitePath := common.SQLitePath
	originalMainDatabaseType := common.MainDatabaseType()
	originalLogDatabaseType := common.LogDatabaseType()
	originalSQLDSN, hadSQLDSN := os.LookupEnv("SQL_DSN")
	originalRedisEnabled := common.RedisEnabled
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	originalPerformanceConfig := common.GetPerformanceMonitorConfig()
	t.Cleanup(func() {
		common.IsMasterNode = originalIsMasterNode
		common.SQLitePath = originalSQLitePath
		common.SetDatabaseTypes(originalMainDatabaseType, originalLogDatabaseType)
		if hadSQLDSN {
			require.NoError(t, os.Setenv("SQL_DSN", originalSQLDSN))
		} else {
			require.NoError(t, os.Unsetenv("SQL_DSN"))
		}
		common.RedisEnabled = originalRedisEnabled
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		common.SetPerformanceMonitorConfig(originalPerformanceConfig)
	})

	gin.SetMode(gin.TestMode)
	common.IsMasterNode = false
	common.SQLitePath = fmt.Sprintf("file:%s_init?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	common.MemoryCacheEnabled = false
	common.SetPerformanceMonitorConfig(common.PerformanceMonitorConfig{Enabled: false})
	require.NoError(t, os.Setenv("SQL_DSN", "local"))

	// 初始化列名（commonGroupCol 等），与 controller 包测试夹具一致
	require.NoError(t, model.InitDB())
	if model.DB != nil {
		sqlDB, err := model.DB.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	}

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	model.LOG_DB = db

	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Channel{}, &model.Ability{}, &model.Model{}, &model.Vendor{}))

	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})

	return db
}

func newPlaygroundRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(sessions.Sessions("session", cookie.NewStore([]byte("playground-router-test"))))
	registerPlaygroundRoutes(router.Group("/api"))
	return router
}

func loginPlaygroundSession(t *testing.T, router *gin.Engine, userId int) []*http.Cookie {
	t.Helper()
	router.GET("/test-login", func(c *gin.Context) {
		session := sessions.Default(c)
		session.Set("id", userId)
		session.Set("username", "pg-route-user")
		session.Set("role", common.RoleCommonUser)
		session.Set("status", common.UserStatusEnabled)
		session.Set("group", "default")
		require.NoError(t, session.Save())
		c.Status(http.StatusNoContent)
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/test-login", nil))
	require.Equal(t, http.StatusNoContent, recorder.Code)
	return recorder.Result().Cookies()
}

func performPlaygroundRouteRequest(t *testing.T, router *gin.Engine, method string, path string, body string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	var request *http.Request
	if body == "" {
		request = httptest.NewRequest(method, path, nil)
	} else {
		request = httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
	}
	for _, sessionCookie := range cookies {
		request.AddCookie(sessionCookie)
	}
	router.ServeHTTP(recorder, request)
	return recorder
}

// TestPlaygroundRoutesRejectUnauthenticatedRequests 未登录（无 session cookie）
// 时 bootstrap 与 chat 均返回 401。
func TestPlaygroundRoutesRejectUnauthenticatedRequests(t *testing.T) {
	setupPlaygroundRouterTestDB(t)
	router := newPlaygroundRouter(t)

	bootstrapRecorder := performPlaygroundRouteRequest(t, router, http.MethodGet, "/api/user/playground/bootstrap", "", nil)
	require.Equal(t, http.StatusUnauthorized, bootstrapRecorder.Code)

	chatRecorder := performPlaygroundRouteRequest(t, router, http.MethodPost, "/api/user/playground/chat",
		`{"model":"zz-route-model","messages":[{"role":"user","content":"hi"}]}`, nil)
	require.Equal(t, http.StatusUnauthorized, chatRecorder.Code)
}

// TestPlaygroundBootstrapServesSessionUserWithoutNewApiUserHeader session 登录后
// 无需 New-Api-User 请求头即可获取 bootstrap；模型跨分组去重排序，无敏感字段。
func TestPlaygroundBootstrapServesSessionUserWithoutNewApiUserHeader(t *testing.T) {
	db := setupPlaygroundRouterTestDB(t)
	user := model.User{
		Id:          2301,
		Username:    "pg-route-bootstrap-user",
		Password:    "password123",
		DisplayName: "Route Bootstrap",
		Email:       "route-secret@example.com",
		Group:       "default",
		Status:      common.UserStatusEnabled,
		Quota:       6666,
	}
	require.NoError(t, db.Create(&user).Error)
	priority := int64(0)
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "default", Model: "zz-route-b-model", ChannelId: 1, Enabled: true, Priority: &priority},
		{Group: "default", Model: "zz-route-a-model", ChannelId: 1, Enabled: true, Priority: &priority},
		{Group: "vip", Model: "zz-route-b-model", ChannelId: 2, Enabled: true, Priority: &priority},
	}).Error)

	router := newPlaygroundRouter(t)
	cookies := loginPlaygroundSession(t, router, user.Id)
	recorder := performPlaygroundRouteRequest(t, router, http.MethodGet, "/api/user/playground/bootstrap", "", cookies)

	require.Equal(t, http.StatusOK, recorder.Code)
	body := recorder.Body.String()
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &struct {
		Success bool `json:"success"`
	}{}))
	assert.Contains(t, body, `"id":2301`)
	assert.Contains(t, body, `"username":"pg-route-bootstrap-user"`)
	assert.Contains(t, body, `"display_name":"Route Bootstrap"`)
	assert.Contains(t, body, `"quota":6666`)
	assert.Contains(t, body, `"zz-route-a-model"`)
	assert.Contains(t, body, `"zz-route-b-model"`)
	assert.Contains(t, body, `"models_by_group"`)
	// 排序：a 在 b 之前
	assert.Less(t, strings.Index(body, "zz-route-a-model"), strings.Index(body, "zz-route-b-model"))
	for _, sensitive := range []string{"route-secret@example.com", "password123", "access_token", `"email"`} {
		assert.NotContains(t, body, sensitive)
	}
}

// TestPlaygroundChatRouteWithSessionEnforcesQuotaGate 验证新 chat 路由在
// session 登录（无 New-Api-User 头）下走完整 Distribute 链路，
// quota <= 0 时返回 403 insufficient_user_quota。
func TestPlaygroundChatRouteWithSessionEnforcesQuotaGate(t *testing.T) {
	db := setupPlaygroundRouterTestDB(t)
	user := model.User{
		Id:       2302,
		Username: "pg-route-quota-user",
		Password: "password123",
		Group:    "default",
		Status:   common.UserStatusEnabled,
		Quota:    0,
	}
	require.NoError(t, db.Create(&user).Error)
	priority := int64(0)
	require.NoError(t, db.Create(&model.Channel{
		Id:     701,
		Type:   constant.ChannelTypeOpenAI,
		Name:   "pg-route-channel",
		Key:    "sk-route-test-key",
		Status: common.ChannelStatusEnabled,
		Group:  "default",
		Models: "zz-route-chat-model",
	}).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group:     "default",
		Model:     "zz-route-chat-model",
		ChannelId: 701,
		Enabled:   true,
		Priority:  &priority,
	}).Error)

	router := newPlaygroundRouter(t)
	cookies := loginPlaygroundSession(t, router, user.Id)
	recorder := performPlaygroundRouteRequest(t, router, http.MethodPost, "/api/user/playground/chat",
		`{"model":"zz-route-chat-model","messages":[{"role":"user","content":"hi"}]}`, cookies)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	var payload playgroundRouteErrorPayload
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	assert.Equal(t, types.ErrorCodeInsufficientUserQuota, payload.Error.Code)
}

// TestPlaygroundRoutesRegisterWithoutConflict 保证 playground 路由与
// /api/user 下既有静态/参数路由（如 GET /api/user/:id）注册无冲突。
func TestPlaygroundRoutesRegisterWithoutConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	require.NotPanics(t, func() {
		SetApiRouter(engine)
	})

	found := map[string]bool{}
	for _, route := range engine.Routes() {
		if strings.HasPrefix(route.Path, "/api/user/playground") {
			found[route.Method+" "+route.Path] = true
		}
	}
	assert.Equal(t, map[string]bool{
		"GET /api/user/playground/bootstrap": true,
		"POST /api/user/playground/chat":     true,
	}, found)
}
