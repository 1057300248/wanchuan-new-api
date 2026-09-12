package affiliate

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

func ReferralCoinSummary(userId int) (map[string]any, error) {
	if userId <= 0 {
		return nil, model.ErrLedgerInvalidEntry
	}
	if _, err := model.MatureReferralCoinRewards(userId); err != nil {
		return nil, err
	}
	balance, err := model.ReferralCoinBalance(userId)
	if err != nil {
		return nil, err
	}
	var pending int64
	if err := model.DB.Model(&model.ReferralCoinReward{}).Where("user_id = ? AND status = ?", userId, model.ReferralCoinPending).Select("COALESCE(SUM(coin_amount), 0)").Scan(&pending).Error; err != nil {
		return nil, err
	}
	if pending < 0 || pending > common.MaxWalletQuota {
		return nil, model.ErrReferralCoinBalanceRange
	}
	step, err := common.ReferralCoinRedemptionStep()
	if err != nil {
		return nil, err
	}
	return map[string]any{"balance": balance, "pending": pending, "scale": common.ReferralCoinScale, "redemption_step": step, "enabled": common.ReferralCoinEnabled, "rate": common.ReferralCoinRate, "freeze_days": common.ReferralCoinFreezeDays}, nil
}

func RedeemReferralCoin(userId, coins int, requestId string) error {
	// Disabling new commission accrual must not strand coins that were already
	// credited. Redemption remains available for an existing balance.
	quota, err := common.ReferralCoinsToQuota(coins)
	if err != nil {
		return err
	}
	if err := model.RedeemReferralCoins(userId, coins, quota, requestId); err != nil {
		return err
	}
	model.RefreshUserQuotaCache(userId, 0)
	return nil
}
