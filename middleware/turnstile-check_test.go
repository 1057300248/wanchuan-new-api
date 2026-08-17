package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type turnstileRoundTripper func(*http.Request) (*http.Response, error)

func (f turnstileRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestTurnstileCheckRequiresMatchingActionAndHostname(t *testing.T) {
	previousEnabled := common.TurnstileCheckEnabled
	previousSecret := common.TurnstileSecretKey
	previousClient := turnstileHTTPClient
	common.TurnstileCheckEnabled = true
	common.TurnstileSecretKey = "test-secret"
	t.Cleanup(func() {
		common.TurnstileCheckEnabled = previousEnabled
		common.TurnstileSecretKey = previousSecret
		turnstileHTTPClient = previousClient
	})

	tests := []struct {
		name       string
		response   string
		wantStatus int
	}{
		{
			name:       "matching action and hostname passes",
			response:   `{"success":true,"action":"login","hostname":"chuanapi.com"}`,
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "mismatched action is rejected",
			response:   `{"success":true,"action":"signup","hostname":"chuanapi.com"}`,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "mismatched hostname is rejected",
			response:   `{"success":true,"action":"login","hostname":"example.com"}`,
			wantStatus: http.StatusForbidden,
		},
	}

	gin.SetMode(gin.TestMode)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			turnstileHTTPClient = &http.Client{Transport: turnstileRoundTripper(func(request *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodPost, request.Method)
				require.NoError(t, request.ParseForm())
				assert.Equal(t, "test-secret", request.Form.Get("secret"))
				assert.Equal(t, "test-token", request.Form.Get("response"))
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(test.response)),
					Header:     make(http.Header),
				}, nil
			})}

			router := gin.New()
			router.GET("/verify", TurnstileCheck("login"), func(c *gin.Context) {
				c.Status(http.StatusNoContent)
			})
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/verify?turnstile=test-token", nil)
			router.ServeHTTP(recorder, request)

			assert.Equal(t, test.wantStatus, recorder.Code)
		})
	}
}

func TestTurnstileCheckSkipsVerificationWhenDisabled(t *testing.T) {
	previousEnabled := common.TurnstileCheckEnabled
	common.TurnstileCheckEnabled = false
	t.Cleanup(func() { common.TurnstileCheckEnabled = previousEnabled })

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/verify", TurnstileCheck("login"), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/verify", nil)
	router.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusNoContent, recorder.Code)
}

func TestTurnstileCheckRejectsReplayedToken(t *testing.T) {
	previousEnabled := common.TurnstileCheckEnabled
	previousSecret := common.TurnstileSecretKey
	previousClient := turnstileHTTPClient
	common.TurnstileCheckEnabled = true
	common.TurnstileSecretKey = "test-secret"
	t.Cleanup(func() {
		common.TurnstileCheckEnabled = previousEnabled
		common.TurnstileSecretKey = previousSecret
		turnstileHTTPClient = previousClient
	})

	verificationCount := 0
	turnstileHTTPClient = &http.Client{Transport: turnstileRoundTripper(func(_ *http.Request) (*http.Response, error) {
		verificationCount++
		body := `{"success":true,"action":"signup","hostname":"chuanapi.com"}`
		if verificationCount > 1 {
			body = `{"success":false,"error-codes":["timeout-or-duplicate"]}`
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/verify", TurnstileCheck("signup"), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	first := httptest.NewRecorder()
	router.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/verify?turnstile=single-use-token", nil))
	assert.Equal(t, http.StatusNoContent, first.Code)

	replay := httptest.NewRecorder()
	router.ServeHTTP(replay, httptest.NewRequest(http.MethodGet, "/verify?turnstile=single-use-token", nil))
	assert.Equal(t, http.StatusForbidden, replay.Code)
}
