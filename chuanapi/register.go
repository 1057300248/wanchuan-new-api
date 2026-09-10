// Package chuanapi is the composition adapter that wires ChuanAPI's schema and
// data migrations into the host through the extension registry.
//
// It exists purely to hold the one dependency edge the generic registry must not
// have: extension/ stays a leaf that knows nothing about model/, service/, or
// router/, so this package imports both sides and joins them. Nothing else may
// import chuanapi except the composition root (main.go) — an import from model/
// or service/ would recreate the cycle this package was created to avoid.
//
// Registration is explicit and build-time only. There is no init() self-wiring
// and no environment variable that skips or reorders a descriptor: the host
// binary either contains this call or it does not.
package chuanapi

import (
	"fmt"

	"github.com/QuantumNous/new-api/extension"
	affiliatemodule "github.com/QuantumNous/new-api/modules/chuanapi/affiliate"
	consolemodule "github.com/QuantumNous/new-api/modules/chuanapi/console"
	paymentmodule "github.com/QuantumNous/new-api/modules/chuanapi/payment"
	walletmodule "github.com/QuantumNous/new-api/modules/chuanapi/wallet"
	walletroutes "github.com/QuantumNous/new-api/modules/chuanapi/wallet/routes"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	"github.com/QuantumNous/new-api/pkg/paymentport"
)

const ExtensionName = "chuanapi"

// Descriptor aliases preserve the historical operator-visible identities while
// ownership of each contribution lives in its module.
const (
	WalletModelsName        = walletmodule.ModelsName
	WalletPolicyRoutesName  = walletroutes.RoutesName
	AffiliateModelsName     = affiliatemodule.ModelsName
	LegacyQuotaBackfillName = walletmodule.LegacyQuotaBackfillName
	LlmsRoutesName          = consolemodule.RoutesName
	PaymentRoutesName       = paymentmodule.RoutesName
)

// WalletModels remains as a compatibility accessor for migration tests and any
// out-of-tree build-time tooling. Concrete model enumeration is module-owned.
func WalletModels() []any { return walletmodule.Models() }

// RegisterRuntimeServices composes ChuanAPI-owned runtime policy services.
func RegisterRuntimeServices() error {
	if err := walletmodule.RegisterRuntime(); err != nil {
		return err
	}
	if err := affiliatemodule.RegisterRuntime(); err != nil {
		return err
	}
	return nil
}

// RegisterPaymentProviders composes ChuanAPI-owned provider catalogues into the
// generic payment registry. Concrete providers register independently.
func RegisterPaymentProviders(registry *paymentport.Registry) error {
	if registry == nil {
		return fmt.Errorf("chuanapi: payment provider registry must not be nil")
	}
	return paymentmodule.RegisterProviders(registry)
}

// RegisterFundingFactories composes ChuanAPI runtime funding implementations.
func RegisterFundingFactories(reg *moneyport.FactoryRegistry) error {
	if reg == nil {
		return fmt.Errorf("chuanapi: funding factory registry must not be nil")
	}
	return walletmodule.RegisterFundingFactory(reg)
}

// Register is the ChuanAPI composition root. It owns no handlers, migrations,
// or GORM model lists; individual modules contribute those capabilities.
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
	return consolemodule.Register(reg)
}
