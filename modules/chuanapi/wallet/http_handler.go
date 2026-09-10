package wallet

import (
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/affiliateport"

	"github.com/gin-gonic/gin"
)

// WalletRuleSummary 是独立 Dashboard 使用的赠金规则快照。
type WalletRuleSummary struct {
	SignupBonusEnabled      bool    `json:"signup_bonus_enabled"`
	SignupBonusQuota        int     `json:"signup_bonus_quota"`
	ReferralBonusEnabled    bool    `json:"referral_bonus_enabled"`
	ReferralBonusRate       float64 `json:"referral_bonus_rate"`
	ReferralBonusFreezeDays int     `json:"referral_bonus_freeze_days"`
}

func walletRuleSummary() WalletRuleSummary {
	return WalletRuleSummary{
		SignupBonusEnabled:      common.SignupBonusEnabled,
		SignupBonusQuota:        common.QuotaForSignupBonus,
		ReferralBonusEnabled:    common.AffiliateRewardEnabled,
		ReferralBonusRate:       common.AffiliateRewardRate,
		ReferralBonusFreezeDays: common.AffiliateRewardFreezeDays,
	}
}

func matureAffiliateRewards(userID int) {
	rewardService, err := affiliateport.Default()
	if err != nil {
		return
	}
	_, _ = rewardService.Mature(userID)
}

// GetWalletSummary 只公开 paid 与 bonus；旧 affiliate 字段由迁移清零并停止暴露。
func GetWalletSummary(c *gin.Context) {
	userId := c.GetInt("id")
	matureAffiliateRewards(userId)
	paid, bonus, _, err := model.GetUserAccountBalances(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"total": paid + bonus,
			"paid":  paid,
			"bonus": bonus,
			"rules": walletRuleSummary(),
		},
	})
}

type LedgerRowDTO struct {
	Id            int    `json:"id"`
	Account       string `json:"account"`
	Delta         int    `json:"delta"`
	BalanceBefore int    `json:"before"`
	BalanceAfter  int    `json:"after"`
	TotalBefore   int    `json:"total_before"`
	TotalAfter    int    `json:"total_after"`
	Reason        string `json:"reason"`
	ReferenceType string `json:"reference_type"`
	ReferenceId   string `json:"reference"`
	RequestId     string `json:"request_id"`
	OperatorId    int    `json:"operator_id"`
	CreatedAt     int64  `json:"created_at"`
}

func GetWalletLedger(c *gin.Context) {
	userId := c.GetInt("id")
	account := c.Query("account")
	if account != "" && account != string(model.AccountPaid) && account != string(model.AccountBonus) {
		common.ApiErrorMsg(c, "account 仅支持 paid 或 bonus")
		return
	}
	filter := model.LedgerQueryFilter{
		UserId:          userId,
		Account:         account,
		AllowedAccounts: []model.LedgerAccount{model.AccountPaid, model.AccountBonus},
		Reason:          c.Query("reason"),
		Page:            getPageParam(c),
		PageSize:        getPageSizeParam(c),
	}
	if v := c.Query("start_time"); v != "" {
		ts, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			common.ApiErrorMsg(c, "start_time 无效")
			return
		}
		filter.StartTime = ts
	}
	if v := c.Query("end_time"); v != "" {
		ts, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			common.ApiErrorMsg(c, "end_time 无效")
			return
		}
		filter.EndTime = ts
	}
	page, err := model.QueryUserLedger(filter)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	rows := make([]LedgerRowDTO, 0, len(page.Rows))
	for _, row := range page.Rows {
		rows = append(rows, LedgerRowDTO{
			Id: row.Id, Account: row.Account, Delta: row.Delta,
			BalanceBefore: row.BalanceBefore, BalanceAfter: row.BalanceAfter,
			TotalBefore: row.TotalBefore, TotalAfter: row.TotalAfter,
			Reason: row.Reason, ReferenceType: row.ReferenceType,
			ReferenceId: row.ReferenceId, RequestId: row.RequestId,
			OperatorId: row.OperatorId, CreatedAt: row.CreatedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    gin.H{"items": rows, "total": page.Total},
	})
}

func GetReferralOverview(c *gin.Context) {
	userId := c.GetInt("id")
	matureAffiliateRewards(userId)
	overview, err := model.GetAffiliateOverview(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	user, err := model.GetUserById(userId, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"aff_code":             user.AffCode,
			"inviter_id":           user.InviterId,
			"invite_count":         user.AffCount,
			"pending_count":        overview.PendingCount,
			"eligible_paid_quota":  overview.EligiblePaidQuota,
			"pending_bonus_quota":  overview.PendingBonusQuota,
			"credited_bonus_quota": overview.CreditedBonusQuota,
			"total_bonus_quota":    overview.TotalBonusQuota,
			"reward_rate":          common.AffiliateRewardRate,
			"freeze_days":          common.AffiliateRewardFreezeDays,
			"rules":                walletRuleSummary(),
		},
	})
}

func GetReferralRewards(c *gin.Context) {
	userId := c.GetInt("id")
	matureAffiliateRewards(userId)
	rewards, total, err := model.ListAffiliateRewards(userId, getPageParam(c), getPageSizeParam(c))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    gin.H{"items": rewards, "total": total},
	})
}

func getPageParam(c *gin.Context) int {
	page, err := strconv.Atoi(c.DefaultQuery("p", "1"))
	if err != nil || page <= 0 {
		return 1
	}
	return page
}

func getPageSizeParam(c *gin.Context) int {
	size, err := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if err != nil || size <= 0 {
		return 20
	}
	if size > 200 {
		return 200
	}
	return size
}
