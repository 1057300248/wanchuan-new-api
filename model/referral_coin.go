package model

import (
	"errors"
	"fmt"
	"math"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	ReferralCoinPending   = "pending"
	ReferralCoinCredited  = "credited"
	ReferralCoinCancelled = "cancelled"
)

// ReferralCoinReward is a B.AI-style commission.  Coin units are integer
// points and are intentionally separate from quota/bonus balances.
type ReferralCoinReward struct {
	Id             int    `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId         int    `json:"user_id" gorm:"not null;index:idx_coin_reward_user_status,priority:1"`
	SourceUserId   int    `json:"source_user_id" gorm:"not null;index"`
	PaidAmount     int    `json:"paid_amount" gorm:"type:bigint;not null"`
	CoinAmount     int    `json:"coin_amount" gorm:"type:bigint;not null"`
	Status         string `json:"status" gorm:"type:varchar(16);not null;index:idx_coin_reward_user_status,priority:2"`
	UnlockAt       int64  `json:"unlock_at" gorm:"bigint;not null;index"`
	ReferenceType  string `json:"reference_type" gorm:"type:varchar(64);not null"`
	ReferenceId    string `json:"reference_id" gorm:"type:varchar(128);not null;index:idx_coin_reward_ref,priority:1"`
	IdempotencyKey string `json:"-" gorm:"type:varchar(255);not null;uniqueIndex"`
	CreatedAt      int64  `json:"created_at" gorm:"bigint;autoCreateTime;index:idx_coin_reward_ref,priority:2"`
	CreditedAt     int64  `json:"credited_at" gorm:"bigint;default:0"`
	CancelledAt    int64  `json:"cancelled_at" gorm:"bigint;default:0"`
}

func (ReferralCoinReward) TableName() string { return "referral_coin_rewards" }

var ErrReferralCoinRewardExists = errors.New("referral coin reward already recorded")
var ErrReferralCoinInsufficient = errors.New("insufficient referral coin balance")
var ErrReferralCoinBalanceRange = errors.New("referral coin balance outside supported wallet range")

// RecordReferralCoinForTopupTx records a commission from the already settled
// paid ledger delta. It is called inside the top-up transaction, so a failed
// wallet credit can never leave a dangling commission. Admin/manual top-ups
// use a different ledger reason and are deliberately excluded.
func RecordReferralCoinForTopupTx(tx *gorm.DB, sourceUserId, paidQuota int, referenceId string) error {
	if tx == nil || !common.ReferralCoinEnabled || paidQuota <= 0 || sourceUserId <= 0 || referenceId == "" ||
		math.IsNaN(common.ReferralCoinRate) || math.IsInf(common.ReferralCoinRate, 0) ||
		common.ReferralCoinRate <= 0 || common.ReferralCoinRate > 1 ||
		common.ReferralCoinFreezeDays < 0 || common.ReferralCoinFreezeDays > 3650 || common.QuotaPerUnit <= 0 || math.IsNaN(common.QuotaPerUnit) || math.IsInf(common.QuotaPerUnit, 0) {
		return nil
	}
	var source User
	if err := tx.Select("id, status, inviter_id").Where("id = ?", sourceUserId).First(&source).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if source.Status != common.UserStatusEnabled || source.InviterId <= 0 || source.InviterId == sourceUserId {
		return nil
	}
	var inviter User
	if err := tx.Select("id, status").Where("id = ?", source.InviterId).First(&inviter).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if inviter.Status != common.UserStatusEnabled {
		return nil
	}
	coinDecimal := decimal.NewFromInt(int64(paidQuota)).Div(decimal.NewFromFloat(common.QuotaPerUnit)).Mul(decimal.NewFromInt(common.ReferralCoinScale)).Mul(decimal.NewFromFloat(common.ReferralCoinRate))
	coinAmount, err := common.WalletQuotaFromDecimalStrict(coinDecimal.Truncate(0))
	if err != nil {
		return err
	}
	if coinAmount <= 0 {
		return nil
	}
	reward := &ReferralCoinReward{UserId: inviter.Id, SourceUserId: sourceUserId, PaidAmount: paidQuota, CoinAmount: coinAmount, Status: ReferralCoinPending, UnlockAt: common.GetTimestamp() + int64(common.ReferralCoinFreezeDays)*86400, ReferenceType: "topup", ReferenceId: referenceId, IdempotencyKey: "referral_coin:topup:" + referenceId}
	return CreateReferralCoinRewardTx(tx, reward)
}

func CreateReferralCoinRewardTx(tx *gorm.DB, reward *ReferralCoinReward) error {
	if tx == nil || reward == nil || reward.UserId <= 0 || reward.SourceUserId <= 0 || reward.CoinAmount <= 0 || reward.CoinAmount > common.MaxWalletQuota || reward.ReferenceType == "" || reward.ReferenceId == "" || reward.IdempotencyKey == "" {
		return fmt.Errorf("invalid referral coin reward")
	}
	if reward.Status == "" {
		reward.Status = ReferralCoinPending
	}
	if reward.UnlockAt == 0 {
		reward.UnlockAt = common.GetTimestamp()
	}
	if reward.CreatedAt == 0 {
		reward.CreatedAt = common.GetTimestamp()
	}
	result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(reward)
	if result.Error != nil {
		return result.Error
	}
	return nil
}

func CreateReferralCoinReward(reward *ReferralCoinReward) error {
	if reward == nil || reward.UserId <= 0 || reward.SourceUserId <= 0 || reward.CoinAmount <= 0 || reward.CoinAmount > common.MaxWalletQuota || reward.ReferenceType == "" || reward.ReferenceId == "" || reward.IdempotencyKey == "" {
		return fmt.Errorf("invalid referral coin reward")
	}
	if reward.Status == "" {
		reward.Status = ReferralCoinPending
	}
	if reward.UnlockAt == 0 {
		reward.UnlockAt = common.GetTimestamp()
	}
	if reward.CreatedAt == 0 {
		reward.CreatedAt = common.GetTimestamp()
	}
	result := DB.Clauses(clause.OnConflict{DoNothing: true}).Create(reward)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrReferralCoinRewardExists
	}
	return nil
}

func MatureReferralCoinRewards(userId int) (int, error) {
	if userId <= 0 {
		return 0, ErrLedgerInvalidEntry
	}
	now := common.GetTimestamp()
	var rewards []ReferralCoinReward
	if err := DB.Where("user_id = ? AND status = ? AND unlock_at <= ?", userId, ReferralCoinPending, now).Order("id asc").Find(&rewards).Error; err != nil {
		return 0, err
	}
	matured := 0
	for _, reward := range rewards {
		credited := false
		err := DB.Transaction(func(tx *gorm.DB) error {
			// Use the same lock order as redemption, serializing every Coin
			// balance mutation for this account across application instances.
			var owner User
			if err := lockForUpdate(tx).Select("id, status").Where("id = ?", userId).First(&owner).Error; err != nil {
				return err
			}
			if owner.Status != common.UserStatusEnabled {
				return nil
			}
			var row ReferralCoinReward
			if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", reward.Id, userId).First(&row).Error; err != nil {
				return err
			}
			if row.Status != ReferralCoinPending || row.UnlockAt > now {
				return nil
			}
			var source User
			if err := tx.Select("id, status, inviter_id").Where("id = ?", row.SourceUserId).First(&source).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return tx.Model(&row).Updates(map[string]any{"status": ReferralCoinCancelled, "cancelled_at": now}).Error
				}
				return err
			}
			if source.Status != common.UserStatusEnabled || source.InviterId != row.UserId {
				return tx.Model(&row).Updates(map[string]any{"status": ReferralCoinCancelled, "cancelled_at": now}).Error
			}
			if row.ReferenceType == "topup" {
				var topup TopUp
				if err := tx.Select("status, user_id").Where("trade_no = ?", row.ReferenceId).First(&topup).Error; err != nil {
					if !errors.Is(err, gorm.ErrRecordNotFound) {
						return err
					}
					return tx.Model(&row).Updates(map[string]any{"status": ReferralCoinCancelled, "cancelled_at": now}).Error
				}
				if topup.Status != common.TopUpStatusSuccess || topup.UserId != row.SourceUserId {
					return tx.Model(&row).Updates(map[string]any{"status": ReferralCoinCancelled, "cancelled_at": now}).Error
				}
			}
			var balance int64
			if err := tx.Model(&ReferralCoinLedger{}).Where("user_id = ?", userId).Select("COALESCE(SUM(delta), 0)").Scan(&balance).Error; err != nil {
				return err
			}
			if row.CoinAmount <= 0 || row.CoinAmount > common.MaxWalletQuota || balance < 0 || balance > int64(common.MaxWalletQuota-row.CoinAmount) {
				return ErrReferralCoinBalanceRange
			}
			entry := ReferralCoinLedger{UserId: row.UserId, Delta: row.CoinAmount, Reason: "referral_commission", ReferenceType: "referral_coin_reward", ReferenceId: strconv.Itoa(row.Id), IdempotencyKey: fmt.Sprintf("referral_coin:credit:%d", row.Id)}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&entry).Error; err != nil {
				return err
			}
			if err := tx.Model(&ReferralCoinReward{}).Where("id = ? AND status = ?", row.Id, ReferralCoinPending).Updates(map[string]any{"status": ReferralCoinCredited, "credited_at": now}).Error; err != nil {
				return err
			}
			credited = true
			return nil
		})
		if err != nil {
			return matured, err
		}
		if credited {
			matured++
		}
	}
	return matured, nil
}

type ReferralCoinLedger struct {
	Id             int    `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId         int    `json:"user_id" gorm:"not null;index"`
	Delta          int    `json:"delta" gorm:"type:bigint;not null"`
	Reason         string `json:"reason" gorm:"type:varchar(64);not null"`
	ReferenceType  string `json:"reference_type" gorm:"type:varchar(64);not null"`
	ReferenceId    string `json:"reference_id" gorm:"type:varchar(128);not null"`
	IdempotencyKey string `json:"-" gorm:"type:varchar(255);not null;uniqueIndex"`
	CreatedAt      int64  `json:"created_at" gorm:"bigint;autoCreateTime;index"`
}

func (ReferralCoinLedger) TableName() string { return "referral_coin_ledger" }

func ReferralCoinBalance(userId int) (int, error) {
	if userId <= 0 {
		return 0, ErrLedgerInvalidEntry
	}
	var value int64
	if err := DB.Model(&ReferralCoinLedger{}).Where("user_id = ?", userId).Select("COALESCE(SUM(delta), 0)").Scan(&value).Error; err != nil {
		return 0, err
	}
	if value < 0 || value > common.MaxWalletQuota {
		return 0, ErrReferralCoinBalanceRange
	}
	return int(value), nil
}

// ListReferralCoinRewards returns the current user's commission history in a
// stable, paginated order. Cancelled rows remain visible so users can explain
// why a pending commission did not unlock.
func ListReferralCoinRewards(userId, page, pageSize int) ([]ReferralCoinReward, int64, error) {
	if userId <= 0 {
		return nil, 0, ErrLedgerInvalidEntry
	}
	page, pageSize = normalizeLedgerPagination(page, pageSize)
	var total int64
	if err := DB.Model(&ReferralCoinReward{}).Where("user_id = ?", userId).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rewards []ReferralCoinReward
	if err := DB.Where("user_id = ?", userId).Order("created_at desc, id desc").
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&rewards).Error; err != nil {
		return nil, 0, err
	}
	return rewards, total, nil
}

// RedeemReferralCoins atomically converts microcoins to bonus quota, enforcing
// precision and user-bound idempotency at the database mutation boundary too.
func RedeemReferralCoins(userId, coins, quota int, requestId string) error {
	if userId <= 0 || coins <= 0 || quota <= 0 || len(requestId) < 8 || len(requestId) > 64 {
		return ErrLedgerInvalidEntry
	}
	expectedQuota, err := common.ReferralCoinsToQuota(coins)
	if err != nil || quota != expectedQuota {
		return common.ErrReferralCoinAmount
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).Where("id = ?", userId).First(&user).Error; err != nil {
			return err
		}
		if user.Status != common.UserStatusEnabled {
			return ErrLedgerInvalidEntry
		}
		key := fmt.Sprintf("referral_coin:redeem:%d:%s", userId, requestId)
		var previous ReferralCoinLedger
		err := tx.Where("idempotency_key = ?", key).First(&previous).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Preserve retries of pre-upgrade operations without sharing their
			// global request namespace with other users.
			err = tx.Where("idempotency_key = ? AND user_id = ?", "referral_coin:redeem:"+requestId, userId).First(&previous).Error
		}
		if err == nil {
			if previous.UserId != userId || previous.Delta != -coins || previous.Reason != "referral_coin_redeem" || previous.ReferenceType != "referral_coin_redeem" || previous.ReferenceId != requestId {
				return ErrLedgerIdempotencyConflict
			}
			var previousBonus BalanceLedger
			if err := tx.Where("idempotency_key = ? AND account = ?", previous.IdempotencyKey+":bonus", string(AccountBonus)).First(&previousBonus).Error; err != nil {
				return ErrLedgerIdempotencyConflict
			}
			if previousBonus.UserId != userId || previousBonus.Delta != quota || previousBonus.Reason != LedgerReasonReferralBonus || previousBonus.ReferenceType != "referral_coin_redeem" || previousBonus.ReferenceId != requestId {
				return ErrLedgerIdempotencyConflict
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var balance int64
		if err := tx.Model(&ReferralCoinLedger{}).Where("user_id = ?", userId).Select("COALESCE(SUM(delta), 0)").Scan(&balance).Error; err != nil {
			return err
		}
		if balance < int64(coins) {
			return ErrReferralCoinInsufficient
		}
		if balance > common.MaxWalletQuota {
			return ErrReferralCoinBalanceRange
		}
		if _, err := CreditQuotaTx(tx, LedgerEntry{UserId: userId, Account: AccountBonus, Delta: quota, Reason: LedgerReasonReferralBonus, ReferenceType: "referral_coin_redeem", ReferenceId: requestId, IdempotencyKey: key + ":bonus"}); err != nil {
			return err
		}
		return tx.Create(&ReferralCoinLedger{UserId: userId, Delta: -coins, Reason: "referral_coin_redeem", ReferenceType: "referral_coin_redeem", ReferenceId: requestId, IdempotencyKey: key}).Error
	})
}
