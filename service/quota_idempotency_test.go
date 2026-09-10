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

func TestBillingSessionWalletSettleRollsBackFundingWhenTokenUpdateFails(t *testing.T) {
	truncate(t)
	const (
		userID      = 9460
		tokenID     = 9461
		tokenKey    = "billing-session-atomic-token"
		requestID   = "billing-session-atomic-request"
		preConsumed = 2_000
		actualQuota = 3_000
	)
	seedBonusUser(t, userID, 0, 10_000)
	seedToken(t, tokenID, userID, tokenKey, 10_000)

	funding := &WalletFunding{
		userId:     userID,
		requestId:  requestID,
		debitOrder: []model.LedgerAccount{model.AccountPaid},
	}
	require.NoError(t, funding.PreConsume(preConsumed))
	require.NoError(t, model.DecreaseTokenQuota(tokenID, tokenKey, preConsumed))

	session := &BillingSession{
		relayInfo: &relaycommon.RelayInfo{
			UserId:        userID,
			TokenId:       tokenID,
			TokenKey:      tokenKey,
			RequestId:     requestID,
			BillingSource: BillingSourceWallet,
		},
		funding:          funding,
		preConsumedQuota: preConsumed,
		tokenConsumed:    preConsumed,
	}

	const trigger = "fail_billing_session_token_update"
	require.NoError(t, model.DB.Exec(`
		CREATE TRIGGER `+trigger+`
		BEFORE UPDATE ON tokens
		WHEN OLD.id = 9461
		BEGIN
			SELECT RAISE(ABORT, 'forced billing session token failure');
		END;
	`).Error)
	t.Cleanup(func() { model.DB.Exec("DROP TRIGGER IF EXISTS " + trigger) })

	err := session.Settle(actualQuota)
	require.Error(t, err)
	assertUserBalances(t, userID, 0, 8_000, 0)
	assert.Equal(t, 8_000, getTokenRemainQuota(t, tokenID))
	assert.False(t, session.fundingSettled)
	assert.False(t, session.settled)
	assert.True(t, session.NeedsRefund())
	assert.Equal(t, []ledgerRow{{
		Account:        "paid",
		Delta:          -preConsumed,
		Reason:         model.LedgerReasonBillingPreConsume,
		IdempotencyKey: "billing:preconsume:" + requestID,
	}}, ledgerRowsFor(t, userID), "failed atomic settlement must not leave a settle ledger row")

	require.NoError(t, model.DB.Exec("DROP TRIGGER IF EXISTS "+trigger).Error)
	require.NoError(t, session.Settle(actualQuota))
	assertUserBalances(t, userID, 0, 7_000, 0)
	assert.Equal(t, 7_000, getTokenRemainQuota(t, tokenID))
	assert.True(t, session.fundingSettled)
	assert.True(t, session.settled)
	assert.False(t, session.NeedsRefund())
	assert.Equal(t, []ledgerRow{
		{Account: "paid", Delta: -preConsumed, Reason: model.LedgerReasonBillingPreConsume, IdempotencyKey: "billing:preconsume:" + requestID},
		{Account: "paid", Delta: -(actualQuota - preConsumed), Reason: model.LedgerReasonBillingSettle, IdempotencyKey: "billing:settle:" + requestID + ":1000"},
	}, ledgerRowsFor(t, userID))
}

func TestBillingSessionWalletRefundSettleRollsBackFundingWhenTokenUpdateFails(t *testing.T) {
	truncate(t)
	const (
		userID      = 9470
		tokenID     = 9471
		tokenKey    = "billing-session-refund-atomic-token"
		requestID   = "billing-session-refund-atomic-request"
		preConsumed = 3_000
		actualQuota = 1_000
	)
	seedBonusUser(t, userID, 0, 10_000)
	seedToken(t, tokenID, userID, tokenKey, 10_000)

	funding := &WalletFunding{
		userId:     userID,
		requestId:  requestID,
		debitOrder: []model.LedgerAccount{model.AccountPaid},
	}
	require.NoError(t, funding.PreConsume(preConsumed))
	require.NoError(t, model.DecreaseTokenQuota(tokenID, tokenKey, preConsumed))

	session := &BillingSession{
		relayInfo: &relaycommon.RelayInfo{
			UserId:        userID,
			TokenId:       tokenID,
			TokenKey:      tokenKey,
			RequestId:     requestID,
			BillingSource: BillingSourceWallet,
		},
		funding:          funding,
		preConsumedQuota: preConsumed,
		tokenConsumed:    preConsumed,
	}

	const trigger = "fail_billing_session_refund_token_update"
	require.NoError(t, model.DB.Exec(`
		CREATE TRIGGER `+trigger+`
		BEFORE UPDATE ON tokens
		WHEN OLD.id = 9471
		BEGIN
			SELECT RAISE(ABORT, 'forced billing session refund token failure');
		END;
	`).Error)
	t.Cleanup(func() { model.DB.Exec("DROP TRIGGER IF EXISTS " + trigger) })

	err := session.Settle(actualQuota)
	require.Error(t, err)
	assertUserBalances(t, userID, 0, 7_000, 0)
	assert.Equal(t, 7_000, getTokenRemainQuota(t, tokenID))
	assert.False(t, session.fundingSettled)
	assert.False(t, session.settled)
	assert.True(t, session.NeedsRefund())
	assert.Equal(t, []ledgerRow{{
		Account:        "paid",
		Delta:          -preConsumed,
		Reason:         model.LedgerReasonBillingPreConsume,
		IdempotencyKey: "billing:preconsume:" + requestID,
	}}, ledgerRowsFor(t, userID), "failed atomic refund settlement must not leave a refund ledger row")

	require.NoError(t, model.DB.Exec("DROP TRIGGER IF EXISTS "+trigger).Error)
	require.NoError(t, session.Settle(actualQuota))
	assertUserBalances(t, userID, 0, 9_000, 0)
	assert.Equal(t, 9_000, getTokenRemainQuota(t, tokenID))
	assert.True(t, session.fundingSettled)
	assert.True(t, session.settled)
	assert.False(t, session.NeedsRefund())
	assert.Equal(t, []ledgerRow{
		{Account: "paid", Delta: -preConsumed, Reason: model.LedgerReasonBillingPreConsume, IdempotencyKey: "billing:preconsume:" + requestID},
		{Account: "paid", Delta: preConsumed - actualQuota, Reason: model.LedgerReasonBillingSettle, IdempotencyKey: "billing:settle_refund:" + requestID + ":2000"},
	}, ledgerRowsFor(t, userID))
}
