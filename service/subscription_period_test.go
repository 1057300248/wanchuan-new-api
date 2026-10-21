package service

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSubscriptionPeriodTaskBilling(t *testing.T) { runSubscriptionPeriodTaskCases(t) }

// Reused by the real three-engine task receipt matrix; no provider calls or
// polling timers are needed to establish the transaction/period invariant.
func runSubscriptionPeriodTaskCases(t *testing.T) {
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
