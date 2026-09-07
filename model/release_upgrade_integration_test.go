package model

import (
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Run the seed phase from the previous release checkout and verify from the
// candidate. Both phases require explicitly supplied disposable database DSNs.
func TestReleaseUpgradePreservesWalletAndIdentity(t *testing.T) {
	phase := os.Getenv("TEST_RELEASE_UPGRADE_PHASE")
	if phase == "" {
		t.Skip("set TEST_RELEASE_UPGRADE_PHASE=seed or verify with disposable SQL_DSN/LOG_SQL_DSN")
	}
	require.Contains(t, []string{"seed", "verify"}, phase)
	require.NotEmpty(t, os.Getenv("SQL_DSN"))
	if path := os.Getenv("TEST_RELEASE_SQLITE_PATH"); path != "" {
		previous := common.SQLitePath
		common.SQLitePath = path
		t.Cleanup(func() { common.SQLitePath = previous })
	}
	previousMaster := common.IsMasterNode
	common.IsMasterNode = true
	t.Cleanup(func() { common.IsMasterNode = previousMaster })
	installDatabaseCompatRegistry(t)

	for range 2 {
		require.NoError(t, InitDB())
		require.NoError(t, InitLogDB())
		require.NoError(t, CloseDB())
	}
	require.NoError(t, InitDB())
	require.NoError(t, InitLogDB())
	t.Cleanup(func() { require.NoError(t, CloseDB()) })
	if phase == "seed" {
		// Refuse to seed a nonempty fixture instead of overwriting existing data.
		var count int64
		require.NoError(t, DB.Model(&User{}).Count(&count).Error)
		require.Zero(t, count)
		user := User{Username: "release-upgrade-owner", Password: "existing-password-hash", AffCode: "release-upgrade-aff",
			Quota: 5000, QuotaPaid: 4000, QuotaBonus: 1000, UsedQuota: 4294967400, AuthVersion: 7,
			AccessToken: common.GetPointer("release-upgrade-access-token")}
		require.NoError(t, DB.Create(&user).Error)
		metadata := Model{ModelName: "release-upgrade-model", ContextLength: common.GetPointer(128000),
			Capabilities: `["vision"]`, BonusEligible: common.GetPointer(false), Status: 1, SyncOfficial: 0}
		require.NoError(t, metadata.Insert())
		require.NoError(t, DB.Save(&Option{Key: "ModelRatio", Value: `{"release-upgrade-model":2.5}`}).Error)
		require.NoError(t, LOG_DB.Create(&Log{UserId: user.Id, Type: LogTypeConsume, Content: "historical release request", Quota: 123,
			RequestId: "release-upgrade-request", CreatedAt: 1788600000}).Error)
		return
	}
	var user User
	require.NoError(t, DB.Where("username = ?", "release-upgrade-owner").First(&user).Error)
	assert.Equal(t, 5000, user.Quota)
	assert.Equal(t, 4000, user.QuotaPaid)
	assert.Equal(t, 1000, user.QuotaBonus)
	assert.EqualValues(t, 4294967400, user.UsedQuota)
	assert.EqualValues(t, 7, user.AuthVersion)
	assert.Equal(t, "existing-password-hash", user.Password)
	require.NotNil(t, user.AccessToken)
	assert.Equal(t, "release-upgrade-access-token", strings.TrimRight(*user.AccessToken, " "), "PostgreSQL CHAR padding is not part of the credential")
	var metadata Model
	require.NoError(t, DB.Where("model_name = ?", "release-upgrade-model").First(&metadata).Error)
	assert.Equal(t, common.GetPointer(128000), metadata.ContextLength)
	assert.Equal(t, `["vision"]`, metadata.Capabilities)
	assert.Equal(t, common.GetPointer(false), metadata.BonusEligible)
	assert.Zero(t, metadata.SyncOfficial)
	var option Option
	require.NoError(t, DB.Where(commonKeyCol+" = ?", "ModelRatio").First(&option).Error)
	assert.JSONEq(t, `{"release-upgrade-model":2.5}`, option.Value)
	var entry Log
	require.NoError(t, LOG_DB.Where("request_id = ?", "release-upgrade-request").First(&entry).Error)
	assert.Equal(t, "historical release request", entry.Content)
	assert.Equal(t, 123, entry.Quota)
	assert.True(t, DB.Migrator().HasColumn(&User{}, "access_token_created_at"))
	assert.True(t, LOG_DB.Migrator().HasTable("audit_logs"))
	assert.True(t, DB.Migrator().HasIndex(&User{}, "idx_users_access_token"))
	duplicate := User{Username: user.Username, Password: "duplicate", AffCode: "duplicate-username"}
	require.Error(t, DB.Create(&duplicate).Error, "username uniqueness must survive migration")
	duplicate = User{Username: "duplicate-token", Password: "duplicate", AffCode: "duplicate-token", AccessToken: user.AccessToken}
	require.Error(t, DB.Create(&duplicate).Error, "access token uniqueness must survive migration")
}
