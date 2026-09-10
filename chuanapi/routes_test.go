package chuanapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/extension"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLlmsRouteSetPreservesLegacyRouteTableAndHandlers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reg := extension.NewRegistry()
	require.NoError(t, Register(reg))
	require.NoError(t, reg.Freeze())

	engine := gin.New()
	api := engine.Group("/api")
	scopes := extension.RouteScopes{
		Engine:                    engine,
		API:                       api,
		UserSelf:                  api.Group("/user"),
		SubscriptionUser:          api.Group("/subscription"),
		RootOptions:               api.Group("/option"),
		AdminModels:               api.Group("/models"),
		AnonymousRequestBodyLimit: func(c *gin.Context) { c.Next() },
	}
	require.NoError(t, reg.ApplyRoutesWithScopes(scopes, extension.PriorityBeforePlugin))

	handlers := make(map[string]string)
	for _, route := range engine.Routes() {
		key := route.Method + " " + route.Path
		handlers[key] = route.Handler
	}
	for _, key := range []string{
		http.MethodGet + " /llms.txt",
		http.MethodGet + " /llms-full.txt",
		http.MethodHead + " /llms.txt",
		http.MethodHead + " /llms-full.txt",
	} {
		assert.Contains(t, handlers, key)
	}
	assert.True(t, strings.HasSuffix(handlers[http.MethodGet+" /llms.txt"], "/console.GetLlmsIndex"))
	assert.True(t, strings.HasSuffix(handlers[http.MethodHead+" /llms.txt"], "/console.GetLlmsIndex"))
	assert.True(t, strings.HasSuffix(handlers[http.MethodGet+" /llms-full.txt"], "/console.GetLlmsFull"))
	assert.True(t, strings.HasSuffix(handlers[http.MethodHead+" /llms-full.txt"], "/console.GetLlmsFull"))
}
