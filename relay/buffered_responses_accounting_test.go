package relay_test

import (
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Called by the existing real-database Responses accounting matrix so the
// wallet runtime and frozen funding registry are configured exactly once.
func runBufferedResponsesAccountingCase(t *testing.T, db, logDB *gorm.DB, format types.RelayFormat, outcome string) {
	t.Helper()
	const requestedModel = "converted-stream-accounting"
	const paid, bonus, reward = 50000, 100, 70
	success := outcome == "success" || outcome == "incomplete" || outcome == "fixed_success_missing_usage"
	noUsage := strings.HasPrefix(outcome, "empty_") || outcome == "fixed_empty" || outcome == "fixed_success_missing_usage"
	fixed := strings.HasPrefix(outcome, "fixed_")
	wantCharge := 15
	if noUsage {
		wantCharge = 0
	}
	if fixed && (!noUsage || success) {
		wantCharge = 5000
	}
	if fixed {
		settings, ok := config.GlobalConfig.Get("billing_setting").(*billing_setting.BillingSetting)
		require.True(t, ok)
		previous := *settings
		settings.BillingMode = maps.Clone(settings.BillingMode)
		settings.BillingExpr = maps.Clone(settings.BillingExpr)
		settings.BillingMode[requestedModel] = billing_setting.BillingModeTieredExpr
		settings.BillingExpr[requestedModel] = `tier("request", fixed(0.01))`
		t.Cleanup(func() { *settings = previous })
	}

	identity := strings.ReplaceAll(uuid.NewString(), "-", "")
	user := model.User{Username: "buffered_" + identity, AffCode: identity, Quota: paid + bonus + reward, QuotaPaid: paid, QuotaBonus: bonus, QuotaReward: reward, Status: common.UserStatusEnabled, Group: "default"}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: "test-buffered-" + identity, RemainQuota: paid, Status: common.TokenStatusEnabled}
	require.NoError(t, db.Create(&token).Error)
	var calls atomic.Int32
	requests := make(chan string, 4)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		requests <- r.URL.Path + "\n" + string(raw)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: "+`{"type":"response.created","response":{"id":"resp_partial","model":"provider-private","status":"in_progress"}}`+"\n\n")
		if !noUsage {
			_, _ = fmt.Fprint(w, "data: "+`{"type":"response.in_progress","response":{"usage":{"input_tokens":11,"output_tokens":4,"total_tokens":15}}}`+"\n\n")
			_, _ = fmt.Fprint(w, "data: "+`{"type":"response.output_text.delta","delta":"partial answer"}`+"\n\n")
		}
		ending := `{"type":"response.failed","response":{"status":"failed","error":{"type":"server_error","code":"overloaded","message":"provider-private failed"}}}`
		switch outcome {
		case "bare_error":
			ending = `{"type":"error","code":"overloaded","message":"provider-private failed"}`
		case "cancelled":
			ending = `{"type":"response.cancelled","response":{"status":"cancelled"}}`
		case "failed_status":
			ending = `{"type":"response.done","response":{"status":"failed","error":{"code":"overloaded","message":"provider-private failed"}}}`
		case "eof", "empty_eof":
			return
		case "done_without_terminal":
			ending = "[DONE]"
		case "incomplete":
			ending = `{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`
		case "success", "fixed_success_missing_usage":
			ending = `{"type":"response.completed","response":{"status":"completed"}}`
		}
		_, _ = fmt.Fprint(w, "data: "+ending+"\n\n")
	}))
	t.Cleanup(upstream.Close)
	channel := model.Channel{Name: "buffered-" + identity, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled,
		Key: "test-only", BaseURL: common.GetPointer(upstream.URL), Models: requestedModel, Group: "default", AutoBan: common.GetPointer(0)}
	path := "/v1/chat/completions"
	body := `{"model":"` + requestedModel + `","messages":[{"role":"user","content":"Please return a helpful answer to this short test request"}],"stream":false}`
	if format == types.RelayFormatClaude {
		path = "/v1/messages"
		body = `{"model":"` + requestedModel + `","messages":[{"role":"user","content":"Please return a helpful answer to this short test request"}],"max_tokens":32,"stream":false}`
	} else if format == types.RelayFormatGemini {
		path = "/v1beta/models/" + requestedModel + ":generateContent"
		body = `{"contents":[{"role":"user","parts":[{"text":"Please return a helpful answer to this short test request"}]}],"generationConfig":{"maxOutputTokens":32}}`
		channel.Type = constant.ChannelTypeAdvancedCustom
		channel.SetOtherSettings(dto.ChannelOtherSettings{AdvancedCustom: &dto.AdvancedCustomConfig{Routes: []dto.AdvancedCustomRoute{{
			IncomingPath: "/v1beta/models/{model}:generateContent", UpstreamPath: "/v1/responses", Converter: relayconvert.ConverterGeminiContentToOpenAIResponses,
		}}}})
	}
	require.NoError(t, db.Create(&channel).Error)
	require.NoError(t, channel.AddAbilities(db))
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	for key, value := range map[constant.ContextKey]any{
		constant.ContextKeyUserId: user.Id, constant.ContextKeyUserQuota: paid + bonus + reward,
		constant.ContextKeyUserGroup: "default", constant.ContextKeyTokenGroup: "default", constant.ContextKeyUsingGroup: "default",
		constant.ContextKeyTokenId: token.Id, constant.ContextKeyTokenKey: token.Key,
		constant.ContextKeyUserSetting: dto.UserSetting{BillingPreference: "wallet_only"},
	} {
		common.SetContextKey(c, key, value)
	}
	c.Set(common.RequestIdKey, identity)
	c.Set("username", user.Username)
	c.Set("token_quota", paid)
	require.Nil(t, middleware.SetupContextForSelectedChannel(c, &channel, requestedModel))
	completion := observeNativeResponsesRefundWithin(t, db, identity, wantCharge == 0, 5*time.Second, &token)
	var verifyBarrier func()
	if format == types.RelayFormatGemini && outcome == "empty_eof" {
		verifyBarrier = observeBufferedRefundCommitBarrier(t, db, completion, token.Id)
	}

	controller.Relay(c, format)
	if verifyBarrier != nil {
		verifyBarrier()
	}
	completion.join(t)

	require.Equal(t, int32(1), calls.Load(), "a buffered failure must not repeat a potentially billable upstream request")
	assert.Equal(t, 1, service.RequestPolicy(c).Attempts)
	assert.Equal(t, success && outcome != "incomplete", service.RequestPolicy(c).Successful, "partial billing must never publish a success outcome for failed or truncated upstream work")
	request := <-requests
	assert.True(t, strings.HasPrefix(request, "/v1/responses\n"), request)
	assert.Contains(t, request, `"input":`)
	assert.NotContains(t, request, `"stream":true`)
	assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyStreamErrorHandled), "the outer controller must write the JSON error")
	wire := recorder.Body.String()
	assert.NotContains(t, recorder.Header().Get("Content-Type"), "text/event-stream")
	assert.NotContains(t, wire, "data:")
	assert.NotContains(t, wire, "provider-private")
	if success {
		assert.Equal(t, http.StatusOK, recorder.Code, wire)
		assert.NotContains(t, wire, `"error":`)
		if outcome == "incomplete" {
			assert.NotContains(t, wire, `"finish_reason":"stop"`)
			assert.NotContains(t, wire, `"stop_reason":"end_turn"`)
			assert.NotContains(t, wire, `"finishReason":"STOP"`)
		}
	} else {
		assert.Equal(t, http.StatusBadGateway, recorder.Code, wire)
		assert.Equal(t, 1, strings.Count(wire, `"error":`))
		assert.NotContains(t, wire, `"finish_reason":`)
		assert.NotContains(t, wire, `"finishReason":`)
	}

	var logs []model.Log
	require.NoError(t, logDB.Where("user_id = ? AND type = ?", user.Id, model.LogTypeConsume).Find(&logs).Error)
	if wantCharge == 0 {
		assert.Empty(t, logs)
	} else {
		require.Len(t, logs, 1, wire)
		assert.Equal(t, wantCharge, logs[0].Quota)
		assert.False(t, logs[0].IsStream)
		if !noUsage {
			assert.Equal(t, 11, logs[0].PromptTokens)
			assert.Equal(t, 4, logs[0].CompletionTokens)
		}
	}
	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	rewardCharge := min(reward, wantCharge)
	paidCharge := wantCharge - rewardCharge
	assert.Equal(t, paid-paidCharge, user.QuotaPaid)
	assert.Equal(t, bonus, user.QuotaBonus)
	assert.Equal(t, reward-rewardCharge, user.QuotaReward)
	assert.Equal(t, paid+bonus+reward-wantCharge, user.Quota)
	assert.Equal(t, paid-wantCharge, token.RemainQuota)
	assert.Equal(t, wantCharge, token.UsedQuota)
	var ledger []model.BalanceLedger
	require.NoError(t, db.Where("user_id = ?", user.Id).Order("id").Find(&ledger).Error)
	require.NotEmpty(t, ledger, "the test must exercise the real BillingSession reservation")
	assert.Negative(t, ledger[0].Delta)
	net := 0
	accountNet := make(map[string]int)
	for _, row := range ledger {
		net += row.Delta
		accountNet[row.Account] += row.Delta
	}
	assert.Equal(t, -wantCharge, net, "settlement followed by the outer refund must move money exactly once")
	assert.Equal(t, -rewardCharge, accountNet[string(model.AccountReward)])
	assert.Equal(t, -paidCharge, accountNet[string(model.AccountPaid)])
	assert.Zero(t, accountNet[string(model.AccountBonus)])
}

// Keep the mandatory Gemini empty-EOF case at the original 5 s budget while
// deterministically exposing the old pre-debit signal and uncommitted refund.
func observeBufferedRefundCommitBarrier(t *testing.T, db *gorm.DB, completion *nativeResponsesRefundCompletion, tokenID int) func() {
	t.Helper()
	oldSignal := make(chan int, 1)
	blocked := make(chan *nativeResponsesRefundTransaction, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	completion.driverWait = make(chan *nativeResponsesRefundTransaction, 1)
	waitDone := make(chan struct{})
	waitStarted := false
	callback := "buffered_refund_commit_barrier_" + completion.requestID
	require.NoError(t, db.Callback().Update().After("native_refund_completion_"+completion.requestID).Register(callback, func(tx *gorm.DB) {
		delta, ok := nativeResponsesTokenQuotaDelta(tx, tokenID)
		if !ok {
			return
		}
		completion.mu.Lock()
		r := completion.records[tx.Statement.Context]
		matches := r != nil && r.matchesToken
		refund := matches && r.role == "refund"
		completion.mu.Unlock()
		if delta < 0 && matches {
			// The removed SQL-only predicate accepted this actual reservation.
			select {
			case oldSignal <- delta:
			default:
			}
		}
		if delta > 0 && refund {
			blocked <- r
			select {
			case <-release:
			case <-tx.Statement.Context.Done():
			}
		}
	}))
	// LIFO: release even on Fatal, join the once-owned wait, then remove hooks.
	// The observer's own cleanup subsequently restores model.DB and its hooks.
	t.Cleanup(func() {
		unblock()
		completion.join(t)
		if waitStarted {
			<-waitDone
		}
		require.NoError(t, db.Callback().Update().Remove(callback))
	})
	return func() {
		t.Helper()
		// The controller-return wait, barrier and final owner query share one deadline.
		completion.deadline = time.Now().Add(completion.budget)
		budget := time.NewTimer(time.Until(completion.deadline))
		defer budget.Stop()
		select {
		case delta := <-oldSignal:
			require.Negative(t, delta, "the old waiter accepted this request's pre-debit")
			t.Log("refund_barrier: initial_negative_observed")
		default:
			require.FailNow(t, "the original token reservation did not exercise the old false signal")
		}
		waitStarted = true
		go func() {
			completion.once.Do(func() { completion.joined, completion.err = completion.wait() })
			close(waitDone)
		}()
		var refund *nativeResponsesRefundTransaction
		select {
		case refund = <-blocked:
			t.Log("refund_barrier: same_owned_refund_precommit")
		case <-budget.C:
			require.FailNow(t, "the original token refund did not reach the pre-commit barrier")
		}
		select {
		case observed := <-completion.driverWait:
			require.Same(t, refund, observed, "wait must select this request's real refund transaction")
		case <-budget.C:
			require.FailNow(t, "completion did not reach the real driver wait")
		}
		select {
		case <-refund.finished:
			require.FailNow(t, "refund completed before the commit barrier was released")
		default:
		}
		select {
		case <-waitDone:
			require.FailNow(t, "the old reservation signal released the refund completion wait")
		default:
		}
		t.Log("refund_barrier: wait_selected_same_tx_not_finished")
		unblock()
		completion.join(t)
		<-waitDone
		require.NoError(t, completion.err)
		t.Log("refund_barrier: released_and_owned_tail_joined")
	}
}
