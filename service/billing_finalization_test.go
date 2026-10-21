package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func legacyRefundSession(t *testing.T, request string, sourceHold, tokenHold int) *BillingSession {
	t.Helper()
	truncate(t)
	seedBonusUser(t, 31201, 0, 1000)
	seedToken(t, 31202, 31201, "synthetic-original-refund-token", 1000)
	funding := &WalletFunding{userId: 31201, requestId: request, debitOrder: []model.LedgerAccount{model.AccountPaid}}
	require.NoError(t, funding.PreConsume(sourceHold))
	require.NoError(t, model.DecreaseTokenQuota(31202, "synthetic-original-refund-token", tokenHold))
	return &BillingSession{relayInfo: &relaycommon.RelayInfo{UserId: 31201, TokenId: 31202, TokenKey: "synthetic-original-refund-token", RequestId: request}, funding: funding, preConsumedQuota: sourceHold, tokenConsumed: tokenHold}
}

func waitRefundCallback(t *testing.T, s *BillingSession) {
	t.Helper()
	require.Eventually(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return !s.refundInFlight }, time.Second*3, time.Millisecond)
}

func TestPolicyPreservingRefundSourceFailureNeverRestoresTokenAlone(t *testing.T) {
	s := legacyRefundSession(t, "original-refund-fault", 100, 100)
	reached := make(chan struct{})
	var once sync.Once
	name := "fail_original_refund_source"
	require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(name, func(tx *gorm.DB) {
		if tx.Statement.Table == "users" {
			once.Do(func() { close(reached) })
			tx.AddError(errors.New("synthetic wallet refund failure"))
		}
	}))
	t.Cleanup(func() { model.DB.Callback().Update().Remove(name) })
	s.Refund(testGinContext(t))
	select {
	case <-reached:
	case <-time.After(3 * time.Second):
		t.Fatal("source failure not reached")
	}
	require.True(t, s.NeedsRefund(), "a failed source refund is not successful completion")
	waitRefundCallback(t, s)
	assertUserBalances(t, 31201, 0, 900, 0)
	var token model.Token
	require.NoError(t, model.DB.First(&token, 31202).Error)
	require.Equal(t, 900, token.RemainQuota)
	require.NoError(t, model.DB.Callback().Update().Remove(name))
	// Discard the live session: only committed intent drives restart recovery.
	// Make this failed receipt due explicitly; operational backoff must not be
	// bypassed merely because the underlying source has recovered.
	require.NoError(t, model.DB.Model(&model.BillingFinalization{}).Where("request_id = ?", "original-refund-fault").Update("next_retry_at", 0).Error)
	n, err := model.RecoverPendingBillingRefunds(context.Background(), 100)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.NoError(t, model.RecoverBillingRefund(context.Background(), "original-refund-fault"))
	s.Refund(testGinContext(t))
	waitRefundCallback(t, s)
	require.False(t, s.NeedsRefund())
	assertUserBalances(t, 31201, 0, 1000, 0)
	require.NoError(t, model.DB.First(&token, 31202).Error)
	require.Equal(t, 1000, token.RemainQuota)
}

func TestPolicyPreservingRefundSeparateHeldAmounts(t *testing.T) {
	s := legacyRefundSession(t, "separate-source-token", 100, 70)
	s.Refund(testGinContext(t))
	waitRefundCallback(t, s)
	require.False(t, s.NeedsRefund())
	assertUserBalances(t, 31201, 0, 1000, 0)
	var token model.Token
	require.NoError(t, model.DB.First(&token, 31202).Error)
	require.Equal(t, 1000, token.RemainQuota)
}

func TestPolicyPreservingRefundIntentFailureClosesLocalAdmission(t *testing.T) {
	s := legacyRefundSession(t, "pending-write-fails", 100, 100)
	name := "fail_pending_intent"
	var failOnce sync.Once
	require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(name, func(tx *gorm.DB) {
		if tx.Statement.Table == "billing_finalizations" {
			failOnce.Do(func() { tx.AddError(errors.New("pending write failed")) })
		}
	}))
	t.Cleanup(func() { model.DB.Callback().Update().Remove(name) })
	retrying, releaseRetry := pauseRefundIntentRetry(t)
	useRefundDispatcher(t, 2, 1, time.Second)
	s.Refund(testGinContext(t))
	awaitRefundSignal(t, retrying)
	require.True(t, s.NeedsRefund())
	require.ErrorIs(t, s.Reserve(200), model.ErrBillingFinalized)
	require.ErrorIs(t, s.Settle(100), model.ErrBillingFinalized)
	var n int64
	require.NoError(t, model.DB.Model(&model.BillingFinalization{}).Count(&n).Error)
	require.Zero(t, n)
	assertUserBalances(t, 31201, 0, 900, 0)
	releaseRetry()
	waitRefundCallback(t, s)
	require.False(t, s.NeedsRefund())
	assertUserBalances(t, 31201, 0, 1000, 0)
	var token model.Token
	require.NoError(t, model.DB.First(&token, 31202).Error)
	require.Equal(t, 1000, token.RemainQuota)
}

func TestPolicyPreservingSettlementFinalMoreLessAndReplay(t *testing.T) {
	for _, actual := range []int{0, 100, 180} {
		t.Run(string(rune('a'+actual)), func(t *testing.T) {
			s := legacyRefundSession(t, "legacy-final-adjustment", 100, 100)
			require.NoError(t, s.Settle(actual))
			require.NoError(t, s.Settle(actual))
			require.Error(t, s.Settle(actual+1))
			s.Refund(testGinContext(t))
			require.False(t, s.NeedsRefund())
			assertUserBalances(t, 31201, 0, 1000-actual, 0)
			var token model.Token
			require.NoError(t, model.DB.First(&token, 31202).Error)
			require.Equal(t, 1000-actual, token.RemainQuota)
		})
	}
}

func TestPolicyPreservingFailedSettlementCanReserveThenRefund(t *testing.T) {
	s := legacyRefundSession(t, "failed-settle-then-reserve", 100, 100)
	name := "fail_settle_token"
	require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(name, func(tx *gorm.DB) {
		if tx.Statement.Table == "tokens" {
			tx.AddError(errors.New("synthetic settlement token failure"))
		}
	}))
	require.Error(t, s.Settle(200))
	assertUserBalances(t, 31201, 0, 900, 0)
	require.NoError(t, model.DB.Callback().Update().Remove(name))
	require.NoError(t, s.Reserve(150))
	s.Refund(testGinContext(t))
	waitRefundCallback(t, s)
	require.False(t, s.NeedsRefund())
	assertUserBalances(t, 31201, 0, 1000, 0)
	var token model.Token
	require.NoError(t, model.DB.First(&token, 31202).Error)
	require.Equal(t, 1000, token.RemainQuota)
}

// Deliberately no HostBillingFinalizationSource marker: extension ownership is
// not inferred from Source()==wallet, even when it implements legacy Tx settle.
type unmarkedTransactionalWallet struct{ inner *WalletFunding }

func (f *unmarkedTransactionalWallet) Source() string         { return BillingSourceWallet }
func (f *unmarkedTransactionalWallet) PreConsume(n int) error { return f.inner.PreConsume(n) }
func (f *unmarkedTransactionalWallet) Settle(n int) error     { return f.inner.Settle(n) }
func (f *unmarkedTransactionalWallet) Refund() error          { return f.inner.Refund() }
func (f *unmarkedTransactionalWallet) SettleInTx(tx *gorm.DB, n int) (moneyport.SettlementResult, error) {
	return f.inner.SettleInTx(tx, n)
}
func (f *unmarkedTransactionalWallet) ApplySettlementResult(n int, r moneyport.SettlementResult) {
	f.inner.ApplySettlementResult(n, r)
}

type unmarkedSubscriptionFunding struct{ streamFailureFunding }

func (*unmarkedSubscriptionFunding) Source() string { return BillingSourceSubscription }

type alternateBillingSettler struct{}

func (alternateBillingSettler) Settle(int) error              { return nil }
func (alternateBillingSettler) Refund(*gin.Context)           {}
func (alternateBillingSettler) NeedsRefund() bool             { return false }
func (alternateBillingSettler) GetPreConsumedQuota() int      { return 0 }
func (alternateBillingSettler) Reserve(int) error             { return nil }
func (alternateBillingSettler) ReserveBeforeSubmit(int) error { return nil }

func TestPolicyPreservingExtensionCapabilityBoundary(t *testing.T) {
	t.Run("custom_wallet_atomic_replay", func(t *testing.T) {
		s := legacyRefundSession(t, "custom-wallet-original-contract", 100, 100)
		inner := s.funding.(*WalletFunding)
		s.funding = &unmarkedTransactionalWallet{inner: inner}
		require.False(t, s.durableTerminalSource())
		require.NoError(t, s.Settle(150))
		replay := &BillingSession{relayInfo: s.relayInfo, funding: &unmarkedTransactionalWallet{inner: inner}, preConsumedQuota: 100, tokenConsumed: 100}
		require.NoError(t, replay.Settle(150))
		assertUserBalances(t, 31201, 0, 850, 0)
		var token model.Token
		require.NoError(t, model.DB.First(&token, 31202).Error)
		require.Equal(t, 850, token.RemainQuota)
		var count int64
		require.NoError(t, model.DB.Model(&model.BillingFinalization{}).Count(&count).Error)
		require.Zero(t, count)
	})
	t.Run("custom_billing_settler_insert", func(t *testing.T) {
		truncate(t)
		task := &model.Task{TaskID: "custom-settler-task", UserId: 991, Data: []byte(`{"inline":true}`)}
		info := &relaycommon.RelayInfo{Billing: alternateBillingSettler{}}
		require.NoError(t, PersistTaskWithBillingOwner(context.Background(), info, task, "data"))
		require.Positive(t, task.ID)
		require.NotEmpty(t, task.Data)
		var stored model.Task
		require.NoError(t, model.DB.First(&stored, task.ID).Error)
		require.Empty(t, stored.Data)
		var count int64
		require.NoError(t, model.DB.Model(&model.BillingFinalization{}).Count(&count).Error)
		require.Zero(t, count)
	})
	t.Run("custom_subscription_delta", func(t *testing.T) {
		funding := &unmarkedSubscriptionFunding{streamFailureFunding: streamFailureFunding{outstanding: 100}}
		info := &relaycommon.RelayInfo{IsPlayground: true}
		s := &BillingSession{relayInfo: info, funding: funding, preConsumedQuota: 100}
		require.False(t, s.durableTerminalSource())
		require.NoError(t, s.Settle(60))
		require.NoError(t, s.Settle(60))
		require.EqualValues(t, -40, info.SubscriptionPostDelta)
		settleCalls, refundCalls := funding.counts()
		require.Equal(t, 1, settleCalls)
		require.Zero(t, refundCalls)
	})
	t.Run("custom_non_wallet", TestStreamFailurePartialUsageSettlesOnceAndNeverRefunds)
}

func TestPolicyPreservingRollbackProbeFailureDoesNotReserve(t *testing.T) {
	s := legacyRefundSession(t, "probe-failure", 100, 100)
	name := "fail_settlement_token_before_probe"
	require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(name, func(tx *gorm.DB) {
		if tx.Statement.Table == "tokens" {
			tx.AddError(errors.New("token unavailable"))
		}
	}))
	require.Error(t, s.Settle(200))
	require.NoError(t, model.DB.Callback().Update().Remove(name))
	name = "fail_probe_create"
	require.NoError(t, model.DB.Callback().Create().Before("gorm:create").Register(name, func(tx *gorm.DB) {
		if tx.Statement.Table == "billing_finalizations" {
			tx.AddError(errors.New("probe unavailable"))
		}
	}))
	require.ErrorContains(t, s.Reserve(150), "probe unavailable")
	require.NoError(t, model.DB.Callback().Create().Remove(name))
	assertUserBalances(t, 31201, 0, 900, 0)
	var token model.Token
	require.NoError(t, model.DB.First(&token, 31202).Error)
	require.Equal(t, 900, token.RemainQuota)
	var count int64
	require.NoError(t, model.DB.Model(&model.BillingFinalization{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, s.Reserve(150))
	s.Refund(testGinContext(t))
	waitRefundCallback(t, s)
	assertUserBalances(t, 31201, 0, 1000, 0)
}

func TestPolicyPreservingRefundIndependentViolationFee(t *testing.T) {
	for _, order := range []string{"fee_before_worker", "worker_before_fee", "unmarked_same_key_replay", "contradictory_marker"} {
		t.Run(order, func(t *testing.T) {
			s := legacyRefundSession(t, "refund-independent-fee", 100, 100)
			s.relayInfo.Billing = s
			s.relayInfo.BillingSource = BillingSourceWallet
			s.relayInfo.ChannelMeta = &relaycommon.ChannelMeta{}
			s.relayInfo.StartTime = time.Now()
			s.relayInfo.UserQuota = 1000
			s.relayInfo.UserSetting.QuotaWarningThreshold = -1
			s.relayInfo.PriceData.GroupRatioInfo.GroupRatio = 1
			settings := model_setting.GetGrokSettings()
			oldEnabled, oldAmount := settings.ViolationDeductionEnabled, settings.ViolationDeductionAmount
			settings.ViolationDeductionEnabled = true
			settings.ViolationDeductionAmount = 50 / common.QuotaPerUnit
			t.Cleanup(func() { settings.ViolationDeductionEnabled = oldEnabled; settings.ViolationDeductionAmount = oldAmount })
			s.mu.Lock()
			receipt, err := s.terminalReceiptLocked()
			s.mu.Unlock()
			require.NoError(t, err)
			_, err = model.PersistBillingRefund(context.Background(), receipt)
			require.NoError(t, err)
			if order == "worker_before_fee" {
				require.NoError(t, model.RecoverBillingRefund(context.Background(), receipt.RequestID))
			}
			if order == "unmarked_same_key_replay" {
				require.NoError(t, PostConsumeQuota(s.relayInfo, 50, 0, false))
			}
			feeErr := types.NewErrorWithStatusCode(errors.New("synthetic fee condition"), types.ErrorCodeViolationFeeGrokCSAM, 400)
			require.True(t, ChargeViolationFeeIfNeeded(testGinContext(t), s.relayInfo, feeErr))
			if order == "contradictory_marker" {
				require.NoError(t, model.DB.Model(&model.BalanceLedger{}).Where("idempotency_key = ?", "billing:settle:"+receipt.RequestID+":50").Update("metadata", `{"independent_fee":"other"}`).Error)
			}
			err = model.RecoverBillingRefund(context.Background(), receipt.RequestID)
			expected := 950
			if order == "unmarked_same_key_replay" || order == "contradictory_marker" {
				require.Error(t, err)
				expected = 850
			} else {
				require.NoError(t, err)
				require.NoError(t, model.RecoverBillingRefund(context.Background(), receipt.RequestID))
			}
			assertUserBalances(t, 31201, 0, expected, 0)
			var token model.Token
			require.NoError(t, model.DB.First(&token, 31202).Error)
			require.Equal(t, expected, token.RemainQuota)
			require.Equal(t, 1000-expected, token.UsedQuota)
		})
	}
}

func TestPolicyPreservingRealtimeIncrementAndTerminal(t *testing.T) {
	// Initialize dialect quoting exactly as production startup, without opening a
	// separate log DSN or changing the current disposable database.
	t.Setenv("LOG_SQL_DSN", "")
	oldLogDB, oldMaster := model.LOG_DB, common.IsMasterNode
	common.IsMasterNode = false
	err := model.InitLogDB()
	common.IsMasterNode = oldMaster
	require.NoError(t, err)
	t.Cleanup(func() { model.LOG_DB = oldLogDB })
	require.NoError(t, model.DB.AutoMigrate(&model.Ability{}))

	for _, terminal := range []string{"final_settle", "refund_initial_hold", "failed_final_then_refund"} {
		t.Run(terminal, func(t *testing.T) {
			truncate(t)
			seedBonusEligibleModel(t, "terminal-wss", boolPtr(false))
			seedBonusUser(t, 31201, 0, 1000)
			seedToken(t, 31202, 31201, "synthetic-original-refund-token", 1000)
			quotaSetting := operation_setting.GetQuotaSetting()
			oldTrust := quotaSetting.TrustQuotaUSD
			quotaSetting.TrustQuotaUSD = 0
			t.Cleanup(func() { quotaSetting.TrustQuotaUSD = oldTrust })
			oldRatios := ratio_setting.ModelRatio2JSONString()
			ratios := ratio_setting.GetModelRatioCopy()
			ratios["terminal-wss"] = 1
			encoded, err := common.Marshal(ratios)
			require.NoError(t, err)
			require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(string(encoded)))
			t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(oldRatios)) })
			info := &relaycommon.RelayInfo{UserId: 31201, TokenId: 31202, TokenKey: "synthetic-original-refund-token", RequestId: "realtime-" + terminal, OriginModelName: "terminal-wss", UsingGroup: "default", UserGroup: "default", StartTime: time.Now(), ChannelMeta: &relaycommon.ChannelMeta{}, UserSetting: dto.UserSetting{BillingPreference: "wallet_only", QuotaWarningThreshold: -1}}
			info.PriceData.GroupRatioInfo.GroupRatio = 1
			c := testGinContext(t)
			c.Set("token_quota", 1000)
			s, apiErr := NewBillingSession(c, info, 100)
			require.Nil(t, apiErr)
			info.Billing = s
			usage := &dto.RealtimeUsage{TotalTokens: 20, InputTokens: 20, InputTokenDetails: dto.InputTokenDetails{TextTokens: 20}}
			require.NoError(t, PreWssConsumeQuota(c, info, usage))
			require.EqualValues(t, 1, info.WssPostConsumeSequence)
			assertUserBalances(t, 31201, 0, 880, 0)
			if terminal == "failed_final_then_refund" {
				name := "fail_realtime_final_token"
				require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(name, func(tx *gorm.DB) {
					if tx.Statement.Table == "tokens" {
						tx.AddError(errors.New("realtime final token failed"))
					}
				}))
				require.Error(t, s.Settle(20))
				require.NoError(t, model.DB.Callback().Update().Remove(name))
				assertUserBalances(t, 31201, 0, 880, 0)
			}
			expected := 980
			if terminal == "final_settle" {
				// Preserve the existing incremental charge PLUS final actual-minus-initial
				// adjustment. This repair does not redesign realtime's pricing arithmetic.
				PostWssConsumeQuota(c, info, "terminal-wss", usage, nil)
				expected = 960
			} else {
				s.Refund(c)
				waitRefundCallback(t, s)
			}
			require.False(t, s.NeedsRefund())
			if terminal != "final_settle" {
				require.NoError(t, model.RecoverBillingRefund(context.Background(), info.RequestId))
			}
			assertUserBalances(t, 31201, 0, expected, 0)
			var token model.Token
			require.NoError(t, model.DB.First(&token, 31202).Error)
			require.Equal(t, expected, token.RemainQuota)
		})
	}
}

func TestPolicyPreservingZeroTokenDeltaRetainsReplacementCounter(t *testing.T) {
	for _, hold := range []int{0, 100} {
		for _, change := range []string{"key", "owner"} {
			t.Run(fmt.Sprintf("hold_%d/%s", hold, change), func(t *testing.T) {
				s := legacyRefundSession(t, "zero-delta-original-identity", hold, hold)
				update := map[string]any{"remain_quota": 777, "used_quota": 23}
				if change == "key" {
					update["key"] = "synthetic-replacement"
				} else {
					update["user_id"] = 39999
				}
				require.NoError(t, model.DB.Model(&model.Token{}).Where("id = ?", 31202).Updates(update).Error)
				require.NoError(t, s.Settle(hold))
				require.NoError(t, s.Settle(hold))
				require.False(t, s.NeedsRefund())
				assertUserBalances(t, 31201, 0, 1000-hold, 0)
				var token model.Token
				require.NoError(t, model.DB.First(&token, 31202).Error)
				require.Equal(t, 777, token.RemainQuota)
				require.Equal(t, 23, token.UsedQuota)
			})
		}
	}
}
