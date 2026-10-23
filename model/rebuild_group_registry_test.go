package model

import (
	"github.com/QuantumNous/new-api/extension"
	"github.com/stretchr/testify/require"
	"testing"
)

// Mirror only descriptors active in this rebuild. Its two wallet tables are
// already root-model-owned; no financial data backfill is activated here.
func installRebuildGroupRegistry(t *testing.T) {
	t.Helper()
	reg := extension.NewRegistry()
	require.NoError(t, reg.RegisterModelSet(extension.ModelSet{
		Extension: "chuanapi", Name: "support_models", Phase: extension.PhaseAfterCoreModels,
		Models: []any{&SupportTicket{}, &SupportTicketMessage{}},
	}))
	require.NoError(t, extension.SetDefault(reg))
}
