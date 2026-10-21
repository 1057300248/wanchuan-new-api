package service

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSubscriptionPeriodTaskBilling(t *testing.T) { runSubscriptionPeriodTaskCases(t) }

// Reused by the real three-engine task receipt matrix; no provider calls or
// polling timers are needed to establish the transaction/period invariant.
func runSubscriptionPeriodTaskCases(t *testing.T) {
	runSubscriptionSessionPeriodCases(t)
	for _, scenario := range []string{"late_submit", "late_terminal_refund", "late_terminal_positive", "unknown_legacy_task", "settled_source_pending_task"} {
		t.Run("subscription_period_"+scenario, func(t *testing.T) {
			user, token, channel := seedReceiptActors(t, 0, 0)
			now := model.GetDBTimestamp()
			plan := &model.SubscriptionPlan{Title: "task period", TotalAmount: 1000, QuotaResetPeriod: model.SubscriptionResetNever}
			require.NoError(t, model.DB.Session(&gorm.Session{SkipHooks: true}).Create(plan).Error)
			model.InvalidateSubscriptionPlanCache(plan.Id)
			t.Cleanup(func() {
				model.InvalidateSubscriptionPlanCache(plan.Id)
				require.NoError(t, model.DB.Delete(plan).Error)
				require.NoError(t, model.DB.Where("user_id = ?", user.Id).Delete(&model.SubscriptionPreConsumeRecord{}).Error)
			})
			sub := &model.UserSubscription{UserId: user.Id, PlanId: plan.Id, AmountTotal: 1000, StartTime: now - 100, EndTime: now + 3600, Status: "active"}
			require.NoError(t, model.DB.Session(&gorm.Session{SkipHooks: true}).Create(sub).Error)
			requestID := fmt.Sprintf("period-task-%d", user.Id)
			receipt, err := model.PreConsumeUserSubscription(requestID, user.Id, "test-model", 0, 200)
			require.NoError(t, err)
			require.NoError(t, model.DB.Model(token).Updates(map[string]any{"remain_quota": 1800, "used_quota": 200}).Error)
			task := makeTask(user.Id, channel.Id, 200, token.Id, BillingSourceSubscription, sub.Id)
			task.TaskID = requestID
			task.PrivateData.RequestId = requestID
			task.PrivateData.SubscriptionQuotaPeriod = receipt.QuotaPeriod
			task.SettlementPending = true
			task.PrivateData.PendingSettlement = &model.TaskSettlement{Phase: "submit", Quota: 100}
			expectedTokenRemain, expectedTokenUsed := 1900, 100
			expectedUsed, expectedTaskQuota := int64(100), 100
			shouldFail := false
			switch scenario {
			case "late_terminal_refund", "late_terminal_positive", "unknown_legacy_task":
				require.NoError(t, model.SettleSubscriptionPreConsume(requestID, sub.Id, 200))
				seedChargedAccounting(t, user.Id, channel.Id, token.Id, 200, 1)
				task.PrivateData.SubmissionAccounted = true
				task.PrivateData.PendingSettlement = &model.TaskSettlement{Phase: "terminal", Quota: 0}
				expectedTokenRemain, expectedTokenUsed = 2000, 0
				expectedTaskQuota = 0
				if scenario == "late_terminal_positive" {
					task.PrivateData.PendingSettlement.Quota = 300
					expectedTokenRemain, expectedTokenUsed = 1800, 200
					expectedTaskQuota = 200
					shouldFail = true
				}
				if scenario == "unknown_legacy_task" {
					task.PrivateData.SubscriptionQuotaPeriod = 0
					expectedTokenRemain, expectedTokenUsed = 1800, 200
					expectedTaskQuota = 200
					shouldFail = true
				}
			case "settled_source_pending_task":
				require.NoError(t, model.SettleSubscriptionPreConsume(requestID, sub.Id, 100))
				expectedTokenRemain, expectedTokenUsed = 1800, 200
				expectedTaskQuota = 200
				shouldFail = true
			}
			_, err = model.AdminResetUserSubscriptionsByPlan(user.Id, plan.Id, false)
			require.NoError(t, err)
			_, err = model.PreConsumeUserSubscription(requestID+"-new", user.Id, "test-model", 0, 100)
			require.NoError(t, err)
			require.NoError(t, model.DB.Create(task).Error)
			var persisted model.Task
			require.NoError(t, model.DB.First(&persisted, task.ID).Error)
			assert.Equal(t, task.PrivateData.SubscriptionQuotaPeriod, persisted.PrivateData.SubscriptionQuotaPeriod)
			err = RecoverTaskSettlement(t.Context(), &persisted)
			if shouldFail {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.NoError(t, RecoverTaskSettlement(t.Context(), &persisted))
			}
			require.NoError(t, model.DB.First(sub, sub.Id).Error)
			assert.Equal(t, expectedUsed, sub.AmountUsed, "old task cannot erase or consume the new allowance")
			assert.Equal(t, expectedTokenRemain, getTokenRemainQuota(t, token.Id))
			assert.Equal(t, expectedTokenUsed, getTokenUsedQuota(t, token.Id))
			require.NoError(t, model.DB.First(&persisted, task.ID).Error)
			assert.Equal(t, shouldFail, persisted.SettlementPending)
			assert.Equal(t, expectedTaskQuota, persisted.Quota)
			if scenario == "settled_source_pending_task" {
				used, count := getUserUsageAccounting(t, user.Id)
				assert.Zero(t, used)
				assert.Zero(t, count)
				assert.Zero(t, getChannelUsedQuota(t, channel.Id))
			}
		})
	}
}

// Exercise the actual factory/session boundary, not a hand-built task whose
// allowance period is prefilled by the test. The controller persists these
// RelayInfo fields before transferring the reservation to the durable task.
func runSubscriptionSessionPeriodCases(t *testing.T) {
	for _, reset := range []bool{false, true} {
		name := "subscription_period_live_session_refund"
		if reset {
			name = "subscription_period_live_session_reset_refund"
		}
		t.Run(name, func(t *testing.T) {
			user, token, channel := seedReceiptActors(t, 0, 0)
			now := model.GetDBTimestamp()
			plan := &model.SubscriptionPlan{Title: "live session period", TotalAmount: 1000, QuotaResetPeriod: model.SubscriptionResetNever}
			require.NoError(t, model.DB.Session(&gorm.Session{SkipHooks: true}).Create(plan).Error)
			model.InvalidateSubscriptionPlanCache(plan.Id)
			t.Cleanup(func() {
				model.InvalidateSubscriptionPlanCache(plan.Id)
				require.NoError(t, model.DB.Delete(plan).Error)
				require.NoError(t, model.DB.Where("user_id = ?", user.Id).Delete(&model.SubscriptionPreConsumeRecord{}).Error)
			})
			sub := &model.UserSubscription{UserId: user.Id, PlanId: plan.Id, AmountTotal: 1000, StartTime: now - 100, EndTime: now + 3600, Status: "active"}
			require.NoError(t, model.DB.Session(&gorm.Session{SkipHooks: true}).Create(sub).Error)
			c := testGinContext(t)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
			info := &relaycommon.RelayInfo{
				UserId: user.Id, TokenId: token.Id, TokenKey: token.Key,
				RequestId: fmt.Sprintf("live-period-task-%d", user.Id), OriginModelName: "test-model",
				ForcePreConsume: true, TaskRelayInfo: &relaycommon.TaskRelayInfo{},
				ChannelMeta: &relaycommon.ChannelMeta{ChannelId: channel.Id},
				UsingGroup:  "default", UserGroup: "default",
			}
			info.UserSetting.BillingPreference = "subscription_only"
			require.Nil(t, PreConsumeBilling(c, 200, info))
			var source model.SubscriptionPreConsumeRecord
			require.NoError(t, model.DB.Where("request_id = ?", info.RequestId).First(&source).Error)
			require.Positive(t, source.QuotaPeriod)
			assert.Equal(t, source.QuotaPeriod, info.SubscriptionQuotaPeriod)
			require.NoError(t, info.Billing.Reserve(300))
			assert.Equal(t, source.QuotaPeriod, info.SubscriptionQuotaPeriod, "top-up retains the original receipt period")
			expectedUsed := int64(0)
			if reset {
				_, err := model.AdminResetUserSubscriptionsByPlan(user.Id, plan.Id, false)
				require.NoError(t, err)
				fresh, err := model.PreConsumeUserSubscription(info.RequestId+"-new", user.Id, "test-model", 0, 100)
				require.NoError(t, err)
				require.Greater(t, fresh.QuotaPeriod, source.QuotaPeriod)
				expectedUsed = 100
			}
			task := model.InitTask("", info)
			// This is the controller's metadata handoff, sourced only from the
			// live session. Never substitute source.QuotaPeriod or a literal.
			task.PrivateData.BillingSource = info.BillingSource
			task.PrivateData.SubscriptionId = info.SubscriptionId
			task.PrivateData.SubscriptionQuotaPeriod = info.SubscriptionQuotaPeriod
			task.PrivateData.TokenId = info.TokenId
			PrepareSubmittedTaskBilling(info, task, 250)
			require.NoError(t, task.Insert())
			var persisted model.Task
			require.NoError(t, model.DB.First(&persisted, task.ID).Error)
			assert.Equal(t, source.QuotaPeriod, persisted.PrivateData.SubscriptionQuotaPeriod)
			require.NoError(t, RecoverTaskSettlement(t.Context(), &persisted))
			require.NoError(t, RecoverTaskSettlement(t.Context(), &persisted))
			assert.Equal(t, 1750, getTokenRemainQuota(t, token.Id))
			assert.Equal(t, 250, getTokenUsedQuota(t, token.Id))
			// Persist a terminal intent, discard the session, then recover using
			// only the saved task. Replay must not credit token or source twice.
			persisted.Status = model.TaskStatusFailure
			persisted.SettlementPending = true
			persisted.PrivateData.PendingSettlement = &model.TaskSettlement{Phase: "terminal", Quota: 0}
			require.NoError(t, model.DB.Save(&persisted).Error)
			persisted = model.Task{}
			require.NoError(t, model.DB.First(&persisted, task.ID).Error)
			require.NoError(t, RecoverTaskSettlement(t.Context(), &persisted))
			require.NoError(t, RecoverTaskSettlement(t.Context(), &persisted))
			assert.Equal(t, expectedUsed, getSubscriptionUsed(t, sub.Id))
			assert.Equal(t, 2000, getTokenRemainQuota(t, token.Id))
			assert.Zero(t, getTokenUsedQuota(t, token.Id))
			assert.Zero(t, persisted.Quota)
			assert.False(t, persisted.SettlementPending)
			used, count := getUserUsageAccounting(t, user.Id)
			assert.Zero(t, used)
			assert.Equal(t, 1, count)
			assert.Zero(t, getChannelUsedQuota(t, channel.Id))
		})
	}
	t.Run("subscription_period_wallet_session_clears_period", func(t *testing.T) {
		user, token, _ := seedReceiptActors(t, 0, 0)
		info := &relaycommon.RelayInfo{
			UserId: user.Id, TokenId: token.Id, TokenKey: token.Key,
			RequestId: fmt.Sprintf("wallet-clear-period-%d", user.Id), ForcePreConsume: true,
			SubscriptionId: 99, SubscriptionQuotaPeriod: 42,
		}
		info.UserSetting.BillingPreference = "wallet_only"
		require.Nil(t, PreConsumeBilling(testGinContext(t), 200, info))
		assert.Equal(t, BillingSourceWallet, info.BillingSource)
		assert.Zero(t, info.SubscriptionId)
		assert.Zero(t, info.SubscriptionQuotaPeriod, "wallet fallback must not leak an old subscription period")
		require.NoError(t, info.Billing.Settle(0))
		assert.Equal(t, 2000, getTokenRemainQuota(t, token.Id))
		assertUserBalances(t, user.Id, 0, 1000, 0)
	})
}
