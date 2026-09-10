package paymentport

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testCatalogue struct {
	descriptor   Descriptor
	contribution Contribution
}

func (c testCatalogue) Descriptor() Descriptor     { return c.descriptor.Clone() }
func (c testCatalogue) Contribution() Contribution { return c.contribution.Clone() }

func TestRegistryMergesProvidersInRegistrationOrder(t *testing.T) {
	registry := NewRegistry()
	require.NoError(t, registry.Register("provider-b", testCatalogue{
		descriptor: Descriptor{MethodTypes: []string{"beta"}, FieldKeys: []string{"beta_enabled"}},
		contribution: Contribution{
			Methods: []map[string]string{{"type": "beta", "name": "Beta"}},
			Fields:  map[string]any{"beta_enabled": true},
		},
	}))
	require.NoError(t, registry.Register("provider-a", testCatalogue{
		descriptor: Descriptor{MethodTypes: []string{"alpha"}, FieldKeys: []string{"alpha_enabled"}},
		contribution: Contribution{
			Methods: []map[string]string{{"type": "alpha", "name": "Alpha"}},
			Fields:  map[string]any{"alpha_enabled": true},
		},
	}))
	require.NoError(t, registry.Freeze())

	snapshot, err := registry.Snapshot()
	require.NoError(t, err)
	require.Len(t, snapshot.Methods, 2)
	assert.Equal(t, "beta", snapshot.Methods[0]["type"])
	assert.Equal(t, "alpha", snapshot.Methods[1]["type"])
	assert.Equal(t, true, snapshot.Fields["beta_enabled"])
	assert.Equal(t, true, snapshot.Fields["alpha_enabled"])
}

func TestRegistryRejectsProviderAndSurfaceCollisions(t *testing.T) {
	base := testCatalogue{descriptor: Descriptor{MethodTypes: []string{"shared"}, FieldKeys: []string{"shared_field"}}}

	t.Run("provider id", func(t *testing.T) {
		registry := NewRegistry()
		require.NoError(t, registry.Register("one", base))
		err := registry.Register("ONE", testCatalogue{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate provider id")
	})

	t.Run("method type", func(t *testing.T) {
		registry := NewRegistry()
		require.NoError(t, registry.Register("one", base))
		err := registry.Register("two", testCatalogue{descriptor: Descriptor{MethodTypes: []string{"SHARED"}}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `method type "shared" owned by both`)
	})

	t.Run("field key", func(t *testing.T) {
		registry := NewRegistry()
		require.NoError(t, registry.Register("one", base))
		err := registry.Register("two", testCatalogue{descriptor: Descriptor{FieldKeys: []string{"shared_field"}}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `field key "shared_field" owned by both`)
	})
}

func TestRegistryFailsClosedOnUndeclaredRuntimeSurface(t *testing.T) {
	registry := NewRegistry()
	require.NoError(t, registry.Register("provider", testCatalogue{
		descriptor: Descriptor{MethodTypes: []string{"declared"}, FieldKeys: []string{"declared_field"}},
		contribution: Contribution{
			Methods: []map[string]string{{"type": "other"}},
			Fields:  map[string]any{"unexpected": true},
		},
	}))
	require.NoError(t, registry.Freeze())
	_, err := registry.Snapshot()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "undeclared method type")
}

func TestRegistryRejectsRegistrationAfterFreeze(t *testing.T) {
	registry := NewRegistry()
	require.NoError(t, registry.Freeze())
	err := registry.Register("late", testCatalogue{})
	assert.ErrorIs(t, err, ErrRegistryFrozen)
}
