package model

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestDatabaseCompatibilityReal exercises the same InitDB/migrateDB path used by
// production against a real database selected by TEST_DATABASE_DIALECT.
// It is opt-in so the normal unit-test suite stays self-contained.
func TestDatabaseCompatibilityReal(t *testing.T) {
	if os.Getenv("TEST_DATABASE_COMPAT") != "1" {
		t.Skip("set TEST_DATABASE_COMPAT=1 to run the real database compatibility test")
	}

	dialect := strings.ToLower(strings.TrimSpace(os.Getenv("TEST_DATABASE_DIALECT")))
	switch dialect {
	case "sqlite":
		oldSQLitePath := common.SQLitePath
		common.SQLitePath = filepath.Join(t.TempDir(), "compat.db") + "?_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)&_txlock=immediate"
		t.Cleanup(func() { common.SQLitePath = oldSQLitePath })
		t.Setenv("SQL_DSN", "local")
	case "mysql", "postgres":
		dsn := strings.TrimSpace(os.Getenv("TEST_DATABASE_DSN"))
		require.NotEmpty(t, dsn, "TEST_DATABASE_DSN is required for %s", dialect)
		t.Setenv("SQL_DSN", dsn)
	default:
		t.Fatalf("unsupported TEST_DATABASE_DIALECT %q", dialect)
	}
	t.Setenv("LOG_SQL_DSN", "")

	require.NoError(t, InitDB(), "production InitDB/migrateDB path must succeed on %s", dialect)
	require.NoError(t, InitLogDB(), "production InitLogDB path must succeed on %s", dialect)
	require.NotNil(t, DB)
	require.NotNil(t, LOG_DB)

	sqlDB, err := DB.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, sqlDB.Ping())

	var version string
	switch dialect {
	case "sqlite":
		require.NoError(t, DB.Raw("SELECT sqlite_version()").Scan(&version).Error)
	case "mysql":
		require.NoError(t, DB.Raw("SELECT VERSION()").Scan(&version).Error)
	case "postgres":
		require.NoError(t, DB.Raw("SHOW server_version").Scan(&version).Error)
	}
	t.Logf("validated %s server version: %s", dialect, version)

	for name, table := range map[string]any{
		"users":              &User{},
		"tokens":             &Token{},
		"options":            &Option{},
		"quota_data":         &QuotaData{},
		"balance_ledger":     &BalanceLedger{},
		"subscription_plans": &SubscriptionPlan{},
	} {
		require.Truef(t, DB.Migrator().HasTable(table), "migrateDB did not create %s on %s", name, dialect)
	}

	// Values above int32 prove that the wallet mirror and paid account survive
	// actual round-trips on the target database rather than only looking correct
	// in GORM metadata.
	const largeQuota = 3_000_000_000
	unique := fmt.Sprintf("dbcompat-%s-%d", dialect, time.Now().UnixNano())
	user := User{
		Username:    unique,
		Password:    "compat-test-password-hash",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Group:       "default",
		Quota:       largeQuota,
		QuotaPaid:   largeQuota,
		AuthVersion: 1,
		AffCode:     unique,
	}
	require.NoError(t, DB.Create(&user).Error)
	require.NotZero(t, user.Id)

	var stored User
	require.NoError(t, DB.First(&stored, user.Id).Error)
	require.Equal(t, largeQuota, stored.Quota)
	require.Equal(t, largeQuota, stored.QuotaPaid)
	require.Equal(t, stored.QuotaPaid+stored.QuotaBonus+stored.QuotaAffiliate, stored.Quota)

	// Exercise the quota_data write/update/group-by path, which is sensitive to
	// SQL dialect differences and is used by the dashboard/export flow.
	bucket := time.Now().Unix()
	bucket -= bucket % 3600
	first := QuotaData{
		UserID: user.Id, Username: unique, ModelName: "compat-model",
		CreatedAt: bucket, UseGroup: "default", TokenID: 101, ChannelID: 202,
		NodeName: "compat-node", TokenUsed: 11, Count: 1, Quota: 123,
	}
	require.NoError(t, DB.Create(&first).Error)

	increaseQuotaData(&QuotaData{
		UserID: user.Id, Username: unique, ModelName: "compat-model",
		CreatedAt: bucket, UseGroup: "default", TokenID: 101, ChannelID: 202,
		NodeName: "compat-node", TokenUsed: 7, Count: 2, Quota: 77,
	})

	rows, err := GetQuotaDataByUserId(user.Id, bucket, bucket)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, 3, rows[0].Count)
	require.Equal(t, 200, rows[0].Quota)
	require.Equal(t, 18, rows[0].TokenUsed)

	// A real transactional update catches lock/transaction syntax differences
	// between SQLite, MySQL 5.7 and PostgreSQL 9.6.
	require.NoError(t, DB.Transaction(func(txDB *gorm.DB) error {
		return txDB.Model(&User{}).Where("id = ?", user.Id).Update("quota_paid", largeQuota+1).Error
	}))
	var updatedPaid int
	require.NoError(t, DB.Model(&User{}).Select("quota_paid").Where("id = ?", user.Id).Scan(&updatedPaid).Error)
	require.Equal(t, largeQuota+1, updatedPaid)
}
