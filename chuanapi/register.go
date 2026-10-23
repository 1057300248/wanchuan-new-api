// Package chuanapi is the explicit composition adapter for ChuanAPI modules.
// Only the application composition root imports this package.
package chuanapi

import (
	"fmt"

	"github.com/QuantumNous/new-api/extension"
	affiliatemodule "github.com/QuantumNous/new-api/modules/chuanapi/affiliate"
	consolemodule "github.com/QuantumNous/new-api/modules/chuanapi/console"
	paymentmodule "github.com/QuantumNous/new-api/modules/chuanapi/payment"
	supportmodule "github.com/QuantumNous/new-api/modules/chuanapi/support"
	walletmodule "github.com/QuantumNous/new-api/modules/chuanapi/wallet"
	walletroutes "github.com/QuantumNous/new-api/modules/chuanapi/wallet/routes"
	"github.com/QuantumNous/new-api/pkg/moneyport"
)

// RegisterRuntimeServices composes ChuanAPI-owned runtime policy services.
func RegisterRuntimeServices() error {
	return walletmodule.RegisterRuntime()
}

// RegisterFundingFactories composes the wallet implementation without coupling
// the host billing service to its concrete module.
func RegisterFundingFactories(reg *moneyport.FactoryRegistry) error {
	if reg == nil {
		return fmt.Errorf("chuanapi: funding factory registry must not be nil")
	}
	return walletmodule.RegisterFundingFactory(reg)
}

func Register(reg *extension.Registry) error {
	if reg == nil {
		return fmt.Errorf("chuanapi: registry must not be nil")
	}
	if err := walletmodule.Register(reg); err != nil {
		return err
	}
	if err := walletroutes.Register(reg); err != nil {
		return err
	}
	if err := affiliatemodule.RegisterSchema(reg); err != nil {
		return err
	}
	if err := paymentmodule.Register(reg); err != nil {
		return err
	}
	if err := consolemodule.Register(reg); err != nil {
		return err
	}
	return supportmodule.Register(reg)
}
