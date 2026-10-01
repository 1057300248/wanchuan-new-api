package channel

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func tenantTestContext(user, token int) *gin.Context {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	if user > 0 {
		ctx.Set("id", user)
	}
	if token > 0 {
		ctx.Set("token_id", token)
	}
	return ctx
}

func tenantTestRelayInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ApiKey: "test-channel-secret-not-a-real-credential",
		HeadersOverride: map[string]any{
			"*":                "",
			"X-Kiro2CC-Tenant": authenticatedTenantPlaceholder,
		},
	}}
}

func TestAuthenticatedTenantIgnoresUnverifiedAuthenticationHeaders(t *testing.T) {
	info := tenantTestRelayInfo()
	a, b := tenantTestContext(11, 101), tenantTestContext(22, 202)
	for _, ctx := range []*gin.Context{a, b} {
		ctx.Request.Header.Set("Authorization", "midjourney-proxy")
		ctx.Request.Header.Set("X-Kiro2CC-Tenant", "attacker-chosen-same-scope")
	}
	a.Request.Header.Set("mj-api-secret", "user-a")
	b.Request.Header.Set("mj-api-secret", "user-b")
	ha, err := processHeaderOverride(info, a)
	require.NoError(t, err)
	hb, err := processHeaderOverride(info, b)
	require.NoError(t, err)
	require.NotEqual(t, ha["x-kiro2cc-tenant"], hb["x-kiro2cc-tenant"])
	require.True(t, strings.HasPrefix(ha["x-kiro2cc-tenant"], "newapi:v1:"))
	require.NotContains(t, ha["x-kiro2cc-tenant"], "user-a")
	require.NotEqual(t, "attacker-chosen-same-scope", ha["x-kiro2cc-tenant"])

	// The same authenticated identity remains stable across header spelling,
	// Bearer versus alternate authentication, and hostile incoming tenant values.
	a.Request.Header.Set("Authorization", "Bearer a-different-raw-spelling")
	a.Request.Header.Set("x-api-key", "an-unverified-extra-header")
	a.Request.Header.Set("X-Kiro2CC-Tenant", hb["x-kiro2cc-tenant"])
	ha2, err := processHeaderOverride(info, a)
	require.NoError(t, err)
	require.Equal(t, ha["x-kiro2cc-tenant"], ha2["x-kiro2cc-tenant"])

	request := httptest.NewRequest(http.MethodPost, "http://kiro.test/v1/responses", nil)
	request.Header.Set("X-Kiro2CC-Tenant", "spoofed")
	applyHeaderOverrideToRequest(request, ha2)
	require.Equal(t, ha["x-kiro2cc-tenant"], request.Header.Get("X-Kiro2CC-Tenant"))
}

func TestAuthenticatedTenantRequiresVerifiedContextAndChannelKey(t *testing.T) {
	for _, ctx := range []*gin.Context{nil, tenantTestContext(0, 0), tenantTestContext(1, 0), tenantTestContext(0, 1)} {
		_, _, err := applyHeaderOverridePlaceholders(authenticatedTenantPlaceholder, ctx, "channel-key")
		require.Error(t, err)
	}
	_, _, err := applyHeaderOverridePlaceholders(authenticatedTenantPlaceholder, tenantTestContext(1, 2), "")
	require.Error(t, err)
}

func TestAuthenticatedTenantChannelTestsHaveSeparateNamespace(t *testing.T) {
	info := tenantTestRelayInfo()
	userHeaders, err := processHeaderOverride(info, tenantTestContext(1, 2))
	require.NoError(t, err)
	info.IsChannelTest = true
	testHeaders, err := processHeaderOverride(info, tenantTestContext(0, 0))
	require.NoError(t, err)
	require.NotEmpty(t, testHeaders["x-kiro2cc-tenant"])
	require.NotEqual(t, userHeaders["x-kiro2cc-tenant"], testHeaders["x-kiro2cc-tenant"])
	// Real-looking context in a channel test still cannot acquire a user scope.
	testHeaders2, err := processHeaderOverride(info, tenantTestContext(1, 2))
	require.NoError(t, err)
	require.Equal(t, testHeaders["x-kiro2cc-tenant"], testHeaders2["x-kiro2cc-tenant"])
}

func TestAuthenticatedTenantRejectsPartialTemplateAndSeparatesTokens(t *testing.T) {
	ctx := tenantTestContext(1, 2)
	for _, value := range []string{"prefix-{authenticated_tenant}", "{authenticated_tenant}:suffix", "{authenticated_tenant", "{authenticated_tenant:Authorization}"} {
		_, _, err := applyHeaderOverridePlaceholders(value, ctx, "channel-key")
		require.Error(t, err)
	}
	a, include, err := applyHeaderOverridePlaceholders("  {authenticated_tenant}  ", ctx, "channel-key")
	require.NoError(t, err)
	require.True(t, include)
	b, _, err := applyHeaderOverridePlaceholders(authenticatedTenantPlaceholder, tenantTestContext(1, 3), "channel-key")
	require.NoError(t, err)
	require.NotEqual(t, a, b)
	c, _, err := applyHeaderOverridePlaceholders(authenticatedTenantPlaceholder, ctx, "rotated-channel-key")
	require.NoError(t, err)
	require.NotEqual(t, a, c)
}
