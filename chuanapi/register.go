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
	consolemodule "github.com/QuantumNous/new-api/modules/chuanapi/console"
	walletmodule "github.com/QuantumNous/new-api/modules/chuanapi/wallet"
)

const ExtensionName = "chuanapi"

// Descriptor aliases preserve the historical operator-visible identities while
// ownership of each contribution lives in its module.
const (
	WalletModelsName        = walletmodule.ModelsName
	LegacyQuotaBackfillName = walletmodule.LegacyQuotaBackfillName
	LlmsRoutesName          = consolemodule.RoutesName
)

// WalletModels remains as a compatibility accessor for migration tests and any
// out-of-tree build-time tooling. Concrete model enumeration is module-owned.
func WalletModels() []any { return walletmodule.Models() }

// Register is the ChuanAPI composition root. It owns no handlers, migrations,
// or GORM model lists; individual modules contribute those capabilities.
func Register(reg *extension.Registry) error {
	if reg == nil {
		return fmt.Errorf("chuanapi: registry must not be nil")
	}
	if err := walletmodule.Register(reg); err != nil {
		return err
	}
	return consolemodule.Register(reg)
}
