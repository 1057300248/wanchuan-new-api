package routes

import (
	"fmt"

	"github.com/QuantumNous/new-api/extension"
	"github.com/QuantumNous/new-api/middleware"
	walletmodule "github.com/QuantumNous/new-api/modules/chuanapi/wallet"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
)

const (
	ExtensionName = "chuanapi"
	RoutesName    = "wallet_bonus_policy_routes"
)

// Register owns HTTP wiring for the Wallet admin surface. Keeping middleware
// dependencies in this adapter package leaves the wallet domain/runtime package
// independent of middleware -> service, so core service tests can import the
// wallet implementation without creating a package cycle.
func Register(reg *extension.Registry) error {
	if reg == nil {
		return fmt.Errorf("chuanapi wallet routes: registry must not be nil")
	}
	return reg.RegisterRouteSet(extension.RouteSet{
		Extension: ExtensionName,
		Name:      RoutesName,
		Priority:  extension.PriorityBeforePlugin,
		Register:  registerRoutes,
	})
}

func registerRoutes(engine *gin.Engine) error {
	if engine == nil {
		return fmt.Errorf("chuanapi wallet routes: engine must not be nil")
	}
	api := engine.Group("/api")
	api.Use(middleware.RouteTag("api"))
	api.Use(gzip.Gzip(gzip.DefaultCompression))
	api.Use(middleware.AccessTokenAudit())
	api.Use(middleware.BodyStorageCleanup())
	api.Use(middleware.GlobalAPIRateLimit())
	models := api.Group("/models")
	models.Use(middleware.AdminAuth())
	models.GET("/bonus_eligible/audits", walletmodule.GetModelBonusEligibleAudits)
	models.POST("/bonus_eligible/batch", walletmodule.BatchUpdateModelBonusEligible)
	return nil
}
