package middleware

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newConsoleCORSRequest(method string, path string, origin string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(ConsoleCORS())
	router.POST(path, func(c *gin.Context) {
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

func withConsoleCORSEnv(t *testing.T, raw string) {
	t.Helper()
	previous, hadPrevious := os.LookupEnv("CONSOLE_CORS_ALLOW_ORIGINS")
	require.NoError(t, os.Setenv("CONSOLE_CORS_ALLOW_ORIGINS", raw))
	t.Cleanup(func() {
		if hadPrevious {
			require.NoError(t, os.Setenv("CONSOLE_CORS_ALLOW_ORIGINS", previous))
		} else {
			require.NoError(t, os.Unsetenv("CONSOLE_CORS_ALLOW_ORIGINS"))
		}
	})
}

// TestConsoleCORSPreflightAllowsDefaultLocalOrigins 验证默认白名单
// （http://localhost:3000 / http://127.0.0.1:3000）的预检请求携带凭据放行。
func TestConsoleCORSPreflightAllowsDefaultLocalOrigins(t *testing.T) {
	for _, origin := range []string{"http://localhost:3000", "http://127.0.0.1:3000"} {
		recorder := newConsoleCORSRequest(http.MethodOptions, "/api/user/playground/bootstrap", origin)

		require.Equal(t, http.StatusNoContent, recorder.Code)
		require.Equal(t, origin, recorder.Header().Get("Access-Control-Allow-Origin"))
		require.Equal(t, "true", recorder.Header().Get("Access-Control-Allow-Credentials"))
	}
}

// TestConsoleCORSPreflightRejectsUnknownOrigin 保证任意 Origin 不会与凭据同时放行。
func TestConsoleCORSPreflightRejectsUnknownOrigin(t *testing.T) {
	recorder := newConsoleCORSRequest(http.MethodOptions, "/api/user/playground/bootstrap", "https://evil.example.com")

	require.Equal(t, http.StatusForbidden, recorder.Code)
}

// TestConsoleCORSPassesThroughSameOriginRequest 同源请求（无 Origin 头）不受影响。
func TestConsoleCORSPassesThroughSameOriginRequest(t *testing.T) {
	recorder := newConsoleCORSRequest(http.MethodPost, "/api/user/playground/bootstrap", "")

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Empty(t, recorder.Header().Get("Access-Control-Allow-Origin"))
}

// TestConsoleCORSReadsAllowOriginsFromEnv 验证 CONSOLE_CORS_ALLOW_ORIGINS
// 逗号分隔列表的读取与空白项清理，并拒绝列表外来源。
func TestConsoleCORSReadsAllowOriginsFromEnv(t *testing.T) {
	withConsoleCORSEnv(t, " https://chuanapi.com ,  https://chuanapi.cn ,, ")

	allowed := newConsoleCORSRequest(http.MethodOptions, "/api/user/playground/bootstrap", "https://chuanapi.com")
	require.Equal(t, http.StatusNoContent, allowed.Code)
	require.Equal(t, "https://chuanapi.com", allowed.Header().Get("Access-Control-Allow-Origin"))
	require.Equal(t, "true", allowed.Header().Get("Access-Control-Allow-Credentials"))

	denied := newConsoleCORSRequest(http.MethodOptions, "/api/user/playground/bootstrap", "https://evil.example.com")
	require.Equal(t, http.StatusForbidden, denied.Code)
}

// TestConsoleCORSEnvDefaultsToLocalOrigins 验证未配置环境变量时回退到本地默认白名单。
func TestConsoleCORSEnvDefaultsToLocalOrigins(t *testing.T) {
	previous, hadPrevious := os.LookupEnv("CONSOLE_CORS_ALLOW_ORIGINS")
	if hadPrevious {
		require.NoError(t, os.Unsetenv("CONSOLE_CORS_ALLOW_ORIGINS"))
	}
	t.Cleanup(func() {
		if hadPrevious {
			require.NoError(t, os.Setenv("CONSOLE_CORS_ALLOW_ORIGINS", previous))
		}
	})

	recorder := newConsoleCORSRequest(http.MethodOptions, "/api/user/playground/bootstrap", "http://localhost:3000")
	require.Equal(t, http.StatusNoContent, recorder.Code)
	require.Equal(t, "http://localhost:3000", recorder.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "true", recorder.Header().Get("Access-Control-Allow-Credentials"))
}
