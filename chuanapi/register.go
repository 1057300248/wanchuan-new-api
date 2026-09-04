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

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/extension"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ExtensionName identifies every descriptor this package contributes. It appears
// in registry validation errors and in migration failure messages, so operators
// can trace a failing step back to ChuanAPI rather than to the host.
const ExtensionName = "chuanapi"

// Descriptor names. They are part of the registry's stable (Extension, Name)
// identity and must not be renamed: a rename changes the identity an operator
// reads in logs and errors.
const (
	// WalletModelsName is the model set carrying the wallet ledger, bonus audit,
	// and affiliate reward schema.
	WalletModelsName = "wallet_models"
	// LegacyQuotaBackfillName is the data step that backfills legacy
	// users.quota balances into the paid account.
	LegacyQuotaBackfillName = "legacy_quota_backfill"
	// LlmsRoutesName is the public llms.txt route set. Its identity is stable
	// because registry failures and route diagnostics expose it to operators.
	LlmsRoutesName = "llms_routes"
)

// WalletModels returns the ChuanAPI GORM models in the exact order the host
// previously listed them at the tail of its own AutoMigrate call.
//
// Order is load-bearing and must not be sorted or regrouped: BalanceLedger
// references the operation header created by BalanceLedgerOperation, and
// AffiliateRewardReconcileState follows the reward rows it reconciles. Callers
// get a fresh slice, so a caller cannot mutate the canonical order.
func WalletModels() []any {
	return []any{
		&model.BalanceLedgerOperation{},
		&model.BalanceLedger{},
		&model.ModelBonusEligibleAudit{},
		&model.AffiliateReward{},
		&model.AffiliateRewardReconcileState{},
	}
}

// Register contributes ChuanAPI's schema and data migrations to reg. It is
// called once by the composition root before model.InitDB, while the registry is
// still mutable, and returns an error rather than panicking so the caller can
// abort startup with a diagnosable message.
//
// The schema lands in PhaseAfterCoreModels so it can be appended to the host's
// core AutoMigrate argument list. The backfill lands in PhaseAfterCoreData and
// runs at the fixed point in migrateDB where the legacy quota backfill has
// always run: after core identity data initialization and before the
// SubscriptionPlan dialect branch.
func Register(reg *extension.Registry) error {
	if reg == nil {
		return fmt.Errorf("chuanapi: registry must not be nil")
	}
	if err := reg.RegisterModelSet(extension.ModelSet{
		Extension: ExtensionName,
		Name:      WalletModelsName,
		Phase:     extension.PhaseAfterCoreModels,
		Models:    WalletModels(),
	}); err != nil {
		return err
	}
	if err := reg.RegisterDataStep(extension.DataStep{
		Extension: ExtensionName,
		Name:      LegacyQuotaBackfillName,
		Phase:     extension.PhaseAfterCoreData,
		Run:       runLegacyQuotaBackfill,
	}); err != nil {
		return err
	}
	return reg.RegisterRouteSet(extension.RouteSet{
		Extension: ExtensionName,
		Name:      LlmsRoutesName,
		Priority:  extension.PriorityBeforePlugin,
		Register:  registerLlmsRoutes,
	})
}

// registerLlmsRoutes preserves the exact route order and handlers previously
// hard-coded by router.SetLlmsRouter. PriorityBeforePlugin makes these public
// host routes visible to the plugin registry's static-route snapshot, so an
// intersecting plugin is rejected explicitly instead of being admitted and
// silently shadowed at request time.
func registerLlmsRoutes(engine *gin.Engine) error {
	engine.GET("/llms.txt", controller.GetLlmsIndex)
	engine.HEAD("/llms.txt", controller.GetLlmsIndex)
	engine.GET("/llms-full.txt", controller.GetLlmsFull)
	engine.HEAD("/llms-full.txt", controller.GetLlmsFull)
	return nil
}

// runLegacyQuotaBackfill is the DataStep body. It uses the database handle the
// registry passes in — the same handle the host is migrating — instead of the
// package-level model.DB, so the step migrates the database it was invoked for.
//
// The step is idempotent: a user whose balance was already split no longer
// matches the candidate query, and the ledger operation header carries a
// globally unique idempotency key.
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
