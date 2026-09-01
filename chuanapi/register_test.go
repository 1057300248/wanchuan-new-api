package chuanapi

import (
	"testing"

	"github.com/QuantumNous/new-api/extension"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// expectedWalletModelOrder is the migration order the host relied on before the
// registry took over: the ledger operation header precedes the ledger rows that
// reference it, and the affiliate reconcile state follows the reward rows it
// reconciles. Asserting the exact type sequence is what makes an accidental
// reorder or a dropped model fail here instead of at a customer's AutoMigrate.
var expectedWalletModelOrder = []any{
	&model.BalanceLedgerOperation{},
	&model.BalanceLedger{},
	&model.ModelBonusEligibleAudit{},
	&model.AffiliateReward{},
	&model.AffiliateRewardReconcileState{},
}

func TestRegisterContributesWalletModelsInMigrationOrder(t *testing.T) {
	reg := extension.NewRegistry()
	require.NoError(t, Register(reg))

	models := reg.ModelsFor(extension.PhaseAfterCoreModels)
	require.Len(t, models, len(expectedWalletModelOrder))
	for i, expected := range expectedWalletModelOrder {
		assert.IsType(t, expected, models[i], "wallet model at index %d changed", i)
	}

	sets := reg.ModelSets()
	require.Len(t, sets, 1)
	assert.Equal(t, ExtensionName, sets[0].Extension)
	assert.Equal(t, WalletModelsName, sets[0].Name)
	assert.Equal(t, extension.PhaseAfterCoreModels, sets[0].Phase)
}

// The backfill mutates balances, so it must run only after the wallet schema
// and host core-data initialization are complete.
func TestRegisterContributesBackfillStepAfterCoreData(t *testing.T) {
	reg := extension.NewRegistry()
	require.NoError(t, Register(reg))

	steps := reg.StepsFor(extension.PhaseAfterCoreData)
	require.Len(t, steps, 1)
	assert.Equal(t, ExtensionName, steps[0].Extension)
	assert.Equal(t, LegacyQuotaBackfillName, steps[0].Name)
	require.NotNil(t, steps[0].Run)

	assert.Empty(t, reg.ModelsFor(extension.PhaseAfterCoreData))
	assert.Empty(t, reg.StepsFor(extension.PhaseAfterCoreModels))
}

func TestRegisterContributesLlmsRoutesBeforePluginSnapshot(t *testing.T) {
	reg := extension.NewRegistry()
	require.NoError(t, Register(reg))

	sets := reg.RouteSetsFor(extension.PriorityBeforePlugin)
	require.Len(t, sets, 1)
	assert.Equal(t, ExtensionName, sets[0].Extension)
	assert.Equal(t, LlmsRoutesName, sets[0].Name)
	require.NotNil(t, sets[0].Register)
	assert.Empty(t, reg.RouteSetsFor(extension.PriorityAfterPlugin))
}

// WalletModels is handed straight to the registry, so a shared backing array
// would let one caller reorder what a later caller registers.
func TestWalletModelsReturnsAFreshSlicePerCall(t *testing.T) {
	first := WalletModels()
	second := WalletModels()
	require.Len(t, first, len(expectedWalletModelOrder))
	require.Len(t, second, len(expectedWalletModelOrder))

	first[0] = nil
	assert.IsType(t, expectedWalletModelOrder[0], second[0],
		"mutating a returned slice must not affect a later call")
}

func TestRegisterRejectsNilRegistry(t *testing.T) {
	assert.Error(t, Register(nil))
}

// A frozen registry means the composition root wired registration too late.
// That is a build-time bug in the host binary, so it must fail loudly rather
// than return an error a caller could ignore and start with no wallet schema.
func TestRegisterOnFrozenRegistryPanics(t *testing.T) {
	reg := extension.NewRegistry()
	require.NoError(t, reg.Freeze())
	assert.Panics(t, func() { _ = Register(reg) })
}

// Registering twice into one registry is a duplicate identity, not a silent
// second copy of the schema in the AutoMigrate list.
func TestRegisterTwiceIsRejectedWithoutDuplicatingModels(t *testing.T) {
	reg := extension.NewRegistry()
	require.NoError(t, Register(reg))
	require.Error(t, Register(reg))

	assert.Len(t, reg.ModelsFor(extension.PhaseAfterCoreModels), len(expectedWalletModelOrder))
	assert.Len(t, reg.StepsFor(extension.PhaseAfterCoreData), 1)
	assert.Len(t, reg.RouteSetsFor(extension.PriorityBeforePlugin), 1)
}
