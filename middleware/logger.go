package middleware

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/accesslog"
	"github.com/gin-gonic/gin"
)

const RouteTagKey = "route_tag"

const accessLogPathSnapshotKey = "newapi_access_log_path_snapshot"

func RouteTag(tag string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(RouteTagKey, tag)
		c.Next()
	}
}

func SetUpLogger(server *gin.Engine) {
	server.Use(redactTaskArtifactAccessQuery())
	server.Use(func(c *gin.Context) {
		c.Set(accessLogPathSnapshotKey, accesslog.Path(c.Request.URL))
		c.Next()
	})
	server.Use(gin.LoggerWithFormatter(func(param gin.LogFormatterParams) string {
		var requestID string
		if param.Keys != nil {
			requestID, _ = param.Keys[common.RequestIdKey].(string)
		}
		tag, _ := param.Keys[RouteTagKey].(string)
		if tag == "" {
			tag = "web"
		}
		path, ok := param.Keys[accessLogPathSnapshotKey].(string)
		if !ok {
			// Never fall back to Gin's combined decoded path/query string.
			path = "[unavailable]"
		}
		return fmt.Sprintf("[GIN] %s | %s | %s | %3d | %13v | %15s | %7s %s\n",
			param.TimeStamp.Format("2006/01/02 - 15:04:05"),
			tag,
			requestID,
			param.StatusCode,
			param.Latency,
			param.ClientIP,
			param.Method,
			path,
		)
	}))
}
