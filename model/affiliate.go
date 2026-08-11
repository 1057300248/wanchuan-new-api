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

// ---------------------------------------------------------------------------
// 推荐返利与提现（TokenMix P1-3）
//
// 返利：源用户每笔实际 paid 消费按系统比例生成一条 affiliate_rewards 记录，
// 状态先为 pending（冻结），成熟（unlock_at 到达）后经幂等 lazy release 转入
// inviter 的 affiliate 账户（balance_ledger，reason=affiliate_reward）。
// 幂等键 affiliate_reward:{refType}:{refId} 保证每个源请求/消费只创建一次。
//
// 提现：申请即从 affiliate 账户冻结扣减（balance_ledger，reason=withdrawal_hold），
// 管理员批准标记人工支付待办（approved -> paid），拒绝则原路退回
// （reason=withdrawal_reject_refund）。申请键 request_id 唯一，网络重试幂等。
// 整个过程不接入任何外部支付渠道。
// ---------------------------------------------------------------------------

// AffiliateRewardStatus 返利状态。
type AffiliateRewardStatus string

const (
	AffiliateRewardPending   AffiliateRewardStatus = "pending"   // 冻结中，未成熟
	AffiliateRewardAvailable AffiliateRewardStatus = "available" // 已成熟，转入 affiliate 余额
	AffiliateRewardCancelled AffiliateRewardStatus = "cancelled" // 冻结期资格失效或消费已退款
)

// AffiliateWithdrawalStatus 提现申请状态。
type AffiliateWithdrawalStatus string

const (
	WithdrawalPending  AffiliateWithdrawalStatus = "pending"  // 待人工审核
	WithdrawalApproved AffiliateWithdrawalStatus = "approved" // 已审批，人工支付待办
	WithdrawalRejected AffiliateWithdrawalStatus = "rejected" // 已拒绝，额度原路退回
	WithdrawalPaid     AffiliateWithdrawalStatus = "paid"     // 人工支付已确认
)

// AffiliateReward 推荐返利记录。
type AffiliateReward struct {
	Id                int    `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId            int    `json:"user_id" gorm:"not null;index:idx_reward_user_status,priority:1"`                 // 邀请人（返利接收者）
	SourceUserId      int    `json:"source_user_id" gorm:"not null;index"`                                            // 源用户（被邀请人）
	EligiblePaidQuota int    `json:"eligible_paid_quota" gorm:"not null"`                                             // 符合条件的实际 paid 消费（quota units）
	RewardQuota       int    `json:"reward_quota" gorm:"not null"`                                                    // 返利额度（quota units）
	Status            string `json:"status" gorm:"type:varchar(16);not null;index:idx_reward_user_status,priority:2"` // pending/available/withdrawn
	UnlockAt          int64  `json:"unlock_at" gorm:"bigint;not null;index"`                                          // 成熟时间戳
	ReferenceType     string `json:"reference_type" gorm:"type:varchar(64);not null"`                                 // "relay" | "task"
	ReferenceId       string `json:"reference_id" gorm:"type:varchar(128);not null;index:idx_reward_ref,priority:1"`
	IdempotencyKey    string `json:"idempotency_key" gorm:"type:varchar(255);not null;uniqueIndex"`
	CreatedAt         int64  `json:"created_at" gorm:"bigint;autoCreateTime;index:idx_reward_ref,priority:2"`
	UnlockedAt        int64  `json:"unlocked_at" gorm:"bigint;default:0"`
	CancelledAt       int64  `json:"cancelled_at" gorm:"bigint;default:0"`
	CancelReason      string `json:"cancel_reason" gorm:"type:varchar(128);not null;default:''"`
}

func (AffiliateReward) TableName() string {
	return "affiliate_rewards"
}

// AffiliateWithdrawal 提现申请记录。
type AffiliateWithdrawal struct {
	Id           int    `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId       int    `json:"user_id" gorm:"not null;index"`
	Amount       int    `json:"amount" gorm:"not null"` // quota units
	Status       string `json:"status" gorm:"type:varchar(16);not null;index"`
	RequestId    string `json:"request_id" gorm:"type:varchar(128);not null;uniqueIndex"`
	RejectReason string `json:"reject_reason" gorm:"type:varchar(512);default:''"`
	OperatorId   int    `json:"operator_id" gorm:"not null;default:0"`
	CreatedAt    int64  `json:"created_at" gorm:"bigint;autoCreateTime;index"`
	UpdatedAt    int64  `json:"updated_at" gorm:"bigint;autoUpdateTime"`
	ApprovedAt   int64  `json:"approved_at" gorm:"bigint;default:0"`
	PaidAt       int64  `json:"paid_at" gorm:"bigint;default:0"`
}

func (AffiliateWithdrawal) TableName() string {
	return "affiliate_withdrawals"
}

var (
	ErrAffiliateRewardExists     = errors.New("affiliate reward already recorded")
	ErrAffiliateWithdrawDisabled = errors.New("提现功能未启用")
	ErrAffiliateWithdrawTooSmall = errors.New("提现金额低于最低限额")
	ErrAffiliateInsufficient     = errors.New("可用返利余额不足")
	ErrAffiliateWithdrawInvalid  = errors.New("invalid withdrawal request")
	ErrAffiliateWithdrawConflict = errors.New("withdrawal request id conflict")
)

// ---------------------------------------------------------------------------
// 返利记录
// ---------------------------------------------------------------------------

// CreateAffiliateReward 幂等创建一条返利记录。同一幂等键已存在时返回
// ErrAffiliateRewardExists（调用方视为成功，不重复入账）。
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
	err := DB.Clauses(clause.OnConflict{DoNothing: true}).Create(reward).Error
	if err != nil {
		return err
	}
	if reward.Id == 0 {
		return ErrAffiliateRewardExists
	}
	return nil
}

// MatureAffiliateRewards 幂等成熟 userId 下所有到期的 pending 返利：
// 行锁 + 状态校验 + 同一事务内 CreditQuotaTx 入 affiliate 账户。
// 账本幂等键 affiliate_reward:unlock:{rewardId} 保证同一记录不会重复入账。
// 返回本次实际成熟的条数。
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
			UserId:         reward.UserId,
			Account:        AccountAffiliate,
			Delta:          adjustedRewardQuota,
			Reason:         LedgerReasonAffiliateReward,
			ReferenceType:  "affiliate_reward",
			ReferenceId:    strconv.Itoa(reward.Id),
			IdempotencyKey: fmt.Sprintf("affiliate_reward:unlock:%d", reward.Id),
		}
		if _, err := CreditQuotaTx(tx, entry); err != nil {
			return err
		}
		reward.Status = string(AffiliateRewardAvailable)
		reward.UnlockedAt = now
		reward.EligiblePaidQuota = currentPaidQuota
		reward.RewardQuota = adjustedRewardQuota
		res := tx.Model(&AffiliateReward{}).
			Where("id = ? AND status = ?", reward.Id, string(AffiliateRewardPending)).
			Updates(map[string]interface{}{
				"status":              reward.Status,
				"unlocked_at":         reward.UnlockedAt,
				"eligible_paid_quota": reward.EligiblePaidQuota,
				"reward_quota":        reward.RewardQuota,
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
	if err != nil {
		return false, err
	}
	return done, nil
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
	if task.PrivateData.RequestId != "" {
		refs = append(refs, [2]string{"relay", task.PrivateData.RequestId})
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

// ---------------------------------------------------------------------------
// 提现申请
// ---------------------------------------------------------------------------

// CreateAffiliateWithdrawal 创建提现申请：同一事务内从 affiliate 账户冻结扣减
// 并写账本。requestId 唯一；重复请求（网络重试）返回重放成功（幂等）。
// 功能开关关闭（common.AffiliateRewardEnabled=false）时不允许新提现。
func CreateAffiliateWithdrawal(userId, amount int, requestId string) (*AffiliateWithdrawal, bool, error) {
	if !common.AffiliateRewardEnabled {
		return nil, false, ErrAffiliateWithdrawDisabled
	}
	if userId <= 0 || amount <= 0 || requestId == "" {
		return nil, false, fmt.Errorf("%w: invalid request", ErrAffiliateWithdrawInvalid)
	}
	if amount < common.AffiliateMinWithdrawQuota {
		return nil, false, fmt.Errorf("%w: amount=%d min=%d", ErrAffiliateWithdrawTooSmall, amount, common.AffiliateMinWithdrawQuota)
	}
	if len(requestId) > 128 {
		return nil, false, fmt.Errorf("%w: request id too long", ErrAffiliateWithdrawInvalid)
	}
	now := common.GetTimestamp()
	var withdrawal *AffiliateWithdrawal
	replayed := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		var existing AffiliateWithdrawal
		result := tx.Where("request_id = ?", requestId).Limit(1).Find(&existing)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected > 0 {
			if existing.UserId != userId || existing.Amount != amount {
				return fmt.Errorf("%w: request_id=%s", ErrAffiliateWithdrawConflict, requestId)
			}
			withdrawal = &existing
			replayed = true
			return nil
		}
		entry := LedgerEntry{
			UserId:         userId,
			Account:        AccountAffiliate,
			Delta:          amount,
			Reason:         LedgerReasonWithdrawalHold,
			ReferenceType:  "withdrawal",
			ReferenceId:    requestId,
			IdempotencyKey: "withdrawal:hold:" + requestId,
		}
		if _, err := DebitQuotaTx(tx, entry, AccountAffiliate); err != nil {
			return err
		}
		withdrawal = &AffiliateWithdrawal{
			UserId:    userId,
			Amount:    amount,
			Status:    string(WithdrawalPending),
			RequestId: requestId,
			CreatedAt: now,
			UpdatedAt: now,
		}
		return tx.Create(withdrawal).Error
	})
	if err != nil {
		// 并发重试可能在唯一键处与已提交请求相撞；事务已整体回滚，
		// 此时重新读取同 request_id，按相同参数返回幂等重放。
		var existing AffiliateWithdrawal
		if lookupErr := DB.Where("request_id = ?", requestId).First(&existing).Error; lookupErr == nil {
			if existing.UserId == userId && existing.Amount == amount {
				return &existing, true, nil
			}
			return nil, false, fmt.Errorf("%w: request_id=%s", ErrAffiliateWithdrawConflict, requestId)
		}
		return nil, false, err
	}
	RefreshUserQuotaCache(userId, 0)
	return withdrawal, replayed, nil
}

// ApproveAffiliateWithdrawal 审批提现申请（pending -> approved），幂等。
// 审批只标记人工支付待办，不改变任何余额。
func ApproveAffiliateWithdrawal(withdrawalId, operatorId int) error {
	return updateWithdrawalStatus(withdrawalId, operatorId, WithdrawalPending, WithdrawalApproved)
}

// MarkAffiliateWithdrawalPaid 确认人工支付完成（approved -> paid），幂等。
func MarkAffiliateWithdrawalPaid(withdrawalId, operatorId int) error {
	return updateWithdrawalStatus(withdrawalId, operatorId, WithdrawalApproved, WithdrawalPaid)
}

func updateWithdrawalStatus(withdrawalId, operatorId int, from, to AffiliateWithdrawalStatus) error {
	var current AffiliateWithdrawal
	if err := DB.Where("id = ?", withdrawalId).First(&current).Error; err != nil {
		return fmt.Errorf("%w: withdrawal=%d not found", ErrAffiliateWithdrawConflict, withdrawalId)
	}
	if current.Status == string(to) {
		return nil // 幂等：目标状态已达成
	}
	if current.Status != string(from) {
		return fmt.Errorf("%w: withdrawal=%d status=%s cannot transition to %s",
			ErrAffiliateWithdrawConflict, withdrawalId, current.Status, to)
	}
	now := common.GetTimestamp()
	updates := map[string]interface{}{
		"status":      string(to),
		"operator_id": operatorId,
		"updated_at":  now,
	}
	if to == WithdrawalApproved {
		updates["approved_at"] = now
	}
	if to == WithdrawalPaid {
		updates["paid_at"] = now
	}
	res := DB.Model(&AffiliateWithdrawal{}).
		Where("id = ? AND status = ?", withdrawalId, string(from)).
		Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return fmt.Errorf("%w: withdrawal=%d concurrent transition", ErrAffiliateWithdrawConflict, withdrawalId)
	}
	return nil
}

// RejectAffiliateWithdrawal 拒绝提现申请（pending -> rejected）并原路退回
// affiliate 余额（同一事务，账本幂等键 withdrawal:refund:{id} 防重复退回）。
// 已拒绝的重复请求幂等成功；已审批/已支付的申请不允许拒绝。
func RejectAffiliateWithdrawal(withdrawalId, operatorId int, reason string) error {
	now := common.GetTimestamp()
	userId := 0
	err := DB.Transaction(func(tx *gorm.DB) error {
		var withdrawal AffiliateWithdrawal
		if err := lockForUpdate(tx).Where("id = ?", withdrawalId).First(&withdrawal).Error; err != nil {
			return err
		}
		switch withdrawal.Status {
		case string(WithdrawalRejected):
			return nil // 幂等：重复拒绝
		case string(WithdrawalPending):
			userId = withdrawal.UserId
		default:
			return fmt.Errorf("%w: status=%s", ErrAffiliateWithdrawConflict, withdrawal.Status)
		}
		entry := LedgerEntry{
			UserId:         withdrawal.UserId,
			Account:        AccountAffiliate,
			Delta:          withdrawal.Amount,
			Reason:         LedgerReasonWithdrawalRejectRefund,
			ReferenceType:  "withdrawal",
			ReferenceId:    withdrawal.RequestId,
			IdempotencyKey: fmt.Sprintf("withdrawal:refund:%d", withdrawal.Id),
		}
		if _, err := CreditQuotaTx(tx, entry); err != nil {
			return err
		}
		return tx.Model(&AffiliateWithdrawal{}).
			Where("id = ? AND status = ?", withdrawal.Id, string(WithdrawalPending)).
			Updates(map[string]interface{}{
				"status":        string(WithdrawalRejected),
				"reject_reason": reason,
				"operator_id":   operatorId,
				"updated_at":    now,
			}).Error
	})
	if err == nil && userId > 0 {
		RefreshUserQuotaCache(userId, 0)
	}
	return err
}

// ---------------------------------------------------------------------------
// 查询
// ---------------------------------------------------------------------------

// AffiliateOverview 用户推荐概览。
type AffiliateOverview struct {
	PendingCount     int
	PendingQuota     int
	AvailableQuota   int // affiliate 账户当前余额
	WithdrawnQuota   int // 已审批+已支付的提现累计
	TotalEarnedQuota int // 累计成熟返利（可用 + 已提现）
}

// GetAffiliateOverview 汇总用户推荐数据（调用方决定是否需要先 Mature）。
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
	var pendingQuota int64
	if err := DB.Model(&AffiliateReward{}).
		Where("user_id = ? AND status = ?", userId, string(AffiliateRewardPending)).
		Select("COALESCE(SUM(reward_quota), 0)").Scan(&pendingQuota).Error; err != nil {
		return nil, err
	}
	overview.PendingQuota = int(pendingQuota)
	paid, bonus, affiliate, err := GetUserAccountBalances(userId)
	if err != nil {
		return nil, err
	}
	_ = paid
	_ = bonus
	overview.AvailableQuota = affiliate
	var withdrawnTotal int64
	if err := DB.Model(&AffiliateWithdrawal{}).
		Where("user_id = ? AND status IN ?", userId, []string{string(WithdrawalApproved), string(WithdrawalPaid)}).
		Select("COALESCE(SUM(amount), 0)").Scan(&withdrawnTotal).Error; err != nil {
		return nil, err
	}
	overview.WithdrawnQuota = int(withdrawnTotal)
	var maturedTotal int64
	if err := DB.Model(&AffiliateReward{}).
		Where("user_id = ? AND status = ?", userId, string(AffiliateRewardAvailable)).
		Select("COALESCE(SUM(reward_quota), 0)").Scan(&maturedTotal).Error; err != nil {
		return nil, err
	}
	overview.TotalEarnedQuota = int(maturedTotal)
	return overview, nil
}

// ListAffiliateRewards 分页查询用户返利记录。
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

// WithdrawalQueryFilter 提现查询过滤条件。
type WithdrawalQueryFilter struct {
	UserId   int
	Status   string
	Page     int
	PageSize int
}

// ListAffiliateWithdrawals 分页查询提现申请（UserId>0 时限定该用户，防止越权）。
func ListAffiliateWithdrawals(filter WithdrawalQueryFilter) ([]AffiliateWithdrawal, int64, error) {
	base := DB.Model(&AffiliateWithdrawal{})
	if filter.UserId > 0 {
		base = base.Where("user_id = ?", filter.UserId)
	}
	if filter.Status != "" {
		switch AffiliateWithdrawalStatus(filter.Status) {
		case WithdrawalPending, WithdrawalApproved, WithdrawalRejected, WithdrawalPaid:
		default:
			return nil, 0, fmt.Errorf("%w: invalid withdrawal status", ErrAffiliateWithdrawInvalid)
		}
		base = base.Where("status = ?", filter.Status)
	}
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	page, pageSize := normalizeLedgerPagination(filter.Page, filter.PageSize)
	var withdrawals []AffiliateWithdrawal
	if err := base.Order("created_at desc, id desc").
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&withdrawals).Error; err != nil {
		return nil, 0, err
	}
	return withdrawals, total, nil
}
