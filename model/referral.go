package model

import "github.com/QuantumNous/new-api/common"

// ReferralInvitee is the privacy-filtered view of a user invited by the
// current account.  It deliberately omits email, quota balances and auth
// fields; the inviter only needs enough information to understand referral
// progress.
type ReferralInvitee struct {
	UserId            int    `json:"user_id"`
	Username          string `json:"username"`
	Status            int    `json:"status"`
	CreatedAt         int64  `json:"created_at"`
	EligiblePaidQuota int    `json:"eligible_paid_quota"`
	RewardQuota       int    `json:"reward_quota"`
	RewardStatus      string `json:"reward_status"`
}

// ListReferralInvitees returns the users directly invited by inviterId and
// the reward totals recorded for each invitee.  Reward rows are read in a
// second query so this remains portable across SQLite, MySQL and PostgreSQL
// (and does not depend on dialect-specific aggregate/JSON syntax).
func ListReferralInvitees(inviterId, page, pageSize int) ([]ReferralInvitee, int64, error) {
	if inviterId <= 0 {
		return nil, 0, ErrLedgerInvalidEntry
	}
	page, pageSize = normalizeLedgerPagination(page, pageSize)
	var total int64
	if err := DB.Model(&User{}).Where("inviter_id = ?", inviterId).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var users []User
	if err := DB.Select("id, username, status, created_at").
		Where("inviter_id = ?", inviterId).
		Order("created_at desc, id desc").
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&users).Error; err != nil {
		return nil, 0, err
	}
	if len(users) == 0 {
		return []ReferralInvitee{}, total, nil
	}
	ids := make([]int, 0, len(users))
	for _, user := range users {
		ids = append(ids, user.Id)
	}
	var rewards []AffiliateReward
	if err := DB.Where("user_id = ? AND source_user_id IN ? AND status <> ?", inviterId, ids, string(AffiliateRewardCancelled)).Find(&rewards).Error; err != nil {
		return nil, 0, err
	}
	totals := make(map[int]*ReferralInvitee, len(users))
	result := make([]ReferralInvitee, 0, len(users))
	for _, user := range users {
		item := ReferralInvitee{UserId: user.Id, Username: user.Username, Status: user.Status, CreatedAt: user.CreatedAt}
		result = append(result, item)
		totals[user.Id] = &result[len(result)-1]
	}
	for _, reward := range rewards {
		item := totals[reward.SourceUserId]
		if item == nil {
			continue
		}
		if reward.EligiblePaidQuota < 0 || reward.EligiblePaidQuota > common.MaxWalletQuota || item.EligiblePaidQuota > common.MaxWalletQuota-reward.EligiblePaidQuota || reward.RewardQuota < 0 || reward.RewardQuota > common.MaxWalletQuota || item.RewardQuota > common.MaxWalletQuota-reward.RewardQuota {
			return nil, 0, ErrLedgerInvalidEntry
		}
		item.EligiblePaidQuota += reward.EligiblePaidQuota
		item.RewardQuota += reward.RewardQuota
		// A credited row takes precedence over pending when a source has both.
		if item.RewardStatus == "" || reward.Status == string(AffiliateRewardCredited) {
			item.RewardStatus = reward.Status
		}
	}
	return result, total, nil
}
