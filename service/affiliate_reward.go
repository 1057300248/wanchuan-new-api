package service

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// ---------------------------------------------------------------------------
// 推荐返利（TokenMix P1-3）— 服务层规则
//
// 目标默认规则：符合条件的实际 paid 消费按 AffiliateRewardRate（20%）计算。
// bonus 消费、订阅消费、退款/失败请求、禁用或异常用户不产生返利。
// 每个源请求/消费只创建一次奖励（幂等键 = 引用），奖励先 frozen/pending，
// 7 天（AffiliateRewardFreezeDays）后经后台维护任务或查询时幂等补偿，进入
// inviter 的模型赠金（bonus）账户。
// ---------------------------------------------------------------------------

// RecordAffiliateRewardForRequest 为一次实际 paid 消费创建返利记录（幂等）。
// paidQuota 必须是该请求结算后的最终 paid 净份额（已含退款冲减）。
// 返回是否创建了记录（重复请求返回 false）。
func RecordAffiliateRewardForRequest(sourceUserId, paidQuota int, refType, refId string) (bool, error) {
	if !common.AffiliateRewardEnabled {
		return false, nil
	}
	if paidQuota <= 0 || sourceUserId <= 0 || refType == "" || refId == "" {
		return false, nil
	}
	if common.AffiliateRewardRate <= 0 || common.AffiliateRewardFreezeDays < 0 {
		return false, nil
	}

	// 源用户必须存在且未禁用，且存在邀请人。
	source, err := model.GetUserById(sourceUserId, false)
	if err != nil {
		return false, nil // 用户已删除：不产生返利
	}
	if source.Status != common.UserStatusEnabled || source.InviterId <= 0 {
		return false, nil
	}

	// 邀请人必须存在且未禁用。
	inviter, err := model.GetUserById(source.InviterId, false)
	if err != nil {
		return false, nil
	}
	if inviter.Status != common.UserStatusEnabled {
		return false, nil
	}

	rewardQuota, clamp := common.QuotaFromFloatChecked(float64(paidQuota) * common.AffiliateRewardRate)
	if clamp != nil {
		common.SysError(fmt.Sprintf("affiliate reward quota saturated: source=%d inviter=%d paid=%d rate=%g clamped=%d",
			sourceUserId, inviter.Id, paidQuota, common.AffiliateRewardRate, rewardQuota))
	}
	if rewardQuota <= 0 {
		return false, nil
	}

	reward := &model.AffiliateReward{
		UserId:            inviter.Id,
		SourceUserId:      sourceUserId,
		EligiblePaidQuota: paidQuota,
		RewardQuota:       rewardQuota,
		Status:            string(model.AffiliateRewardPending),
		UnlockAt:          common.GetTimestamp() + int64(common.AffiliateRewardFreezeDays)*86400,
		ReferenceType:     refType,
		ReferenceId:       refId,
		IdempotencyKey:    fmt.Sprintf("affiliate_reward:%s:%s", refType, refId),
	}
	err = model.CreateAffiliateReward(reward)
	if err != nil {
		if err == model.ErrAffiliateRewardExists {
			return false, nil
		}
		common.SysError(fmt.Sprintf("create affiliate reward failed: source=%d inviter=%d ref=%s/%s err=%s",
			sourceUserId, inviter.Id, refType, refId, err.Error()))
		return false, err
	}
	common.SysLog(fmt.Sprintf("affiliate reward created: inviter=%d source=%d paid=%d reward=%d ref=%s/%s unlock_at=%d",
		inviter.Id, sourceUserId, paidQuota, rewardQuota, refType, refId, reward.UnlockAt))
	return true, nil
}

func recordAffiliateRewardOrLog(sourceUserId, paidQuota int, refType, refId string) {
	if _, err := RecordAffiliateRewardForRequest(sourceUserId, paidQuota, refType, refId); err != nil {
		common.SysError(fmt.Sprintf("affiliate reward deferred to reconciliation: source=%d ref=%s/%s err=%s",
			sourceUserId, refType, refId, err.Error()))
	}
}

// MatureAffiliateRewards 幂等成熟用户所有到期返利并返回本次成熟条数。
// 供懒加载（钱包/推荐概览读取时）与显式 API 触发共用。
func MatureAffiliateRewards(userId int) (int, error) {
	return model.MatureAffiliateRewards(userId)
}

// ---------------------------------------------------------------------------
// 钱包成本记录（P1-2）— 日志 other 中反映实际 wallet account split
// ---------------------------------------------------------------------------

// FillWalletCostLogInfo 把该请求钱包账户的最终净份额写入日志 other：
//   - other["wallet_quota_deducted"] = 净消费总额（quota units）
//   - other["wallet_cost"] = {"paid": n, "bonus": m}（quota units）
//
// 净份额从 balance_ledger 汇总（预扣/追扣/结算/退款全部计入），退款后即反映
// 最终净份额。订阅消费调用方不会走到这里，不伪造钱包成本。
// 返回 paid 净消费额（供调用方生成返利）。
func FillWalletCostLogInfo(other map[string]interface{}, userId int, refs [][2]string) (paidNet int) {
	if other == nil || userId <= 0 || len(refs) == 0 {
		return 0
	}
	cost, err := model.NetCostByReferences(userId, refs)
	if err != nil {
		common.SysError(fmt.Sprintf("query wallet net cost failed: user=%d err=%s", userId, err.Error()))
		return 0
	}
	// 账本净变动为负（扣减）；取反得到各账户净消费。
	paidNet = -cost[model.AccountPaid]
	if paidNet < 0 {
		paidNet = 0
	}
	bonusNet := -cost[model.AccountBonus]
	if bonusNet < 0 {
		bonusNet = 0
	}
	total := paidNet + bonusNet
	other["wallet_quota_deducted"] = total
	if paidNet > 0 || bonusNet > 0 {
		walletCost := make(map[string]int)
		if paidNet > 0 {
			walletCost["paid"] = paidNet
		}
		if bonusNet > 0 {
			walletCost["bonus"] = bonusNet
		}
		other["wallet_cost"] = walletCost
	}
	return paidNet
}

// RelayWalletCostRefs 返回同步请求在账本中的引用（relay 引用）。
func RelayWalletCostRefs(relayInfo *relaycommon.RelayInfo) [][2]string {
	if relayInfo == nil || relayInfo.RequestId == "" {
		return nil
	}
	return [][2]string{{"relay", relayInfo.RequestId}}
}

// TaskWalletCostRefs 返回异步任务在账本中的引用（提交请求 relay 引用 + 任务 task 引用）。
func TaskWalletCostRefs(task *model.Task) [][2]string {
	if task == nil {
		return nil
	}
	var refs [][2]string
	if reqID := task.PrivateData.EffectiveRequestID(); reqID != "" {
		refs = append(refs, [2]string{"relay", reqID})
	}
	if task.TaskID != "" {
		refs = append(refs, [2]string{"task", task.TaskID})
	}
	return refs
}
