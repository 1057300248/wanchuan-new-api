package service

import (
	"fmt"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// 推荐返利：创建、幂等、成熟
// ---------------------------------------------------------------------------

func seedAffiliateUser(t *testing.T, id int, inviterId int) {
	t.Helper()
	user := &model.User{
		Id:        id,
		Username:  fmt.Sprintf("affiliate-user-%d", id),
		AffCode:   fmt.Sprintf("affcode-%d", id),
		InviterId: inviterId,
		Quota:     0,
		Status:    common.UserStatusEnabled,
	}
	require.NoError(t, model.DB.Create(user).Error)
}

func seedAffiliateRewardConfig(t *testing.T, enabled bool, rate float64) {
	t.Helper()
	previousEnabled := common.AffiliateRewardEnabled
	previousRate := common.AffiliateRewardRate
	previousFreeze := common.AffiliateRewardFreezeDays
	common.AffiliateRewardEnabled = enabled
	common.AffiliateRewardRate = rate
	common.AffiliateRewardFreezeDays = 7
	t.Cleanup(func() {
		common.AffiliateRewardEnabled = previousEnabled
		common.AffiliateRewardRate = previousRate
		common.AffiliateRewardFreezeDays = previousFreeze
	})
}

func countAffiliateRewards(t *testing.T, inviterId int) int64 {
	t.Helper()
	var count int64
	require.NoError(t, model.DB.Model(&model.AffiliateReward{}).Where("user_id = ?", inviterId).Count(&count).Error)
	return count
}

func seedAffiliatePaidCost(t *testing.T, sourceUserId, inviterId, quota int, referenceId string) {
	t.Helper()
	seedAffiliateUser(t, sourceUserId, inviterId)
	_, err := model.CreditQuota(model.LedgerEntry{
		UserId:         sourceUserId,
		Account:        model.AccountPaid,
		Delta:          quota,
		Reason:         model.LedgerReasonSystem,
		ReferenceType:  "test_seed",
		ReferenceId:    referenceId,
		IdempotencyKey: fmt.Sprintf("test:affiliate:credit:%d:%s", sourceUserId, referenceId),
	})
	require.NoError(t, err)
	_, err = model.DebitQuota(model.LedgerEntry{
		UserId:         sourceUserId,
		Delta:          quota,
		Reason:         model.LedgerReasonBillingPreConsume,
		ReferenceType:  "relay",
		ReferenceId:    referenceId,
		IdempotencyKey: fmt.Sprintf("test:affiliate:debit:%d:%s", sourceUserId, referenceId),
	}, model.AccountPaid)
	require.NoError(t, err)
}

func TestRecordAffiliateRewardPaidConsumptionCreatesReward(t *testing.T) {
	truncate(t)
	seedAffiliateRewardConfig(t, true, 0.2)
	seedAffiliateUser(t, 601, 0) // inviter
	seedAffiliateUser(t, 602, 601)

	created, err := RecordAffiliateRewardForRequest(602, 1000, "relay", "REQ-paid")
	require.NoError(t, err)
	require.True(t, created)

	var reward model.AffiliateReward
	require.NoError(t, model.DB.Where("user_id = ? AND source_user_id = ?", 601, 602).First(&reward).Error)
	assert.Equal(t, 1000, reward.EligiblePaidQuota)
	assert.Equal(t, 200, reward.RewardQuota) // 20%
	assert.Equal(t, string(model.AffiliateRewardPending), reward.Status)
	assert.Equal(t, "REQ-paid", reward.ReferenceId)
	assert.Greater(t, reward.UnlockAt, common.GetTimestamp())
}

func TestRecordAffiliateRewardExcludesDisabledAndUninvitedUsers(t *testing.T) {
	truncate(t)
	seedAffiliateRewardConfig(t, true, 0.2)
	seedAffiliateUser(t, 603, 0)

	// 源用户无邀请人：不产生返利
	seedAffiliateUser(t, 604, 0)
	created, err := RecordAffiliateRewardForRequest(604, 1000, "relay", "REQ-no-inviter")
	require.NoError(t, err)
	require.False(t, created)

	// 禁用源用户：不产生返利
	seedAffiliateUser(t, 605, 603)
	disabled := getUserBalances(t, 605)
	disabled.Status = common.UserStatusDisabled
	require.NoError(t, model.DB.Save(&disabled).Error)
	created, err = RecordAffiliateRewardForRequest(605, 1000, "relay", "REQ-disabled-source")
	require.NoError(t, err)
	require.False(t, created)

	// 禁用邀请人：不产生返利
	seedAffiliateUser(t, 606, 603)
	inviter := getUserBalances(t, 603)
	inviter.Status = common.UserStatusDisabled
	require.NoError(t, model.DB.Save(&inviter).Error)
	created, err = RecordAffiliateRewardForRequest(606, 1000, "relay", "REQ-disabled-inviter")
	require.NoError(t, err)
	require.False(t, created)

	assert.Zero(t, countAffiliateRewards(t, 603))
}

func TestRecordAffiliateRewardDisabledSwitchAndZeroPaid(t *testing.T) {
	truncate(t)
	seedAffiliateRewardConfig(t, false, 0.2)
	seedAffiliateUser(t, 607, 0)
	seedAffiliateUser(t, 608, 607)

	// 功能开关关闭：不产生返利
	created, err := RecordAffiliateRewardForRequest(608, 1000, "relay", "REQ-disabled")
	require.NoError(t, err)
	require.False(t, created)
	assert.Zero(t, countAffiliateRewards(t, 607))

	// paid 净消费为 0（纯 bonus 消费 / 已全额退款）：不产生返利
	seedAffiliateRewardConfig(t, true, 0.2)
	created, err = RecordAffiliateRewardForRequest(608, 0, "relay", "REQ-zero-paid")
	require.NoError(t, err)
	require.False(t, created)
	assert.Zero(t, countAffiliateRewards(t, 607))
}

func TestRecordAffiliateRewardConcurrentIdempotent(t *testing.T) {
	truncate(t)
	seedAffiliateRewardConfig(t, true, 0.2)
	seedAffiliateUser(t, 609, 0)
	seedAffiliateUser(t, 610, 609)

	const workers = 8
	var wg sync.WaitGroup
	errs := make([]error, workers)
	createdFlags := make([]bool, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			createdFlags[index], errs[index] = RecordAffiliateRewardForRequest(610, 1000, "relay", "REQ-concurrent")
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}

	// 同一源请求只创建一条奖励。
	assert.Equal(t, int64(1), countAffiliateRewards(t, 609))
	successful := 0
	for _, created := range createdFlags {
		if created {
			successful++
		}
	}
	assert.Equal(t, 1, successful, "exactly one goroutine should create the reward")
}

func TestMatureAffiliateRewardsSevenDayFreeze(t *testing.T) {
	truncate(t)
	seedAffiliateRewardConfig(t, true, 0.2)
	seedAffiliateUser(t, 611, 0)
	seedAffiliatePaidCost(t, 612, 611, 1000, "REQ-future")
	seedAffiliatePaidCost(t, 613, 611, 500, "REQ-due")

	now := common.GetTimestamp()
	// 未到期（未来解锁）：保持 pending，不转入余额
	future := &model.AffiliateReward{
		UserId: 611, SourceUserId: 612, EligiblePaidQuota: 1000, RewardQuota: 200,
		Status: string(model.AffiliateRewardPending), UnlockAt: now + 7*86400,
		ReferenceType: "relay", ReferenceId: "REQ-future",
		IdempotencyKey: "affiliate_reward:relay:REQ-future", CreatedAt: now,
	}
	require.NoError(t, model.CreateAffiliateReward(future))
	// 已到期：应成熟
	due := &model.AffiliateReward{
		UserId: 611, SourceUserId: 613, EligiblePaidQuota: 500, RewardQuota: 100,
		Status: string(model.AffiliateRewardPending), UnlockAt: now - 1,
		ReferenceType: "relay", ReferenceId: "REQ-due",
		IdempotencyKey: "affiliate_reward:relay:REQ-due", CreatedAt: now,
	}
	require.NoError(t, model.CreateAffiliateReward(due))

	matured, err := MatureAffiliateRewards(611)
	require.NoError(t, err)
	assert.Equal(t, 1, matured)

	user := getUserBalances(t, 611)
	assert.Equal(t, 100, user.QuotaAffiliate)
	assert.Equal(t, 100, user.Quota)
	assertQuotaInvariant(t, user)

	var dueRow model.AffiliateReward
	require.NoError(t, model.DB.Where("id = ?", due.Id).First(&dueRow).Error)
	assert.Equal(t, string(model.AffiliateRewardAvailable), dueRow.Status)
	assert.Equal(t, now, dueRow.UnlockedAt)

	var futureRow model.AffiliateReward
	require.NoError(t, model.DB.Where("id = ?", future.Id).First(&futureRow).Error)
	assert.Equal(t, string(model.AffiliateRewardPending), futureRow.Status)

	// 幂等：再次成熟不重复入账
	matured, err = MatureAffiliateRewards(611)
	require.NoError(t, err)
	assert.Zero(t, matured)
	user = getUserBalances(t, 611)
	assert.Equal(t, 100, user.QuotaAffiliate)
	assertQuotaInvariant(t, user)
}

func TestMatureAffiliateRewardsRecalculatesAfterPartialRefund(t *testing.T) {
	truncate(t)
	seedAffiliateRewardConfig(t, true, 0.2)
	seedAffiliateUser(t, 650, 0)
	seedAffiliatePaidCost(t, 651, 650, 1000, "REQ-partial-refund")

	now := common.GetTimestamp()
	reward := &model.AffiliateReward{
		UserId: 650, SourceUserId: 651, EligiblePaidQuota: 1000, RewardQuota: 200,
		Status: string(model.AffiliateRewardPending), UnlockAt: now - 1,
		ReferenceType: "relay", ReferenceId: "REQ-partial-refund",
		IdempotencyKey: "affiliate_reward:relay:REQ-partial-refund", CreatedAt: now,
	}
	require.NoError(t, model.CreateAffiliateReward(reward))
	_, err := model.CreditQuota(model.LedgerEntry{
		UserId:         651,
		Account:        model.AccountPaid,
		Delta:          500,
		Reason:         model.LedgerReasonBillingRefund,
		ReferenceType:  "relay",
		ReferenceId:    "REQ-partial-refund",
		IdempotencyKey: "test:affiliate:partial-refund",
	})
	require.NoError(t, err)

	matured, err := MatureAffiliateRewards(650)
	require.NoError(t, err)
	assert.Equal(t, 1, matured)

	user := getUserBalances(t, 650)
	assert.Equal(t, 100, user.QuotaAffiliate)
	var row model.AffiliateReward
	require.NoError(t, model.DB.First(&row, reward.Id).Error)
	assert.Equal(t, 500, row.EligiblePaidQuota)
	assert.Equal(t, 100, row.RewardQuota)
	assert.Equal(t, string(model.AffiliateRewardAvailable), row.Status)
}

func TestMatureAffiliateRewardsCancelsDisabledSource(t *testing.T) {
	truncate(t)
	seedAffiliateRewardConfig(t, true, 0.2)
	seedAffiliateUser(t, 652, 0)
	seedAffiliatePaidCost(t, 653, 652, 500, "REQ-disabled-at-maturity")

	now := common.GetTimestamp()
	reward := &model.AffiliateReward{
		UserId: 652, SourceUserId: 653, EligiblePaidQuota: 500, RewardQuota: 100,
		Status: string(model.AffiliateRewardPending), UnlockAt: now - 1,
		ReferenceType: "relay", ReferenceId: "REQ-disabled-at-maturity",
		IdempotencyKey: "affiliate_reward:relay:REQ-disabled-at-maturity", CreatedAt: now,
	}
	require.NoError(t, model.CreateAffiliateReward(reward))
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", 653).
		Update("status", common.UserStatusDisabled).Error)

	matured, err := MatureAffiliateRewards(652)
	require.NoError(t, err)
	assert.Zero(t, matured)
	assert.Zero(t, getUserBalances(t, 652).QuotaAffiliate)

	var row model.AffiliateReward
	require.NoError(t, model.DB.First(&row, reward.Id).Error)
	assert.Equal(t, string(model.AffiliateRewardCancelled), row.Status)
	assert.Equal(t, "source_disabled", row.CancelReason)
	assert.Equal(t, now, row.CancelledAt)
}

func TestAffiliateLedgerReconciliationCreatesDurableReward(t *testing.T) {
	truncate(t)
	seedAffiliateRewardConfig(t, true, 0.2)
	seedAffiliateUser(t, 654, 0)
	seedAffiliatePaidCost(t, 655, 654, 750, "REQ-reconcile")

	candidates, err := model.ListAffiliatePaidCostCandidates(common.GetTimestamp()+1, 10)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	reconcileAffiliatePaidCost(candidates[0])

	var reward model.AffiliateReward
	require.NoError(t, model.DB.Where(
		"source_user_id = ? AND reference_type = ? AND reference_id = ?",
		655, "relay", "REQ-reconcile",
	).First(&reward).Error)
	assert.Equal(t, 750, reward.EligiblePaidQuota)
	assert.Equal(t, 150, reward.RewardQuota)

	var state model.AffiliateRewardReconcileState
	require.NoError(t, model.DB.Where(
		"source_user_id = ? AND reference_type = ? AND reference_id = ?",
		655, "relay", "REQ-reconcile",
	).First(&state).Error)
	assert.Equal(t, model.AffiliateReconcileProcessed, state.Status)
	assert.Equal(t, "relay", state.CanonicalReferenceType)
	assert.Equal(t, "REQ-reconcile", state.CanonicalReferenceId)
	assert.Equal(t, 1, state.AttemptCount)

	remaining, err := model.ListAffiliatePaidCostCandidates(common.GetTimestamp()+1, 10)
	require.NoError(t, err)
	assert.Empty(t, remaining)
}

func TestFillWalletCostLogInfoNetSharesAfterRefund(t *testing.T) {
	truncate(t)
	// bonus=70, paid=30 的账户组合，预扣 100 后部分退款 40（按比例退回）。
	user := seedBonusUser(t, 614, 70, 30)
	funding := &WalletFunding{
		userId:     user,
		requestId:  "wallet-cost-REQ-1",
		debitOrder: []model.LedgerAccount{model.AccountBonus, model.AccountPaid},
	}
	require.NoError(t, funding.PreConsume(100))
	require.NoError(t, funding.Settle(-40))

	other := make(map[string]interface{})
	paidNet := FillWalletCostLogInfo(other, user, [][2]string{{"relay", "wallet-cost-REQ-1"}})
	assert.Equal(t, 18, paidNet)
	assert.Equal(t, 60, other["wallet_quota_deducted"])
	walletCost, ok := other["wallet_cost"].(map[string]int)
	require.True(t, ok)
	assert.Equal(t, 18, walletCost["paid"])
	assert.Equal(t, 42, walletCost["bonus"])

	// 订阅/无引用：不写入 wallet_cost，不伪造钱包成本
	other = make(map[string]interface{})
	paidNet = FillWalletCostLogInfo(other, user, nil)
	assert.Zero(t, paidNet)
	_, hasCost := other["wallet_cost"]
	assert.False(t, hasCost)
}

// ---------------------------------------------------------------------------
// 提现：最低额、幂等、冻结、审批/拒绝
// ---------------------------------------------------------------------------

func seedWithdrawalConfig(t *testing.T, minQuota int) {
	t.Helper()
	previousEnabled := common.AffiliateRewardEnabled
	previousMin := common.AffiliateMinWithdrawQuota
	common.AffiliateRewardEnabled = true
	common.AffiliateMinWithdrawQuota = minQuota
	t.Cleanup(func() {
		common.AffiliateRewardEnabled = previousEnabled
		common.AffiliateMinWithdrawQuota = previousMin
	})
}

func seedAffiliateBalance(t *testing.T, id int, affiliateQuota int) {
	t.Helper()
	user := &model.User{
		Id:             id,
		Username:       fmt.Sprintf("aff-balance-%d", id),
		AffCode:        fmt.Sprintf("affb-%d", id),
		Quota:          affiliateQuota,
		QuotaAffiliate: affiliateQuota,
		Status:         common.UserStatusEnabled,
	}
	require.NoError(t, model.DB.Create(user).Error)
}

func TestCreateAffiliateWithdrawalMinimumAndFreeze(t *testing.T) {
	truncate(t)
	seedWithdrawalConfig(t, 100)
	seedAffiliateBalance(t, 615, 500)

	// 功能开关关闭：不允许新提现
	previousEnabled := common.AffiliateRewardEnabled
	common.AffiliateRewardEnabled = false
	_, _, err := model.CreateAffiliateWithdrawal(615, 200, "WD-disabled")
	require.ErrorIs(t, err, model.ErrAffiliateWithdrawDisabled)
	common.AffiliateRewardEnabled = previousEnabled

	// 低于最低限额：拒绝
	_, _, err = model.CreateAffiliateWithdrawal(615, 99, "WD-min")
	require.ErrorIs(t, err, model.ErrAffiliateWithdrawTooSmall)

	// 余额不足：拒绝
	_, _, err = model.CreateAffiliateWithdrawal(615, 600, "WD-insufficient")
	require.ErrorIs(t, err, model.ErrLedgerInsufficientBalance)

	// 正常申请：冻结扣减并写账本
	withdrawal, replayed, err := model.CreateAffiliateWithdrawal(615, 200, "WD-1")
	require.NoError(t, err)
	require.False(t, replayed)
	assert.Equal(t, string(model.WithdrawalPending), withdrawal.Status)

	user := getUserBalances(t, 615)
	assert.Equal(t, 300, user.QuotaAffiliate)
	assert.Equal(t, 300, user.Quota)
	assertQuotaInvariant(t, user)

	var holdRows []model.BalanceLedger
	require.NoError(t, model.DB.Where("user_id = ? AND reason = ?", 615, model.LedgerReasonWithdrawalHold).Find(&holdRows).Error)
	require.Len(t, holdRows, 1)
	assert.Equal(t, -200, holdRows[0].Delta)
	assert.Equal(t, string(model.AccountAffiliate), holdRows[0].Account)

	// 同一 request_id 重试：幂等重放，不重复扣减
	_, replayed, err = model.CreateAffiliateWithdrawal(615, 200, "WD-1")
	require.NoError(t, err)
	require.True(t, replayed)
	user = getUserBalances(t, 615)
	assert.Equal(t, 300, user.QuotaAffiliate)

	// 同 request_id 不同参数：冲突
	_, _, err = model.CreateAffiliateWithdrawal(615, 300, "WD-1")
	require.ErrorIs(t, err, model.ErrAffiliateWithdrawConflict)
}

func TestWithdrawalApproveRejectPayFlow(t *testing.T) {
	truncate(t)
	seedWithdrawalConfig(t, 100)
	seedAffiliateBalance(t, 616, 1000)

	withdrawal, _, err := model.CreateAffiliateWithdrawal(616, 300, "WD-flow")
	require.NoError(t, err)

	// 拒绝 pending：原路退回 affiliate 余额并写账本
	require.NoError(t, model.RejectAffiliateWithdrawal(withdrawal.Id, 1, "资料不完整"))
	user := getUserBalances(t, 616)
	assert.Equal(t, 1000, user.QuotaAffiliate)
	assertQuotaInvariant(t, user)
	var refundRows []model.BalanceLedger
	require.NoError(t, model.DB.Where("user_id = ? AND reason = ?", 616, model.LedgerReasonWithdrawalRejectRefund).Find(&refundRows).Error)
	require.Len(t, refundRows, 1)
	assert.Equal(t, 300, refundRows[0].Delta)

	// 重复拒绝：幂等
	require.NoError(t, model.RejectAffiliateWithdrawal(withdrawal.Id, 1, "again"))
	user = getUserBalances(t, 616)
	assert.Equal(t, 1000, user.QuotaAffiliate)

	// 已拒绝的申请不允许审批
	require.Error(t, model.ApproveAffiliateWithdrawal(withdrawal.Id, 1))

	// 新申请 → 审批 → 支付（余额在申请时已冻结）
	withdrawal2, _, err := model.CreateAffiliateWithdrawal(616, 300, "WD-flow-2")
	require.NoError(t, err)
	require.NoError(t, model.ApproveAffiliateWithdrawal(withdrawal2.Id, 1))
	user = getUserBalances(t, 616)
	assert.Equal(t, 700, user.QuotaAffiliate)

	var approved model.AffiliateWithdrawal
	require.NoError(t, model.DB.Where("id = ?", withdrawal2.Id).First(&approved).Error)
	assert.Equal(t, string(model.WithdrawalApproved), approved.Status)
	assert.NotZero(t, approved.ApprovedAt)

	// 已审批申请不能拒绝（余额已冻结，需先走人工支付）
	require.Error(t, model.RejectAffiliateWithdrawal(withdrawal2.Id, 1, "late"))

	require.NoError(t, model.MarkAffiliateWithdrawalPaid(withdrawal2.Id, 1))
	var paid model.AffiliateWithdrawal
	require.NoError(t, model.DB.Where("id = ?", withdrawal2.Id).First(&paid).Error)
	assert.Equal(t, string(model.WithdrawalPaid), paid.Status)
	assert.NotZero(t, paid.PaidAt)

	// 重复支付：幂等（状态已不在 approved，no-op 不报错）
	require.NoError(t, model.MarkAffiliateWithdrawalPaid(withdrawal2.Id, 1))
}

func TestAffiliateOverviewAggregates(t *testing.T) {
	truncate(t)
	seedAffiliateRewardConfig(t, true, 0.2)
	seedAffiliateUser(t, 617, 0)

	now := common.GetTimestamp()
	for i := 0; i < 3; i++ {
		reward := &model.AffiliateReward{
			UserId: 617, SourceUserId: 618, EligiblePaidQuota: 1000, RewardQuota: 200,
			Status: string(model.AffiliateRewardPending), UnlockAt: now + 86400,
			ReferenceType: "relay", ReferenceId: fmt.Sprintf("REQ-p%d", i),
			IdempotencyKey: fmt.Sprintf("affiliate_reward:relay:REQ-p%d", i), CreatedAt: now,
		}
		require.NoError(t, model.CreateAffiliateReward(reward))
	}

	overview, err := model.GetAffiliateOverview(617)
	require.NoError(t, err)
	assert.Equal(t, 3, overview.PendingCount)
	assert.Equal(t, 600, overview.PendingQuota)
	assert.Zero(t, overview.AvailableQuota)
	assert.Zero(t, overview.WithdrawnQuota)
}
