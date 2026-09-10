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

// Models is the current compatibility wiring for ChuanAPI-owned wallet schema.
// Phase 5 moves the concrete types themselves into this module; until then this
// adapter is the only module location allowed to enumerate the root-model types.
func Models() []any {
	return []any{
		&model.BalanceLedgerOperation{},
		&model.BalanceLedger{},
		&model.ModelBonusEligibleAudit{},
		&model.AffiliateReward{},
		&model.AffiliateRewardReconcileState{},
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
