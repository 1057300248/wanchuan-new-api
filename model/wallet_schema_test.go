package model

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// This test always enters the production InitDB path. The external native gate
// selects a fresh isolated database or a separately proven 093-created fixture;
// no test AutoMigrate is allowed to supply the required wallet tables.
func TestWalletSchemaInitDBPreservesDataAndRestarts(t *testing.T) {
	dialect := os.Getenv("TEST_WALLET_DIALECT")
	if dialect == "" {
		dialect = "sqlite"
	}
	mode := os.Getenv("TEST_WALLET_SCHEMA_MODE")
	require.Contains(t, []string{"", "fresh", "upgrade093"}, mode)
	upgrade := mode == "upgrade093"
	oldDB, oldLogDB := DB, LOG_DB
	oldMainType, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
	oldSQLitePath, oldMaster := common.SQLitePath, common.IsMasterNode
	t.Cleanup(func() {
		DB, LOG_DB = oldDB, oldLogDB
		common.SetDatabaseTypes(oldMainType, oldLogType)
		common.SQLitePath, common.IsMasterNode = oldSQLitePath, oldMaster
		initCol()
	})
	common.IsMasterNode = true
	t.Setenv("SKIP_64BIT_QUOTA_SCHEMA_CHECK", "false")
	var expectedType common.DatabaseType
	var logSQLitePath string
	switch dialect {
	case "sqlite":
		expectedType = common.DatabaseTypeSQLite
		if upgrade {
			common.SQLitePath = os.Getenv("TEST_WALLET_SQLITE_PATH")
			require.True(t, filepath.IsAbs(common.SQLitePath))
			_, err := os.Stat(common.SQLitePath)
			require.NoError(t, err, "upgrade requires the externally prepared 093 database")
		} else {
			common.SQLitePath = filepath.Join(t.TempDir(), "primary.db")
		}
		logSQLitePath = filepath.Join(t.TempDir(), "logs.db")
		t.Setenv("SQL_DSN", "local")
		t.Setenv("LOG_SQL_DSN", "local")
	case "mysql", "postgres":
		expectedType = common.DatabaseType(dialect)
		dsn, logDSN := os.Getenv("TEST_WALLET_DSN"), os.Getenv("TEST_WALLET_LOG_DSN")
		require.NotEmpty(t, dsn)
		require.NotEmpty(t, logDSN)
		require.NotEqual(t, dsn, logDSN, "use separate, owned primary and log fixture databases")
		t.Setenv("SQL_DSN", dsn)
		t.Setenv("LOG_SQL_DSN", logDSN)
	default:
		t.Fatalf("unsupported wallet fixture dialect %q", dialect)
	}
	probe, actualType, err := chooseDB("SQL_DSN", false)
	require.NoError(t, err)
	require.Equal(t, expectedType, actualType)
	probeSQL, err := probe.DB()
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, probeSQL.Close()) })
	var original map[string][]map[string]any
	if upgrade {
		// This assertion is not source provenance by itself: the external gate
		// must also record the actual 093 source/compiler/seed process evidence.
		require.Equal(t, "093c7197d5eac50787971fd5ff7553a414142fc7", os.Getenv("TEST_WALLET_BASELINE_COMMIT"))
		for _, table := range []string{"users", "balance_ledger_operations", "balance_ledger"} {
			require.True(t, probe.Migrator().HasTable(table), "093 fixture is incomplete: %s", table)
		}
		original = walletSchemaRows(t, probe)
		for table, rows := range original {
			require.NotEmpty(t, rows, "093 preservation fixture must contain %s rows", table)
		}
	} else {
		tables, err := probe.Migrator().GetTables()
		require.NoError(t, err)
		require.Empty(t, tables, "fresh mode must not target an existing or partially initialized database")
	}

	walletSchemaInitDB(t)
	assert.Equal(t, expectedType, common.MainDatabaseType())
	for _, table := range []string{"users", "balance_ledger_operations", "balance_ledger"} {
		require.True(t, DB.Migrator().HasTable(table), "production InitDB omitted %s", table)
	}
	if upgrade {
		assert.Equal(t, original, walletSchemaRows(t, DB), "schema-only migration must retain every original fixture field")
	} else {
		user := User{Id: 771001, Username: "schema-wallet", Password: "synthetic-not-authentication", Status: 2,
			Quota: 5_000_000_038, QuotaPaid: 5_000_000_000, QuotaBonus: 31, QuotaReward: 7,
			UsedQuota: 3_000_000_002, AffQuota: 3_000_000_001, AffHistoryQuota: 4_000_000_001, AuthVersion: 1}
		operation := BalanceLedgerOperation{UserId: user.Id, IdempotencyKey: "schema-fixture-original", Operation: "credit",
			OperationSignature: strings.Repeat("a", 64), TotalBefore: 38, TotalAfter: user.Quota, CreatedAt: 1700000000}
		require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(&user).Error; err != nil {
				return err
			}
			if err := tx.Create(&operation).Error; err != nil {
				return err
			}
			return tx.Create(&BalanceLedger{OperationId: operation.Id, UserId: user.Id, Account: "paid",
				Delta: user.QuotaPaid, BalanceBefore: 0, BalanceAfter: user.QuotaPaid,
				TotalBefore: 38, TotalAfter: user.Quota, Reason: "schema_fixture", ReferenceType: "fixture",
				ReferenceId: "original", RequestId: "original-request", OperatorId: 771002,
				Metadata: `{"fixture":true}`, IdempotencyKey: operation.IdempotencyKey, CreatedAt: operation.CreatedAt}).Error
		}))
		var stored User
		require.NoError(t, DB.First(&stored, user.Id).Error)
		assert.Equal(t, user.Quota, stored.Quota)
		assert.Equal(t, user.QuotaPaid, stored.QuotaPaid)
		assert.Equal(t, user.QuotaBonus, stored.QuotaBonus)
		assert.Equal(t, user.QuotaReward, stored.QuotaReward)
		assert.Equal(t, user.UsedQuota, stored.UsedQuota)
		assert.Equal(t, user.AffQuota, stored.AffQuota)
		assert.Equal(t, user.AffHistoryQuota, stored.AffHistoryQuota)
		t.Run("stale_nonfinancial_update_preserves_wallet", func(t *testing.T) {
			walletSchemaAssertNonfinancialUpdate(t, stored)
		})
		original = walletSchemaRows(t, DB)
	}

	columns, err := DB.Migrator().ColumnTypes(&User{})
	require.NoError(t, err)
	for _, name := range []string{"quota", "used_quota", "aff_quota", "aff_history", "quota_paid", "quota_bonus", "quota_reward"} {
		found := false
		for _, column := range columns {
			if column.Name() == name {
				found = true
				if expectedType != common.DatabaseTypeSQLite {
					assert.Contains(t, []string{"bigint", "int8", "bigint unsigned", "unsigned bigint"}, strings.ToLower(column.DatabaseTypeName()), name)
				}
			}
		}
		assert.True(t, found, "money column missing: %s", name)
	}
	require.True(t, DB.Migrator().HasIndex(&BalanceLedgerOperation{}, "idx_balance_ledger_operations_idempotency_key"))
	var existing BalanceLedgerOperation
	require.NoError(t, DB.First(&existing).Error)
	existing.Id = 0
	assert.Error(t, DB.Create(&existing).Error, "the same operation key must remain globally unique")
	assert.Equal(t, original, walletSchemaRows(t, DB))

	primarySQLitePath := common.SQLitePath
	if logSQLitePath != "" {
		common.SQLitePath = logSQLitePath
	}
	logProbe, _, err := chooseDB("LOG_SQL_DSN", true)
	require.NoError(t, err)
	logProbeSQL, err := logProbe.DB()
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, logProbeSQL.Close()) })
	logTables, err := logProbe.Migrator().GetTables()
	require.NoError(t, err)
	require.Empty(t, logTables, "the separate log fixture must start empty")
	err = InitLogDB()
	common.SQLitePath = primarySQLitePath
	if LOG_DB != oldLogDB && LOG_DB != nil {
		logSQL, sqlErr := LOG_DB.DB()
		require.NoError(t, sqlErr)
		t.Cleanup(func() { assert.NoError(t, logSQL.Close()) })
	}
	require.NoError(t, err)
	require.NotSame(t, DB, LOG_DB)
	for _, table := range []string{"users", "balance_ledger_operations", "balance_ledger"} {
		assert.False(t, LOG_DB.Migrator().HasTable(table), "financial evidence must not be migrated into the separate log DB")
	}
	walletSchemaInitDB(t)
	assert.Equal(t, original, walletSchemaRows(t, DB), "second production startup must not rewrite wallet data")
	require.True(t, DB.Migrator().HasIndex(&BalanceLedgerOperation{}, "idx_balance_ledger_operations_idempotency_key"))
}

// The fixture records a later synthetic wallet transaction, then exercises the
// existing generic User writer with the earlier snapshot. It does not implement
// or enable a wallet funding operation, and the tables must come from InitDB.
func walletSchemaAssertNonfinancialUpdate(t *testing.T, stale User) {
	t.Helper()
	latest := stale
	latest.QuotaPaid += 5
	latest.QuotaBonus += 7
	latest.QuotaReward += 11
	latest.Quota += 23
	operation := BalanceLedgerOperation{UserId: stale.Id, IdempotencyKey: "schema-fixture-later", Operation: "credit",
		OperationSignature: strings.Repeat("b", 64), TotalBefore: stale.Quota, TotalAfter: latest.Quota, CreatedAt: 1700000001}
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&User{}).Where("id = ?", stale.Id).Updates(map[string]any{
			"quota": latest.Quota, "quota_paid": latest.QuotaPaid, "quota_bonus": latest.QuotaBonus, "quota_reward": latest.QuotaReward,
		}).Error; err != nil {
			return err
		}
		if err := tx.Create(&operation).Error; err != nil {
			return err
		}
		for _, account := range []struct {
			name          string
			before, delta int
		}{{"paid", stale.QuotaPaid, 5}, {"bonus", stale.QuotaBonus, 7}, {"reward", stale.QuotaReward, 11}} {
			if err := tx.Create(&BalanceLedger{OperationId: operation.Id, UserId: stale.Id, Account: account.name,
				Delta: account.delta, BalanceBefore: account.before, BalanceAfter: account.before + account.delta,
				TotalBefore: operation.TotalBefore, TotalAfter: operation.TotalAfter, Reason: "schema_fixture",
				IdempotencyKey: operation.IdempotencyKey, CreatedAt: operation.CreatedAt}).Error; err != nil {
				return err
			}
		}
		return nil
	}))
	var current User
	require.NoError(t, DB.First(&current, stale.Id).Error)
	require.Equal(t, latest, current, "the later fixture transaction must be committed before the stale update")
	before := walletSchemaRows(t, DB)
	stale.DisplayName = "profile-after-credit"
	latest.DisplayName = stale.DisplayName
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return stale.UpdateWithTx(tx, false) }))
	var actual User
	require.NoError(t, DB.First(&actual, stale.Id).Error)
	assert.Equal(t, latest, actual, "a generic nonfinancial update must not restore stale wallet balances")
	after := walletSchemaRows(t, DB)
	assert.Equal(t, before["balance_ledger_operations"], after["balance_ledger_operations"])
	assert.Equal(t, before["balance_ledger"], after["balance_ledger"])
}

func walletSchemaInitDB(t *testing.T) {
	t.Helper()
	previous := DB
	err := InitDB()
	if DB != previous && DB != nil {
		sqlDB, sqlErr := DB.DB()
		require.NoError(t, sqlErr)
		t.Cleanup(func() { assert.NoError(t, sqlDB.Close()) })
	}
	require.NoError(t, err, "production InitDB must supply the wallet schema itself")
}

func walletSchemaRows(t *testing.T, db *gorm.DB) map[string][]map[string]any {
	t.Helper()
	result := make(map[string][]map[string]any)
	for _, table := range []string{"users", "balance_ledger_operations", "balance_ledger"} {
		var rows []map[string]any
		require.NoError(t, db.Table(table).Order("id asc").Find(&rows).Error)
		result[table] = rows
	}
	return result
}

func TestWalletSchemaGuardRetainsNonBypassableWidthCheck(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "narrow.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.Exec(`CREATE TABLE users (id INTEGER PRIMARY KEY,
		quota BIGINT DEFAULT 0, used_quota BIGINT DEFAULT 0, aff_quota BIGINT DEFAULT 0, aff_history BIGINT DEFAULT 0,
		quota_paid INT DEFAULT 0, quota_bonus INT DEFAULT 0, quota_reward INT DEFAULT 0)`).Error)
	// This is a unit branch check using SQLite's declared column metadata, not a
	// claim that a real MySQL/PostgreSQL migration ran; the native gate is separate.
	for _, dialect := range []common.DatabaseType{common.DatabaseTypeMySQL, common.DatabaseTypePostgreSQL} {
		for _, skip := range []string{"false", "true"} {
			t.Run(string(dialect)+"/skip="+skip, func(t *testing.T) {
				t.Setenv("SKIP_64BIT_QUOTA_SCHEMA_CHECK", skip)
				err := ensureUserQuotaColumns(db, dialect)
				require.Error(t, err)
				assert.Contains(t, err.Error(), "quota_paid")
				assert.Contains(t, err.Error(), "SKIP_64BIT_QUOTA_SCHEMA_CHECK does not apply")
			})
		}
	}
	assert.Error(t, migrateRequiredWalletSchema(nil))
}
