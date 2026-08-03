package middleware

import (
	"os"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

// defaultConsoleCORSAllowOrigins 是可信来源的默认白名单，
// 仅覆盖本地开发；生产环境必须通过 CONSOLE_CORS_ALLOW_ORIGINS 显式配置。
var defaultConsoleCORSAllowOrigins = []string{"http://localhost:3000", "http://127.0.0.1:3000"}

// CORS 是全局统一的 CORS 策略，必须在 SetRouter 中通过 router.Use 挂载一次，
// 覆盖全部路由以及无 OPTIONS 路由的预检路径：
//   - CONSOLE_CORS_ALLOW_ORIGINS（逗号分隔，默认本地开发地址）中的可信来源：
//     回显精确的 Access-Control-Allow-Origin 并开启 Access-Control-Allow-Credentials，
//     供独立官网（如 https://chuanapi.com/playground）以 credentials=include 访问；
//   - 其他来源保持 New API 公共 relay 的无凭据跨域能力
//     （Access-Control-Allow-Origin: *），绝不与 credentials 同时放行；
//   - 同源请求（无 Origin 头或 Origin 与 Host 一致）直接放行，不写任何 CORS 头。
func CORS() gin.HandlerFunc {
	trustedOrigins := consoleCORSAllowOrigins()
	trustedCors := cors.New(cors.Config{
		AllowOrigins: trustedOrigins,
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
	})
	publicCors := cors.New(cors.Config{
		AllowAllOrigins: true,
		AllowMethods:    []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:    []string{"*"},
		MaxAge:          12 * time.Hour,
	})
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" {
			for _, trusted := range trustedOrigins {
				if origin == trusted {
					trustedCors(c)
					return
				}
			}
		}
		publicCors(c)
	}
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
