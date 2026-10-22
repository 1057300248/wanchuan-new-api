package service

import (
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Dedicated fresh databases only, separate from the model matrix.
// This matrix covers retained normal settlement and official refund sequencing.
func TestBillingFinalizationSessionDatabaseMatrix(t *testing.T) {
	for _, dialect := range []struct {
		name common.DatabaseType
		env  string
	}{
		{common.DatabaseTypeSQLite, ""}, {common.DatabaseTypeMySQL, "TEST_FINALIZATION_SERVICE_MYSQL_DSN"}, {common.DatabaseTypePostgreSQL, "TEST_FINALIZATION_SERVICE_POSTGRES_DSN"},
	} {
		t.Run(string(dialect.name), func(t *testing.T) {
			dsn := os.Getenv(dialect.env)
			if dialect.env != "" && dsn == "" {
				t.Skip(dialect.env + " not configured")
			}
			var driver gorm.Dialector = sqlite.Open(":memory:")
			if dialect.name == common.DatabaseTypeMySQL {
				driver = mysql.Open(dsn)
			} else if dialect.name == common.DatabaseTypePostgreSQL {
				driver = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
			}
			db, err := gorm.Open(driver, &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			tables, err := db.Migrator().GetTables()
			require.NoError(t, err)
			require.Empty(t, tables, "refusing nonempty disposable test database")
			oldDB, oldLogDB := model.DB, model.LOG_DB
			oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
			model.DB, model.LOG_DB = db, db
			common.SetDatabaseTypes(dialect.name, dialect.name)
			t.Cleanup(func() {
				model.DB, model.LOG_DB = oldDB, oldLogDB
				common.SetDatabaseTypes(oldMain, oldLog)
				model.InvalidateModelBonusEligibleCache()
			})
			require.NoError(t, db.AutoMigrate(&model.Option{}, &model.BillingFinalization{}, &model.Task{}, &model.User{}, &model.Token{}, &model.Log{}, &model.Channel{}, &model.Ability{}, &model.Midjourney{}, &model.TopUp{}, &model.UserSubscription{}, &model.SystemTask{}, &model.SystemTaskLock{}, &model.BalanceLedgerOperation{}, &model.BalanceLedger{}, &model.Model{}, &model.Vendor{}, &model.ModelBonusEligibleAudit{}, &model.ReferralCommission{}, &model.Redemption{}, &model.SubscriptionPlan{}, &model.SubscriptionOrder{}, &model.SubscriptionPreConsumeRecord{}))
			var version string
			q := "SELECT version()"
			if dialect.name == common.DatabaseTypeSQLite {
				q = "SELECT sqlite_version()"
			}
			require.NoError(t, db.Raw(q).Scan(&version).Error)
			t.Logf("terminal session matrix: %s %s", dialect.name, version)
			t.Run("separate_held_amounts", TestPolicyPreservingRefundSeparateHeldAmounts)
			t.Run("official_request_source_failure", TestOfficialRequestRefundMarksBeforeSourceAndDoesNotRetry)
			t.Run("official_request_token_failure", TestOfficialRequestRefundTokenFailureKeepsSourceCredit)
			t.Run("official_subscription_three_attempts", TestOfficialSubscriptionBaseFailureStillReturnsExtraAndToken)
			t.Run("official_task_independent_steps", TestOfficialTaskRefundIndependentStages)
			t.Run("official_post_commit_errors", TestOfficialRefundPostCommitErrorsDoNotRollBack)
			t.Run("historical_pending_read_only", TestOfficialTaskHistoricalPendingIsReadOnly)
			t.Run("inherited_093_allocation_limits", TestOfficial093WalletSnapshotLimitationsRemainExplicit)
			t.Run("commit_boundaries", TestPolicyPreservingTerminalCommitBoundaries)
			t.Run("settlement_deadline_driver_pool", TestBillingSettlementDeadlineDriverAndConnectionWait)
			if dialect.name != common.DatabaseTypeSQLite {
				t.Run("settlement_deadline_row_lock", TestBillingSettlementDeadlineNativeRowLock)
			}
			t.Run("settlement_deadline_commit_evidence", TestBillingSettlementDeadlineCommitEvidence)
			t.Run("official_refund_before_lost_ack_readback", TestOfficialRefundBeforeLostACKReadbackKeepsOfficialSequence)
			t.Run("settlement_deadline_redis", TestBillingSettlementDeadlineRedisBoundaries)
			t.Run("settlement_cancelled_client", TestBillingSettlementSurvivesCancelledClient)
			t.Run("extension_capability", TestPolicyPreservingExtensionCapabilityBoundary)
			t.Run("zero_delta_replacement", TestPolicyPreservingZeroTokenDeltaRetainsReplacementCounter)
			t.Run("probe_failure", TestPolicyPreservingRollbackProbeFailureDoesNotReserve)
			t.Run("independent_violation_fee", TestPolicyPreservingRefundIndependentViolationFee)
			t.Run("realtime_increment_terminal", TestPolicyPreservingRealtimeIncrementAndTerminal)
			t.Run("failed_settle_reserve_refund", TestPolicyPreservingFailedSettlementCanReserveThenRefund)
			t.Run("legacy_final_more_less", TestPolicyPreservingSettlementFinalMoreLessAndReplay)
			for _, scenario := range []string{"partial_more", "partial_less", "trusted", "playground", "unlimited", "subscription_zero_delta", "subscription_zero_delta_replaced_token"} {
				t.Run("real_session_"+scenario, func(t *testing.T) {
					truncate(t)
					seedBonusEligibleModel(t, "terminal-real-model", boolPtr(true))
					seedBonusUser(t, 31201, 300, 700)
					seedToken(t, 31202, 31201, "synthetic-original-refund-token", 1000)
					setting := operation_setting.GetQuotaSetting()
					oldTrust := setting.TrustQuotaUSD
					setting.TrustQuotaUSD = 0
					t.Cleanup(func() { setting.TrustQuotaUSD = oldTrust })
					info := &relaycommon.RelayInfo{UserId: 31201, TokenId: 31202, TokenKey: "synthetic-original-refund-token", RequestId: "real-" + scenario, OriginModelName: "terminal-real-model", UserSetting: dto.UserSetting{BillingPreference: "wallet_only"}}
					if scenario == "trusted" {
						setting.TrustQuotaUSD = 0.0001
					}
					if scenario == "playground" {
						info.IsPlayground = true
					}
					if scenario == "unlimited" {
						info.TokenUnlimited = true
						require.NoError(t, db.Model(&model.Token{}).Where("id = ?", 31202).Update("unlimited_quota", true).Error)
					}
					isSub := scenario == "subscription_zero_delta" || scenario == "subscription_zero_delta_replaced_token" || scenario == "subscription_refund" || scenario == "subscription_reset_refund"
					if isSub {
						plan := &model.SubscriptionPlan{Title: "terminal session plan", TotalAmount: 1000, QuotaResetPeriod: model.SubscriptionResetNever}
						require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(plan).Error)
						model.InvalidateSubscriptionPlanCache(plan.Id)
						t.Cleanup(func() { model.InvalidateSubscriptionPlanCache(plan.Id) })
						now := model.GetDBTimestamp()
						require.NoError(t, db.Create(&model.UserSubscription{Id: 31203, UserId: 31201, PlanId: plan.Id, AmountTotal: 1000, AmountUsed: 500, QuotaPeriod: 1, Status: "active", StartTime: now - 10, EndTime: now + 3600}).Error)
						info.UserSetting.BillingPreference = "subscription_only"
					}
					c := testGinContext(t)
					c.Set("token_quota", 1000)
					s, apiErr := NewBillingSession(c, info, 100)
					require.Nil(t, apiErr)
					info.Billing = s
					if scenario == "trusted" {
						require.Zero(t, s.GetPreConsumedQuota())
					} else {
						require.Equal(t, 100, s.GetPreConsumedQuota())
					}
					actual := 180
					if scenario == "partial_less" {
						actual = 60
					}
					if scenario == "subscription_zero_delta" || scenario == "subscription_zero_delta_replaced_token" {
						actual = 100
					}
					if scenario == "subscription_zero_delta_replaced_token" {
						require.NoError(t, db.Model(&model.Token{}).Where("id = ?", 31202).Updates(map[string]any{"key": "synthetic-replaced-zero-delta", "remain_quota": 777, "used_quota": 23}).Error)
					}

					require.NoError(t, s.Settle(actual))
					require.NoError(t, s.Settle(actual))
					s.Refund(c)
					require.False(t, s.NeedsRefund())
					var token model.Token
					require.NoError(t, db.First(&token, 31202).Error)
					expectedToken := 1000 - actual
					if scenario == "subscription_zero_delta_replaced_token" {
						expectedToken = 777
					}
					if scenario == "playground" {
						expectedToken = 1000
					}
					require.Equal(t, expectedToken, token.RemainQuota)
					if isSub {
						require.Equal(t, int64(500+actual), getSubscriptionUsed(t, 31203))
						var r model.SubscriptionPreConsumeRecord
						require.NoError(t, db.First(&r, "request_id = ?", info.RequestId).Error)
						require.Equal(t, "consumed", r.Status, "official normal settlement does not close the base preconsume receipt")
					} else {
						var u model.User
						require.NoError(t, db.First(&u, 31201).Error)
						require.Equal(t, 1000-actual, u.Quota)
					}
				})
			}
		})
	}
}
