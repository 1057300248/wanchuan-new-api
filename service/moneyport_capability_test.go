package service

// moneyport_capability_test.go 锁定 BillingSession 按 moneyport 能力（而非具体
// 资金来源类型）分派后的语义等价性：补充预扣的账本序列、订阅增量换算、
// 扣减回执契约，以及能力缺失时的 fail-closed 行为。

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ledgerRow 是账本条目中与计费语义相关的字段投影。
type ledgerRow struct {
	Account        string
	Delta          int
	Reason         string
	IdempotencyKey string
}

func ledgerRowsFor(t *testing.T, userID int) []ledgerRow {
	t.Helper()
	var entries []model.BalanceLedger
	require.NoError(t, model.DB.Where("user_id = ?", userID).Order("id asc").Find(&entries).Error)
	rows := make([]ledgerRow, 0, len(entries))
	for _, entry := range entries {
		rows = append(rows, ledgerRow{
			Account:        entry.Account,
			Delta:          entry.Delta,
			Reason:         entry.Reason,
			IdempotencyKey: entry.IdempotencyKey,
		})
	}
	return rows
}

// fundingWithoutCapabilities 只实现 FundingSource，不具备任何 moneyport 能力，
// 用于验证能力缺失时 BillingSession 必须 fail-closed 而非静默降级。
type fundingWithoutCapabilities struct{}

func (fundingWithoutCapabilities) Source() string       { return "stub" }
func (fundingWithoutCapabilities) PreConsume(int) error { return nil }
func (fundingWithoutCapabilities) Settle(int) error     { return nil }
func (fundingWithoutCapabilities) Refund() error        { return nil }

// ---------------------------------------------------------------------------
// 钱包：逐级补充预扣的账本序列必须与类型断言时期完全一致
// ---------------------------------------------------------------------------

func TestWalletReserveLedgerSequenceUnchanged(t *testing.T) {
	truncate(t)

	const userID = 8810
	const requestID = "moneyport-wallet-reserve-1"
	seedBonusUser(t, userID, 0, 20_000)

	funding := &WalletFunding{
		userId:     userID,
		requestId:  requestID,
		debitOrder: []model.LedgerAccount{model.AccountPaid},
	}
	require.NoError(t, funding.PreConsume(2_000))

	session := &BillingSession{
		relayInfo:        &relaycommon.RelayInfo{IsPlayground: true},
		funding:          funding,
		preConsumedQuota: 2_000,
		tokenConsumed:    2_000,
	}

	// 逐级抬升目标：每级只补差额，幂等键由目标预扣值派生。
	require.NoError(t, session.Reserve(5_000))
	require.NoError(t, session.Reserve(8_000))
	// 重复同一目标与下降目标都必须是 no-op，不得新增账本条目。
	require.NoError(t, session.Reserve(8_000))
	require.NoError(t, session.Reserve(3_000))

	assert.Equal(t, []ledgerRow{
		{Account: "paid", Delta: -2_000, Reason: model.LedgerReasonBillingPreConsume, IdempotencyKey: "billing:preconsume:" + requestID},
		{Account: "paid", Delta: -3_000, Reason: model.LedgerReasonBillingReserve, IdempotencyKey: "billing:reserve:" + requestID + ":5000"},
		{Account: "paid", Delta: -3_000, Reason: model.LedgerReasonBillingReserve, IdempotencyKey: "billing:reserve:" + requestID + ":8000"},
	}, ledgerRowsFor(t, userID))

	assert.Equal(t, 12_000, getUserQuota(t, userID))
	assert.Equal(t, 8_000, funding.consumed)
	assert.Equal(t, 8_000, session.preConsumedQuota)
	assert.Equal(t, 6_000, session.extraReserved)
}

// ---------------------------------------------------------------------------
// 钱包回执：顺序、值、防御性复制，以及 service 边界的 DTO 还原
// ---------------------------------------------------------------------------

func TestWalletReceiptOrderValuesAndDefensiveCopy(t *testing.T) {
	truncate(t)

	const userID = 8811
	seedBonusUser(t, userID, 1_200, 5_000)

	funding := &WalletFunding{
		userId:     userID,
		requestId:  "moneyport-wallet-receipt-1",
		debitOrder: []model.LedgerAccount{model.AccountBonus, model.AccountPaid},
	}
	// 预扣 2000 跨两账户：bonus 1200 用尽后由 paid 补 800。
	require.NoError(t, funding.PreConsume(2_000))

	want := []moneyport.AccountShare{
		{Account: "bonus", Amount: 1_200},
		{Account: "paid", Amount: 800},
	}
	receipt := funding.Receipt()
	assert.Equal(t, want, receipt, "回执必须按 debitOrder 顺序输出各账户份额")

	// 防御性复制：调用方改写返回值不得污染资金来源的内部份额记录。
	receipt[0].Amount = 999_999
	receipt[1].Account = "tampered"
	assert.Equal(t, want, funding.Receipt(), "回执必须是副本")

	// service 边界把叶子 DTO 还原为账本份额，外部契约保持 model.LedgerShare。
	session := &BillingSession{relayInfo: &relaycommon.RelayInfo{IsPlayground: true}, funding: funding}
	wantShares := []model.LedgerShare{
		{Account: model.AccountBonus, Amount: 1_200},
		{Account: model.AccountPaid, Amount: 800},
	}
	assert.Equal(t, wantShares, session.WalletDebitShares())
	assert.Equal(t, wantShares, WalletSessionDebitShares(&relaycommon.RelayInfo{Billing: session}),
		"RelayInfo 路径必须与会话直接取值一致")
}

func TestReceiptNilForFundingWithoutReceiptCapability(t *testing.T) {
	// 订阅不按账户拆分扣减，没有回执能力：必须返回 nil 而不是空切片，
	// 保证 TaskPrivateData.WalletDebit 在订阅计费下保持不落字段。
	subSession := &BillingSession{
		relayInfo: &relaycommon.RelayInfo{IsPlayground: true},
		funding:   &SubscriptionFunding{subscriptionId: 1, preConsumed: 100},
	}
	assert.Nil(t, subSession.Receipt())
	assert.Nil(t, subSession.WalletDebitShares())
	assert.Nil(t, WalletSessionDebitShares(&relaycommon.RelayInfo{Billing: subSession}))

	// 无回执能力的资金来源同样返回 nil。
	assert.Nil(t, (&BillingSession{funding: fundingWithoutCapabilities{}}).WalletDebitShares())

	// 无会话 / 非 BillingSession 计费实现：保持既有 nil 契约。
	assert.Nil(t, WalletSessionDebitShares(nil))
	assert.Nil(t, WalletSessionDebitShares(&relaycommon.RelayInfo{}))

	// 钱包未发生任何扣减时也必须是 nil，不能是长度为 0 的切片。
	assert.Nil(t, (&BillingSession{funding: &WalletFunding{}}).WalletDebitShares())
}

// ---------------------------------------------------------------------------
// 订阅：Reserve 按目标值换算增量，且不与 RelayInfo 同步值双计
// ---------------------------------------------------------------------------

func TestSubscriptionReserveUsesTargetDeltaWithoutDoubleCount(t *testing.T) {
	truncate(t)

	const userID, subID = 8812, 5501
	seedBonusUser(t, userID, 0, 1_000)
	// 订阅已预扣 2000，模拟 PreConsume 完成后的状态。
	seedSubscription(t, subID, userID, 100_000, 2_000)

	funding := &SubscriptionFunding{
		requestId:       "moneyport-sub-reserve-1",
		userId:          userID,
		modelName:       "test-model",
		amount:          2_000,
		subscriptionId:  subID,
		preConsumed:     2_000,
		AmountTotal:     100_000,
		AmountUsedAfter: 2_000,
	}
	relayInfo := &relaycommon.RelayInfo{IsPlayground: true}
	session := &BillingSession{
		relayInfo:        relayInfo,
		funding:          funding,
		preConsumedQuota: 2_000,
		tokenConsumed:    2_000,
	}

	// 只补 target 与预扣基线（amount + reserveDelta）的差额。
	require.NoError(t, session.Reserve(5_000))
	assert.EqualValues(t, 5_000, getSubscriptionUsed(t, subID))

	// 重复同一 target 与下降 target 都是 no-op，不得再动订阅用量。
	require.NoError(t, session.Reserve(5_000))
	require.NoError(t, session.Reserve(1_000))
	assert.EqualValues(t, 5_000, getSubscriptionUsed(t, subID))

	require.NoError(t, session.Reserve(8_000))
	assert.EqualValues(t, 8_000, getSubscriptionUsed(t, subID))

	// 资金来源自身也必须守住 no-op：绕过会话直接调用不得重复补扣。
	require.NoError(t, funding.Reserve(8_000))
	require.NoError(t, funding.Reserve(4_000))
	assert.EqualValues(t, 8_000, getSubscriptionUsed(t, subID))

	// RelayInfo 同步值只由 BillingSession.extraReserved 累加，
	// 订阅侧的 reserveDelta 不得再叠加一次（否则预扣值会翻倍）。
	assert.Equal(t, 6_000, session.extraReserved)
	assert.Equal(t, 6_000, funding.reserveDelta)
	assert.EqualValues(t, 8_000, relayInfo.SubscriptionPreConsumed)
	assert.EqualValues(t, 8_000, relayInfo.SubscriptionAmountUsedAfterPreConsume)
	assert.Equal(t, 8_000, relayInfo.FinalPreConsumedQuota)
	assert.Equal(t, subID, relayInfo.SubscriptionId)
}

// ---------------------------------------------------------------------------
// 退款判定：OutstandingReserve 能力缺失视为 0
// ---------------------------------------------------------------------------

func TestNeedsRefundUsesOutstandingReserveCapability(t *testing.T) {
	newSession := func(funding FundingSource) *BillingSession {
		return &BillingSession{relayInfo: &relaycommon.RelayInfo{IsPlayground: true}, funding: funding}
	}

	// 订阅报告了待退预扣：tokenConsumed=0 时仍需退款。
	assert.True(t, newSession(&SubscriptionFunding{preConsumed: 1_500}).NeedsRefund())
	// 订阅无待退预扣：无需退款。
	assert.False(t, newSession(&SubscriptionFunding{}).NeedsRefund())

	// 钱包不报告待退预扣（能力缺失视为 0）：预扣状态只由 tokenConsumed 反映，
	// 否则失败请求会对已由令牌路径覆盖的预扣重复退款。
	walletSession := newSession(&WalletFunding{consumed: 2_000})
	assert.False(t, walletSession.NeedsRefund())
	walletSession.tokenConsumed = 2_000
	assert.True(t, walletSession.NeedsRefund())

	// 完全无能力的资金来源同样视为 0。
	assert.False(t, newSession(fundingWithoutCapabilities{}).NeedsRefund())

	// 已结算 / 已退款 / 资金已提交的会话不再需要退款。
	for _, mutate := range []func(*BillingSession){
		func(s *BillingSession) { s.settled = true },
		func(s *BillingSession) { s.refunded = true },
		func(s *BillingSession) { s.fundingSettled = true },
	} {
		session := newSession(&SubscriptionFunding{preConsumed: 1_500})
		mutate(session)
		assert.False(t, session.NeedsRefund())
	}
}

// ---------------------------------------------------------------------------
// 能力缺失 fail-closed：Reserve 明确报错并回滚已扣令牌额度
// ---------------------------------------------------------------------------

func TestReserveFailsClosedWithoutReservableCapability(t *testing.T) {
	truncate(t)

	const userID, tokenID = 8813, 7701
	const tokenKey = "moneyport-stub-token"
	seedBonusUser(t, userID, 0, 20_000)
	seedToken(t, tokenID, userID, tokenKey, 10_000)

	session := &BillingSession{
		relayInfo: &relaycommon.RelayInfo{
			UserId:   userID,
			TokenId:  tokenID,
			TokenKey: tokenKey,
		},
		funding:          fundingWithoutCapabilities{},
		preConsumedQuota: 2_000,
		tokenConsumed:    2_000,
	}

	err := session.Reserve(5_000)
	require.Error(t, err, "资金来源缺少 Reserve 能力必须明确失败，不得静默放行")

	var apiErr *types.NewAPIError
	require.True(t, errors.As(err, &apiErr))
	assert.Equal(t, types.ErrorCodeUpdateDataError, apiErr.GetErrorCode())
	assert.Contains(t, err.Error(), "unsupported funding source: stub")

	// 先令牌后资金：资金侧失败必须回滚已扣令牌额度，且会话状态不前进。
	assert.Equal(t, 10_000, getTokenRemainQuota(t, tokenID), "令牌额度必须回滚")
	assert.Equal(t, 2_000, session.preConsumedQuota)
	assert.Equal(t, 2_000, session.tokenConsumed)
	assert.Zero(t, session.extraReserved)
}
