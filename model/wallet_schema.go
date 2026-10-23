package model

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// The required wallet data contract remains in the primary model boundary so
// ledger evidence, User and original Token can later share one transaction.
// These table definitions retain the released 093 schema. This stage does not
// backfill balances or activate the wallet funding runtime.
type BalanceLedgerOperation struct {
	Id                 int    `json:"id" gorm:"primaryKey;autoIncrement"`
	IdempotencyKey     string `json:"idempotency_key" gorm:"type:varchar(255);not null;uniqueIndex"`
	UserId             int    `json:"user_id" gorm:"not null;index"`
	Operation          string `json:"operation" gorm:"type:varchar(16);not null"`
	OperationSignature string `json:"operation_signature" gorm:"type:char(64);not null"`
	TotalBefore        int    `json:"total_before" gorm:"type:bigint;not null"`
	TotalAfter         int    `json:"total_after" gorm:"type:bigint;not null"`
	CreatedAt          int64  `json:"created_at" gorm:"bigint;autoCreateTime;index"`
}

func (BalanceLedgerOperation) TableName() string {
	return "balance_ledger_operations"
}

type BalanceLedger struct {
	Id             int    `json:"id" gorm:"primaryKey;autoIncrement"`
	OperationId    int    `json:"operation_id" gorm:"not null;index"`
	UserId         int    `json:"user_id" gorm:"not null;index:idx_ledger_user_time,priority:1"`
	Account        string `json:"account" gorm:"type:varchar(16);not null"`
	Delta          int    `json:"delta" gorm:"type:bigint;not null"`
	BalanceBefore  int    `json:"balance_before" gorm:"type:bigint;not null"`
	BalanceAfter   int    `json:"balance_after" gorm:"type:bigint;not null"`
	TotalBefore    int    `json:"total_before" gorm:"type:bigint;not null"`
	TotalAfter     int    `json:"total_after" gorm:"type:bigint;not null"`
	Reason         string `json:"reason" gorm:"type:varchar(64);not null"`
	ReferenceType  string `json:"reference_type" gorm:"type:varchar(64);index:idx_ledger_ref,priority:1"`
	ReferenceId    string `json:"reference_id" gorm:"type:varchar(128);index:idx_ledger_ref,priority:2"`
	RequestId      string `json:"request_id" gorm:"type:varchar(64);index"`
	OperatorId     int    `json:"operator_id" gorm:"default:0"`
	Metadata       string `json:"metadata" gorm:"type:text"`
	IdempotencyKey string `json:"idempotency_key" gorm:"type:varchar(255);not null;index"`
	CreatedAt      int64  `json:"created_at" gorm:"bigint;autoCreateTime;index:idx_ledger_user_time,priority:2"`
}

func (BalanceLedger) TableName() string {
	return "balance_ledger"
}

// migrateRequiredWalletSchema is reached by production InitDB/migrateDB, not by
// optional module registration. It is never called by the separate log DB path.
// Data normalization and runtime funding are separate, gated stages.
func migrateRequiredWalletSchema(db *gorm.DB) error {
	if db == nil {
		return errors.New("wallet schema: primary database must not be nil")
	}
	return db.AutoMigrate(&BalanceLedgerOperation{}, &BalanceLedger{})
}

// ensureRequiredWalletColumns preserves the existing custom fail-closed rule:
// SKIP_64BIT_QUOTA_SCHEMA_CHECK applies only to the official quota columns, not
// the three authoritative wallet balances. Missing columns are allowed here;
// User's explicit BIGINT tags must create them during the following migration.
func ensureRequiredWalletColumns(db *gorm.DB, dbType common.DatabaseType) error {
	if db == nil || dbType == common.DatabaseTypeSQLite {
		return nil
	}
	if !db.Migrator().HasTable(&User{}) {
		return nil
	}
	columns, err := db.Migrator().ColumnTypes(&User{})
	if err != nil {
		return fmt.Errorf("wallet schema: failed to inspect users schema: %w", err)
	}
	for _, expected := range []string{"quota_paid", "quota_bonus", "quota_reward"} {
		for _, column := range columns {
			if strings.EqualFold(column.Name(), expected) && !is64BitIntegerType(dbType, column.DatabaseTypeName()) {
				return fmt.Errorf("users.%s uses %s; 32-bit is not supported; this is a ChuanAPI wallet balance column and SKIP_64BIT_QUOTA_SCHEMA_CHECK does not apply to it", expected, column.DatabaseTypeName())
			}
		}
	}
	return nil
}
