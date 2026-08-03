package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newSessionAuthRouter 构建带 session cookie 存储、SessionAuth 保护端点与
// 登录端点的测试路由。受保护端点回显 SessionAuth 写入的上下文字段。
func newSessionAuthRouter(t *testing.T) (*gin.Engine, gin.HandlerFunc) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(sessions.Sessions("session", cookie.NewStore([]byte("session-auth-test"))))
	router.GET("/login", func(c *gin.Context) {
		session := sessions.Default(c)
		session.Set("username", "session-user")
		session.Set("role", common.RoleCommonUser)
		session.Set("id", 42)
		session.Set("status", common.UserStatusEnabled)
		session.Set("group", "default")
		require.NoError(t, session.Save())
		c.Status(http.StatusNoContent)
	})
	protected := SessionAuth()
	router.GET("/api/test", protected, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"success":      true,
			"username":     c.GetString("username"),
			"role":         c.GetInt("role"),
			"id":           c.GetInt("id"),
			"group":        c.GetString("group"),
			"user_group":   c.GetString("user_group"),
			"use_access":   c.GetBool("use_access_token"),
			"auth_version": c.Writer.Header().Get("Auth-Version"),
		})
	})
	return router, protected
}

func loginSessionCookie(t *testing.T, router *gin.Engine) []*http.Cookie {
	t.Helper()
	loginRecorder := httptest.NewRecorder()
	router.ServeHTTP(loginRecorder, httptest.NewRequest(http.MethodGet, "/login", nil))
	require.Equal(t, http.StatusNoContent, loginRecorder.Code)
	return loginRecorder.Result().Cookies()
}

func performSessionAuthRequest(t *testing.T, router *gin.Engine, authenticated bool, authHeader string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	if authHeader != "" {
		request.Header.Set("Authorization", authHeader)
	}
	if authenticated {
		for _, sessionCookie := range loginSessionCookie(t, router) {
			request.AddCookie(sessionCookie)
		}
	}
	router.ServeHTTP(recorder, request)
	return recorder
}

// TestSessionAuthAcceptsSessionWithoutNewApiUserHeader 是 SessionAuth 的核心契约：
// 只要携带有效浏览器 session cookie，就无需 New-Api-User 请求头，
// 且写入与 UserAuth 一致的上下文字段。
func TestSessionAuthAcceptsSessionWithoutNewApiUserHeader(t *testing.T) {
	router, _ := newSessionAuthRouter(t)
	recorder := performSessionAuthRequest(t, router, true, "")

	require.Equal(t, http.StatusOK, recorder.Code)
	var payload struct {
		Success     bool   `json:"success"`
		Username    string `json:"username"`
		Role        int    `json:"role"`
		Id          int    `json:"id"`
		Group       string `json:"group"`
		UserGroup   string `json:"user_group"`
		UseAccess   bool   `json:"use_access"`
		AuthVersion string `json:"auth_version"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	assert.True(t, payload.Success)
	assert.Equal(t, "session-user", payload.Username)
	assert.Equal(t, common.RoleCommonUser, payload.Role)
	assert.Equal(t, 42, payload.Id)
	assert.Equal(t, "default", payload.Group)
	assert.Equal(t, "default", payload.UserGroup)
	assert.False(t, payload.UseAccess)
	assert.Equal(t, "864b7076dbcd0a3c01b5520316720ebf", payload.AuthVersion)
}

// TestSessionAuthRejectsMissingSession 保证未登录请求返回 401，
// 与 UserAuth 的未登录语义一致。
func TestSessionAuthRejectsMissingSession(t *testing.T) {
	router, _ := newSessionAuthRouter(t)
	recorder := performSessionAuthRequest(t, router, false, "")

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

// TestSessionAuthIgnoresAccessToken 保证 SessionAuth 不接受 access token 兜底：
// 仅携带 Authorization 头而没有 session cookie 时仍返回 401。
func TestSessionAuthIgnoresAccessToken(t *testing.T) {
	router, _ := newSessionAuthRouter(t)
	recorder := performSessionAuthRequest(t, router, false, "Bearer sk-1234567890abcdef")

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

// TestSessionAuthRejectsBannedUser 与被封禁用户的 UserAuth 语义保持一致。
func TestSessionAuthRejectsBannedUser(t *testing.T) {
	router, _ := newSessionAuthRouter(t)
	router.GET("/login-banned", func(c *gin.Context) {
		session := sessions.Default(c)
		session.Set("username", "banned-user")
		session.Set("role", common.RoleCommonUser)
		session.Set("id", 43)
		session.Set("status", common.UserStatusDisabled)
		session.Set("group", "default")
		require.NoError(t, session.Save())
		c.Status(http.StatusNoContent)
	})
	loginRecorder := httptest.NewRecorder()
	router.ServeHTTP(loginRecorder, httptest.NewRequest(http.MethodGet, "/login-banned", nil))
	require.Equal(t, http.StatusNoContent, loginRecorder.Code)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	for _, sessionCookie := range loginRecorder.Result().Cookies() {
		request.AddCookie(sessionCookie)
	}
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	var payload struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	assert.False(t, payload.Success)
}

// TestUserAuthStillRequiresNewApiUserHeader 保护现有 UserAuth 行为不被本次
// SessionAuth 改动破坏：session 登录后缺少 New-Api-User 头仍然返回 401。
func TestUserAuthStillRequiresNewApiUserHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(sessions.Sessions("session", cookie.NewStore([]byte("user-auth-test"))))
	router.GET("/login", func(c *gin.Context) {
		session := sessions.Default(c)
		session.Set("username", "console-user")
		session.Set("role", common.RoleCommonUser)
		session.Set("id", 44)
		session.Set("status", common.UserStatusEnabled)
		session.Set("group", "default")
		require.NoError(t, session.Save())
		c.Status(http.StatusNoContent)
	})
	router.GET("/api/test", UserAuth(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	loginRecorder := httptest.NewRecorder()
	router.ServeHTTP(loginRecorder, httptest.NewRequest(http.MethodGet, "/login", nil))
	require.Equal(t, http.StatusNoContent, loginRecorder.Code)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	for _, sessionCookie := range loginRecorder.Result().Cookies() {
		request.AddCookie(sessionCookie)
	}
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}
