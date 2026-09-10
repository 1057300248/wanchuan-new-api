package service

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPostConsumeQuotaIdempotencyCoversWalletAndToken(t *testing.T) {
	truncate(t)
	seedBonusEligibleModel(t, "idempotent-paid", boolPtr(false))
	const (
		userID    = 9440
		tokenID   = 9441
		tokenKey  = "idempotent-token"
		requestID = "idempotent-request"
	)
	seedBonusUser(t, userID, 0, 10_000)
	seedToken(t, tokenID, userID, tokenKey, 10_000)
	relayInfo := &relaycommon.RelayInfo{
		UserId:          userID,
		TokenId:         tokenID,
		TokenKey:        tokenKey,
		RequestId:       requestID,
		OriginModelName: "idempotent-paid",
		BillingSource:   BillingSourceWallet,
	}

	require.NoError(t, PostConsumeQuota(relayInfo, 100, 0, false))
	require.NoError(t, PostConsumeQuota(relayInfo, 100, 0, false))

	assertUserBalances(t, userID, 0, 9900, 0)
	var token model.Token
	require.NoError(t, model.DB.First(&token, tokenID).Error)
	assert.Equal(t, 9900, token.RemainQuota)
	assert.Equal(t, 100, token.UsedQuota)
	assert.Len(t, ledgerRowsFor(t, userID), 1)
}

func TestPostConsumeQuotaRefundIdempotencyCoversWalletAndToken(t *testing.T) {
	truncate(t)
	seedBonusEligibleModel(t, "idempotent-refund", boolPtr(false))
	const (
		userID    = 9445
		tokenID   = 9446
		tokenKey  = "idempotent-refund-token"
		requestID = "idempotent-refund-request"
	)
	seedBonusUser(t, userID, 0, 9900)
	seedToken(t, tokenID, userID, tokenKey, 9900)
	require.NoError(t, model.DB.Model(&model.Token{}).Where("id = ?", tokenID).Update("used_quota", 100).Error)
	relayInfo := &relaycommon.RelayInfo{
		UserId:          userID,
		TokenId:         tokenID,
		TokenKey:        tokenKey,
		RequestId:       requestID,
		OriginModelName: "idempotent-refund",
		BillingSource:   BillingSourceWallet,
	}

	require.NoError(t, PostConsumeQuota(relayInfo, -100, 0, false))
	require.NoError(t, PostConsumeQuota(relayInfo, -100, 0, false))

	assertUserBalances(t, userID, 0, 10_000, 0)
	var token model.Token
	require.NoError(t, model.DB.First(&token, tokenID).Error)
	assert.Equal(t, 10_000, token.RemainQuota)
	assert.Zero(t, token.UsedQuota)
	assert.Len(t, ledgerRowsFor(t, userID), 1)
}

func TestPostConsumeQuotaRollsBackWalletWhenTokenUpdateFails(t *testing.T) {
	truncate(t)
	seedBonusEligibleModel(t, "atomic-paid", boolPtr(false))
	const userID = 9450
	seedBonusUser(t, userID, 0, 10_000)
	relayInfo := &relaycommon.RelayInfo{
		UserId:          userID,
		TokenId:         999_999,
		TokenKey:        "missing-token",
		RequestId:       "atomic-failure-request",
		OriginModelName: "atomic-paid",
		BillingSource:   BillingSourceWallet,
	}

	err := PostConsumeQuota(relayInfo, 100, 0, false)
	require.Error(t, err)
	assertUserBalances(t, userID, 0, 10_000, 0)
	assert.Empty(t, ledgerRowsFor(t, userID))
}
