package payment

import (
	"testing"

	"github.com/QuantumNous/new-api/extension"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegisterContributesExpectedPaymentRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reg := extension.NewRegistry()
	require.NoError(t, Register(reg))
	models := reg.ModelsFor(extension.PhaseAfterCoreModels)
	require.Len(t, models, 1)
	assert.IsType(t, &PaymentOrderBinding{}, models[0])
	require.NoError(t, reg.Freeze())
	engine := gin.New()
	api := engine.Group("/api")
	scopes := extension.RouteScopes{
		Engine:                    engine,
		API:                       api,
		UserSelf:                  api.Group("/user"),
		SubscriptionUser:          api.Group("/subscription"),
		RootOptions:               api.Group("/option"),
		AnonymousRequestBodyLimit: func(c *gin.Context) { c.Next() },
	}
	require.NoError(t, reg.ApplyRoutesWithScopes(scopes, extension.PriorityBeforePlugin))

	got := map[string]bool{}
	for _, route := range engine.Routes() {
		got[route.Method+" "+route.Path] = true
	}
	for _, route := range []string{
		"POST /api/waffo/webhook",
		"POST /api/waffo-pancake/webhook/:env",
		"POST /api/user/waffo/amount",
		"POST /api/user/waffo/pay",
		"POST /api/user/waffo-pancake/amount",
		"POST /api/user/waffo-pancake/pay",
		"POST /api/subscription/waffo-pancake/pay",
		"GET /api/option/waffo-pancake/catalog",
		"POST /api/option/waffo-pancake/pair",
		"POST /api/option/waffo-pancake/save",
		"POST /api/option/waffo-pancake/subscription-product",
		"GET /api/option/waffo-pancake/subscription-product-options",
	} {
		assert.True(t, got[route], route)
	}
}
