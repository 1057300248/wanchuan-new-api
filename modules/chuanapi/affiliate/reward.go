package affiliate

import (
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/affiliateport"
)

// RewardService 承载推荐赠金的生命周期操作。新返利由充值结算事务在 model
// 层直接生成（前三次成功充值各按比例），本服务只负责幂等成熟到期奖励。
type RewardService struct{}

var _ affiliateport.RewardService = RewardService{}

func NewRewardService() affiliateport.RewardService { return RewardService{} }

func (RewardService) Mature(userID int) (int, error) {
	return model.MatureAffiliateRewards(userID)
}
