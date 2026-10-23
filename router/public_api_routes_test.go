package router

import (
	"github.com/QuantumNous/new-api/extension"
	consolemodule "github.com/QuantumNous/new-api/modules/chuanapi/console"
	"github.com/stretchr/testify/require"
	"testing"
)

func applyConsoleRoutes(t *testing.T, scopes extension.RouteScopes) {
	t.Helper()
	registry := extension.NewRegistry()
	require.NoError(t, consolemodule.Register(registry))
	require.NoError(t, registry.Freeze())
	require.NoError(t, registry.ApplyRoutesWithScopes(scopes, extension.PriorityBeforePlugin))
}
