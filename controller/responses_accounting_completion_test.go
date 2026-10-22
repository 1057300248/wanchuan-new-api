package controller

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// HTTP/WS handler completion does not join the official asynchronous refund.
// Source credit precedes independent token credit. Pause the token update and
// prove that this source-first window is not mistaken for full completion. The
// first accounting read releases credit only AFTER reading the old token row,
// so a one-shot assertion still deterministically fails.
func TestResponsesAccountingWaitsForAsynchronousTokenRefund(t *testing.T) {
	fixture := newResponsesWSBillingTest(t, `tier("request", fixed(0.002))`, func(*websocket.Conn, *http.Request) {})
	oldRetry := common.RetryTimes
	common.RetryTimes = 0
	common.RedisEnabled = false
	t.Cleanup(func() { common.RetryTimes = oldRetry })
	fixture.httpUpstream = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":{"message":"synthetic unavailable","type":"upstream_error"}}`)
	}
	// A second read-only connection observes the controlled pre-UPDATE window
	// while the refund transaction owns the first connection. There are no
	// competing writers; this does not relax production locking.
	sqlDB, err := model.DB.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(2)
	t.Cleanup(func() { sqlDB.SetMaxOpenConns(1) })
	entered, release := make(chan struct{}), make(chan struct{})
	var enterOnce, releaseOnce sync.Once
	var accountingRead, firstReadHeldToken atomic.Bool
	var updateHits atomic.Int32
	requestStarted, handlerJoined := false, false
	releaseRefund := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseRefund)
	const updateHook = "test:pause-responses-token-refund"
	const queryHook = "test:release-responses-refund-after-read"
	require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(updateHook, func(tx *gorm.DB) {
		if tx.Statement.Table != "tokens" {
			return
		}
		updates, ok := tx.Statement.Dest.(map[string]any)
		if !ok {
			return
		}
		delta, ok := updates["remain_quota"].(clause.Expr)
		if !ok || delta.SQL != "remain_quota + ?" || len(delta.Vars) != 1 || delta.Vars[0] != 1000 {
			return
		}
		updateHits.Add(1)
		enterOnce.Do(func() { close(entered) })
		<-release
	}))
	require.NoError(t, model.DB.Callback().Query().After("gorm:query").Register(queryHook, func(tx *gorm.DB) {
		if tx.Statement.Table == "tokens" && accountingRead.Load() {
			if token, ok := tx.Statement.Dest.(*model.Token); ok && token.Id == fixture.token.Id && token.RemainQuota == 2000 && token.UsedQuota == 1000 {
				firstReadHeldToken.Store(true)
			}
			releaseRefund()
		}
	}))
	t.Cleanup(func() {
		releaseRefund()
		if requestStarted && !handlerJoined {
			select {
			case <-fixture.httpDone:
				handlerJoined = true
			case <-time.After(3 * time.Second):
				t.Error("HTTP handler did not join before refund cleanup")
			}
		}
		fixture.closeAndWait(t)
		// The handler has stopped producing work and Redis is disabled. Unlike
		// balances or refunded=true, zero joins the actual finite pool callbacks.
		require.Eventually(t, func() bool { return gopool.WorkerCount() == 0 }, 5*time.Second, time.Millisecond,
			"refund callbacks must return before removing hooks or restoring fixture globals")
		require.NoError(t, model.DB.Callback().Update().Remove(updateHook))
		require.NoError(t, model.DB.Callback().Query().Remove(queryHook))
	})

	request, err := http.NewRequest(http.MethodPost, fixture.gatewayURL+"/v1/responses", strings.NewReader(`{"model":"ws-billing","input":"synthetic"}`))
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer sk-"+fixture.token.Key)
	request.Header.Set("Content-Type", "application/json")
	requestStarted = true
	response, err := (&http.Client{Timeout: 3 * time.Second}).Do(request)
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
	select {
	case <-fixture.httpDone:
		handlerJoined = true
	case <-time.After(3 * time.Second):
		t.Fatal("HTTP request did not finish")
	}
	fixture.closeAndWait(t)
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("asynchronous token refund was not admitted")
	}
	var token model.Token
	var user model.User
	require.NoError(t, model.DB.First(&token, fixture.token.Id).Error)
	require.NoError(t, model.DB.First(&user, fixture.user.Id).Error)
	require.Equal(t, 2000, token.RemainQuota, "handler completion precedes token refund")
	require.Equal(t, 1000, token.UsedQuota)
	require.Equal(t, 100000, user.Quota, "official source credit precedes the independently paused token credit")
	require.Equal(t, 100000, user.QuotaPaid)
	require.Zero(t, user.QuotaBonus)
	require.Zero(t, user.QuotaReward)
	var owners int64
	require.NoError(t, model.DB.Model(&model.BillingFinalization{}).
		Where("user_id = ? AND request_id = ?", fixture.user.Id, "responses-http-billing").Count(&owners).Error)
	require.Zero(t, owners, "official request Refund does not create a durable finalization owner")
	accountingRead.Store(true)
	assertResponsesWSAccounting(t, fixture, nil)
	require.True(t, firstReadHeldToken.Load(), "the first accounting read must observe the paused original token row")
	require.Equal(t, int32(1), updateHits.Load())
	require.Eventually(t, func() bool { return gopool.WorkerCount() == 0 }, 5*time.Second, time.Millisecond)
}
