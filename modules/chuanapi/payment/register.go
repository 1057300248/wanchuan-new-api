package payment

import (
	"fmt"

	"github.com/QuantumNous/new-api/extension"
	"github.com/QuantumNous/new-api/middleware"
)

const (
	ExtensionName = "chuanapi"
	ModelsName    = "payment_order_bindings"
	RoutesName    = "payment_routes"
)

// Register owns ChuanAPI payment-provider route and handler contribution. The
// upstream-hot router/api-router.go and root controller packages do not know
// concrete Waffo/Waffo-Pancake endpoints.
func Register(reg *extension.Registry) error {
	if reg == nil {
		return fmt.Errorf("chuanapi payment: registry must not be nil")
	}
	if err := reg.RegisterModelSet(extension.ModelSet{
		Extension: ExtensionName,
		Name:      ModelsName,
		Phase:     extension.PhaseAfterCoreModels,
		Models:    []any{&PaymentOrderBinding{}},
	}); err != nil {
		return err
	}
	return reg.RegisterRouteSet(extension.RouteSet{
		Extension:      ExtensionName,
		Name:           RoutesName,
		Priority:       extension.PriorityBeforePlugin,
		RegisterScoped: registerRoutes,
	})
}

func registerRoutes(scopes extension.RouteScopes) error {
	if scopes.API == nil || scopes.UserSelf == nil || scopes.SubscriptionUser == nil || scopes.RootOptions == nil || scopes.AnonymousRequestBodyLimit == nil {
		return fmt.Errorf("chuanapi payment: required host API route scopes are unavailable")
	}

	scopes.API.POST("/waffo/webhook", scopes.AnonymousRequestBodyLimit, WaffoWebhook)
	scopes.API.POST("/waffo-pancake/webhook/:env", scopes.AnonymousRequestBodyLimit, WaffoPancakeWebhook)

	scopes.UserSelf.POST("/waffo/amount", RequestWaffoAmount)
	scopes.UserSelf.POST("/waffo/pay", middleware.CriticalRateLimit(), RequestWaffoPay)
	scopes.UserSelf.POST("/waffo-pancake/amount", RequestWaffoPancakeAmount)
	scopes.UserSelf.POST("/waffo-pancake/pay", middleware.CriticalRateLimit(), RequestWaffoPancakePay)

	scopes.SubscriptionUser.POST("/waffo-pancake/pay", middleware.CriticalRateLimit(), SubscriptionRequestWaffoPancakePay)

	scopes.RootOptions.GET("/waffo-pancake/catalog", ListWaffoPancakeCatalog)
	scopes.RootOptions.POST("/waffo-pancake/pair", CreateWaffoPancakePair)
	scopes.RootOptions.POST("/waffo-pancake/save", SaveWaffoPancake)
	scopes.RootOptions.POST("/waffo-pancake/subscription-product", CreateWaffoPancakeSubscriptionProduct)
	scopes.RootOptions.GET("/waffo-pancake/subscription-product-options", ListWaffoPancakeSubscriptionProductOptions)
	return nil
}
