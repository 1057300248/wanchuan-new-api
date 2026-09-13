package service

import (
	"fmt"
	"strconv"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// 推荐返利：每位受邀用户前三次成功充值各 5%，第四次及以后不再生成
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

// seedPendingTopUp 创建一笔待支付的在线充值订单。
func seedPendingTopUp(t *testing.T, userId int, amount int64, tradeNo string) {
	t.Helper()
	topUp := &model.TopUp{
		UserId:          userId,
		Amount:          amount,
		Money:           float64(amount),
		TradeNo:         tradeNo,
		PaymentProvider: model.PaymentProviderEpay,
		CreateTime:      common.GetTimestamp(),
		Status:          common.TopUpStatusPending,
	}
	require.NoError(t, model.DB.Create(topUp).Error)
}

// settleTopUp 走真实易支付回调完成充值。
func settleTopUp(t *testing.T, tradeNo string, amount int64) {
	t.Helper()
	alreadyDone, err := model.RechargeEpay(tradeNo, "alipay", strconv.FormatFloat(float64(amount), 'f', 2, 64), "127.0.0.1")
	require.NoError(t, err)
	require.False(t, alreadyDone)
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

func TestReferralRewardFirstThreeTopUpsGetFivePercent(t *testing.T) {
	truncate(t)
	seedAffiliateRewardConfig(t, true, 0.05)
	seedAffiliateUser(t, 601, 0) // inviter
	seedAffiliateUser(t, 602, 601)

	tradeNos := []string{"REF-TOPUP-1", "REF-TOPUP-2", "REF-TOPUP-3", "REF-TOPUP-4"}
	for _, tradeNo := range tradeNos {
		seedPendingTopUp(t, 602, 2, tradeNo)
		settleTopUp(t, tradeNo, 2)
	}

	var rewards []model.AffiliateReward
	require.NoError(t, model.DB.Where("user_id = ? AND source_user_id = ?", 601, 602).Order("id asc").Find(&rewards).Error)
	require.Len(t, rewards, 3, "只有前三次充值产生返利")
	for i, reward := range rewards {
		assert.Equal(t, 1000000, reward.EligiblePaidQuota)
		assert.Equal(t, 50000, reward.RewardQuota, "第 %d 次充值返利应为 5%%", i+1)
		assert.Equal(t, tradeNos[i], reward.ReferenceId)
		assert.Equal(t, model.AffiliateReferenceTopUp, reward.ReferenceType)
		assert.Equal(t, string(model.AffiliateRewardPending), reward.Status)
	}

	var fourth int64
	require.NoError(t, model.DB.Model(&model.AffiliateReward{}).
		Where("user_id = ? AND reference_id = ?", 601, tradeNos[3]).Count(&fourth).Error)
	assert.Zero(t, fourth, "第四次充值不得生成返利记录")

	// pending 奖励不解锁前不产生余额变动。
	assert.Zero(t, getUserBalances(t, 601).QuotaBonus)
	assertQuotaInvariant(t, getUserBalances(t, 601))
}

func TestReferralRewardRepeatedTopUpCallbackIsIdempotent(t *testing.T) {
	truncate(t)
	seedAffiliateRewardConfig(t, true, 0.05)
	seedAffiliateUser(t, 603, 0)
	seedAffiliateUser(t, 604, 603)

	seedPendingTopUp(t, 604, 2, "REF-REPEAT-1")
	settleTopUp(t, "REF-REPEAT-1", 2)

	alreadyDone, err := model.RechargeEpay("REF-REPEAT-1", "alipay", "2.00", "127.0.0.1")
	require.NoError(t, err)
	assert.True(t, alreadyDone, "重复回调应是幂等的已完成状态")
	assert.Equal(t, int64(1), countAffiliateRewards(t, 603))
}

func TestReferralRewardConcurrentTopUpsStillCapAtThree(t *testing.T) {
	truncate(t)
	seedAffiliateRewardConfig(t, true, 0.05)
	seedAffiliateUser(t, 605, 0)
	seedAffiliateUser(t, 606, 605)

	const events = 5
	for i := range events {
		seedPendingTopUp(t, 606, 1, fmt.Sprintf("REF-CONCURRENT-%d", i))
	}

	var wg sync.WaitGroup
	errs := make([]error, events)
	for i := range events {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			_, errs[index] = model.RechargeEpay(
				fmt.Sprintf("REF-CONCURRENT-%d", index), "alipay", "1.00", "127.0.0.1")
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		require.NoError(t, err, "并发充值 %d 失败", i)
	}

	assert.Equal(t, int64(3), countAffiliateRewards(t, 605), "并发下第四次及以后不得生成返利")
}

func TestReferralRewardRedemptionCountsTowardTopUpCap(t *testing.T) {
	truncate(t)
	seedAffiliateRewardConfig(t, true, 0.05)
	seedAffiliateUser(t, 607, 0)
	seedAffiliateUser(t, 608, 607)

	for i := range 2 {
		tradeNo := fmt.Sprintf("REF-REDEEM-TOPUP-%d", i)
		seedPendingTopUp(t, 608, 2, tradeNo)
		settleTopUp(t, tradeNo, 2)
	}

	first := &model.Redemption{Key: "ref-redeem-1", Status: common.RedemptionCodeStatusEnabled, Quota: 400000}
	require.NoError(t, model.DB.Create(first).Error)
	quota, err := model.Redeem(first.Key, 608)
	require.NoError(t, err)
	assert.Equal(t, 400000, quota)

	second := &model.Redemption{Key: "ref-redeem-2", Status: common.RedemptionCodeStatusEnabled, Quota: 400000}
	require.NoError(t, model.DB.Create(second).Error)
	_, err = model.Redeem(second.Key, 608)
	require.NoError(t, err)

	require.Equal(t, int64(3), countAffiliateRewards(t, 607), "兑换码计入充值次数，第四次不再生成")

	var redemptionReward model.AffiliateReward
	require.NoError(t, model.DB.Where("user_id = ? AND reference_type = ?", 607, model.AffiliateReferenceRedemption).
		First(&redemptionReward).Error)
	assert.Equal(t, 400000, redemptionReward.EligiblePaidQuota)
	assert.Equal(t, 20000, redemptionReward.RewardQuota)

	var fourth int64
	require.NoError(t, model.DB.Model(&model.AffiliateReward{}).
		Where("user_id = ? AND reference_id = ?", 607, strconv.Itoa(second.Id)).Count(&fourth).Error)
	assert.Zero(t, fourth)
}

func TestReferralRewardSubscriptionAndBalancePurchaseCounting(t *testing.T) {
	truncate(t)
	seedAffiliateRewardConfig(t, true, 0.05)
	seedAffiliateUser(t, 609, 0)
	seedAffiliateUser(t, 610, 609)

	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", 610).
		Updates(map[string]any{"quota": 5000000, "quota_paid": 5000000}).Error)

	plan := &model.SubscriptionPlan{
		Title:         "Pro",
		PriceAmount:   10,
		Currency:      "USD",
		DurationUnit:  model.SubscriptionDurationMonth,
		DurationValue: 1,
		Enabled:       true,
		TotalAmount:   1000000,
	}
	require.NoError(t, model.DB.Create(plan).Error)

	// 余额购买订阅不是充值事件：不计次、不生成返利。
	require.NoError(t, model.PurchaseSubscriptionWithBalance(610, plan.Id))
	assert.Zero(t, countAffiliateRewards(t, 609))

	// 外部订阅支付各计一次，返利基数为实付金额折算的额度。
	for i := range 3 {
		tradeNo := fmt.Sprintf("REF-SUB-%d", i)
		order := &model.SubscriptionOrder{
			UserId:          610,
			PlanId:          plan.Id,
			Money:           plan.PriceAmount,
			TradeNo:         tradeNo,
			PaymentProvider: model.PaymentProviderEpay,
			CreateTime:      common.GetTimestamp(),
			Status:          common.TopUpStatusPending,
		}
		require.NoError(t, model.DB.Create(order).Error)
		require.NoError(t, model.CompleteSubscriptionOrder(tradeNo, "", model.PaymentProviderEpay, ""))
	}

	require.Equal(t, int64(3), countAffiliateRewards(t, 609))
	var rewards []model.AffiliateReward
	require.NoError(t, model.DB.Where("user_id = ?", 609).Find(&rewards).Error)
	for _, reward := range rewards {
		assert.Equal(t, model.AffiliateReferenceSubscriptionOrder, reward.ReferenceType)
		assert.Equal(t, 5000000, reward.EligiblePaidQuota)
		assert.Equal(t, 250000, reward.RewardQuota)
	}
}

func TestMatureAffiliateRewardsCreditsDueTopUpRewardOnce(t *testing.T) {
	truncate(t)
	seedAffiliateRewardConfig(t, true, 0.05)
	seedAffiliateUser(t, 611, 0)
	seedAffiliateUser(t, 612, 611)

	seedPendingTopUp(t, 612, 2, "REF-MATURE-1")
	settleTopUp(t, "REF-MATURE-1", 2)
	require.NoError(t, model.DB.Model(&model.AffiliateReward{}).
		Where("user_id = ?", 611).
		Update("unlock_at", common.GetTimestamp()-1).Error)

	matured, err := model.MatureAffiliateRewards(611)
	require.NoError(t, err)
	assert.Equal(t, 1, matured)

	user := getUserBalances(t, 611)
	assert.Equal(t, 50000, user.QuotaBonus)
	assert.Equal(t, 50000, user.Quota)
	assertQuotaInvariant(t, user)

	matured, err = model.MatureAffiliateRewards(611)
	require.NoError(t, err)
	assert.Zero(t, matured, "重复成熟不得再次入账")
	assert.Equal(t, 50000, getUserBalances(t, 611).QuotaBonus)
}

func TestMatureAffiliateRewardsCancelsMissingTopUp(t *testing.T) {
	truncate(t)
	seedAffiliateRewardConfig(t, true, 0.05)
	seedAffiliateUser(t, 613, 0)
	seedAffiliateUser(t, 614, 613)

	now := common.GetTimestamp()
	reward := &model.AffiliateReward{
		UserId: 613, SourceUserId: 614, EligiblePaidQuota: 1000000, RewardQuota: 50000,
		Status: string(model.AffiliateRewardPending), UnlockAt: now - 1,
		ReferenceType: model.AffiliateReferenceTopUp, ReferenceId: "REF-MISSING-TOPUP",
		IdempotencyKey: "affiliate_reward:topup:REF-MISSING-TOPUP", CreatedAt: now,
	}
	require.NoError(t, model.CreateAffiliateReward(reward))

	matured, err := model.MatureAffiliateRewards(613)
	require.NoError(t, err)
	assert.Zero(t, matured)
	assert.Zero(t, getUserBalances(t, 613).QuotaBonus)

	var row model.AffiliateReward
	require.NoError(t, model.DB.First(&row, reward.Id).Error)
	assert.Equal(t, string(model.AffiliateRewardCancelled), row.Status)
	assert.Equal(t, "topup_missing", row.CancelReason)
}

func TestMatureAffiliateRewardsCancelsDisabledSource(t *testing.T) {
	truncate(t)
	seedAffiliateRewardConfig(t, true, 0.05)
	seedAffiliateUser(t, 652, 0)
	seedAffiliateUser(t, 653, 652)
	seedPendingTopUp(t, 653, 1, "REF-DISABLED-SOURCE")
	settleTopUp(t, "REF-DISABLED-SOURCE", 1)

	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", 653).
		Update("status", common.UserStatusDisabled).Error)
	require.NoError(t, model.DB.Model(&model.AffiliateReward{}).
		Where("user_id = ?", 652).
		Update("unlock_at", common.GetTimestamp()-1).Error)

	matured, err := model.MatureAffiliateRewards(652)
	require.NoError(t, err)
	assert.Zero(t, matured)
	assert.Zero(t, getUserBalances(t, 652).QuotaBonus)

	var row model.AffiliateReward
	require.NoError(t, model.DB.Where("user_id = ?", 652).First(&row).Error)
	assert.Equal(t, string(model.AffiliateRewardCancelled), row.Status)
	assert.Equal(t, "source_disabled", row.CancelReason)
}

// 旧历史（relay/task 消费引用）仍需按成本复核成熟：部分退款后按比例调整。
func TestMatureAffiliateRewardsLegacyRelayRecalculatesAfterPartialRefund(t *testing.T) {
	truncate(t)
	seedAffiliateRewardConfig(t, true, 0.05)
	seedAffiliateUser(t, 650, 0)
	seedAffiliatePaidCost(t, 651, 650, 1000, "REQ-partial-refund")

	now := common.GetTimestamp()
	reward := &model.AffiliateReward{
		UserId: 650, SourceUserId: 651, EligiblePaidQuota: 1000, RewardQuota: 50,
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

	matured, err := model.MatureAffiliateRewards(650)
	require.NoError(t, err)
	assert.Equal(t, 1, matured)

	user := getUserBalances(t, 650)
	assert.Equal(t, 25, user.QuotaBonus)
	var row model.AffiliateReward
	require.NoError(t, model.DB.First(&row, reward.Id).Error)
	assert.Equal(t, 500, row.EligiblePaidQuota)
	assert.Equal(t, 25, row.RewardQuota)
	assert.Equal(t, string(model.AffiliateRewardCredited), row.Status)
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

	other := model.NewLogOther()
	paidNet := FillWalletCostLogInfo(other, user, [][2]string{{"relay", "wallet-cost-REQ-1"}})
	otherValues := other.Snapshot()
	assert.Equal(t, 18, paidNet)
	assert.Equal(t, 60, otherValues["wallet_quota_deducted"])
	walletCost, ok := otherValues["wallet_cost"].(map[string]int)
	require.True(t, ok)
	assert.Equal(t, 18, walletCost["paid"])
	assert.Equal(t, 42, walletCost["bonus"])

	// 订阅/无引用：不写入 wallet_cost，不伪造钱包成本
	other = model.NewLogOther()
	paidNet = FillWalletCostLogInfo(other, user, nil)
	assert.Zero(t, paidNet)
	_, hasCost := other.Snapshot()["wallet_cost"]
	assert.False(t, hasCost)
}

func TestAffiliateOverviewAggregates(t *testing.T) {
	truncate(t)
	seedAffiliateRewardConfig(t, true, 0.05)
	seedAffiliateUser(t, 617, 0)

	now := common.GetTimestamp()
	for i := range 3 {
		reward := &model.AffiliateReward{
			UserId: 617, SourceUserId: 618, EligiblePaidQuota: 1000000, RewardQuota: 50000,
			Status: string(model.AffiliateRewardPending), UnlockAt: now + 86400,
			ReferenceType: model.AffiliateReferenceTopUp, ReferenceId: fmt.Sprintf("REF-p%d", i),
			IdempotencyKey: fmt.Sprintf("affiliate_reward:topup:REF-p%d", i), CreatedAt: now,
		}
		require.NoError(t, model.CreateAffiliateReward(reward))
	}

	overview, err := model.GetAffiliateOverview(617)
	require.NoError(t, err)
	assert.Equal(t, 3, overview.PendingCount)
	assert.Equal(t, 3000000, overview.EligiblePaidQuota)
	assert.Equal(t, 150000, overview.PendingBonusQuota)
	assert.Zero(t, overview.CreditedBonusQuota)
	assert.Equal(t, 150000, overview.TotalBonusQuota)
}
