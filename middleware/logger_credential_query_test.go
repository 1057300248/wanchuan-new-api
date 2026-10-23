package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Preserve the observable log/request contract, not Gin's internal path format.
func TestAccessLogRedactsCredentialQueryWithoutChangingRequest(t *testing.T) {
	tests := []struct {
		name          string
		path          string
		query         string
		want          string
		mutateRequest bool
	}{
		{name: "encoded question mark in path", path: "/v1beta/models/part%3Fsection", query: "key=fixture-secret&page=2", want: "key=[REDACTED]&page=2"},
		{name: "downstream mutation cannot replace snapshot", query: "key=fixture-secret&page=2", want: "key=[REDACTED]&page=2", mutateRequest: true},
		{name: "Gemini key", query: "key=fixture-secret&page=2", want: "key=[REDACTED]&page=2"},
		{name: "duplicate and encoded names", query: "key=fixture-secret&%6b%65%79=fixture-secret&KEY=fixture-secret", want: "key=[REDACTED]&%6b%65%79=[REDACTED]&KEY=[REDACTED]"},
		{name: "credential aliases", query: "api_key=fixture-secret&x-goog-api-key=fixture-secret&mj-api-secret=fixture-secret&access_token=fixture-secret&client_secret=fixture-secret", want: "api_key=[REDACTED]&x-goog-api-key=[REDACTED]&mj-api-secret=[REDACTED]&access_token=[REDACTED]&client_secret=[REDACTED]"},
		{name: "signature and session aliases", query: "signature=fixture-secret&sig=fixture-secret&sign=fixture-secret&session=fixture-secret&session_id=fixture-secret&sid=fixture-secret", want: "signature=[REDACTED]&sig=[REDACTED]&sign=[REDACTED]&session=[REDACTED]&session_id=[REDACTED]&sid=[REDACTED]"},
		{name: "signed URL aliases", query: "X-Amz-Signature=fixture-secret&X-Amz-Credential=fixture-secret&X-Amz-Security-Token=fixture-secret", want: "X-Amz-Signature=[REDACTED]&X-Amz-Credential=[REDACTED]&X-Amz-Security-Token=[REDACTED]"},
		{name: "encoded value", query: "key=fixture%2Dsecret&model=demo%2Fmodel", want: "key=[REDACTED]&model=demo%2Fmodel"},
		{name: "empty values", query: "key&key=&page=1", want: "key=[REDACTED]&key=[REDACTED]&page=1"},
		{name: "malformed query fails closed", query: "key=fixture-secret&bad=%ZZ", want: "[REDACTED]"},
		{name: "malformed encoded name fails closed", query: "%ZZ=fixture-secret&page=2", want: "[REDACTED]"},
		{name: "semicolon query fails closed", query: "page=1;key=fixture-secret", want: "[REDACTED]"},
		{name: "harmless diagnostics retain bytes", query: "page=2&model=demo%2Fmodel&apikey_hint=visible", want: "page=2&model=demo%2Fmodel&apikey_hint=visible"},
		{name: "no query"},
		{name: "OAuth API callback", path: "/api/oauth/fixture", query: "code=fixture-secret&state=fixture-secret"},
		{name: "OAuth callback", path: "/oauth/fixture", query: "code=fixture-secret&state=fixture-secret"},
		{name: "Stripe callback", path: "/api/stripe/webhook", query: "provider_custom=fixture-secret"},
		{name: "Creem callback", path: "/api/creem/webhook", query: "provider_custom=fixture-secret"},
		{name: "Waffo callback", path: "/api/waffo/webhook", query: "provider_custom=fixture-secret"},
		{name: "Waffo pancake callback", path: "/api/waffo-pancake/webhook/fixture", query: "provider_custom=fixture-secret"},
		{name: "topup Epay callback", path: "/api/user/epay/notify", query: "provider_custom=fixture-secret"},
		{name: "subscription Epay callback", path: "/api/subscription/epay/notify", query: "provider_custom=fixture-secret"},
		{name: "subscription Epay return", path: "/api/subscription/epay/return", query: "provider_custom=fixture-secret"},
	}

	originalWriter := gin.DefaultWriter
	originalMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() {
		gin.DefaultWriter = originalWriter
		gin.SetMode(originalMode)
	})

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var logs bytes.Buffer
			gin.DefaultWriter = &logs
			server := gin.New()
			SetUpLogger(server)
			path := test.path
			if path == "" {
				path = "/v1beta/models/demo"
			}
			target := path
			if test.query != "" {
				target += "?" + test.query
			}
			request := httptest.NewRequest(http.MethodGet, target, nil)
			originalURL := *request.URL
			server.NoRoute(func(c *gin.Context) {
				require.Equal(t, originalURL, *c.Request.URL, "display policy must not mutate any URL component")
				require.Equal(t, test.query, c.Request.URL.RawQuery, "log privacy must not change signature input")
				require.Equal(t, target, c.Request.RequestURI)
				if test.mutateRequest {
					c.Request.URL.Path = "/rewritten"
					c.Request.URL.RawQuery = "key=fixture-after-handler"
				}
				c.Status(http.StatusNoContent)
			})
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)
			require.Equal(t, http.StatusNoContent, response.Code)
			wantPath := path
			if test.want != "" {
				wantPath += "?" + test.want
			}
			require.Contains(t, logs.String(), "GET "+wantPath+"\n")
			require.NotContains(t, logs.String(), "fixture-secret")
			require.NotContains(t, logs.String(), "fixture%2Dsecret")
			require.NotContains(t, logs.String(), "fixture-after-handler")
		})
	}
}
