package payment

import (
	"fmt"

	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/extension"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
)

const (
	ExtensionName = "chuanapi"
	RoutesName    = "payment_routes"
)

// Register owns ChuanAPI payment-provider route contribution. Handler
// implementations remain host adapters during Phase 7, but the upstream-hot
// router/api-router.go no longer knows Waffo/Waffo-Pancake route paths.
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
	api.POST("/waffo/webhook", anonymousBodyLimit, controller.WaffoWebhook)
	api.POST("/waffo-pancake/webhook/:env", anonymousBodyLimit, controller.WaffoPancakeWebhook)

	user := api.Group("/user")
	user.Use(middleware.DisableCache(), middleware.UserAuth())
	user.POST("/waffo/amount", controller.RequestWaffoAmount)
	user.POST("/waffo/pay", middleware.CriticalRateLimit(), controller.RequestWaffoPay)
	user.POST("/waffo-pancake/amount", controller.RequestWaffoPancakeAmount)
	user.POST("/waffo-pancake/pay", middleware.CriticalRateLimit(), controller.RequestWaffoPancakePay)

	subscription := api.Group("/subscription")
	subscription.Use(middleware.UserAuth())
	subscription.POST("/waffo-pancake/pay", middleware.CriticalRateLimit(), controller.SubscriptionRequestWaffoPancakePay)

	option := api.Group("/option")
	option.Use(middleware.RootAuth())
	option.GET("/waffo-pancake/catalog", controller.ListWaffoPancakeCatalog)
	option.POST("/waffo-pancake/pair", controller.CreateWaffoPancakePair)
	option.POST("/waffo-pancake/save", controller.SaveWaffoPancake)
	option.POST("/waffo-pancake/subscription-product", controller.CreateWaffoPancakeSubscriptionProduct)
	option.GET("/waffo-pancake/subscription-product-options", controller.ListWaffoPancakeSubscriptionProductOptions)
	return nil
}
