// Package chuanapi is the explicit composition adapter for ChuanAPI modules.
// Only the application composition root imports this package.
package chuanapi

import (
	"github.com/QuantumNous/new-api/extension"
	consolemodule "github.com/QuantumNous/new-api/modules/chuanapi/console"
	supportmodule "github.com/QuantumNous/new-api/modules/chuanapi/support"
)

func Register(reg *extension.Registry) error {
	if err := consolemodule.Register(reg); err != nil {
		return err
	}
	return supportmodule.Register(reg)
}
