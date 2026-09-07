package router

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/extension"
	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-gonic/gin"
)

func SetRouter(router *gin.Engine, assets WebAssets) error {
	return setRouterWithRegistry(router, assets, extension.Default())
}

// setRouterWithRegistry keeps the production composition path injectable for
// contract tests without mutating the process-wide write-once registry.
func setRouterWithRegistry(router *gin.Engine, assets WebAssets, registry *extension.Registry) error {
	router.Use(middleware.CORS())
	SetApiRouter(router)
	SetDashboardRouter(router)
	SetRelayRouter(router)
	SetTaskPluginProtocolRouter(router)
	SetVideoRouter(router)
	SetTaskRouter(router)
	pluginDispatcher, err := setPluginAndExtensionRoutes(router, registry)
	if err != nil {
		return err
	}
	frontendBaseUrl := os.Getenv("FRONTEND_BASE_URL")
	if common.IsMasterNode && frontendBaseUrl != "" {
		frontendBaseUrl = ""
		common.SysLog("FRONTEND_BASE_URL is ignored on master node")
	}
	if frontendBaseUrl == "" {
		SetWebRouter(router, assets, pluginDispatcher)
	} else {
		frontendBaseUrl = strings.TrimSuffix(frontendBaseUrl, "/")
		router.NoRoute(
			pluginDispatcher,
			middleware.RouteTag("web"),
			middleware.AccessTokenAudit(),
			func(c *gin.Context) {
				c.Redirect(http.StatusMovedPermanently, fmt.Sprintf("%s%s", frontendBaseUrl, c.Request.RequestURI))
			},
		)
	}
	return nil
}

// setPluginAndExtensionRoutes owns the two registry expansion points around
// SetPluginRouter's static-route snapshot. BeforePlugin routes are visible to
// plugin conflict detection; AfterPlugin routes preserve the historical
// /llms.txt behavior and remain outside that snapshot.
func setPluginAndExtensionRoutes(router *gin.Engine, registry *extension.Registry) (gin.HandlerFunc, error) {
	if registry == nil {
		return nil, fmt.Errorf("router: extension registry must not be nil")
	}
	if err := registry.ApplyRoutes(router, extension.PriorityBeforePlugin); err != nil {
		return nil, err
	}
	pluginDispatcher := SetPluginRouter(router)
	if err := registry.ApplyRoutes(router, extension.PriorityAfterPlugin); err != nil {
		return nil, err
	}
	return pluginDispatcher, nil
}
