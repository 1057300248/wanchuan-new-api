package wallet

import (
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"testing"
)

// Preserve the released093 allocation, including its known remainder limit.
// This is a compatibility regression, not a claim of per-account conservation.
func TestOfficialCompatibilityKeeps093RefundRemainder(t *testing.T) {
	shares := []model.LedgerShare{{Account: model.AccountBonus, Amount: 1}, {Account: model.AccountReward, Amount: 1}, {Account: model.AccountPaid, Amount: 1}}
	require.Equal(t, []model.LedgerShare{{Account: model.AccountBonus, Amount: 2}}, splitRefund(shares, 2))
}
