package middleware

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setCORSEnv 设置/清除 CONSOLE_CORS_ALLOW_ORIGINS，nil 表示清除，并自动还原。
func setCORSEnv(t *testing.T, raw *string) {
	t.Helper()
	previous, hadPrevious := os.LookupEnv("CONSOLE_CORS_ALLOW_ORIGINS")
	if raw == nil {
		require.NoError(t, os.Unsetenv("CONSOLE_CORS_ALLOW_ORIGINS"))
	} else {
		require.NoError(t, os.Setenv("CONSOLE_CORS_ALLOW_ORIGINS", *raw))
	}
	t.Cleanup(func() {
		if hadPrevious {
			require.NoError(t, os.Setenv("CONSOLE_CORS_ALLOW_ORIGINS", previous))
		} else {
			require.NoError(t, os.Unsetenv("CONSOLE_CORS_ALLOW_ORIGINS"))
		}
	})
}

// newGlobalCORSRequest 按生产顺序构建引擎：CORS 在任何路由注册前全局挂载，
// 再注册 /api 路由；预检请求（无 OPTIONS 路由）会走 NoRoute 链，
// 与生产冒烟发现的问题路径一致。
func newGlobalCORSRequest(method string, path string, origin string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(CORS())
	apiGroup := router.Group("/api")
	apiGroup.POST(strings.TrimPrefix(path, "/api"), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, nil)
	if origin != "" {
		request.Header.Set("Origin", origin)
		if method == http.MethodOptions {
			request.Header.Set("Access-Control-Request-Method", http.MethodPost)
			request.Header.Set("Access-Control-Request-Headers", "content-type")
		}
	}
	router.ServeHTTP(recorder, request)
	return recorder
}

// TestCORSTrustedOriginActualRequest 可信来源的真实请求回显精确 Origin
// 并携带凭据，路由 body 不受影响。
func TestCORSTrustedOriginActualRequest(t *testing.T) {
	prod := "https://chuanapi.com,https://www.chuanapi.com"
	setCORSEnv(t, &prod)

	for _, origin := range []string{"https://chuanapi.com", "https://www.chuanapi.com"} {
		recorder := newGlobalCORSRequest(http.MethodPost, "/api/user/playground/bootstrap", origin)

		require.Equal(t, http.StatusOK, recorder.Code)
		assert.Equal(t, origin, recorder.Header().Get("Access-Control-Allow-Origin"))
		assert.Equal(t, "true", recorder.Header().Get("Access-Control-Allow-Credentials"))
		assert.Contains(t, recorder.Body.String(), `"success":true`)
	}
}

// TestCORSTrustedOriginPreflight 可信来源的预检（走 NoRoute 链）同样回显
// 精确 Origin 并携带凭据，放行 GET/POST/OPTIONS 与 Content-Type/Accept 等
// 页面内试验场所需请求头；这是修复前的生产故障路径。
func TestCORSTrustedOriginPreflight(t *testing.T) {
	prod := "https://chuanapi.com"
	setCORSEnv(t, &prod)

	recorder := newGlobalCORSRequest(http.MethodOptions, "/api/user/playground/bootstrap", "https://chuanapi.com")

	require.Equal(t, http.StatusNoContent, recorder.Code)
	assert.Equal(t, "https://chuanapi.com", recorder.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "true", recorder.Header().Get("Access-Control-Allow-Credentials"))
	allowMethods := recorder.Header().Get("Access-Control-Allow-Methods")
	assert.Contains(t, allowMethods, "GET")
	assert.Contains(t, allowMethods, "POST")
	assert.Contains(t, allowMethods, "OPTIONS")
	allowHeaders := recorder.Header().Get("Access-Control-Allow-Headers")
	assert.Contains(t, allowHeaders, "Content-Type")
	assert.Contains(t, allowHeaders, "Accept")
	assert.NotEmpty(t, recorder.Header().Get("Access-Control-Max-Age"))
}

// TestCORSPublicOriginPreflightWildcardWithoutCredentials 不可信来源的预检
// 保持通配 * 且不带 credentials。
func TestCORSPublicOriginPreflightWildcardWithoutCredentials(t *testing.T) {
	prod := "https://chuanapi.com"
	setCORSEnv(t, &prod)

	recorder := newGlobalCORSRequest(http.MethodOptions, "/api/user/playground/bootstrap", "https://evil.example.com")

	require.Equal(t, http.StatusNoContent, recorder.Code)
	assert.Equal(t, "*", recorder.Header().Get("Access-Control-Allow-Origin"))
	assert.Empty(t, recorder.Header().Get("Access-Control-Allow-Credentials"))
}

// TestCORSPublicOriginActualRequestWildcardWithoutCredentials 不可信来源的
// 真实请求保持通配 * 且不带 credentials，不破坏 SDK/网页客户端。
func TestCORSPublicOriginActualRequestWildcardWithoutCredentials(t *testing.T) {
	prod := "https://chuanapi.com"
	setCORSEnv(t, &prod)

	recorder := newGlobalCORSRequest(http.MethodPost, "/api/user/playground/bootstrap", "https://evil.example.com")

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "*", recorder.Header().Get("Access-Control-Allow-Origin"))
	assert.Empty(t, recorder.Header().Get("Access-Control-Allow-Credentials"))
	assert.Contains(t, recorder.Body.String(), `"success":true`)
}

// TestCORSPassesThroughNoOriginRequest 同源请求（无 Origin 头）不写任何 CORS 头。
func TestCORSPassesThroughNoOriginRequest(t *testing.T) {
	prod := "https://chuanapi.com"
	setCORSEnv(t, &prod)

	recorder := newGlobalCORSRequest(http.MethodPost, "/api/user/playground/bootstrap", "")

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Empty(t, recorder.Header().Get("Access-Control-Allow-Origin"))
	assert.Empty(t, recorder.Header().Get("Access-Control-Allow-Credentials"))
}

// TestCORSOriginMatchingHostIsTreatedAsSameOrigin 即使带 Origin 头，
// Origin 与 Host 一致（fetch 同源请求）也不写任何 CORS 头。
func TestCORSOriginMatchingHostIsTreatedAsSameOrigin(t *testing.T) {
	prod := "https://chuanapi.com"
	setCORSEnv(t, &prod)
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.Use(CORS())
	router.GET("/status", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "https://chuanapi.com/status", nil)
	request.Header.Set("Origin", "https://chuanapi.com")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Empty(t, recorder.Header().Get("Access-Control-Allow-Origin"))
	assert.Empty(t, recorder.Header().Get("Access-Control-Allow-Credentials"))
}

// TestCORSActualRouteResponseNotOverridden 真实路由响应不会被全局通配 CORS
// 覆盖：可信来源回显精确 Origin 且路由 body/状态完整，不可信来源保持 *。
func TestCORSActualRouteResponseNotOverridden(t *testing.T) {
	prod := "https://chuanapi.com,https://www.chuanapi.com"
	setCORSEnv(t, &prod)

	trusted := newGlobalCORSRequest(http.MethodPost, "/api/user/playground/bootstrap", "https://www.chuanapi.com")
	require.Equal(t, http.StatusOK, trusted.Code)
	assert.Equal(t, "https://www.chuanapi.com", trusted.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "true", trusted.Header().Get("Access-Control-Allow-Credentials"))
	assert.Contains(t, trusted.Body.String(), `"success":true`)

	untrusted := newGlobalCORSRequest(http.MethodPost, "/api/user/playground/bootstrap", "https://evil.example.com")
	require.Equal(t, http.StatusOK, untrusted.Code)
	assert.Equal(t, "*", untrusted.Header().Get("Access-Control-Allow-Origin"))
	assert.Empty(t, untrusted.Header().Get("Access-Control-Allow-Credentials"))
	assert.Contains(t, untrusted.Body.String(), `"success":true`)
}

// TestCORSReadsAllowOriginsFromEnv 验证 CONSOLE_CORS_ALLOW_ORIGINS
// 逗号分隔列表的读取与空白项清理，并拒绝列表外来源（回退到通配无凭据）。
func TestCORSReadsAllowOriginsFromEnv(t *testing.T) {
	raw := " https://chuanapi.com ,  https://chuanapi.cn ,, "
	setCORSEnv(t, &raw)

	allowed := newGlobalCORSRequest(http.MethodOptions, "/api/user/playground/bootstrap", "https://chuanapi.com")
	require.Equal(t, http.StatusNoContent, allowed.Code)
	assert.Equal(t, "https://chuanapi.com", allowed.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "true", allowed.Header().Get("Access-Control-Allow-Credentials"))

	other := newGlobalCORSRequest(http.MethodOptions, "/api/user/playground/bootstrap", "https://chuanapi.cn")
	require.Equal(t, http.StatusNoContent, other.Code)
	assert.Equal(t, "https://chuanapi.cn", other.Header().Get("Access-Control-Allow-Origin"))

	denied := newGlobalCORSRequest(http.MethodOptions, "/api/user/playground/bootstrap", "https://evil.example.com")
	require.Equal(t, http.StatusNoContent, denied.Code)
	assert.Equal(t, "*", denied.Header().Get("Access-Control-Allow-Origin"))
	assert.Empty(t, denied.Header().Get("Access-Control-Allow-Credentials"))
}

// TestCORSEnvDefaultsToLocalOrigins 验证未配置环境变量时回退到本地默认白名单。
func TestCORSEnvDefaultsToLocalOrigins(t *testing.T) {
	setCORSEnv(t, nil)

	recorder := newGlobalCORSRequest(http.MethodOptions, "/api/user/playground/bootstrap", "http://localhost:3000")
	require.Equal(t, http.StatusNoContent, recorder.Code)
	assert.Equal(t, "http://localhost:3000", recorder.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "true", recorder.Header().Get("Access-Control-Allow-Credentials"))
}
