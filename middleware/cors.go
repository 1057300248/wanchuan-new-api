package middleware

import (
	"os"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func CORS() gin.HandlerFunc {
	config := cors.DefaultConfig()
	config.AllowAllOrigins = true
	config.AllowCredentials = true
	config.AllowMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}
	config.AllowHeaders = []string{"*"}
	return cors.New(config)
}

// defaultConsoleCORSAllowOrigins 是 ConsoleCORS 的默认允许来源，
// 仅覆盖本地开发；生产环境必须通过 CONSOLE_CORS_ALLOW_ORIGINS 显式配置。
var defaultConsoleCORSAllowOrigins = []string{"http://localhost:3000", "http://127.0.0.1:3000"}

// ConsoleCORS 为跨子域独立官网（如 https://chuanapi.com/playground）提供的
// Console/Browser CORS 中间件，仅用于 /api 路由。允许来源从环境变量
// CONSOLE_CORS_ALLOW_ORIGINS（逗号分隔）读取，默认只放行本地开发地址；
// 开启凭据（credentials=true）时绝不放行任意 Origin。
func ConsoleCORS() gin.HandlerFunc {
	config := cors.Config{
		AllowOrigins: consoleCORSAllowOrigins(),
		AllowMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders: []string{
			"Origin",
			"Accept",
			"Authorization",
			"Content-Type",
			"Cache-Control",
			"New-Api-User",
		},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}
	return cors.New(config)
}

func consoleCORSAllowOrigins() []string {
	raw := os.Getenv("CONSOLE_CORS_ALLOW_ORIGINS")
	origins := make([]string, 0)
	for _, origin := range strings.Split(raw, ",") {
		origin = strings.TrimSpace(origin)
		if origin != "" {
			origins = append(origins, origin)
		}
	}
	if len(origins) == 0 {
		return defaultConsoleCORSAllowOrigins
	}
	return origins
}

func Version() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-New-Api-Version", common.Version)
		c.Next()
	}
}
