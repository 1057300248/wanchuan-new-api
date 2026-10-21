package model

import (
	"database/sql"
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
		require.False(t, DB.Migrator().HasTable("billing_finalizations"), "exact released schema must not already contain the candidate terminal table")
		// Refuse to seed a nonempty fixture instead of overwriting existing data.
		var count int64
		require.NoError(t, DB.Model(&User{}).Count(&count).Error)
		require.Zero(t, count)
		require.NoError(t, LOG_DB.Model(&AuditLog{}).Count(&count).Error)
		require.Zero(t, count)
		user := User{Username: "release-upgrade-owner", Password: "existing-password-hash", AffCode: "release-upgrade-aff",
			Quota: 5000, QuotaPaid: 4000, QuotaBonus: 1000, UsedQuota: 4294967400, AuthVersion: 7,
			AccessToken: common.GetPointer("release-upgrade-access-token")}
		require.NoError(t, DB.Create(&user).Error)
		token := Token{UserId: user.Id, Key: strings.Repeat("k", 48), Name: "release-upgrade-token",
			Status: common.TokenStatusEnabled, ExpiredTime: -1, Group: "default", RemainQuota: 700,
			UsedQuota: 300, ModelLimitsEnabled: true, ModelLimits: "release-upgrade-model"}
		require.NoError(t, DB.Create(&token).Error)
		subscriptionPlan := SubscriptionPlan{Title: "release-upgrade-subscription", TotalAmount: 1000, QuotaResetPeriod: SubscriptionResetNever}
		require.NoError(t, DB.Create(&subscriptionPlan).Error)
		subscription := UserSubscription{UserId: user.Id, PlanId: subscriptionPlan.Id, AmountTotal: 1000, AmountUsed: 350,
			StartTime: 1788600000, EndTime: 4102444800, Status: "active", Source: "order", AllowWalletOverflow: true}
		require.NoError(t, DB.Create(&subscription).Error)
		require.NoError(t, DB.Create(&SubscriptionPreConsumeRecord{RequestId: "release-upgrade-subscription-request", UserId: user.Id,
			UserSubscriptionId: subscription.Id, PreConsumed: 200, Status: "consumed"}).Error)
		mj := Midjourney{UserId: user.Id, MjId: "release-upgrade-mj", Action: "IMAGINE", Status: "IN_PROGRESS", Progress: "50%",
			Quota: 300, TokenId: token.Id, ChannelId: 19, BillingChannelId: 23, Prompt: "historical Midjourney prompt", SubmitTime: 1788600000}
		require.NoError(t, DB.Create(&mj).Error)
		metadata := Model{ModelName: "release-upgrade-model", ContextLength: common.GetPointer(128000),
			Capabilities: `["vision"]`, BonusEligible: common.GetPointer(false), Status: 1, SyncOfficial: 0}
		require.NoError(t, metadata.Insert())
		require.NoError(t, DB.Save(&Option{Key: "ModelRatio", Value: `{"release-upgrade-model":2.5}`}).Error)
		require.NoError(t, LOG_DB.Create(&Log{UserId: user.Id, Type: LogTypeConsume, Content: "historical release request", Quota: 123,
			RequestId: "release-upgrade-request", CreatedAt: 1788600000}).Error)
		for _, eventID := range []string{"release-upgrade-event-a", "release-upgrade-event-z"} {
			RecordAuditLog(nil, AuditLog{EventId: eventID, UserId: user.Id, Username: user.Username,
				ActorRole: common.RoleAdminUser, CreatedAt: 1788600000, Category: AuditCategoryOperation,
				RequestId: "release-upgrade-audit", Content: "historical release audit", Success: false,
				Other: AuditOther{Op: &AuditOperation{Action: "model.update", Params: AuditFields{
					"enabled": false, "limit": 0, "large_id": uint64(9007199254740993),
				}}}})
		}
		require.NoError(t, LOG_DB.Model(&AuditLog{}).Where("request_id = ?", "release-upgrade-audit").Count(&count).Error)
		require.EqualValues(t, 2, count, "the released version must persist the audit fixture")
		return
	}
	require.True(t, DB.Migrator().HasTable("billing_finalizations"), "candidate startup must add the terminal table")
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
	var token Token
	require.NoError(t, DB.Where("user_id = ? AND name = ?", user.Id, "release-upgrade-token").First(&token).Error)
	assert.Equal(t, strings.Repeat("k", 48), token.Key)
	assert.Equal(t, common.TokenStatusEnabled, token.Status)
	assert.EqualValues(t, -1, token.ExpiredTime)
	assert.Equal(t, "default", token.Group)
	assert.Equal(t, 700, token.RemainQuota)
	assert.Equal(t, 300, token.UsedQuota)
	assert.True(t, token.ModelLimitsEnabled)
	assert.Equal(t, "release-upgrade-model", token.ModelLimits)
	var subscriptionPlan SubscriptionPlan
	require.NoError(t, DB.Where("title = ?", "release-upgrade-subscription").First(&subscriptionPlan).Error)
	var subscription UserSubscription
	require.NoError(t, DB.Where("user_id = ? AND plan_id = ?", user.Id, subscriptionPlan.Id).First(&subscription).Error)
	assert.EqualValues(t, 1000, subscription.AmountTotal)
	assert.EqualValues(t, 350, subscription.AmountUsed)
	assert.EqualValues(t, 1788600000, subscription.StartTime)
	assert.EqualValues(t, 4102444800, subscription.EndTime)
	assert.Equal(t, "active", subscription.Status)
	assert.True(t, subscription.AllowWalletOverflow)
	var subscriptionReceipt SubscriptionPreConsumeRecord
	require.NoError(t, DB.Where("request_id = ?", "release-upgrade-subscription-request").First(&subscriptionReceipt).Error)
	assert.Equal(t, subscription.Id, subscriptionReceipt.UserSubscriptionId)
	assert.EqualValues(t, 200, subscriptionReceipt.PreConsumed)
	assert.Equal(t, "consumed", subscriptionReceipt.Status)
	// SQL reads keep this exact fixture compilable in the previous-release
	// checkout. Old receipts stay unknown; a timestamp is not an allowance ID.
	var subPeriod, receiptPeriod, reservedAmount, settledAmount sql.NullInt64
	require.NoError(t, DB.Model(&UserSubscription{}).Select("quota_period").Where("id = ?", subscription.Id).Row().Scan(&subPeriod))
	require.NoError(t, DB.Model(&SubscriptionPreConsumeRecord{}).Select("quota_period", "reserved_amount", "settled_amount").Where("id = ?", subscriptionReceipt.Id).Row().Scan(&receiptPeriod, &reservedAmount, &settledAmount))
	assert.True(t, subPeriod.Valid)
	assert.Zero(t, subPeriod.Int64)
	assert.True(t, receiptPeriod.Valid)
	assert.Zero(t, receiptPeriod.Int64)
	assert.Zero(t, reservedAmount.Int64)
	assert.Zero(t, settledAmount.Int64)
	require.ErrorContains(t, RefundSubscriptionPreConsume(subscriptionReceipt.RequestId), "subscription allowance period is unknown")
	freshReservation, err := PreConsumeUserSubscription("release-upgrade-new-subscription-request", user.Id, "release-upgrade-model", 0, 100)
	require.NoError(t, err)
	assert.Equal(t, subscription.Id, freshReservation.UserSubscriptionId)
	require.ErrorContains(t, RefundSubscriptionPreConsume(subscriptionReceipt.RequestId), "subscription allowance period is unknown")
	require.NoError(t, DB.First(&subscription, subscription.Id).Error)
	assert.EqualValues(t, 450, subscription.AmountUsed, "unresolved old refund must not consume newly reserved allowance")
	require.NoError(t, RefundSubscriptionPreConsume("release-upgrade-new-subscription-request"))
	require.NoError(t, DB.First(&subscription, subscription.Id).Error)
	assert.EqualValues(t, 350, subscription.AmountUsed)
	// Read newly introduced fields through SQL so this same fixture still
	// compiles against the exact previous release during its seed phase.
	require.True(t, DB.Migrator().HasColumn("tokens", "allow_user_quota_query"))
	var quotaQuery sql.NullBool
	require.NoError(t, DB.Table("tokens").Select("allow_user_quota_query").Where("id = ?", token.Id).Row().Scan(&quotaQuery))
	assert.False(t, quotaQuery.Bool, "existing tokens must not acquire permission to reveal user quota")
	var mj Midjourney
	require.NoError(t, DB.Where("user_id = ? AND mj_id = ?", user.Id, "release-upgrade-mj").First(&mj).Error)
	assert.Equal(t, "IMAGINE", mj.Action)
	assert.Equal(t, "IN_PROGRESS", mj.Status)
	assert.Equal(t, "50%", mj.Progress)
	assert.Equal(t, 300, mj.Quota)
	assert.Equal(t, token.Id, mj.TokenId)
	assert.Equal(t, 19, mj.ChannelId)
	assert.Equal(t, 23, mj.BillingChannelId)
	assert.Equal(t, "historical Midjourney prompt", mj.Prompt)
	assert.EqualValues(t, 1788600000, mj.SubmitTime)
	require.True(t, DB.Migrator().HasColumn("midjourneys", "wallet_debit"))
	var walletDebit sql.NullString
	require.NoError(t, DB.Table("midjourneys").Select("wallet_debit").Where("id = ?", mj.Id).Row().Scan(&walletDebit))
	assert.False(t, walletDebit.Valid, "old Midjourney tasks must retain the legacy NULL receipt")
	for _, column := range []string{"user_id", "action", "mj_id", "submit_time", "start_time", "finish_time", "status", "progress"} {
		assert.True(t, DB.Migrator().HasIndex("midjourneys", DB.NamingStrategy.IndexName("midjourneys", column)), "Midjourney index %s must survive", column)
	}
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
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		var create string
		require.NoError(t, LOG_DB.Raw("SHOW CREATE TABLE audit_logs").Scan(&create).Error)
		assert.Contains(t, create, "ORDER BY (created_at, event_id)")
		assert.NotContains(t, strings.ToUpper(create), "TTL", "usage-log retention must not delete audit history")
	} else {
		assert.True(t, LOG_DB.Migrator().HasIndex(&AuditLog{}, "idx_audit_time_event"))
		assert.Error(t, LOG_DB.Create(&AuditLog{EventId: "release-upgrade-event-a"}).Error, "event ID uniqueness must survive migration")
	}
	auditEntries, auditCount, err := GetAuditLogs(AuditLogFilter{RequestId: "release-upgrade-audit"}, 0, 10, common.RoleRootUser)
	require.NoError(t, err)
	require.EqualValues(t, 2, auditCount)
	require.Len(t, auditEntries, 2)
	assert.Equal(t, []string{"release-upgrade-event-z", "release-upgrade-event-a"}, []string{auditEntries[0].EventId, auditEntries[1].EventId})
	for _, auditEntry := range auditEntries {
		assert.Equal(t, user.Id, auditEntry.UserId)
		assert.Equal(t, user.Username, auditEntry.Username)
		assert.Equal(t, common.RoleAdminUser, auditEntry.ActorRole)
		assert.EqualValues(t, 1788600000, auditEntry.CreatedAt)
		assert.Equal(t, "historical release audit", auditEntry.Content)
		assert.False(t, auditEntry.Success)
		encoded, err := common.Marshal(auditEntry.Other)
		require.NoError(t, err)
		assert.JSONEq(t, `{"op":{"action":"model.update","params":{"enabled":false,"limit":0,"large_id":9007199254740993}}}`, string(encoded))
		var numericMetadata struct {
			Op struct {
				Params struct {
					LargeID uint64 `json:"large_id"`
				}
			}
		}
		require.NoError(t, common.Unmarshal(encoded, &numericMetadata))
		assert.EqualValues(t, 9007199254740993, numericMetadata.Op.Params.LargeID, "historical audit numbers must retain their precision")
	}
	assert.True(t, DB.Migrator().HasIndex(&User{}, "idx_users_access_token"))
	duplicate := User{Username: user.Username, Password: "duplicate", AffCode: "duplicate-username"}
	require.Error(t, DB.Create(&duplicate).Error, "username uniqueness must survive migration")
	duplicate = User{Username: "duplicate-token", Password: "duplicate", AffCode: "duplicate-token", AccessToken: user.AccessToken}
	require.Error(t, DB.Create(&duplicate).Error, "access token uniqueness must survive migration")
}
