package service

import (
	"errors"
	"fmt"
	"sync"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/moneyport"
)

var (
	fundingRegistryMu sync.RWMutex
	fundingRegistry   *moneyport.FactoryRegistry
)

// RegisterCoreFundingFactories installs the compatibility implementations used
// before module extraction. Phase 5 replaces the wallet registration with the
// ChuanAPI wallet module; subscription remains host-owned.
func RegisterCoreFundingFactories(reg *moneyport.FactoryRegistry) error {
	if reg == nil {
		return errors.New("service: nil funding factory registry")
	}
	if err := reg.Register(moneyport.SourceWallet, moneyport.FundingFactoryFunc(func(request moneyport.FactoryRequest) (moneyport.FundingSource, error) {
		order := make([]model.LedgerAccount, 0, len(request.AccountOrder))
		for _, account := range request.AccountOrder {
			switch model.LedgerAccount(account) {
			case model.AccountBonus, model.AccountPaid, model.AccountAffiliate:
				order = append(order, model.LedgerAccount(account))
			default:
				return nil, fmt.Errorf("service: unsupported wallet account %q", account)
			}
		}
		return &WalletFunding{userId: request.UserID, requestId: request.RequestID, debitOrder: order}, nil
	})); err != nil {
		return err
	}
	return reg.Register(moneyport.SourceSubscription, moneyport.FundingFactoryFunc(func(request moneyport.FactoryRequest) (moneyport.FundingSource, error) {
		return &SubscriptionFunding{
			requestId: request.RequestID,
			userId:    request.UserID,
			modelName: request.BillingModel,
			amount:    int64(request.Amount),
		}, nil
	}))
}

// ConfigureFundingFactoryRegistry freezes and installs the process registry.
// It is write-once in production; tests configure it once from TestMain.
func ConfigureFundingFactoryRegistry(reg *moneyport.FactoryRegistry) error {
	if reg == nil {
		return errors.New("service: nil funding factory registry")
	}
	if !reg.Frozen() {
		if err := reg.Freeze(); err != nil {
			return err
		}
	}
	fundingRegistryMu.Lock()
	defer fundingRegistryMu.Unlock()
	if fundingRegistry != nil {
		return errors.New("service: funding factory registry already configured")
	}
	fundingRegistry = reg
	return nil
}

func createFundingSource(source string, request moneyport.FactoryRequest) (moneyport.FundingSource, error) {
	fundingRegistryMu.RLock()
	reg := fundingRegistry
	fundingRegistryMu.RUnlock()
	if reg == nil {
		return nil, errors.New("service: funding factory registry is not configured")
	}
	return reg.Create(source, request)
}
