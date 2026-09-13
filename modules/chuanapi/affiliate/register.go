package affiliate

import (
	"fmt"

	"github.com/QuantumNous/new-api/extension"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/affiliateport"
)

const (
	ExtensionName = "chuanapi"
	ModelsName    = "affiliate_models"
)

func Models() []any {
	return []any{
		&model.AffiliateReward{},
		&model.AffiliateRewardReconcileState{},
	}
}

// RegisterSchema owns Affiliate's AutoMigrate contribution. The concrete root
// model types remain a temporary data-ownership boundary, but they are no longer
// grouped under the Wallet descriptor.
func RegisterSchema(reg *extension.Registry) error {
	if reg == nil {
		return fmt.Errorf("chuanapi affiliate: registry must not be nil")
	}
	return reg.RegisterModelSet(extension.ModelSet{
		Extension: ExtensionName,
		Name:      ModelsName,
		Phase:     extension.PhaseAfterCoreModels,
		Models:    Models(),
	})
}

// RegisterRuntime installs ChuanAPI's reward policy behind a generic port.
func RegisterRuntime() error {
	return affiliateport.SetDefault(NewRewardService())
}
