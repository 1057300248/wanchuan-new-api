package service

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type officialBlockedRefund struct {
	FundingSource
	entered chan struct{}
	release chan struct{}
	calls   atomic.Int32
	once    sync.Once
}

func (f *officialBlockedRefund) Refund() error {
	f.calls.Add(1)
	f.once.Do(func() { close(f.entered) })
	<-f.release
	return errors.New("synthetic source refund failure")
}

func TestOfficialRequestRefundMarksBeforeSourceAndDoesNotRetry(t *testing.T) {
	if runRefundFixtureChild(t) {
		return
	}
	s := legacyRefundSession(t, "official-source-failure", 100, 70)
	funding := &officialBlockedRefund{FundingSource: s.funding, entered: make(chan struct{}), release: make(chan struct{})}
	s.funding = funding
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(funding.release) }); waitRefundCallback(t, s) })
	c := testGinContext(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Request = httptest.NewRequest("POST", "/official-refund", nil).WithContext(ctx)
	s.Refund(c)
	select {
	case <-funding.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("official source callback did not start")
	}
	require.False(t, s.NeedsRefund(), "official flag means scheduled, not committed")
	s.Refund(c)
	require.EqualValues(t, 1, funding.calls.Load())
	assertUserBalances(t, 31201, 0, 900, 0)
	assert.Equal(t, 930, getTokenRemainQuota(t, 31202))
	release.Do(func() { close(funding.release) })
	waitRefundCallback(t, s)
	assertUserBalances(t, 31201, 0, 900, 0)
	assert.Equal(t, 1000, getTokenRemainQuota(t, 31202), "source error does not prevent official independent token credit")
	s.Refund(c)
	waitRefundCallback(t, s)
	assert.EqualValues(t, 1, funding.calls.Load(), "no custom automatic retry after the official first call")
	var rows int64
	require.NoError(t, model.DB.Model(&model.BillingFinalization{}).Where("request_id = ?", s.relayInfo.RequestId).Count(&rows).Error)
	assert.Zero(t, rows, "official Refund never creates an intent/terminal owner")
}

func TestOfficialRequestRefundTokenFailureKeepsSourceCredit(t *testing.T) {
	if runRefundFixtureChild(t) {
		return
	}
	s := legacyRefundSession(t, "official-token-failure", 100, 100)
	fault := injectOfficialUpdateFault(t, "tokens", officialFailureBeforeSQL)
	t.Cleanup(func() { waitRefundCallback(t, s); fault.disable() })
	s.Refund(testGinContext(t))
	waitRefundCallback(t, s)
	fault.requireOneHit(t)
	assertUserBalances(t, 31201, 0, 1000, 0)
	assert.Equal(t, 900, getTokenRemainQuota(t, 31202))
	require.False(t, s.NeedsRefund())
	fault.disable()
	s.Refund(testGinContext(t))
	waitRefundCallback(t, s)
	fault.requireOneHit(t)
	assert.Equal(t, 900, getTokenRemainQuota(t, 31202), "the official in-memory first-call flag does not retry a failed token step")
}

func TestOfficialSubscriptionBaseFailureStillReturnsExtraAndToken(t *testing.T) {
	if runRefundFixtureChild(t) {
		return
	}
	truncate(t)
	seedBonusUser(t, 31201, 0, 1000)
	seedToken(t, 31202, 31201, "synthetic-original-refund-token", 850)
	seedSubscription(t, 31203, 31201, 1000, 250)
	var attempts atomic.Int32
	name := "test:official-subscription-base-failure"
	require.NoError(t, model.DB.Callback().Query().Before("gorm:query").Register(name, func(tx *gorm.DB) {
		if tx.Statement.Table == "subscription_pre_consume_records" {
			attempts.Add(1)
			tx.AddError(errors.New("synthetic base receipt unavailable"))
		}
	}))
	t.Cleanup(func() { require.NoError(t, model.DB.Callback().Query().Remove(name)) })
	funding := &SubscriptionFunding{requestId: "official-subscription-failure", subscriptionId: 31203, preConsumed: 100, amount: 100, reserveDelta: 50}
	s := &BillingSession{funding: funding, relayInfo: &relaycommon.RelayInfo{UserId: 31201, TokenId: 31202, TokenKey: "synthetic-original-refund-token", SubscriptionId: 31203}, preConsumedQuota: 150, tokenConsumed: 150, extraReserved: 50}
	t.Cleanup(func() { waitRefundCallback(t, s) })
	s.Refund(testGinContext(t))
	waitRefundCallback(t, s)
	require.EqualValues(t, 3, attempts.Load(), "official base subscription refund has exactly three in-process attempts")
	assert.EqualValues(t, 200, getSubscriptionUsed(t, 31203), "only the independently held extra50 is returned after all base attempts fail")
	assert.Equal(t, 1000, getTokenRemainQuota(t, 31202))
	s.Refund(testGinContext(t))
	waitRefundCallback(t, s)
	assert.EqualValues(t, 3, attempts.Load())
	assert.EqualValues(t, 200, getSubscriptionUsed(t, 31203))
}

func TestOfficialTaskRefundIndependentStages(t *testing.T) {
	for _, stage := range []string{"source", "token", "quota_clear"} {
		t.Run(stage, func(t *testing.T) {
			user, token, channel := seedReceiptActors(t, 100, 40)
			task := seedReceiptTask(t, user, token, channel, 300)
			table := map[string]string{"source": "users", "token": "tokens", "quota_clear": "tasks"}[stage]
			var fault *officialUpdateFault
			if stage == "source" {
				// Preserve the already-passing post-write outer-transaction
				// rollback proof for the three-wallet source adapter.
				failReceiptUpdates(t, table)
			} else {
				fault = injectOfficialUpdateFault(t, table, officialFailureBeforeSQL)
			}
			result := RefundTaskQuota(t.Context(), task, "official ordered refund")
			if fault != nil {
				fault.requireOneHit(t)
			}
			var persisted model.Task
			require.NoError(t, model.DB.First(&persisted, task.ID).Error)
			if stage == "source" {
				require.False(t, result)
				assertUserBalances(t, user.Id, 0, 840, 0)
				assert.Equal(t, 1700, getTokenRemainQuota(t, token.Id))
				assert.Equal(t, 300, persisted.Quota)
				assert.Zero(t, countReceiptLogs(t, user.Id))
				for _, entity := range []any{&model.BalanceLedgerOperation{}, &model.BalanceLedger{}} {
					var rows int64
					require.NoError(t, model.DB.Model(entity).Where("user_id = ? AND idempotency_key = ?", user.Id, "task:refund:"+task.TaskID).Count(&rows).Error)
					assert.Zero(t, rows, "post-write source failure must roll back both its operation and detail rows")
				}
				return
			}
			require.True(t, result)
			assertUserBalances(t, user.Id, 100, 1000, 40)
			assert.Zero(t, task.Quota)
			assert.EqualValues(t, 1, countReceiptLogs(t, user.Id))
			if stage == "token" {
				assert.Equal(t, 1700, getTokenRemainQuota(t, token.Id))
				assert.Zero(t, persisted.Quota)
			} else {
				assert.Equal(t, 2000, getTokenRemainQuota(t, token.Id))
				assert.Equal(t, 300, persisted.Quota, "official quota-clear error is logged after successful money steps")
				fault.disable()
				require.True(t, RefundTaskQuota(t.Context(), &persisted, "explicit repeated call"))
				fault.requireOneHit(t)
				assertUserBalances(t, user.Id, 100, 1000, 40)
				assert.Equal(t, 2300, getTokenRemainQuota(t, token.Id), "inherited source-ledger idempotency is not official token exactly-once")
				assert.EqualValues(t, 2, countReceiptLogs(t, user.Id))
			}
		})
	}
}

func TestOfficialRefundPostCommitErrorsDoNotRollBack(t *testing.T) {
	if runRefundFixtureChild(t) {
		return
	}
	t.Run("request_token", func(t *testing.T) {
		s := legacyRefundSession(t, "official-token-post-commit-error", 100, 100)
		fault := injectOfficialUpdateFault(t, "tokens", officialFailureAfterCommit)
		t.Cleanup(func() { waitRefundCallback(t, s); fault.disable() })
		s.Refund(testGinContext(t))
		waitRefundCallback(t, s)
		fault.requireOneHit(t)
		assertUserBalances(t, 31201, 0, 1000, 0)
		assert.Equal(t, 1000, getTokenRemainQuota(t, 31202), "an error after COMMIT does not undo the official token credit")
		require.False(t, s.NeedsRefund())
		fault.disable()
		s.Refund(testGinContext(t))
		waitRefundCallback(t, s)
		fault.requireOneHit(t)
		assert.Equal(t, 1000, getTokenRemainQuota(t, 31202))
	})
	t.Run("task_quota_clear", func(t *testing.T) {
		user, token, channel := seedReceiptActors(t, 100, 40)
		task := seedReceiptTask(t, user, token, channel, 300)
		fault := injectOfficialUpdateFault(t, "tasks", officialFailureAfterCommit)
		require.True(t, RefundTaskQuota(t.Context(), task, "post-commit clear error"))
		fault.requireOneHit(t)
		var persisted model.Task
		require.NoError(t, model.DB.First(&persisted, task.ID).Error)
		assert.Zero(t, persisted.Quota, "the synthetic post-COMMIT error cannot restore the previous quota")
		assert.Equal(t, task.PrivateData.WalletDebit, persisted.PrivateData.WalletDebit)
		assertUserBalances(t, user.Id, 100, 1000, 40)
		assert.Equal(t, 2000, getTokenRemainQuota(t, token.Id))
		assert.EqualValues(t, 1, countReceiptLogs(t, user.Id))
		fault.disable()
		require.True(t, RefundTaskQuota(t.Context(), &persisted, "reloaded zero quota is a no-op"))
		fault.requireOneHit(t)
		assert.Equal(t, 2000, getTokenRemainQuota(t, token.Id))
		assert.EqualValues(t, 1, countReceiptLogs(t, user.Id))
		assertUserBalances(t, user.Id, 100, 1000, 40)
	})
}

func TestOfficialTaskHistoricalPendingIsReadOnly(t *testing.T) {
	for _, kind := range []string{"flag", "private_only", "both"} {
		t.Run(kind, func(t *testing.T) {
			user, token, channel := seedReceiptActors(t, 100, 40)
			task := seedReceiptTask(t, user, token, channel, 100)
			task.Status = model.TaskStatusInProgress
			task.Progress = "50%"
			task.SubmitTime = time.Now().Add(-48 * time.Hour).Unix()
			task.SettlementPending = kind != "private_only"
			if kind != "flag" {
				task.PrivateData.PendingSettlement = &model.TaskSettlement{Phase: "submit", Quota: 200}
			}
			require.NoError(t, model.DB.Save(task).Error)
			before := *task
			require.False(t, RefundTaskQuota(t.Context(), task, "not a new official intent"))
			RecalculateTaskQuota(t.Context(), task, 0, common.NewMessage("must not settle"))
			require.NoError(t, ApplyTaskPollResult(t.Context(), nil, task, relaycommon.FailTaskInfo("failure"), nil))
			finalizeTerminalTask(t.Context(), nil, task, relaycommon.FailTaskInfo("failure"))
			oldTimeout := constant.TaskTimeoutMinutes
			constant.TaskTimeoutMinutes = 1
			t.Cleanup(func() { constant.TaskTimeoutMinutes = oldTimeout })
			sweepTimedOutTasks(t.Context())
			require.NoError(t, model.DB.First(task, task.ID).Error)
			assert.Equal(t, before, *task, "compatibility boundary must not rewrite status, quota, metadata or old intent")
			assertUserBalances(t, user.Id, 0, 1000, 40)
			assert.Equal(t, 1900, getTokenRemainQuota(t, token.Id))
			assert.Zero(t, countReceiptLogs(t, user.Id))
			for _, active := range model.GetAllUnFinishSyncTasks(100) {
				assert.NotEqual(t, task.ID, active.ID)
			}
		})
	}
}

func TestOfficial093WalletSnapshotLimitationsRemainExplicit(t *testing.T) {
	t.Run("small_three_account_remainder", func(t *testing.T) {
		original := []model.LedgerShare{{Account: model.AccountBonus, Amount: 1}, {Account: model.AccountReward, Amount: 1}, {Account: model.AccountPaid, Amount: 1}}
		require.Equal(t, []model.LedgerShare{{Account: model.AccountBonus, Amount: 2}}, SplitRefund(original, 2))
		t.Log("KNOWN 093 LIMITATION: total2 is conserved but first-account remainder2 exceeds its original share1; no rounding fix is included")
	})
	for _, order := range []string{"submit_before_task_refund", "task_refund_before_submit"} {
		t.Run(order, func(t *testing.T) {
			truncate(t)
			seedBonusUser(t, 31201, 1, 1)
			seedToken(t, 31202, 31201, "synthetic-original-refund-token", 1000)
			seedChannel(t, 31204)
			funding := &WalletFunding{userId: 31201, requestId: "official-gross-" + order, debitOrder: []model.LedgerAccount{model.AccountBonus, model.AccountPaid}}
			require.NoError(t, funding.PreConsume(2))
			require.NoError(t, model.DecreaseTokenQuota(31202, "synthetic-original-refund-token", 2))
			info := &relaycommon.RelayInfo{UserId: 31201, TokenId: 31202, TokenKey: "synthetic-original-refund-token", RequestId: "official-gross-" + order}
			s := &BillingSession{relayInfo: info, funding: funding, preConsumedQuota: 2, tokenConsumed: 2}
			info.Billing = s
			task := makeTask(31201, 31204, 1, 31202, BillingSourceWallet, 0)
			task.PrivateData.WalletDebit = WalletSessionDebitShares(info)
			require.Equal(t, []model.LedgerShare{{Account: model.AccountBonus, Amount: 1}, {Account: model.AccountPaid, Amount: 1}}, task.PrivateData.WalletDebit)
			require.NoError(t, model.DB.Create(task).Error)
			var saved model.Task
			require.NoError(t, model.DB.First(&saved, task.ID).Error)
			assert.Equal(t, task.PrivateData.WalletDebit, saved.PrivateData.WalletDebit, "093 persists the actual gross snapshot before final Settle, not an invented net receipt")
			if order == "submit_before_task_refund" {
				require.NoError(t, s.Settle(1))
			}
			require.True(t, RefundTaskQuota(t.Context(), &saved, "official caller decision"))
			if order == "task_refund_before_submit" {
				require.NoError(t, s.Settle(1))
			}
			assertUserBalances(t, 31201, 2, 0, 0)
			assert.Equal(t, 1000, getTokenRemainQuota(t, 31202))
			t.Log("KNOWN 093 LIMITATION: two independent proportional refunds preserve total2 but return Bonus2/Paid0 rather than the original Bonus1/Paid1")
		})
	}
}
