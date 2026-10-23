package wallet

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/extension"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

const (
	ExtensionName           = "chuanapi"
	ModelsName              = "wallet_models"
	LegacyQuotaBackfillName = "legacy_quota_backfill"
)

// Models contributes only the module-owned eligibility audit table. The host's
// required wallet-schema migration is the sole owner of both ledger tables and
// its BIGINT guard must finish before the AfterCoreData normalization runs.
func Models() []any {
	return []any{
		&model.ModelBonusEligibleAudit{},
	}
}

func Register(reg *extension.Registry) error {
	if reg == nil {
		return fmt.Errorf("chuanapi wallet: registry must not be nil")
	}
	if err := reg.RegisterModelSet(extension.ModelSet{
		Extension: ExtensionName,
		Name:      ModelsName,
		Phase:     extension.PhaseAfterCoreModels,
		Models:    Models(),
	}); err != nil {
		return err
	}
	return reg.RegisterDataStep(extension.DataStep{
		Extension: ExtensionName,
		Name:      LegacyQuotaBackfillName,
		Phase:     extension.PhaseAfterCoreData,
		Run:       runLegacyQuotaBackfill,
	})
}

func runLegacyQuotaBackfill(db *gorm.DB) error {
	affected, err := model.BackfillLegacyQuotaAccountsWithDB(db)
	if err != nil {
		return err
	}
	if affected > 0 {
		common.SysLog(fmt.Sprintf("legacy quota backfilled to paid account for %d users", affected))
	}
	return nil
}
