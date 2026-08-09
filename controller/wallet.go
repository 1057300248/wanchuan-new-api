package controller

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// TokenMix P1-2 / P1-3：钱包分账、账本与推荐返利 API
// ---------------------------------------------------------------------------

// WalletRuleSummary 钱包/返利可运营规则摘要（配置快照）。
type WalletRuleSummary struct {
	SignupBonusEnabled        bool    `json:"signup_bonus_enabled"`
	SignupBonusQuota          int     `json:"signup_bonus_quota"`
	AffiliateRewardEnabled    bool    `json:"affiliate_reward_enabled"`
	AffiliateRewardRate       float64 `json:"affiliate_reward_rate"`
	AffiliateFreezeDays       int     `json:"affiliate_freeze_days"`
	AffiliateMinWithdrawQuota int     `json:"affiliate_min_withdraw_quota"`
}

func walletRuleSummary() WalletRuleSummary {
	return WalletRuleSummary{
		SignupBonusEnabled:        common.SignupBonusEnabled,
		SignupBonusQuota:          common.QuotaForSignupBonus,
		AffiliateRewardEnabled:    common.AffiliateRewardEnabled,
		AffiliateRewardRate:       common.AffiliateRewardRate,
		AffiliateFreezeDays:       common.AffiliateRewardFreezeDays,
		AffiliateMinWithdrawQuota: common.AffiliateMinWithdrawQuota,
	}
}

// GetWalletSummary 返回登录用户钱包汇总：资产总额、可消费余额、paid/bonus/affiliate 与规则摘要。
// 读取前先幂等成熟到期返利（lazy release）。
func GetWalletSummary(c *gin.Context) {
	userId := c.GetInt("id")
	_, _ = service.MatureAffiliateRewards(userId)
	paid, bonus, affiliate, err := model.GetUserAccountBalances(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	total := paid + bonus + affiliate
	spendable := paid + bonus
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"total":        total,
			"spendable":    spendable,
			"withdrawable": affiliate,
			"paid":         paid,
			"bonus":        bonus,
			"affiliate":    affiliate,
			"rules":        walletRuleSummary(),
		},
	})
}

// LedgerRowDTO 账本明细返回结构。
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

// GetWalletLedger 分页查询登录用户自己的 balance_ledger（强制 user_id 过滤）。
// 支持 account/reason/时间范围过滤。
func GetWalletLedger(c *gin.Context) {
	userId := c.GetInt("id")
	filter := model.LedgerQueryFilter{
		UserId:   userId,
		Account:  c.Query("account"),
		Reason:   c.Query("reason"),
		Page:     getPageParam(c),
		PageSize: getPageSizeParam(c),
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
			Id:            row.Id,
			Account:       row.Account,
			Delta:         row.Delta,
			BalanceBefore: row.BalanceBefore,
			BalanceAfter:  row.BalanceAfter,
			TotalBefore:   row.TotalBefore,
			TotalAfter:    row.TotalAfter,
			Reason:        row.Reason,
			ReferenceType: row.ReferenceType,
			ReferenceId:   row.ReferenceId,
			RequestId:     row.RequestId,
			OperatorId:    row.OperatorId,
			CreatedAt:     row.CreatedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"items": rows,
			"total": page.Total,
		},
	})
}

// GetReferralOverview 用户推荐概览：邀请码/链接、规则、pending/available/withdrawn。
func GetReferralOverview(c *gin.Context) {
	userId := c.GetInt("id")
	_, _ = service.MatureAffiliateRewards(userId)
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
			"aff_code":           user.AffCode,
			"inviter_id":         user.InviterId,
			"invite_count":       user.AffCount,
			"pending_count":      overview.PendingCount,
			"pending_quota":      overview.PendingQuota,
			"available_quota":    overview.AvailableQuota,
			"withdrawn_quota":    overview.WithdrawnQuota,
			"total_earned_quota": overview.TotalEarnedQuota,
			"rules":              walletRuleSummary(),
		},
	})
}

// GetReferralRewards 分页查询用户返利记录。
func GetReferralRewards(c *gin.Context) {
	userId := c.GetInt("id")
	_, _ = service.MatureAffiliateRewards(userId)
	rewards, total, err := model.ListAffiliateRewards(userId, getPageParam(c), getPageSizeParam(c))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"items": rewards,
			"total": total,
		},
	})
}

// PostMatureReferralRewards 显式触发到期返利成熟（幂等）。
func PostMatureReferralRewards(c *gin.Context) {
	userId := c.GetInt("id")
	matured, err := service.MatureAffiliateRewards(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    gin.H{"matured": matured},
	})
}

// WithdrawalCreateRequest 提现申请请求体。
type WithdrawalCreateRequest struct {
	Amount    int    `json:"amount"`
	RequestId string `json:"request_id"`
}

// PostReferralWithdrawal 提交提现申请：冻结扣减可用 affiliate 余额，进入人工审核流程。
func PostReferralWithdrawal(c *gin.Context) {
	userId := c.GetInt("id")
	if !common.AffiliateRewardEnabled {
		common.ApiErrorMsg(c, "提现功能未启用")
		return
	}
	var request WithdrawalCreateRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	request.RequestId = strings.TrimSpace(request.RequestId)
	if request.RequestId == "" {
		common.ApiErrorMsg(c, "缺少请求 ID（request_id）")
		return
	}
	if len(request.RequestId) > 128 {
		common.ApiErrorMsg(c, "请求 ID 过长")
		return
	}
	minQuota := common.AffiliateMinWithdrawQuota
	if request.Amount < minQuota {
		common.ApiErrorMsg(c, fmt.Sprintf("提现金额低于最低限额（%d quota units）", minQuota))
		return
	}
	withdrawal, replayed, err := model.CreateAffiliateWithdrawal(userId, request.Amount, request.RequestId)
	if err != nil {
		if errors.Is(err, model.ErrLedgerInsufficientBalance) {
			common.ApiErrorMsg(c, "可用返利余额不足")
			return
		}
		common.ApiErrorMsg(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"id":       withdrawal.Id,
			"status":   withdrawal.Status,
			"replayed": replayed,
		},
	})
}

// GetReferralWithdrawals 分页查询登录用户自己的提现申请。
func GetReferralWithdrawals(c *gin.Context) {
	userId := c.GetInt("id")
	withdrawals, total, err := model.ListAffiliateWithdrawals(model.WithdrawalQueryFilter{
		UserId:   userId,
		Status:   c.Query("status"),
		Page:     getPageParam(c),
		PageSize: getPageSizeParam(c),
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"items": withdrawals,
			"total": total,
		},
	})
}

// ---------------------------------------------------------------------------
// 管理员：提现审核（人工审核账本流程，不接外部支付渠道）
// ---------------------------------------------------------------------------

// AdminListWithdrawals 管理员分页查询全部提现申请（可按状态过滤）。
func AdminListWithdrawals(c *gin.Context) {
	withdrawals, total, err := model.ListAffiliateWithdrawals(model.WithdrawalQueryFilter{
		Status:   c.Query("status"),
		Page:     getPageParam(c),
		PageSize: getPageSizeParam(c),
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"items": withdrawals,
			"total": total,
		},
	})
}

// AdminApproveWithdrawal 审批提现申请（幂等）：标记人工支付待办，余额已在申请时冻结。
func AdminApproveWithdrawal(c *gin.Context) {
	withdrawalId, err := getWithdrawalIdParam(c)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	if err := model.ApproveAffiliateWithdrawal(withdrawalId, c.GetInt("id")); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

// AdminRejectWithdrawal 拒绝提现申请（幂等）：原路退回 affiliate 余额。
func AdminRejectWithdrawal(c *gin.Context) {
	withdrawalId, err := getWithdrawalIdParam(c)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	var request struct {
		Reason string `json:"reason"`
	}
	_ = common.DecodeJson(c.Request.Body, &request)
	if err := model.RejectAffiliateWithdrawal(withdrawalId, c.GetInt("id"), strings.TrimSpace(request.Reason)); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

// AdminPayWithdrawal 确认人工支付完成（幂等）。
func AdminPayWithdrawal(c *gin.Context) {
	withdrawalId, err := getWithdrawalIdParam(c)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	if err := model.MarkAffiliateWithdrawalPaid(withdrawalId, c.GetInt("id")); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

func getWithdrawalIdParam(c *gin.Context) (int, error) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("无效的提现申请 ID")
	}
	return id, nil
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
