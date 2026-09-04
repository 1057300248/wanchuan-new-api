package model

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupUserUpdateTestState(t *testing.T) {
	t.Helper()
	truncateTables(t)
	require.NoError(t, DB.Exec("DELETE FROM users").Error)

	oldRedisEnabled := common.RedisEnabled
	oldBatchUpdateEnabled := common.BatchUpdateEnabled
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	t.Cleanup(func() {
		common.RedisEnabled = oldRedisEnabled
		common.BatchUpdateEnabled = oldBatchUpdateEnabled
	})
}

func createUserBindTestUser(t *testing.T) User {
	t.Helper()
	user := User{
		Username:    "bind-test-user",
		Password:    "unused-password-hash",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AuthVersion: 1,
		AffCode:     "bind-test-aff-code",
	}
	require.NoError(t, DB.Create(&user).Error)
	return user
}

func TestUserUpdateDoesNotOverwriteConcurrentAccountingOrTokenChanges(t *testing.T) {
	setupUserUpdateTestState(t)

	user := User{
		Id:              1,
		Username:        "quota-race-user",
		Password:        "password",
		DisplayName:     "before",
		Status:          common.UserStatusEnabled,
		Quota:           1000,
		QuotaPaid:       1000,
		UsedQuota:       20,
		RequestCount:    3,
		AffCount:        2,
		AffQuota:        800,
		AffHistoryQuota: 1200,
	}
	user.SetAccessToken("old-token")
	require.NoError(t, DB.Create(&user).Error)

	staleUser, err := GetUserById(user.Id, true)
	require.NoError(t, err)

	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Updates(map[string]interface{}{
		"quota":           gorm.Expr("quota - ?", 400),
		"used_quota":      gorm.Expr("used_quota + ?", 400),
		"request_count":   gorm.Expr("request_count + ?", 1),
		"quota_paid":      gorm.Expr("quota_paid - ?", 400),
		"quota_bonus":     gorm.Expr("quota_bonus + ?", 100),
		"quota_affiliate": gorm.Expr("quota_affiliate + ?", 300),
		"aff_count":       gorm.Expr("aff_count + ?", 1),
		"aff_quota":       gorm.Expr("aff_quota - ?", 500),
		"aff_history":     gorm.Expr("aff_history + ?", 500),
		"access_token":    "rotated-token",
	}).Error)

	staleUser.DisplayName = "after"
	require.NoError(t, staleUser.Update(false))

	var got User
	require.NoError(t, DB.First(&got, user.Id).Error)
	assert.Equal(t, "after", got.DisplayName)
	assert.Equal(t, 600, got.Quota)
	assert.Equal(t, 420, got.UsedQuota)
	assert.Equal(t, 4, got.RequestCount)
	assert.Equal(t, 600, got.QuotaPaid)
	assert.Equal(t, 100, got.QuotaBonus)
	assert.Equal(t, 300, got.QuotaAffiliate)
	assert.Equal(t, 3, got.AffCount)
	assert.Equal(t, 300, got.AffQuota)
	assert.Equal(t, 1700, got.AffHistoryQuota)
	assert.Equal(t, "rotated-token", got.GetAccessToken())
}

func TestUsageAccountingSupportsSignedDirectAndBatchDeltas(t *testing.T) {
	setupUserUpdateTestState(t)
	resetBatchUpdateTestState(t)

	user := User{
		Id:           10,
		Username:     "usage-adjustment-user",
		Password:     "password",
		Status:       common.UserStatusEnabled,
		UsedQuota:    1000,
		RequestCount: 3,
	}
	channel := Channel{
		Id:        10,
		Name:      "usage-adjustment-channel",
		Key:       "sk-test",
		Status:    common.ChannelStatusEnabled,
		UsedQuota: 1000,
	}
	require.NoError(t, DB.Create(&user).Error)
	require.NoError(t, DB.Create(&channel).Error)

	UpdateUserUsedQuota(user.Id, -200)
	UpdateUserUsedQuota(user.Id, 50)
	UpdateChannelUsedQuota(channel.Id, -200)
	UpdateChannelUsedQuota(channel.Id, 50)

	var got User
	require.NoError(t, DB.Select("used_quota", "request_count").First(&got, user.Id).Error)
	assert.Equal(t, 850, got.UsedQuota)
	assert.Equal(t, 3, got.RequestCount)
	var gotChannel Channel
	require.NoError(t, DB.Select("used_quota").First(&gotChannel, channel.Id).Error)
	assert.Equal(t, int64(850), gotChannel.UsedQuota)

	common.BatchUpdateEnabled = true
	UpdateUserUsedQuota(user.Id, 400)
	UpdateUserUsedQuota(user.Id, -100)
	UpdateChannelUsedQuota(channel.Id, 400)
	UpdateChannelUsedQuota(channel.Id, -100)

	require.NoError(t, DB.Select("used_quota", "request_count").First(&got, user.Id).Error)
	assert.Equal(t, 850, got.UsedQuota, "batch deltas must remain queued until flush")
	assert.Equal(t, 3, got.RequestCount)
	require.NoError(t, DB.Select("used_quota").First(&gotChannel, channel.Id).Error)
	assert.Equal(t, int64(850), gotChannel.UsedQuota, "batch deltas must remain queued until flush")

	batchUpdate()
	require.NoError(t, DB.Select("used_quota", "request_count").First(&got, user.Id).Error)
	assert.Equal(t, 1150, got.UsedQuota)
	assert.Equal(t, 3, got.RequestCount)
	require.NoError(t, DB.Select("used_quota").First(&gotChannel, channel.Id).Error)
	assert.Equal(t, int64(1150), gotChannel.UsedQuota)
}

func TestUpdateUserAccessTokenOnlyUpdatesAccessToken(t *testing.T) {
	setupUserUpdateTestState(t)

	user := User{
		Id:              2,
		Username:        "token-rotation-user",
		Password:        "password",
		DisplayName:     "before",
		Status:          common.UserStatusEnabled,
		Quota:           1000,
		AffQuota:        800,
		AffHistoryQuota: 1200,
	}
	require.NoError(t, DB.Create(&user).Error)

	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Updates(map[string]interface{}{
		"quota":        gorm.Expr("quota + ?", 500),
		"aff_quota":    gorm.Expr("aff_quota - ?", 500),
		"display_name": "concurrent-update",
	}).Error)

	require.NoError(t, UpdateUserAccessToken(user.Id, "rotated-token"))

	var got User
	require.NoError(t, DB.First(&got, user.Id).Error)
	assert.Equal(t, "rotated-token", got.GetAccessToken())
	assert.Equal(t, "concurrent-update", got.DisplayName)
	assert.Equal(t, 1500, got.Quota)
	assert.Equal(t, 300, got.AffQuota)
	assert.Equal(t, 1200, got.AffHistoryQuota)
}

func TestUpdateUserAccessTokenRejectsSoftDeletedUser(t *testing.T) {
	setupUserUpdateTestState(t)

	user := User{
		Id:       3,
		Username: "deleted-token-rotation-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
	}
	user.SetAccessToken("old-token")
	require.NoError(t, DB.Create(&user).Error)
	require.NoError(t, DB.Delete(&user).Error)

	err := UpdateUserAccessToken(user.Id, "orphaned-token")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	var got User
	require.NoError(t, DB.Unscoped().First(&got, user.Id).Error)
	assert.Equal(t, "old-token", got.GetAccessToken())
}

func TestUpdateUserSettingOnlyUpdatesSetting(t *testing.T) {
	setupUserUpdateTestState(t)

	user := User{
		Id:           2,
		Username:     "setting-user",
		Password:     "password",
		Status:       common.UserStatusEnabled,
		Quota:        1000,
		UsedQuota:    20,
		RequestCount: 3,
	}
	require.NoError(t, DB.Create(&user).Error)

	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Updates(map[string]interface{}{
		"quota":         gorm.Expr("quota - ?", 250),
		"used_quota":    gorm.Expr("used_quota + ?", 250),
		"request_count": gorm.Expr("request_count + ?", 1),
	}).Error)

	require.NoError(t, UpdateUserSetting(user.Id, dto.UserSetting{Language: "zh"}))

	var got User
	require.NoError(t, DB.First(&got, user.Id).Error)
	assert.Equal(t, 750, got.Quota)
	assert.Equal(t, 270, got.UsedQuota)
	assert.Equal(t, 4, got.RequestCount)
	assert.Equal(t, "zh", got.GetSetting().Language)
}

func TestEnsureEmailAvailableRejectsExistingEmailCaseInsensitive(t *testing.T) {
	setupUserUpdateTestState(t)

	require.NoError(t, DB.Create(&User{
		Username: "existing",
		Password: "old-password",
		Email:    "Taken@Example.com",
		Status:   common.UserStatusEnabled,
	}).Error)

	err := EnsureEmailAvailable(" taken@example.COM ", 0)
	require.ErrorIs(t, err, ErrEmailAlreadyTaken)

	user, err := GetUniqueUserByEmail("TAKEN@example.com")
	require.NoError(t, err)
	assert.Equal(t, "existing", user.Username)

	require.NoError(t, EnsureEmailAvailable("taken@example.com", user.Id))
}

func TestInsertRejectsDuplicateEmailWithoutUniqueIndex(t *testing.T) {
	setupUserUpdateTestState(t)

	require.NoError(t, DB.Create(&User{
		Username: "existing",
		Password: "old-password",
		Email:    "taken@example.com",
		Status:   common.UserStatusEnabled,
	}).Error)

	user := &User{
		Username: "oauth-user",
		Email:    "TAKEN@example.com",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}

	err := user.Insert(0)
	require.ErrorIs(t, err, ErrEmailAlreadyTaken)

	var count int64
	require.NoError(t, DB.Model(&User{}).Where("username = ?", "oauth-user").Count(&count).Error)
	assert.Zero(t, count)
}

func TestInsertKeepsBlankPasswordForPasswordlessUser(t *testing.T) {
	setupUserUpdateTestState(t)

	user := &User{
		Username: "passwordless-user",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}

	require.NoError(t, user.Insert(0))

	var stored User
	require.NoError(t, DB.Where("username = ?", user.Username).First(&stored).Error)
	assert.Empty(t, stored.Password)
}

func TestUpdateUserBindColumnOnlyTouchesTheBindingColumn(t *testing.T) {
	truncateTables(t)

	user := createUserBindTestUser(t)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Updates(map[string]interface{}{
		"role":   common.RoleAdminUser,
		"status": common.UserStatusEnabled,
		"group":  "vip",
	}).Error)

	require.NoError(t, UpdateUserBindColumn(user.Id, "github_id", "gh-12345"))

	reloaded, err := GetUserById(user.Id, true)
	require.NoError(t, err)
	assert.Equal(t, "gh-12345", reloaded.GitHubId)
	assert.Equal(t, common.RoleAdminUser, reloaded.Role)
	assert.Equal(t, common.UserStatusEnabled, reloaded.Status)
	assert.Equal(t, "vip", reloaded.Group)
}

func TestUpdateUserBindColumnPreservesRestrictiveChange(t *testing.T) {
	truncateTables(t)

	user := createUserBindTestUser(t)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).
		Update("status", common.UserStatusDisabled).Error)
	require.NoError(t, UpdateUserBindColumn(user.Id, "wechat_id", "wx-open-id"))

	reloaded, err := GetUserById(user.Id, true)
	require.NoError(t, err)
	assert.Equal(t, "wx-open-id", reloaded.WeChatId)
	assert.Equal(t, common.UserStatusDisabled, reloaded.Status)
}

func TestUpdateUserBindColumnRejectsNonWhitelistedColumns(t *testing.T) {
	truncateTables(t)

	user := createUserBindTestUser(t)
	for _, column := range []string{"role", "status", "group", "quota", "username", "password", "id"} {
		assert.Error(t, UpdateUserBindColumn(user.Id, column, "1"), "column %s must be rejected", column)
	}
	assert.Error(t, UpdateUserBindColumn(user.Id, "github_id; DROP TABLE users", "x"))
	assert.Error(t, UpdateUserBindColumn(0, "github_id", "x"))
}

func TestValidateAndFillRejectsPasswordlessUser(t *testing.T) {
	setupUserUpdateTestState(t)

	require.NoError(t, DB.Create(&User{
		Username: "passwordless-user",
		Password: "",
		Status:   common.UserStatusEnabled,
	}).Error)

	loginUser := User{
		Username: "passwordless-user",
		Password: "NewPassword123",
	}
	err := loginUser.ValidateAndFill()
	require.ErrorIs(t, err, ErrInvalidCredentials)

	var stored User
	require.NoError(t, DB.Where("username = ?", "passwordless-user").First(&stored).Error)
	assert.Empty(t, stored.Password)
}

func TestResetUserPasswordByEmailRequiresSingleActiveMatch(t *testing.T) {
	setupUserUpdateTestState(t)

	require.NoError(t, DB.Create(&User{
		Username: "duplicate-1",
		Password: "old-1",
		Email:    "legacy@example.com",
		AffCode:  "dupe1",
		Status:   common.UserStatusEnabled,
	}).Error)
	require.NoError(t, DB.Create(&User{
		Username: "duplicate-2",
		Password: "old-2",
		Email:    "LEGACY@example.com",
		AffCode:  "dupe2",
		Status:   common.UserStatusEnabled,
	}).Error)

	err := ResetUserPasswordByEmail("legacy@example.com", "NewPassword123")
	require.ErrorIs(t, err, ErrEmailAmbiguous)

	var duplicates []User
	require.NoError(t, DB.Where("LOWER(email) = ?", "legacy@example.com").Order("username asc").Find(&duplicates).Error)
	require.Len(t, duplicates, 2)
	assert.Equal(t, "old-1", duplicates[0].Password)
	assert.Equal(t, "old-2", duplicates[1].Password)

	require.NoError(t, DB.Create(&User{
		Username: "unique",
		Password: "old",
		Email:    "unique@example.com",
		AffCode:  "unique",
		Status:   common.UserStatusEnabled,
	}).Error)

	require.NoError(t, ResetUserPasswordByEmail("UNIQUE@example.com", "NewPassword123"))

	var unique User
	require.NoError(t, DB.Where("username = ?", "unique").First(&unique).Error)
	assert.True(t, common.ValidatePasswordAndHash("NewPassword123", unique.Password))

	err = ResetUserPasswordByEmail("missing@example.com", "NewPassword123")
	require.True(t, errors.Is(err, ErrEmailNotFound))
}

// ---------------------------------------------------------------------------
// inviteUser 并发回归：邀请更新不得覆盖同时发生的钱包/账本变更
// ---------------------------------------------------------------------------

func setupInviteRaceTestState(t *testing.T) {
	t.Helper()
	setupLedgerTestState(t)
}

// TestInviteUserDoesNotOverwriteConcurrentLedgerChanges 回归测试：inviteUser
// 只能做邀请计数的原子列增量。历史实现 GetUserById + DB.Save(user) 会把
// 读取时刻的旧快照写回全行，覆盖并发的账本事务对
// quota_paid/quota_bonus/quota_affiliate（及 quota 镜像）的更新。
func TestInviteUserDoesNotOverwriteConcurrentLedgerChanges(t *testing.T) {
	setupInviteRaceTestState(t)

	user := User{
		Id:              101,
		Username:        "invite-race-user",
		Password:        "password",
		Status:          common.UserStatusEnabled,
		Quota:           1500,
		QuotaPaid:       1000,
		QuotaBonus:      200,
		QuotaAffiliate:  300,
		AffCount:        5,
		AffQuota:        800,
		AffHistoryQuota: 1200,
	}
	require.NoError(t, DB.Create(&user).Error)

	// 模拟旧实现的读快照时刻：并发账本变更之前读取的用户行。
	staleUser, err := GetUserById(user.Id, true)
	require.NoError(t, err)
	require.NotNil(t, staleUser)
	assert.Equal(t, 1000, staleUser.QuotaPaid, "前置条件：快照必须持有变更前的余额")

	// 并发钱包扣减（真实账本路径：事务内更新余额列 + quota 镜像 + balance_ledger）
	result, err := DebitQuota(LedgerEntry{
		UserId:         user.Id,
		Delta:          400,
		Reason:         LedgerReasonBillingPreConsume,
		ReferenceType:  "test",
		ReferenceId:    "invite-race-debit",
		IdempotencyKey: "test:invite-race-debit",
	}, AccountPaid, AccountBonus)
	require.NoError(t, err)
	require.False(t, result.Replayed)

	// 邀请更新只允许触碰 aff_count。
	require.NoError(t, inviteUser(user.Id))

	got := getUserBalances(t, user.Id)
	// 钱包余额与镜像必须保留并发账本的结果；旧实现 DB.Save(staleUser)
	// 会把这些断言需要的值覆盖回 1000/200/300/1500。
	assert.Equal(t, 600, got.QuotaPaid)
	assert.Equal(t, 200, got.QuotaBonus)
	assert.Equal(t, 300, got.QuotaAffiliate)
	assert.Equal(t, 1100, got.Quota)
	assertQuotaInvariant(t, got)
	// 邀请计数精确递增一次。
	assert.Equal(t, 6, got.AffCount)
	assert.Equal(t, 800, got.AffQuota)
	assert.Equal(t, 1200, got.AffHistoryQuota)
}

// TestInviteUserConcurrentWithLedgerDebits 并发验证：多次邀请更新与钱包账本
// 扣减交错执行时，无任何余额/镜像丢失，邀请计数精确累加。
// 测试库使用单连接 SQLite，数据库访问天然串行，断言的是最终不变量。
func TestInviteUserConcurrentWithLedgerDebits(t *testing.T) {
	setupInviteRaceTestState(t)

	user := User{
		Id:              102,
		Username:        "invite-concurrent-user",
		Password:        "password",
		Status:          common.UserStatusEnabled,
		Quota:           1500,
		QuotaPaid:       1000,
		QuotaBonus:      200,
		QuotaAffiliate:  300,
		AffCount:        0,
		AffQuota:        0,
		AffHistoryQuota: 0,
	}
	require.NoError(t, DB.Create(&user).Error)

	const inviters = 8
	const debits = 3
	var wg sync.WaitGroup
	inviteErrs := make([]error, inviters)
	debitErrs := make([]error, debits)
	wg.Add(inviters + debits)
	for i := 0; i < inviters; i++ {
		go func(idx int) {
			defer wg.Done()
			inviteErrs[idx] = inviteUser(user.Id)
		}(i)
	}
	for i := 0; i < debits; i++ {
		go func(idx int) {
			defer wg.Done()
			_, err := DebitQuota(LedgerEntry{
				UserId:         user.Id,
				Delta:          100,
				Reason:         LedgerReasonBillingPreConsume,
				ReferenceType:  "test",
				ReferenceId:    fmt.Sprintf("invite-concurrent-debit-%d", idx),
				IdempotencyKey: fmt.Sprintf("test:invite-concurrent-debit-%d", idx),
			}, AccountPaid, AccountBonus)
			debitErrs[idx] = err
		}(i)
	}
	wg.Wait()
	for i, err := range inviteErrs {
		require.NoError(t, err, "invite %d", i)
	}
	for i, err := range debitErrs {
		require.NoError(t, err, "ledger debit %d", i)
	}

	got := getUserBalances(t, user.Id)
	// 三次扣减全部生效（paid 先扣 100*3），邀请更新未吞掉任何一笔账本变更。
	assert.Equal(t, 700, got.QuotaPaid)
	assert.Equal(t, 200, got.QuotaBonus)
	assert.Equal(t, 300, got.QuotaAffiliate)
	assert.Equal(t, 1200, got.Quota)
	assertQuotaInvariant(t, got)
	// 八次邀请全部精确计数，不创建注册时奖励余额。
	assert.Equal(t, inviters, got.AffCount)
	assert.Zero(t, got.AffQuota)
	assert.Zero(t, got.AffHistoryQuota)
}

// TestInviteUserRejectsSoftDeletedUser 邀请更新必须命中用户行：
// 已（软）删除用户的邀请计数不得被"复活"写入。
func TestInviteUserRejectsSoftDeletedUser(t *testing.T) {
	setupInviteRaceTestState(t)

	user := User{
		Id:              103,
		Username:        "invite-deleted-user",
		Password:        "password",
		Status:          common.UserStatusEnabled,
		Quota:           500,
		QuotaPaid:       500,
		AffCount:        1,
		AffQuota:        100,
		AffHistoryQuota: 100,
	}
	require.NoError(t, DB.Create(&user).Error)
	require.NoError(t, DB.Delete(&user).Error)

	err := inviteUser(user.Id)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	var got User
	require.NoError(t, DB.Unscoped().First(&got, user.Id).Error)
	assert.Equal(t, 1, got.AffCount)
	assert.Equal(t, 100, got.AffQuota)
	assert.Equal(t, 100, got.AffHistoryQuota)
	assert.Equal(t, 500, got.QuotaPaid)
}
