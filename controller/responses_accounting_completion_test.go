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
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// HTTP/WS handler completion does not join the asynchronous refund. The host
// finalization transaction now keeps BOTH wallet and token held until commit,
// unlike the historical source-first window reproduced before F7. Pause the
// token credit and require that no partial refund is externally visible. The
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
	var accountingRead atomic.Bool
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
		enterOnce.Do(func() { close(entered) })
		<-release
	}))
	require.NoError(t, model.DB.Callback().Query().After("gorm:query").Register(queryHook, func(tx *gorm.DB) {
		if tx.Statement.Table == "tokens" && accountingRead.Load() {
			releaseRefund()
		}
	}))
	t.Cleanup(func() {
		releaseRefund()
		require.Eventually(t, func() bool {
			var token model.Token
			return model.DB.First(&token, fixture.token.Id).Error == nil && token.RemainQuota == 3000 && token.UsedQuota == 0
		}, 3*time.Second, time.Millisecond, "refund must finish before restoring fixture globals")
		require.NoError(t, model.DB.Callback().Update().Remove(updateHook))
		require.NoError(t, model.DB.Callback().Query().Remove(queryHook))
	})

	request, err := http.NewRequest(http.MethodPost, fixture.gatewayURL+"/v1/responses", strings.NewReader(`{"model":"ws-billing","input":"synthetic"}`))
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer sk-"+fixture.token.Key)
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 3 * time.Second}).Do(request)
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
	select {
	case <-fixture.httpDone:
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
	require.Equal(t, 99000, user.Quota, "wallet refund must not publish before the token transaction commits")
	require.Equal(t, 99000, user.QuotaPaid)
	require.Zero(t, user.QuotaBonus)
	require.Zero(t, user.QuotaReward)
	var owner model.BillingFinalization
	require.NoError(t, model.DB.Where("user_id = ?", fixture.user.Id).First(&owner).Error)
	require.Equal(t, model.BillingRefundPending, owner.State, "scheduled recovery is not committed refund completion")
	require.Equal(t, 1000, owner.SourceHeld)
	require.Equal(t, 1000, owner.TokenHeld)
	accountingRead.Store(true)
	assertResponsesWSAccounting(t, fixture, nil)
}
