package model

import (
	"errors"
	"fmt"
	"math"
	"strconv"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AffiliateRewardStatus 邀请赠金状态。
type AffiliateRewardStatus string

const (
	AffiliateRewardPending   AffiliateRewardStatus = "pending"
	AffiliateRewardCredited  AffiliateRewardStatus = "credited"
	AffiliateRewardCancelled AffiliateRewardStatus = "cancelled"
)

// AffiliateReward 记录下级实际 paid 消费产生的模型赠金。
type AffiliateReward struct {
	Id                int    `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId            int    `json:"user_id" gorm:"not null;index:idx_reward_user_status,priority:1"`
	SourceUserId      int    `json:"source_user_id" gorm:"not null;index"`
	EligiblePaidQuota int    `json:"eligible_paid_quota" gorm:"type:bigint;not null"`
	RewardQuota       int    `json:"reward_quota" gorm:"type:bigint;not null"`
	Status            string `json:"status" gorm:"type:varchar(16);not null;index:idx_reward_user_status,priority:2"`
	UnlockAt          int64  `json:"unlock_at" gorm:"bigint;not null;index"`
	ReferenceType     string `json:"reference_type" gorm:"type:varchar(64);not null"`
	ReferenceId       string `json:"reference_id" gorm:"type:varchar(128);not null;index:idx_reward_ref,priority:1"`
	IdempotencyKey    string `json:"idempotency_key" gorm:"type:varchar(255);not null;uniqueIndex"`
	CreatedAt         int64  `json:"created_at" gorm:"bigint;autoCreateTime;index:idx_reward_ref,priority:2"`
	UnlockedAt        int64  `json:"unlocked_at" gorm:"bigint;default:0"`
	CancelledAt       int64  `json:"cancelled_at" gorm:"bigint;default:0"`
	CancelReason      string `json:"cancel_reason" gorm:"type:varchar(128);not null;default:''"`
}

func (AffiliateReward) TableName() string { return "affiliate_rewards" }

var (
	ErrAffiliateRewardExists        = errors.New("affiliate reward already recorded")
	ErrAffiliateRewardLimitExceeded = errors.New("affiliate reward limit exceeded")
)

const maxAffiliateRewardsPerSourceUser = 3

// CreateAffiliateReward 幂等创建奖励；同一消费引用只生成一次记录。
func CreateAffiliateReward(reward *AffiliateReward) error {
	if reward == nil || reward.UserId <= 0 || reward.SourceUserId <= 0 ||
		reward.RewardQuota <= 0 || reward.IdempotencyKey == "" ||
		reward.ReferenceType == "" || reward.ReferenceId == "" {
		return fmt.Errorf("%w: invalid reward", ErrLedgerInvalidEntry)
	}
	if reward.Status == "" {
		reward.Status = string(AffiliateRewardPending)
	}
	if reward.UnlockAt == 0 {
		reward.UnlockAt = common.GetTimestamp()
	}
	if reward.CreatedAt == 0 {
		reward.CreatedAt = common.GetTimestamp()
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var existingRewards []AffiliateReward
		if err := lockForUpdate(tx).
			Where("source_user_id = ? AND status <> ?", reward.SourceUserId, string(AffiliateRewardCancelled)).
			Find(&existingRewards).Error; err != nil {
			return err
		}
		if len(existingRewards) >= maxAffiliateRewardsPerSourceUser {
			return fmt.Errorf("%w: source user %d", ErrAffiliateRewardLimitExceeded, reward.SourceUserId)
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(reward).Error; err != nil {
			return err
		}
		if reward.Id == 0 {
			return ErrAffiliateRewardExists
		}
		return nil
	})
}

// MatureAffiliateRewards 幂等处理该用户所有到期奖励，成熟后直接进入 bonus。
func MatureAffiliateRewards(userId int) (int, error) {
	if userId <= 0 {
		return 0, fmt.Errorf("%w: missing user id", ErrLedgerInvalidEntry)
	}
	now := common.GetTimestamp()
	var pending []AffiliateReward
	if err := DB.Where("user_id = ? AND status = ? AND unlock_at <= ?",
		userId, string(AffiliateRewardPending), now).Order("id asc").Find(&pending).Error; err != nil {
		return 0, err
	}
	matured := 0
	for _, reward := range pending {
		done, err := matureOneAffiliateReward(userId, reward.Id, now)
		if err != nil {
			return matured, err
		}
		if done {
			matured++
		}
	}
	if matured > 0 {
		RefreshUserQuotaCache(userId, 0)
	}
	return matured, nil
}

func matureOneAffiliateReward(userId, rewardId int, now int64) (bool, error) {
	done := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		var reward AffiliateReward
		if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", rewardId, userId).First(&reward).Error; err != nil {
			return err
		}
		if reward.Status != string(AffiliateRewardPending) || reward.UnlockAt > now {
			return nil
		}

		var receiver User
		if err := lockForUpdate(tx).Where("id = ?", reward.UserId).First(&receiver).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return cancelAffiliateRewardTx(tx, &reward, now, "receiver_missing")
			}
			return err
		}
		if receiver.Status != common.UserStatusEnabled {
			return cancelAffiliateRewardTx(tx, &reward, now, "receiver_disabled")
		}

		var source User
		if err := lockForUpdate(tx).Where("id = ?", reward.SourceUserId).First(&source).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return cancelAffiliateRewardTx(tx, &reward, now, "source_missing")
			}
			return err
		}
		if source.Status != common.UserStatusEnabled {
			return cancelAffiliateRewardTx(tx, &reward, now, "source_disabled")
		}
		if source.InviterId != reward.UserId {
			return cancelAffiliateRewardTx(tx, &reward, now, "inviter_changed")
		}

		refs, eligible, err := affiliateRewardCostRefsTx(tx, &reward)
		if err != nil {
			return err
		}
		if !eligible {
			return cancelAffiliateRewardTx(tx, &reward, now, "source_not_eligible")
		}
		cost, err := NetCostByReferencesTx(tx, reward.SourceUserId, refs)
		if err != nil {
			return err
		}
		currentPaidQuota := -cost[AccountPaid]
		if currentPaidQuota <= 0 {
			return cancelAffiliateRewardTx(tx, &reward, now, "paid_cost_refunded")
		}

		adjustedRewardQuota := reward.RewardQuota
		if reward.EligiblePaidQuota > 0 && currentPaidQuota != reward.EligiblePaidQuota {
			rateSnapshot := float64(reward.RewardQuota) / float64(reward.EligiblePaidQuota)
			adjustedRewardQuota = int(math.Round(float64(currentPaidQuota) * rateSnapshot))
		}
		if adjustedRewardQuota <= 0 {
			return cancelAffiliateRewardTx(tx, &reward, now, "reward_rounds_to_zero")
		}

		entry := LedgerEntry{
			UserId:        reward.UserId,
			Account:       AccountBonus,
			Delta:         adjustedRewardQuota,
			Reason:        LedgerReasonReferralBonus,
			ReferenceType: "referral_bonus",
			ReferenceId:   strconv.Itoa(reward.Id),
			// 保留既有幂等键，避免升级过程中对同一历史奖励重复入账。
			IdempotencyKey: fmt.Sprintf("affiliate_reward:unlock:%d", reward.Id),
		}
		if _, err := CreditQuotaTx(tx, entry); err != nil {
			return err
		}
		res := tx.Model(&AffiliateReward{}).
			Where("id = ? AND status = ?", reward.Id, string(AffiliateRewardPending)).
			Updates(map[string]interface{}{
				"status":              string(AffiliateRewardCredited),
				"unlocked_at":         now,
				"eligible_paid_quota": currentPaidQuota,
				"reward_quota":        adjustedRewardQuota,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return fmt.Errorf("affiliate reward %d concurrent maturity conflict", reward.Id)
		}
		done = true
		return nil
	})
	return done, err
}

func affiliateRewardCostRefsTx(tx *gorm.DB, reward *AffiliateReward) ([][2]string, bool, error) {
	if reward == nil || reward.ReferenceType == "" || reward.ReferenceId == "" {
		return nil, false, nil
	}
	refs := [][2]string{{reward.ReferenceType, reward.ReferenceId}}
	if reward.ReferenceType != "task" {
		return refs, true, nil
	}
	var task Task
	if err := tx.Where("user_id = ? AND task_id = ?", reward.SourceUserId, reward.ReferenceId).First(&task).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if task.Status != TaskStatusSuccess {
		return nil, false, nil
	}
	refs = refs[:0]
	if reqID := task.PrivateData.EffectiveRequestID(); reqID != "" {
		refs = append(refs, [2]string{"relay", reqID})
	}
	refs = append(refs, [2]string{"task", task.TaskID})
	return refs, true, nil
}

func cancelAffiliateRewardTx(tx *gorm.DB, reward *AffiliateReward, now int64, reason string) error {
	if reward == nil {
		return fmt.Errorf("nil affiliate reward")
	}
	res := tx.Model(&AffiliateReward{}).
		Where("id = ? AND status = ?", reward.Id, string(AffiliateRewardPending)).
		Updates(map[string]interface{}{
			"status":        string(AffiliateRewardCancelled),
			"cancelled_at":  now,
			"cancel_reason": reason,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return fmt.Errorf("affiliate reward %d concurrent cancellation conflict", reward.Id)
	}
	return nil
}

// AffiliateOverview 邀请赠金概览。
type AffiliateOverview struct {
	PendingCount       int
	EligiblePaidQuota  int
	PendingBonusQuota  int
	CreditedBonusQuota int
	TotalBonusQuota    int
}

func GetAffiliateOverview(userId int) (*AffiliateOverview, error) {
	if userId <= 0 {
		return nil, fmt.Errorf("%w: missing user id", ErrLedgerInvalidEntry)
	}
	overview := &AffiliateOverview{}
	var pendingCount int64
	if err := DB.Model(&AffiliateReward{}).
		Where("user_id = ? AND status = ?", userId, string(AffiliateRewardPending)).
		Count(&pendingCount).Error; err != nil {
		return nil, err
	}
	overview.PendingCount = int(pendingCount)
	var eligiblePaid, pendingBonus, creditedBonus int64
	activeStatuses := []string{string(AffiliateRewardPending), string(AffiliateRewardCredited)}
	if err := DB.Model(&AffiliateReward{}).Where("user_id = ? AND status IN ?", userId, activeStatuses).
		Select("COALESCE(SUM(eligible_paid_quota), 0)").Scan(&eligiblePaid).Error; err != nil {
		return nil, err
	}
	if err := DB.Model(&AffiliateReward{}).Where("user_id = ? AND status = ?", userId, string(AffiliateRewardPending)).
		Select("COALESCE(SUM(reward_quota), 0)").Scan(&pendingBonus).Error; err != nil {
		return nil, err
	}
	if err := DB.Model(&AffiliateReward{}).Where("user_id = ? AND status = ?", userId, string(AffiliateRewardCredited)).
		Select("COALESCE(SUM(reward_quota), 0)").Scan(&creditedBonus).Error; err != nil {
		return nil, err
	}
	overview.EligiblePaidQuota = int(eligiblePaid)
	overview.PendingBonusQuota = int(pendingBonus)
	overview.CreditedBonusQuota = int(creditedBonus)
	overview.TotalBonusQuota = overview.PendingBonusQuota + overview.CreditedBonusQuota
	return overview, nil
}

func ListAffiliateRewards(userId, page, pageSize int) ([]AffiliateReward, int64, error) {
	if userId <= 0 {
		return nil, 0, fmt.Errorf("%w: missing user id", ErrLedgerInvalidEntry)
	}
	page, pageSize = normalizeLedgerPagination(page, pageSize)
	var total int64
	if err := DB.Model(&AffiliateReward{}).Where("user_id = ?", userId).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rewards []AffiliateReward
	if err := DB.Where("user_id = ?", userId).Order("created_at desc, id desc").
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&rewards).Error; err != nil {
		return nil, 0, err
	}
	return rewards, total, nil
}
