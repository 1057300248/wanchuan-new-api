package console

import (
	"fmt"

	"github.com/QuantumNous/new-api/extension"
	"github.com/gin-gonic/gin"
)

const (
	ExtensionName = "chuanapi"
	RoutesName    = "llms_routes"
)

// Register contributes ChuanAPI-owned public console routes. The module owns
// both handler implementation and route wiring, so the composition root never
// imports the host controller package.
func Register(reg *extension.Registry) error {
	if reg == nil {
		return fmt.Errorf("chuanapi console: registry must not be nil")
	}
	return reg.RegisterRouteSet(extension.RouteSet{
		Extension: ExtensionName,
		Name:      RoutesName,
		Priority:  extension.PriorityBeforePlugin,
		Register:  registerRoutes,
	})
}

func registerRoutes(engine *gin.Engine) error {
	engine.GET("/llms.txt", GetLlmsIndex)
	engine.HEAD("/llms.txt", GetLlmsIndex)
	engine.GET("/llms-full.txt", GetLlmsFull)
	engine.HEAD("/llms-full.txt", GetLlmsFull)
	return nil
}
