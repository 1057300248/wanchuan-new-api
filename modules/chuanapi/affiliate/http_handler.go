package affiliate

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// GetReferralInvitees lists the direct invitees of the current user together
// with privacy-filtered, aggregate reward progress.  It is intentionally
// scoped to the authenticated user; binding occurs once during registration.
func GetReferralInvitees(c *gin.Context) {
	userId := c.GetInt("id")
	if _, err := ReferralCoinSummary(userId); err != nil {
		common.ApiError(c, err)
		return
	}
	invitees, total, err := model.ListReferralInvitees(userId, getPageParam(c), getPageSizeParam(c))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    gin.H{"items": invitees, "total": total},
	})
}

func GetReferralCoins(c *gin.Context) {
	data, err := ReferralCoinSummary(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": data})
}

func GetReferralCoinRewards(c *gin.Context) {
	userId := c.GetInt("id")
	if _, err := model.MatureReferralCoinRewards(userId); err != nil {
		common.ApiError(c, err)
		return
	}
	rewards, total, err := model.ListReferralCoinRewards(userId, getPageParam(c), getPageSizeParam(c))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{"items": rewards, "total": total}})
}

type referralCoinRedeemRequest struct {
	Coins     int    `json:"coins" binding:"required,gt=0,lte=9007199254740991"`
	RequestId string `json:"request_id" binding:"required,min=8,max=64"`
}

func RedeemReferralCoins(c *gin.Context) {
	var req referralCoinRedeemRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid coins or request_id"})
		return
	}
	if err := RedeemReferralCoin(c.GetInt("id"), req.Coins, req.RequestId); err != nil {
		if errors.Is(err, common.ErrReferralCoinAmount) || errors.Is(err, model.ErrLedgerInvalidEntry) {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid coin amount or precision"})
			return
		}
		if errors.Is(err, model.ErrLedgerIdempotencyConflict) {
			c.JSON(http.StatusConflict, gin.H{"success": false, "message": "redemption request conflict"})
			return
		}
		if err == model.ErrReferralCoinInsufficient {
			common.ApiErrorMsg(c, "邀请 Coin 余额不足")
			return
		}
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{"coins": req.Coins}})
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
	return min(size, 200)
}
