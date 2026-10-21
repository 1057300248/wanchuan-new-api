package service

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/lifecycle"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func useRefundDispatcher(t *testing.T, capacity, concurrency int, timeout time.Duration) *billingRefundDispatcher {
	t.Helper()
	original := billingRefundIntents
	originalGroup := billingBackground
	d := newBillingRefundDispatcher(lifecycle.NewGroup(), capacity, concurrency, timeout, time.Millisecond, 10*time.Millisecond)
	billingRefundIntents = d
	billingBackground = d.group
	t.Cleanup(func() {
		d.group.BeginDrain()
		d.group.ForceCancel()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		require.NoError(t, d.group.Wait(ctx), "join refund jobs before restoring DB/cache globals")
		billingRefundIntents = original
		billingBackground = originalGroup
	})
	return d
}

// Pause the next retry BEFORE opening its SQL transaction. The first attempt
// can roll back (or lose its committed ACK) and release its connection, allowing
// independent reads of the actual durable state without a timing assumption.
type refundRetryBarrierPool struct {
	gorm.ConnPool
	started  atomic.Int32
	reached  chan struct{}
	released chan struct{}
}

func (p *refundRetryBarrierPool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	if p.started.Add(1) == 2 {
		close(p.reached)
		select {
		case <-p.released:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if beginner, ok := p.ConnPool.(gorm.ConnPoolBeginner); ok {
		return beginner.BeginTx(ctx, opts)
	}
	return p.ConnPool.(gorm.TxBeginner).BeginTx(ctx, opts)
}

func pauseRefundIntentRetry(t *testing.T) (<-chan struct{}, func()) {
	t.Helper()
	original := model.DB
	wrapped := original.Session(&gorm.Session{NewDB: true, Context: context.Background()})
	config := *wrapped.Config
	wrapped.Config = &config
	pool := &refundRetryBarrierPool{ConnPool: original.ConnPool, reached: make(chan struct{}), released: make(chan struct{})}
	wrapped.Config.ConnPool, wrapped.Statement.ConnPool = pool, pool
	model.DB = wrapped
	var release sync.Once
	releaseRetry := func() { release.Do(func() { close(pool.released) }) }
	t.Cleanup(func() { releaseRetry(); model.DB = original })
	return pool.reached, releaseRetry
}

func awaitRefundSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatal("refund worker did not reach the expected transaction boundary")
	}
}

// This driver seam does not acquire the fixture's sole SQLite connection while
// waiting, so cancellation cannot destroy an in-memory database connection.
type refundDeadlinePool struct {
	gorm.ConnPool
	first    atomic.Bool
	entered  chan context.Context
	released chan struct{}
}

func (p *refundDeadlinePool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	if p.first.CompareAndSwap(false, true) {
		p.entered <- ctx
		<-p.released
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return p.ConnPool.(gorm.TxBeginner).BeginTx(ctx, opts)
}

func TestBillingRefundIntentDeadlineSurvivesRequestCancellation(t *testing.T) {
	s := legacyRefundSession(t, "cancelled-client-intent-retry", 100, 70)
	original := model.DB
	wrapped := original.Session(&gorm.Session{NewDB: true, Context: context.Background()})
	config := *wrapped.Config
	wrapped.Config = &config
	pool := &refundDeadlinePool{ConnPool: original.ConnPool, entered: make(chan context.Context, 1), released: make(chan struct{})}
	wrapped.Config.ConnPool, wrapped.Statement.ConnPool = pool, pool
	model.DB = wrapped
	t.Cleanup(func() { model.DB = original })
	useRefundDispatcher(t, 2, 1, 100*time.Millisecond)
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(pool.released) }) })
	c := testGinContext(t)
	request, cancel := context.WithCancel(context.Background())
	cancel()
	c.Request = httptest.NewRequest("POST", "/refund-test", nil).WithContext(request)
	s.Refund(c) // exactly once, despite the already-cancelled client
	var attempt context.Context
	select {
	case attempt = <-pool.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("refund did not reach the independent DB context")
	}
	_, bounded := attempt.Deadline()
	require.True(t, bounded)
	require.NotErrorIs(t, attempt.Err(), context.Canceled, "request cancellation must not cancel intent persistence")
	admission := make(chan error, 1)
	go func() { admission <- s.Reserve(200) }()
	select {
	case err := <-admission:
		require.ErrorIs(t, err, model.ErrBillingFinalized)
	case <-time.After(3 * time.Second):
		t.Fatal("persistence held the session mutex")
	}
	require.ErrorIs(t, s.Settle(180), model.ErrBillingFinalized)
	release.Do(func() { close(pool.released) })
	waitRefundCallback(t, s)
	require.ErrorIs(t, attempt.Err(), context.DeadlineExceeded)
	require.False(t, s.NeedsRefund())
	assertUserBalances(t, 31201, 0, 1000, 0)
	var token model.Token
	require.NoError(t, model.DB.First(&token, 31202).Error)
	require.Equal(t, 1000, token.RemainQuota)
}

func TestBillingRefundIntentSaturationAndSealedGroup(t *testing.T) {
	for _, scenario := range []string{"queue_full", "sealed", "database_busy"} {
		t.Run(scenario, func(t *testing.T) {
			s := legacyRefundSession(t, "bounded-admission-"+scenario, 100, 70)
			d := useRefundDispatcher(t, 1, 1, 20*time.Millisecond)
			if scenario == "sealed" {
				d.group.BeginDrain()
			} else {
				d.slots <- struct{}{}
				defer func() { <-d.slots }()
			}
			if scenario == "database_busy" {
				d.database <- struct{}{}
				defer func() { <-d.database }()
			}
			done := make(chan struct{})
			c := testGinContext(t)
			go func() { s.Refund(c); close(done) }()
			awaitRefundSignal(t, done)
			require.True(t, s.NeedsRefund(), "persistence-only fallback must not mark a monetary refund complete")
			require.ErrorIs(t, s.Reserve(200), model.ErrBillingFinalized)
			assertUserBalances(t, 31201, 0, 900, 0)
			var token model.Token
			require.NoError(t, model.DB.First(&token, 31202).Error)
			require.Equal(t, 930, token.RemainQuota)
			var rows []model.BillingFinalization
			require.NoError(t, model.DB.Find(&rows).Error)
			if scenario == "database_busy" {
				require.Empty(t, rows, "timed-out semaphore wait must not attempt DB work")
				return
			}
			require.Len(t, rows, 1)
			require.Equal(t, model.BillingRefundPending, rows[0].State)
			require.NoError(t, model.RecoverBillingRefund(context.Background(), rows[0].RequestID))
			assertUserBalances(t, 31201, 0, 1000, 0)
			require.NoError(t, model.DB.First(&token, 31202).Error)
			require.Equal(t, 1000, token.RemainQuota)
		})
	}
}

func TestBillingRefundIntentActiveJobSaturatesWithoutExtraDatabaseWork(t *testing.T) {
	first := legacyRefundSession(t, "active-intent-capacity", 100, 70)
	funding := &WalletFunding{userId: 31201, requestId: "overflow-intent-capacity", debitOrder: []model.LedgerAccount{model.AccountPaid}}
	require.NoError(t, funding.PreConsume(100))
	require.NoError(t, model.DecreaseTokenQuota(31202, "synthetic-original-refund-token", 70))
	second := &BillingSession{relayInfo: &relaycommon.RelayInfo{UserId: 31201, TokenId: 31202, TokenKey: "synthetic-original-refund-token", RequestId: funding.requestId}, funding: funding, preConsumedQuota: 100, tokenConsumed: 70}
	original := model.DB
	wrapped := original.Session(&gorm.Session{NewDB: true, Context: context.Background()})
	config := *wrapped.Config
	wrapped.Config = &config
	pool := &refundDeadlinePool{ConnPool: original.ConnPool, entered: make(chan context.Context, 1), released: make(chan struct{})}
	wrapped.Config.ConnPool, wrapped.Statement.ConnPool = pool, pool
	model.DB = wrapped
	t.Cleanup(func() { model.DB = original })
	useRefundDispatcher(t, 1, 1, 20*time.Millisecond)
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(pool.released) }) })
	first.Refund(testGinContext(t))
	select {
	case <-pool.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("first admitted job did not reach the DB seam")
	}
	// The first driver's test seam remains blocked even past cancellation.
	// A saturated second request must return on its own permit deadline, not
	// spawn an untracked worker or bypass the shared concurrency cap.
	c := testGinContext(t)
	returned := make(chan struct{})
	go func() { second.Refund(c); close(returned) }()
	awaitRefundSignal(t, returned)
	var count int64
	require.NoError(t, model.DB.Model(&model.BillingFinalization{}).Count(&count).Error)
	require.Zero(t, count)
	require.True(t, first.NeedsRefund())
	require.True(t, second.NeedsRefund())
	assertUserBalances(t, 31201, 0, 800, 0)
	release.Do(func() { close(pool.released) })
	waitRefundCallback(t, first)
	require.False(t, first.NeedsRefund())
	require.True(t, second.NeedsRefund(), "unpersisted overflow is explicitly unresolved, not a fictitious refund")
	assertUserBalances(t, 31201, 0, 900, 0)
	var token model.Token
	require.NoError(t, model.DB.First(&token, 31202).Error)
	require.Equal(t, 930, token.RemainQuota)
}

func TestBillingRefundIntentShutdownKeepsAbsentDecisionUnrecoverable(t *testing.T) {
	s := legacyRefundSession(t, "shutdown-before-intent-commit", 100, 70)
	name := "refund_intent_store_unavailable"
	require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(name, func(tx *gorm.DB) {
		if tx.Statement.Table == "billing_finalizations" {
			tx.AddError(errors.New("synthetic unavailable intent store"))
		}
	}))
	t.Cleanup(func() { model.DB.Callback().Update().Remove(name) })
	retrying, _ := pauseRefundIntentRetry(t)
	d := useRefundDispatcher(t, 2, 1, time.Second)
	s.Refund(testGinContext(t))
	awaitRefundSignal(t, retrying)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, DrainBillingBackground(ctx), context.Canceled, "failed initial intents remain owned until the drain deadline")
	join, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	require.NoError(t, d.group.Wait(join))
	require.True(t, s.NeedsRefund())
	require.NoError(t, model.DB.Callback().Update().Remove(name))
	completed, err := model.RecoverPendingBillingRefunds(context.Background(), 100)
	require.NoError(t, err)
	require.Zero(t, completed, "a source hold alone does not prove a refund decision after memory is gone")
	assertUserBalances(t, 31201, 0, 900, 0)
	var token model.Token
	require.NoError(t, model.DB.First(&token, 31202).Error)
	require.Equal(t, 930, token.RemainQuota)
}

func TestBillingRefundIntentDrainFinishesAcceptedRetry(t *testing.T) {
	s := legacyRefundSession(t, "drain-accepted-intent-retry", 100, 100)
	terminalCommitFault(t, 1)
	retrying, releaseRetry := pauseRefundIntentRetry(t)
	d := useRefundDispatcher(t, 2, 1, time.Second)
	s.Refund(testGinContext(t))
	awaitRefundSignal(t, retrying)
	d.group.BeginDrain()
	releaseRetry()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, DrainBillingBackground(ctx))
	require.False(t, s.NeedsRefund(), "quiescing must not cancel accepted intent recovery")
	assertUserBalances(t, 31201, 0, 1000, 0)
	var token model.Token
	require.NoError(t, model.DB.First(&token, 31202).Error)
	require.Equal(t, 1000, token.RemainQuota)
}

func TestBillingRefundIntentRetryHonorsCommittedTerminalOwner(t *testing.T) {
	s := legacyRefundSession(t, "retry-sees-other-terminal-owner", 100, 100)
	terminalCommitFault(t, 1)
	retrying, releaseRetry := pauseRefundIntentRetry(t)
	useRefundDispatcher(t, 2, 1, time.Second)
	s.Refund(testGinContext(t))
	awaitRefundSignal(t, retrying)
	// A different session/process can win the terminal CAS while the first
	// intent was definitely absent. The queued refund must observe that owner,
	// never convert a settled source into a new refund just because it retried.
	other := &BillingSession{relayInfo: s.relayInfo, funding: s.funding, preConsumedQuota: 100, tokenConsumed: 100}
	require.NoError(t, other.Settle(100))
	releaseRetry()
	waitRefundCallback(t, s)
	require.False(t, s.NeedsRefund())
	assertUserBalances(t, 31201, 0, 900, 0)
	var token model.Token
	require.NoError(t, model.DB.First(&token, 31202).Error)
	require.Equal(t, 900, token.RemainQuota)
	var terminal model.BillingFinalization
	require.NoError(t, model.DB.First(&terminal, "request_id = ?", s.relayInfo.RequestId).Error)
	require.Equal(t, model.BillingSettled, terminal.State)
}
func TestBillingRefundIntentReportBoundsUnvalidatedIdentity(t *testing.T) {
	var output bytes.Buffer
	common.LogWriterMu.Lock()
	original := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = &output
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultErrorWriter = original
		common.LogWriterMu.Unlock()
	})
	secret := "synthetic-secret-never-log"
	receipt := model.BillingFinalization{RequestID: strings.Repeat("r", 64) + "\n" + secret, Source: "invalid\n" + secret, TokenFingerprint: secret, SourceHeld: 100, TokenHeld: 70}
	reportBillingRefundIntent("capture_failed", receipt, 0, errors.New(secret))
	common.LogWriterMu.Lock()
	line := output.String()
	gin.DefaultErrorWriter = original
	common.LogWriterMu.Unlock()
	require.NotContains(t, line, secret)
	require.Contains(t, line, "request_id=\""+strings.Repeat("r", 64)+"\"")
	require.Contains(t, line, "source=\"invalid\"")
	require.Contains(t, line, "code=\"recovery_error\"")
	require.Contains(t, line, "reconciliation_required=true")
	require.Equal(t, 1, strings.Count(line, "\n"), "unvalidated identity must not inject log records")
}

func TestBillingRefundIntentOutlivesReleasedSessionWithoutRetainingRequest(t *testing.T) {
	collected := make(chan struct{})
	var retrying <-chan struct{}
	var releaseRetry func()
	var dispatcher *billingRefundDispatcher
	func() {
		s := legacyRefundSession(t, "released-session-refund-retry", 100, 70)
		// Attach cleanup to the session itself, not a separately collectible
		// payload. It fires only if the queued retry no longer keeps s alive.
		runtime.AddCleanup(s, func(signal chan struct{}) { close(signal) }, collected)
		terminalCommitFault(t, 1)
		retrying, releaseRetry = pauseRefundIntentRetry(t)
		dispatcher = useRefundDispatcher(t, 2, 1, time.Second)
		s.Refund(testGinContext(t))
	}()
	awaitRefundSignal(t, retrying)
	runtime.GC()
	awaitRefundSignal(t, collected)
	assertUserBalances(t, 31201, 0, 900, 0)
	releaseRetry()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	dispatcher.group.BeginDrain()
	require.NoError(t, dispatcher.group.Wait(ctx))
	assertUserBalances(t, 31201, 0, 1000, 0)
	var token model.Token
	require.NoError(t, model.DB.First(&token, 31202).Error)
	require.Equal(t, 1000, token.RemainQuota)
	var terminal model.BillingFinalization
	require.NoError(t, model.DB.First(&terminal, "request_id = ?", "released-session-refund-retry").Error)
	require.Equal(t, model.BillingRefunded, terminal.State)
}
