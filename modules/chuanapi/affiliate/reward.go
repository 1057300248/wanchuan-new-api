package affiliate

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/affiliateport"
)

type RewardService struct{}

var _ affiliateport.RewardService = RewardService{}

func NewRewardService() affiliateport.RewardService { return RewardService{} }

func (RewardService) Record(sourceUserID, paidQuota int, refType, refID string) (bool, error) {
	if !common.AffiliateRewardEnabled {
		return false, nil
	}
	if paidQuota <= 0 || sourceUserID <= 0 || refType == "" || refID == "" {
		return false, nil
	}
	if common.AffiliateRewardRate <= 0 || common.AffiliateRewardFreezeDays < 0 {
		return false, nil
	}

	source, err := model.GetUserById(sourceUserID, false)
	if err != nil || source.Status != common.UserStatusEnabled || source.InviterId <= 0 {
		return false, nil
	}
	inviter, err := model.GetUserById(source.InviterId, false)
	if err != nil || inviter.Status != common.UserStatusEnabled {
		return false, nil
	}

	rewardQuota, clamp := common.QuotaFromFloatChecked(float64(paidQuota) * common.AffiliateRewardRate)
	if clamp != nil {
		common.SysError(fmt.Sprintf("affiliate reward quota saturated: source=%d inviter=%d paid=%d rate=%g clamped=%d",
			sourceUserID, inviter.Id, paidQuota, common.AffiliateRewardRate, rewardQuota))
	}
	if rewardQuota <= 0 {
		return false, nil
	}

	reward := &model.AffiliateReward{
		UserId: inviter.Id, SourceUserId: sourceUserID, EligiblePaidQuota: paidQuota,
		RewardQuota: rewardQuota, Status: string(model.AffiliateRewardPending),
		UnlockAt:      common.GetTimestamp() + int64(common.AffiliateRewardFreezeDays)*86400,
		ReferenceType: refType, ReferenceId: refID,
		IdempotencyKey: fmt.Sprintf("affiliate_reward:%s:%s", refType, refID),
	}
	if err := model.CreateAffiliateReward(reward); err != nil {
		if err == model.ErrAffiliateRewardExists {
			return false, nil
		}
		common.SysError(fmt.Sprintf("create affiliate reward failed: source=%d inviter=%d ref=%s/%s err=%s",
			sourceUserID, inviter.Id, refType, refID, err.Error()))
		return false, err
	}
	common.SysLog(fmt.Sprintf("affiliate reward created: inviter=%d source=%d paid=%d reward=%d ref=%s/%s unlock_at=%d",
		inviter.Id, sourceUserID, paidQuota, rewardQuota, refType, refID, reward.UnlockAt))
	return true, nil
}

func (RewardService) Mature(userID int) (int, error) {
	return model.MatureAffiliateRewards(userID)
}
