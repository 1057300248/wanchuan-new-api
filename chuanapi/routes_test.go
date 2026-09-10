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
	require.NoError(t, reg.ApplyRoutes(engine, extension.PriorityBeforePlugin))

	var got []string
	handlers := make(map[string]string)
	for _, route := range engine.Routes() {
		key := route.Method + " " + route.Path
		got = append(got, key)
		handlers[key] = route.Handler
	}
	assert.Equal(t, []string{
		http.MethodGet + " /llms.txt",
		http.MethodGet + " /llms-full.txt",
		http.MethodHead + " /llms.txt",
		http.MethodHead + " /llms-full.txt",
	}, got)
	assert.True(t, strings.HasSuffix(handlers[http.MethodGet+" /llms.txt"], "/console.GetLlmsIndex"))
	assert.True(t, strings.HasSuffix(handlers[http.MethodHead+" /llms.txt"], "/console.GetLlmsIndex"))
	assert.True(t, strings.HasSuffix(handlers[http.MethodGet+" /llms-full.txt"], "/console.GetLlmsFull"))
	assert.True(t, strings.HasSuffix(handlers[http.MethodHead+" /llms-full.txt"], "/console.GetLlmsFull"))
}
