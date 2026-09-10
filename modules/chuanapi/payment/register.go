package payment

import (
	"fmt"

	"github.com/QuantumNous/new-api/extension"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
)

const (
	ExtensionName = "chuanapi"
	RoutesName    = "payment_routes"
)

// Register owns ChuanAPI payment-provider route and handler contribution. The
// upstream-hot router/api-router.go and root controller packages do not know
// concrete Waffo/Waffo-Pancake endpoints.
func Register(reg *extension.Registry) error {
	if reg == nil {
		return fmt.Errorf("chuanapi payment: registry must not be nil")
	}
	return reg.RegisterRouteSet(extension.RouteSet{
		Extension: ExtensionName,
		Name:      RoutesName,
		Priority:  extension.PriorityBeforePlugin,
		Register:  registerRoutes,
	})
}

func registerRoutes(engine *gin.Engine) error {
	api := engine.Group("/api")
	api.Use(middleware.RouteTag("api"))
	api.Use(gzip.Gzip(gzip.DefaultCompression))
	api.Use(middleware.AccessTokenAudit())
	api.Use(middleware.BodyStorageCleanup())
	api.Use(middleware.GlobalAPIRateLimit())

	anonymousBodyLimit := middleware.AnonymousRequestBodyLimit()
	api.POST("/waffo/webhook", anonymousBodyLimit, WaffoWebhook)
	api.POST("/waffo-pancake/webhook/:env", anonymousBodyLimit, WaffoPancakeWebhook)

	user := api.Group("/user")
	user.Use(middleware.DisableCache(), middleware.UserAuth())
	user.POST("/waffo/amount", RequestWaffoAmount)
	user.POST("/waffo/pay", middleware.CriticalRateLimit(), RequestWaffoPay)
	user.POST("/waffo-pancake/amount", RequestWaffoPancakeAmount)
	user.POST("/waffo-pancake/pay", middleware.CriticalRateLimit(), RequestWaffoPancakePay)

	subscription := api.Group("/subscription")
	subscription.Use(middleware.UserAuth())
	subscription.POST("/waffo-pancake/pay", middleware.CriticalRateLimit(), SubscriptionRequestWaffoPancakePay)

	option := api.Group("/option")
	option.Use(middleware.RootAuth())
	option.GET("/waffo-pancake/catalog", ListWaffoPancakeCatalog)
	option.POST("/waffo-pancake/pair", CreateWaffoPancakePair)
	option.POST("/waffo-pancake/save", SaveWaffoPancake)
	option.POST("/waffo-pancake/subscription-product", CreateWaffoPancakeSubscriptionProduct)
	option.GET("/waffo-pancake/subscription-product-options", ListWaffoPancakeSubscriptionProductOptions)
	return nil
}
