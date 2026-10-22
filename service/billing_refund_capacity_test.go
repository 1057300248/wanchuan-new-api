package service

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"slices"
	"strconv"
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
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// This is a bounded fault-capacity assessment of the real refund dispatcher,
// not a full gateway/provider throughput benchmark. All holds are real; the
// BeginTx seam makes the intent store unavailable without losing SQLite's DB.
// In particular, the production two-second budget is NOT shortened for tests.
func TestBillingRefundDispatcherProductionCapacity(t *testing.T) {
	if os.Getenv("TEST_REFUND_CAPACITY") != "1" {
		t.Skip("explicit bounded capacity assessment not enabled")
	}
	f := newRefundCapacityFixture(t, 576, "")
	assessRefundDispatcherCapacity(t, f)
}

// The native variant shares the same workload and production dispatcher limits.
// Its fault is an actual independent transaction holding the original user row,
// not the SQLite BeginTx seam. It requires a separate EMPTY disposable database.
func TestBillingRefundDispatcherPostgresRowLockCapacity(t *testing.T) {
	dsn := os.Getenv("TEST_REFUND_CAPACITY_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_REFUND_CAPACITY_POSTGRES_DSN not configured")
	}
	require.Equal(t, "1", os.Getenv("TEST_REFUND_CAPACITY_DISPOSABLE"))
	f := newRefundCapacityFixture(t, 576, dsn)
	assessRefundDispatcherCapacity(t, f)
}

func assessRefundDispatcherCapacity(t *testing.T, f *refundCapacityFixture) {
	t.Helper()
	const queued, firstBurst, secondBurst = 256, 64, 256
	require.Equal(t, queued, cap(f.dispatcher.slots))
	require.Equal(t, 4, cap(f.dispatcher.database))
	require.Equal(t, 2*time.Second, f.dispatcher.attemptTimeout)
	require.Equal(t, time.Second, f.dispatcher.minBackoff)
	require.Equal(t, 30*time.Second, f.dispatcher.maxBackoff)
	before := refundCapacityResources()
	var admitted []refundCapacityRawSample
	for index := range queued - 1 {
		result := f.call(index)
		require.NoError(t, result.Err)
		require.Equal(t, http.StatusBadGateway, result.Status)
		admitted = append(admitted, f.rawSample(result))
	}
	require.Len(t, f.dispatcher.slots, queued-1)
	f.awaitDatabasePermits(t, 4)
	last := f.call(queued - 1)
	require.NoError(t, last.Err)
	require.Equal(t, http.StatusBadGateway, last.Status)
	admitted = append(admitted, f.rawSample(last))
	require.Len(t, f.dispatcher.slots, queued)
	f.logJSON(t, "refund_capacity_admission", map[string]any{"logical_calls": queued, "retained_jobs": len(f.dispatcher.slots), "raw_samples": admitted})
	full := refundCapacityResources()
	for _, burst := range []struct{ offset, size int }{{queued, firstBurst}, {queued + firstBurst, secondBurst}} {
		if !t.Run(fmt.Sprintf("overflow_%d", burst.size), func(t *testing.T) {
			f.awaitDatabasePermits(t, 4)
			phase := &refundCapacityPhase{want: int32(burst.size), entered: make(chan struct{})}
			f.phase.Store(phase)
			start := make(chan struct{})
			results := make(chan refundCapacityHTTPResult, burst.size)
			var ready, callers sync.WaitGroup
			defer func() {
				if t.Failed() {
					// Release both cooperative and real SQL faults before joining
					// callers, even if a liveness/accounting assertion failed.
					f.stop()
				}
				callers.Wait()
				f.phase.Store(nil)
			}()
			ready.Add(burst.size)
			for index := range burst.size {
				callers.Go(func() {
					ready.Done()
					<-start
					results <- f.call(burst.offset + index)
				})
			}
			ready.Wait()
			began := time.Now()
			close(start)
			select {
			case <-phase.entered:
			case <-time.After(8 * time.Second):
				t.Fatal("bounded HTTP burst did not enter the refund seam")
			}
			activeClientsAtSnapshot := burst.size - len(results)
			peak := refundCapacityResources()
			require.Len(t, f.dispatcher.slots, queued, "overflow must not grow the retained receipt queue")
			require.LessOrEqual(t, len(f.dispatcher.database), 4)
			// A live fallback must not keep the session mutex while awaiting the
			// shared permits/SQL budget. These are real public admission checks.
			probe := make(chan bool, 1)
			callers.Go(func() {
				s := f.sessions[burst.offset]
				// Handler-entry notification occurs immediately before Refund.
				// Observe its decision without blocking on a broken session lock;
				// do not mistake scheduler order for an admission-policy failure.
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				for {
					requested := false
					if s.mu.TryLock() {
						requested = s.refundRequested && s.refundInFlight
						s.mu.Unlock()
					}
					if requested {
						break
					}
					if ctx.Err() != nil {
						probe <- false
						return
					}
					runtime.Gosched()
				}
				probe <- s.NeedsRefund() && errors.Is(s.Reserve(20), model.ErrBillingFinalized) && errors.Is(s.Settle(10), model.ErrBillingFinalized)
			})
			select {
			case responsive := <-probe:
				require.True(t, responsive)
			case <-time.After(time.Second):
				t.Fatal("refund fallback held the session admission mutex")
			}
			var httpTimes, refundTimes []time.Duration
			var raw []refundCapacityRawSample
			watchdog := time.NewTimer(8 * time.Second)
			defer watchdog.Stop()
			for range burst.size {
				select {
				case result := <-results:
					require.NoError(t, result.Err)
					require.Equal(t, http.StatusBadGateway, result.Status)
					httpTimes = append(httpTimes, result.Duration)
					refundTimes = append(refundTimes, time.Duration(f.refundNanos[result.Index].Load()))
					raw = append(raw, f.rawSample(result))
				case <-watchdog.C:
					t.Fatal("concurrent overflow exceeded the test liveness watchdog; not a production SLA")
				}
			}
			callers.Wait()
			slices.SortFunc(raw, func(a, b refundCapacityRawSample) int { return a.Index - b.Index })
			require.Equal(t, int32(burst.size), phase.started.Load())
			require.Positive(t, phase.sawFourPermits.Load(), "observe an overflow while the shared four DB permits are occupied")
			require.Len(t, f.dispatcher.slots, queued)
			f.assertHeld(t, 0)
			report := map[string]any{"scope": "loopback Gin finalization seam, not full API", "fault": f.mode, "logical_calls": burst.size, "overflow_unpersisted": burst.size,
				"elapsed_ms":   float64(time.Since(began)) / float64(time.Millisecond),
				"http_wall_ms": refundCapacityDistribution(httpTimes), "refund_wall_ms": refundCapacityDistribution(refundTimes),
				"raw_samples": raw, "sample_units": "nanoseconds", "percentile_definition": "sorted[floor((n-1)*p/100)]",
				"limits":                  "256 retained dispatcher jobs; fixture independently starts 64 or 256 clients; not a process goroutine cap",
				"measurement_caveats":     "fast in-memory logging; HTTP MaxConnsPerHost=256 is synthetic, not production admission; snapshots include fixture/client/pprof overhead; cooperative 2s budget is not a hard wall-time cap",
				"maximum_active_handlers": phase.peak.Load(), "queued_jobs": len(f.dispatcher.slots),
				"client_calls_inflight_at_snapshot": activeClientsAtSnapshot,
				"peak_blocked_db_attempts":          f.fault.peak.Load(), "resources_before": before, "resources_full_queue": full, "resources_during_burst": peak}
			f.logJSON(t, "refund_capacity_metrics", report)
		}) {
			return
		}
	}
	require.Equal(t, int32(4), f.fault.peak.Load())
	f.logs.mu.Lock()
	unconfirmed := make(map[string]int, len(f.logs.unconfirmed))
	for requestID, count := range f.logs.unconfirmed {
		unconfirmed[requestID] = count
	}
	secretSeen, missingReview := f.logs.secretSeen, f.logs.missingReview
	f.logs.mu.Unlock()
	for index := queued; index < len(f.sessions); index++ {
		require.Equal(t, 1, unconfirmed[f.sessions[index].relayInfo.RequestId])
	}
	require.False(t, secretSeen)
	require.False(t, missingReview)
	// Only already accepted decisions can proceed after the outage is removed.
	// The overflow calls have returned and remain unpersisted, not silently
	// upgraded into durable intents or claimed as successful refunds.
	f.dispatcher.group.BeginDrain()
	f.releaseFault()
	joined, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	require.NoError(t, f.dispatcher.group.Wait(joined))
	require.Empty(t, f.dispatcher.slots)
	require.Empty(t, f.dispatcher.database)
	pending := 0
	for index := range queued {
		requestID := f.sessions[index].relayInfo.RequestId
		var receipt model.BillingFinalization
		require.NoError(t, f.db.First(&receipt, "request_id = ?", requestID).Error)
		require.Contains(t, []string{model.BillingRefundPending, model.BillingRefunded}, receipt.State)
		if receipt.State == model.BillingRefundPending {
			pending++
		}
		// An explicit retry of this PROVED pending owner is valid; no absent
		// overflow row is ever manufactured by this reconciliation check.
		for range 2 {
			ctx, stop := context.WithTimeout(context.Background(), 2*time.Second)
			err := model.RecoverBillingRefund(ctx, requestID)
			stop()
			require.NoError(t, err)
		}
	}
	f.assertHeld(t, queued)
	sqlDB, err := f.db.DB()
	require.NoError(t, err)
	require.Zero(t, sqlDB.Stats().InUse, "all independent blocker and worker SQL connections must be returned")
	if f.mode == "postgres_row_lock" {
		var active int64
		require.NoError(t, f.db.Raw("SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND pid <> pg_backend_pid() AND xact_start IS NOT NULL").Scan(&active).Error)
		require.Zero(t, active, "no abandoned native transaction after joining all workers")
	}
	f.logJSON(t, "refund_capacity_recovery", map[string]any{"accepted": queued, "durable_pending_before_explicit_retry": pending, "unresolved_overflow": firstBurst + secondBurst, "resources_after": refundCapacityResources(), "database_attempts": f.fault.samples(), "attempt_notes": "permit_wait inferred from the unchanged 2s context deadline; DB wall includes pool, driver, transaction and cleanup, not pure row-lock time; attempts are not attributed to HTTP indices"})
}

type refundCapacityFaultPool struct {
	gorm.ConnPool
	controlled bool
	timeout    time.Duration
	active     atomic.Int32
	peak       atomic.Int32
	changed    chan struct{}
	open       chan struct{}
	once       sync.Once
	mu         sync.Mutex
	attempts   []refundCapacityDatabaseSample
}

func (p *refundCapacityFaultPool) release() { p.once.Do(func() { close(p.open) }) }

func (p *refundCapacityFaultPool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	began := time.Now()
	deadline, bounded := ctx.Deadline()
	sample := refundCapacityDatabaseSample{StartedUnixNS: began.UnixNano(), Bounded: bounded}
	if bounded {
		sample.PermitWaitNS = int64(began.Sub(deadline.Add(-p.timeout)))
	}
	p.mu.Lock()
	sample.Index = len(p.attempts)
	p.attempts = append(p.attempts, sample)
	p.mu.Unlock()
	active := p.active.Add(1)
	for previous := p.peak.Load(); active > previous && !p.peak.CompareAndSwap(previous, active); previous = p.peak.Load() {
	}
	select {
	case p.changed <- struct{}{}:
	default:
	}
	var finished sync.Once
	finish := func(err error) {
		finished.Do(func() {
			p.active.Add(-1)
			sample.DatabaseWallNS = int64(time.Since(began))
			sample.ErrorCode, _ = model.BillingRefundErrorCode(err)
			p.mu.Lock()
			p.attempts[sample.Index] = sample
			p.mu.Unlock()
			select {
			case p.changed <- struct{}{}:
			default:
			}
		})
	}
	if p.controlled {
		select {
		case <-ctx.Done():
			finish(ctx.Err())
			return nil, ctx.Err()
		case <-p.open:
		}
	}
	if err := ctx.Err(); err != nil {
		finish(err)
		return nil, err
	}
	tx, err := p.ConnPool.(gorm.TxBeginner).BeginTx(ctx, opts)
	if err != nil {
		finish(err)
		return nil, err
	}
	return &refundCapacityTx{Tx: tx, ctx: ctx, finish: finish}, nil
}

type refundCapacityDatabaseSample struct {
	Index          int    `json:"index"`
	StartedUnixNS  int64  `json:"started_unix_ns"`
	PermitWaitNS   int64  `json:"permit_wait_ns"`
	DatabaseWallNS int64  `json:"database_wall_ns"`
	Bounded        bool   `json:"bounded"`
	ErrorCode      string `json:"error_code"`
}

func (p *refundCapacityFaultPool) samples() []refundCapacityDatabaseSample {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.attempts)
}

type refundCapacityTx struct {
	*sql.Tx
	ctx    context.Context
	finish func(error)
}

func (tx *refundCapacityTx) Commit() error {
	err := tx.Tx.Commit()
	tx.finish(err)
	return err
}

func (tx *refundCapacityTx) Rollback() error {
	err := tx.Tx.Rollback()
	if tx.ctx.Err() != nil {
		tx.finish(tx.ctx.Err())
	} else {
		tx.finish(err)
	}
	return err
}

type refundCapacityLogSink struct {
	mu            sync.Mutex
	unconfirmed   map[string]int
	secretSeen    bool
	missingReview bool
}

func (s *refundCapacityLogSink) Write(data []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	line := string(data)
	if strings.Contains(line, "synthetic-capacity-token") || strings.Contains(line, model.BillingTokenFingerprint("synthetic-capacity-token")) {
		s.secretSeen = true
	}
	if strings.Contains(line, "event=admission_unconfirmed ") {
		if !strings.Contains(line, "reconciliation_required=true") {
			s.missingReview = true
		}
		_, tail, found := strings.Cut(line, "request_id=\"")
		if found {
			request, _, _ := strings.Cut(tail, "\"")
			s.unconfirmed[request]++
		}
	}
	return len(data), nil
}

type refundCapacityPhase struct {
	want           int32
	started        atomic.Int32
	active         atomic.Int32
	peak           atomic.Int32
	sawFourPermits atomic.Int32
	entered        chan struct{}
}

type refundCapacityHTTPResult struct {
	Index         int
	Status        int
	StartedUnixNS int64
	Duration      time.Duration
	Err           error
}

type refundCapacityRawSample struct {
	Index               int   `json:"index"`
	HTTPStartedUnixNS   int64 `json:"http_started_unix_ns"`
	RefundStartedUnixNS int64 `json:"refund_started_unix_ns"`
	HTTPWallNS          int64 `json:"http_wall_ns"`
	RefundWallNS        int64 `json:"refund_wall_ns"`
	HTTPStatus          int   `json:"http_status"`
}

type refundCapacityFixture struct {
	db            *gorm.DB
	mode          string
	dispatcher    *billingRefundDispatcher
	fault         *refundCapacityFaultPool
	logs          *refundCapacityLogSink
	sessions      []*BillingSession
	refundStarted []atomic.Int64
	refundNanos   []atomic.Int64
	phase         atomic.Pointer[refundCapacityPhase]
	server        *httptest.Server
	client        *http.Client
	releaseFault  func()
	stopOnce      sync.Once
}

func newRefundCapacityFixture(t *testing.T, count int, postgresDSN string) *refundCapacityFixture {
	t.Helper()
	setupBegan := time.Now()
	var driver gorm.Dialector = sqlite.Open(filepath.Join(t.TempDir(), "refund-capacity.db"))
	dialect, mode := common.DatabaseTypeSQLite, "controlled_begin_tx"
	if postgresDSN != "" {
		driver = postgres.New(postgres.Config{DSN: postgresDSN, PreferSimpleProtocol: true})
		dialect, mode = common.DatabaseTypePostgreSQL, "postgres_row_lock"
	}
	db, err := gorm.Open(driver, &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	if postgresDSN != "" {
		sqlDB.SetMaxOpenConns(8) // blocker + four shared permits + independent evidence reads
	}
	tables, err := db.Migrator().GetTables()
	require.NoError(t, err)
	require.Empty(t, tables, "refusing a nonempty capacity database")
	oldDB, oldLog := model.DB, model.LOG_DB
	oldMain, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
	oldDispatcher, oldGroup := billingRefundIntents, billingBackground
	oldRedis, oldBatch := common.RedisEnabled, common.BatchUpdateEnabled
	common.LogWriterMu.Lock()
	oldWriter := gin.DefaultErrorWriter
	logs := &refundCapacityLogSink{unconfirmed: map[string]int{}}
	gin.DefaultErrorWriter = logs
	common.LogWriterMu.Unlock()
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(dialect, dialect)
	common.RedisEnabled, common.BatchUpdateEnabled = false, false
	d := newBillingRefundDispatcher(lifecycle.NewGroup(), cap(oldDispatcher.slots), cap(oldDispatcher.database), oldDispatcher.attemptTimeout, oldDispatcher.minBackoff, oldDispatcher.maxBackoff)
	billingRefundIntents, billingBackground = d, d.group
	f := &refundCapacityFixture{db: db, mode: mode, dispatcher: d, logs: logs, sessions: make([]*BillingSession, count), refundStarted: make([]atomic.Int64, count), refundNanos: make([]atomic.Int64, count)}
	t.Cleanup(func() {
		f.stop()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		require.NoError(t, d.group.Wait(ctx), "join all receipt workers BEFORE restoring any database/cache globals")
		billingRefundIntents, billingBackground = oldDispatcher, oldGroup
		model.DB, model.LOG_DB = oldDB, oldLog
		common.SetDatabaseTypes(oldMain, oldLogType)
		common.RedisEnabled, common.BatchUpdateEnabled = oldRedis, oldBatch
		common.LogWriterMu.Lock()
		gin.DefaultErrorWriter = oldWriter
		common.LogWriterMu.Unlock()
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.BalanceLedger{}, &model.BalanceLedgerOperation{}, &model.BillingFinalization{}))
	var version string
	versionQuery := "SELECT sqlite_version()"
	if postgresDSN != "" {
		versionQuery = "SELECT version()"
	}
	require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
	t.Logf("refund_capacity_environment dialect=%s version=%s fault=%s", dialect, version, mode)
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&model.User{Id: 41201, Username: "capacity-user", AffCode: "capacity-user", Quota: 100000, QuotaPaid: 100000}).Error)
	require.NoError(t, db.Create(&model.Token{Id: 41202, UserId: 41201, Key: "synthetic-capacity-token", Status: common.TokenStatusEnabled, RemainQuota: 100000}).Error)
	for index := range count {
		requestID := fmt.Sprintf("capacity-%03d", index)
		funding := &WalletFunding{userId: 41201, requestId: requestID, debitOrder: []model.LedgerAccount{model.AccountPaid}}
		require.NoError(t, funding.PreConsume(10))
		require.NoError(t, model.DecreaseTokenQuota(41202, "synthetic-capacity-token", 7))
		f.sessions[index] = &BillingSession{relayInfo: &relaycommon.RelayInfo{UserId: 41201, TokenId: 41202, TokenKey: "synthetic-capacity-token", RequestId: requestID}, funding: funding, preConsumedQuota: 10, tokenConsumed: 7}
	}
	require.NoError(t, db.Model(&model.Token{}).Where("id = ?", 41202).Update("status", common.TokenStatusDisabled).Error)
	wrapped := db.Session(&gorm.Session{NewDB: true, Context: context.Background()})
	config := *wrapped.Config
	wrapped.Config = &config
	f.fault = &refundCapacityFaultPool{ConnPool: db.ConnPool, controlled: postgresDSN == "", timeout: d.attemptTimeout, changed: make(chan struct{}, 1), open: make(chan struct{})}
	f.releaseFault = f.fault.release
	if postgresDSN != "" {
		lockCtx, cancelLock := context.WithTimeout(context.Background(), 2*time.Minute)
		held, lockErr := sqlDB.BeginTx(lockCtx, nil)
		if lockErr != nil {
			cancelLock()
		}
		require.NoError(t, lockErr)
		var release sync.Once
		f.releaseFault = func() { release.Do(func() { _ = held.Rollback(); cancelLock(); f.fault.release() }) }
		var id int
		// Explicit PostgreSQL-only independent blocker. Runtime locks still
		// execute through the real unchanged model helper/driver.
		require.NoError(t, held.QueryRowContext(lockCtx, "SELECT id FROM users WHERE id = $1 FOR UPDATE", 41201).Scan(&id))
		require.Equal(t, 41201, id)
	}
	wrapped.Config.ConnPool, wrapped.Statement.ConnPool = f.fault, f.fault
	model.DB = wrapped
	router := gin.New()
	router.POST("/controlled-refund/:id", func(c *gin.Context) {
		index, err := strconv.Atoi(c.Param("id"))
		if err != nil || index < 0 || index >= len(f.sessions) {
			c.Status(http.StatusBadRequest)
			return
		}
		phase := f.phase.Load()
		if phase != nil {
			active := phase.active.Add(1)
			for old := phase.peak.Load(); active > old && !phase.peak.CompareAndSwap(old, active); old = phase.peak.Load() {
			}
			if len(d.database) == 4 {
				phase.sawFourPermits.Add(1)
			}
			if phase.started.Add(1) == phase.want {
				close(phase.entered)
			}
			defer phase.active.Add(-1)
		}
		began := time.Now()
		f.refundStarted[index].Store(began.UnixNano())
		f.sessions[index].Refund(c)
		f.refundNanos[index].Store(int64(time.Since(began)))
		c.String(http.StatusBadGateway, "synthetic upstream failure\n")
	})
	f.server = httptest.NewServer(router)
	f.client = &http.Client{Timeout: 8 * time.Second, Transport: &http.Transport{MaxIdleConns: 256, MaxIdleConnsPerHost: 256, MaxConnsPerHost: 256}}
	t.Logf("refund_capacity_setup original_receipts=%d wall_ns=%d", count, time.Since(setupBegan))
	return f
}

func (f *refundCapacityFixture) stop() {
	f.stopOnce.Do(func() {
		f.dispatcher.group.BeginDrain()
		f.dispatcher.group.ForceCancel()
		if f.releaseFault != nil {
			f.releaseFault()
		}
		if f.server != nil {
			f.server.Close()
		}
		if f.client != nil {
			f.client.CloseIdleConnections()
		}
	})
}

func (f *refundCapacityFixture) rawSample(result refundCapacityHTTPResult) refundCapacityRawSample {
	return refundCapacityRawSample{Index: result.Index, HTTPStartedUnixNS: result.StartedUnixNS, RefundStartedUnixNS: f.refundStarted[result.Index].Load(), HTTPWallNS: int64(result.Duration), RefundWallNS: f.refundNanos[result.Index].Load(), HTTPStatus: result.Status}
}

func (f *refundCapacityFixture) logJSON(t *testing.T, label string, value any) {
	t.Helper()
	encoded, err := common.Marshal(value)
	require.NoError(t, err)
	t.Logf("%s %s", label, encoded)
}

func (f *refundCapacityFixture) call(index int) refundCapacityHTTPResult {
	began := time.Now()
	result := refundCapacityHTTPResult{Index: index, StartedUnixNS: began.UnixNano()}
	response, err := f.client.Post(fmt.Sprintf("%s/controlled-refund/%d", f.server.URL, index), "application/octet-stream", nil)
	if err == nil {
		result.Status = response.StatusCode
		_, err = io.Copy(io.Discard, response.Body)
		closeErr := response.Body.Close()
		if err == nil {
			err = closeErr
		}
	}
	result.Duration, result.Err = time.Since(began), err
	return result
}

func (f *refundCapacityFixture) awaitDatabasePermits(t *testing.T, count int32) {
	t.Helper()
	watchdog := time.NewTimer(8 * time.Second)
	defer watchdog.Stop()
	for f.fault.active.Load() != count {
		select {
		case <-f.fault.changed:
		case <-watchdog.C:
			t.Fatal("controlled DB failure did not occupy the expected shared permits")
		}
	}
	if f.mode == "postgres_row_lock" {
		// The client-side BeginTx barrier is insufficient evidence of a real
		// row wait. Observe actual server lock waits in this dedicated DB. This
		// bounded SQL handshake needs no scheduling sleep or guessed delay.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		for probes := 1; ; probes++ {
			var blocked int64
			err := f.db.WithContext(ctx).Raw("SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE '%FROM \"users\"%' AND cardinality(pg_blocking_pids(pid)) > 0").Scan(&blocked).Error
			require.NoError(t, err, "native lock-wait synchronization exceeded its watchdog")
			if blocked == int64(count) {
				t.Logf("refund_capacity_native_lock_barrier actual_blocked=%d probes=%d", blocked, probes)
				return
			}
		}
	}
}

func (f *refundCapacityFixture) assertHeld(t *testing.T, refunded int) {
	t.Helper()
	var user model.User
	var token model.Token
	require.NoError(t, f.db.First(&user, 41201).Error)
	require.NoError(t, f.db.First(&token, 41202).Error)
	require.Equal(t, 100000-(len(f.sessions)-refunded)*10, user.Quota)
	require.Equal(t, user.Quota, user.QuotaPaid)
	require.Zero(t, user.QuotaBonus)
	require.Zero(t, user.QuotaReward)
	require.Equal(t, 100000-(len(f.sessions)-refunded)*7, token.RemainQuota)
	require.Equal(t, (len(f.sessions)-refunded)*7, token.UsedQuota)
	require.Equal(t, common.TokenStatusDisabled, token.Status)
	var owners, rows, operations int64
	require.NoError(t, f.db.Model(&model.BillingFinalization{}).Count(&owners).Error)
	require.NoError(t, f.db.Model(&model.BalanceLedger{}).Count(&rows).Error)
	require.NoError(t, f.db.Model(&model.BalanceLedgerOperation{}).Count(&operations).Error)
	require.Equal(t, int64(refunded), owners)
	require.Equal(t, int64(len(f.sessions)+refunded), rows)
	require.Equal(t, rows, operations)
	for index, session := range f.sessions {
		var original model.BalanceLedger
		require.NoError(t, f.db.First(&original, "idempotency_key = ?", "billing:preconsume:"+session.relayInfo.RequestId).Error)
		require.Equal(t, -10, original.Delta)
		require.Equal(t, 41201, original.UserId)
		require.Equal(t, string(model.AccountPaid), original.Account)
		require.Equal(t, model.LedgerReasonBillingPreConsume, original.Reason)
		if index >= refunded {
			require.True(t, session.NeedsRefund())
		}
		if index < refunded {
			var receipt model.BillingFinalization
			require.NoError(t, f.db.First(&receipt, "request_id = ?", session.relayInfo.RequestId).Error)
			require.Equal(t, model.BillingRefunded, receipt.State)
			require.Equal(t, 10, receipt.SourceHeld)
			require.Equal(t, 7, receipt.TokenHeld)
			require.Equal(t, 10, receipt.Paid)
			require.Equal(t, 41201, receipt.UserID)
			require.Equal(t, 41202, receipt.TokenID)
			require.Equal(t, model.BillingTokenFingerprint("synthetic-capacity-token"), receipt.TokenFingerprint)
			var refund model.BalanceLedger
			require.NoError(t, f.db.First(&refund, "idempotency_key = ?", "billing:refund:"+session.relayInfo.RequestId).Error)
			require.Equal(t, 10, refund.Delta)
			require.Equal(t, original.UserId, refund.UserId)
			require.Equal(t, original.Account, refund.Account)
			require.Equal(t, original.RequestId, refund.RequestId)
			require.Equal(t, model.LedgerReasonBillingRefund, refund.Reason)
		}
	}
}

func refundCapacityDistribution(values []time.Duration) map[string]float64 {
	ordered := slices.Clone(values)
	slices.Sort(ordered)
	result := map[string]float64{"count": float64(len(ordered))}
	for name, percentile := range map[string]int{"p50": 50, "p95": 95, "p99": 99, "max": 100} {
		result[name] = float64(ordered[(len(ordered)-1)*percentile/100]) / float64(time.Millisecond)
	}
	return result
}

func refundCapacityResources() map[string]any {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	var stacks bytes.Buffer
	_ = pprof.Lookup("goroutine").WriteTo(&stacks, 2)
	text := stacks.String()
	result := map[string]any{"goroutines_total": runtime.NumGoroutine(), "heap_alloc_bytes": memory.HeapAlloc, "heap_inuse_bytes": memory.HeapInuse,
		"dispatcher_complete_frames":    strings.Count(text, "(*billingRefundDispatcher).complete("),
		"http_client_readloop_frames":   strings.Count(text, "(*persistConn).readLoop("),
		"http_client_writeloop_frames":  strings.Count(text, "(*persistConn).writeLoop("),
		"http_server_connection_frames": strings.Count(text, "(*conn).serve(")}
	for _, path := range []string{"/sys/fs/cgroup/memory.current", "/sys/fs/cgroup/memory.max"} {
		data, err := os.ReadFile(path)
		if err != nil {
			result[path] = "unavailable"
		} else {
			result[path] = strings.TrimSpace(string(data))
		}
	}
	return result
}
