package wallet

import (
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/walletport"
)

// Policy keeps ChuanAPI's bonus-balance commercial rule outside the generic
// billing engine. Root model access remains the temporary pricing/data adapter.
type Policy struct{}

var _ walletport.Policy = Policy{}

func (Policy) BonusEligible(billingModel string) bool {
	return model.IsModelBonusEligible(billingModel)
}

func (p Policy) AccountOrder(billingModel string) []string {
	if p.BonusEligible(billingModel) {
		return []string{walletport.AccountBonus, walletport.AccountPaid}
	}
	return []string{walletport.AccountPaid}
}

func RegisterRuntime() error {
	return walletport.SetDefault(Policy{})
}
