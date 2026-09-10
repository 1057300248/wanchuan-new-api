package payment

import (
	"testing"

	"github.com/QuantumNous/new-api/pkg/paymentport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegisterProvidersContributesIndependentProviderSurfaces(t *testing.T) {
	registry := paymentport.NewRegistry()
	require.NoError(t, RegisterProviders(registry))
	require.NoError(t, registry.Freeze())

	snapshot, err := registry.Snapshot()
	require.NoError(t, err)
	for _, key := range []string{
		"enable_waffo_topup",
		"waffo_min_topup",
		"waffo_pay_methods",
		"enable_waffo_pancake_topup",
		"waffo_pancake_min_topup",
	} {
		assert.Contains(t, snapshot.Fields, key)
	}
}

func TestRegisterProvidersRejectsDuplicateComposition(t *testing.T) {
	registry := paymentport.NewRegistry()
	require.NoError(t, RegisterProviders(registry))
	err := RegisterProviders(registry)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate provider id")
}
