package model

// AffiliateRewardReconcileState 保留历史消费返利对账表，仅用于升级兼容：
// 应用层不再读写，但 AutoMigrate 仍保留该表，已有数据不会被删除。
type AffiliateRewardReconcileState struct {
	Id                     int    `json:"id" gorm:"primaryKey;autoIncrement"`
	SourceUserId           int    `json:"source_user_id" gorm:"not null;uniqueIndex:idx_aff_reconcile_ref,priority:1"`
	ReferenceType          string `json:"reference_type" gorm:"type:varchar(64);not null;uniqueIndex:idx_aff_reconcile_ref,priority:2"`
	ReferenceId            string `json:"reference_id" gorm:"type:varchar(128);not null;uniqueIndex:idx_aff_reconcile_ref,priority:3"`
	CanonicalReferenceType string `json:"canonical_reference_type" gorm:"type:varchar(64);not null;default:''"`
	CanonicalReferenceId   string `json:"canonical_reference_id" gorm:"type:varchar(128);not null;default:''"`
	Status                 string `json:"status" gorm:"type:varchar(16);not null;index"`
	AttemptCount           int    `json:"attempt_count" gorm:"not null;default:0"`
	LastError              string `json:"last_error" gorm:"type:varchar(512);not null;default:''"`
	CreatedAt              int64  `json:"created_at" gorm:"bigint;autoCreateTime"`
	UpdatedAt              int64  `json:"updated_at" gorm:"bigint;autoUpdateTime;index"`
}

func (AffiliateRewardReconcileState) TableName() string {
	return "affiliate_reward_reconcile_states"
}

// ListDueAffiliateRewardUserIds returns distinct recipients with matureable
// pending rewards so the maintenance worker can release them without relying
// on a wallet page visit.
func ListDueAffiliateRewardUserIds(now int64, limit int) ([]int, error) {
	if limit <= 0 {
		limit = 200
	}
	var ids []int
	err := DB.Model(&AffiliateReward{}).
		Where("status = ? AND unlock_at <= ?", string(AffiliateRewardPending), now).
		Distinct().Order("user_id ASC").Limit(limit).Pluck("user_id", &ids).Error
	return ids, err
}
