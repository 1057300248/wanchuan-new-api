package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/lifecycle"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// withSystemTaskRegistry swaps the package registry for the given handlers for
// the duration of a test and restores the original registry afterward.
func withSystemTaskRegistry(t *testing.T, handlers ...SystemTaskHandler) {
	t.Helper()
	systemTaskHandlersMu.Lock()
	saved := systemTaskHandlers
	systemTaskHandlers = map[string]SystemTaskHandler{}
	for _, h := range handlers {
		systemTaskHandlers[h.Type()] = h
	}
	systemTaskHandlersMu.Unlock()
	t.Cleanup(func() {
		systemTaskHandlersMu.Lock()
		systemTaskHandlers = saved
		systemTaskHandlersMu.Unlock()
	})
}

type stubScheduledHandler struct {
	taskType string
	enabled  bool
	interval time.Duration
	onRun    func(ctx context.Context, task *model.SystemTask, runnerID string)
}

type stubSystemTaskRunResult struct {
	taskID   string
	taskType string
	err      error
}

func (h *stubScheduledHandler) Type() string { return h.taskType }

func (h *stubScheduledHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	if h.onRun != nil {
		h.onRun(ctx, task, runnerID)
	}
}

func (h *stubScheduledHandler) Enabled() bool           { return h.enabled }
func (h *stubScheduledHandler) Interval() time.Duration { return h.interval }
func (h *stubScheduledHandler) NewPayload() any         { return nil }

func countSystemTasks(t *testing.T, taskType string) int64 {
	t.Helper()
	var count int64
	require.NoError(t, model.DB.Model(&model.SystemTask{}).Where("type = ?", taskType).Count(&count).Error)
	return count
}

func TestSystemTaskSchedulerCreatesWhenDueAndDedups(t *testing.T) {
	truncate(t)

	handler := &stubScheduledHandler{taskType: "test_scheduled", enabled: true, interval: time.Minute}
	withSystemTaskRegistry(t, handler)

	runSystemTaskScheduler()
	require.Equal(t, int64(1), countSystemTasks(t, handler.taskType))

	// An active (pending) row already exists, so a second pass must not create
	// another row.
	runSystemTaskScheduler()
	require.Equal(t, int64(1), countSystemTasks(t, handler.taskType))

	// Finish the run; with a fresh updated_at the next run is not due yet.
	latest, err := model.GetLatestSystemTask(handler.taskType)
	require.NoError(t, err)
	require.NotNil(t, latest)
	_, claimed, err := model.ClaimSystemTask(latest.ID, handler.taskType, "runner-a", common.GetTimestamp()+60)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, model.FinishSystemTask(latest.TaskID, "runner-a", model.SystemTaskStatusSucceeded, nil, ""))

	runSystemTaskScheduler()
	require.Equal(t, int64(1), countSystemTasks(t, handler.taskType))

	// Backdate the finished row beyond the interval -> the job becomes due again.
	require.NoError(t, model.DB.Model(&model.SystemTask{}).
		Where("task_id = ?", latest.TaskID).
		Update("updated_at", common.GetTimestamp()-120).Error)

	runSystemTaskScheduler()
	require.Equal(t, int64(2), countSystemTasks(t, handler.taskType))
}

func TestSystemTaskSchedulerSkipsDisabled(t *testing.T) {
	truncate(t)

	handler := &stubScheduledHandler{taskType: "test_disabled", enabled: false, interval: time.Minute}
	withSystemTaskRegistry(t, handler)

	runSystemTaskScheduler()
	assert.Equal(t, int64(0), countSystemTasks(t, handler.taskType))
}

func TestSystemTaskClaimPassDispatchesByType(t *testing.T) {
	truncate(t)

	ran := make(chan stubSystemTaskRunResult, 1)
	handler := &stubScheduledHandler{
		taskType: "test_dispatch",
		enabled:  true,
		interval: time.Minute,
		onRun: func(_ context.Context, task *model.SystemTask, runnerID string) {
			ran <- stubSystemTaskRunResult{
				taskType: task.Type,
				err:      model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusSucceeded, nil, ""),
			}
		},
	}
	withSystemTaskRegistry(t, handler)

	_, err := model.CreateSystemTask(handler.taskType, nil, nil)
	require.NoError(t, err)

	runSystemTaskClaimPass("runner-dispatch")

	select {
	case got := <-ran:
		require.NoError(t, got.err)
		assert.Equal(t, handler.taskType, got.taskType)
	case <-time.After(2 * time.Second):
		t.Fatal("claimed task was not dispatched to its handler")
	}

	require.Eventually(t, func() bool {
		latest, err := model.GetLatestSystemTask(handler.taskType)
		return err == nil && latest != nil && latest.Status == model.SystemTaskStatusSucceeded
	}, 2*time.Second, 20*time.Millisecond)
}

func TestSystemTaskClaimPassDispatchesEarliestPendingByType(t *testing.T) {
	truncate(t)

	ran := make(chan stubSystemTaskRunResult, 2)
	handlerA := &stubScheduledHandler{
		taskType: "test_dispatch_a",
		enabled:  true,
		interval: time.Minute,
		onRun: func(_ context.Context, task *model.SystemTask, runnerID string) {
			ran <- stubSystemTaskRunResult{
				taskID: task.TaskID,
				err:    model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusSucceeded, nil, ""),
			}
		},
	}
	handlerB := &stubScheduledHandler{
		taskType: "test_dispatch_b",
		enabled:  true,
		interval: time.Minute,
		onRun: func(_ context.Context, task *model.SystemTask, runnerID string) {
			ran <- stubSystemTaskRunResult{
				taskID: task.TaskID,
				err:    model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusSucceeded, nil, ""),
			}
		},
	}
	withSystemTaskRegistry(t, handlerA, handlerB)

	firstA, err := model.CreateSystemTask(handlerA.taskType, nil, nil)
	require.NoError(t, err)
	secondTaskID, err := model.GenerateSystemTaskID()
	require.NoError(t, err)
	secondA := &model.SystemTask{
		TaskID: secondTaskID,
		Type:   handlerA.taskType,
		Status: model.SystemTaskStatusPending,
	}
	require.NoError(t, model.DB.Create(secondA).Error)
	firstB, err := model.CreateSystemTask(handlerB.taskType, nil, nil)
	require.NoError(t, err)

	runSystemTaskClaimPass("runner-dispatch")

	got := map[string]bool{}
	for range 2 {
		select {
		case result := <-ran:
			require.NoError(t, result.err)
			got[result.taskID] = true
		case <-time.After(2 * time.Second):
			t.Fatal("claimed tasks were not dispatched to their handlers")
		}
	}

	assert.True(t, got[firstA.TaskID])
	assert.True(t, got[firstB.TaskID])
	assert.False(t, got[secondA.TaskID])

	require.Eventually(t, func() bool {
		reloaded, err := model.GetSystemTaskByTaskID(secondA.TaskID)
		return err == nil && reloaded != nil && reloaded.Status == model.SystemTaskStatusPending
	}, 2*time.Second, 20*time.Millisecond)
}

func TestEnqueueSystemTaskReportsCreatedAndExistingActive(t *testing.T) {
	truncate(t)

	first, created, err := EnqueueSystemTask("test_enqueue", map[string]bool{"manual": true})
	require.NoError(t, err)
	require.True(t, created)
	require.NotNil(t, first)

	existing, created, err := EnqueueSystemTask("test_enqueue", nil)
	require.NoError(t, err)
	require.False(t, created)
	require.NotNil(t, existing)
	assert.Equal(t, first.TaskID, existing.TaskID)

	_, claimed, err := model.ClaimSystemTask(first.ID, first.Type, "runner-a", common.GetTimestamp()+60)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, model.FinishSystemTask(first.TaskID, "runner-a", model.SystemTaskStatusSucceeded, nil, ""))

	second, created, err := EnqueueSystemTask("test_enqueue", nil)
	require.NoError(t, err)
	require.True(t, created)
	require.NotNil(t, second)
	assert.NotEqual(t, first.TaskID, second.TaskID)
}

func TestOwnedSystemTaskRunnerCanceledBeforeStartDoesNotClaim(t *testing.T) {
	truncate(t)
	wasMaster := common.IsMasterNode
	common.IsMasterNode = true
	t.Cleanup(func() { common.IsMasterNode = wasMaster })
	handler := &stubScheduledHandler{taskType: "owned_precanceled"}
	withSystemTaskRegistry(t, handler)
	task, err := model.CreateSystemTask(handler.Type(), nil, nil)
	require.NoError(t, err)
	schedule, stop := context.WithCancel(context.Background())
	stop()
	RunSystemTaskRunner(schedule, context.Background())
	reloaded, err := model.GetSystemTaskByTaskID(task.TaskID)
	require.NoError(t, err)
	require.NotNil(t, reloaded)
	assert.Equal(t, model.SystemTaskStatusPending, reloaded.Status)
	var locks int64
	require.NoError(t, model.DB.Model(&model.SystemTaskLock{}).Count(&locks).Error)
	assert.Zero(t, locks)
}

func TestOwnedSystemTaskRunnerCancellationAndTerminalJoin(t *testing.T) {
	for _, mode := range []string{"graceful", "forced", "panic"} {
		t.Run(mode, func(t *testing.T) {
			truncate(t)
			wasMaster := common.IsMasterNode
			common.IsMasterNode = true
			t.Cleanup(func() { common.IsMasterNode = wasMaster })
			started := make(chan context.Context, 1)
			finished := make(chan error, 1)
			release := make(chan struct{})
			var releaseOnce sync.Once
			handler := &stubScheduledHandler{
				taskType: "owned_" + mode,
				onRun: func(ctx context.Context, task *model.SystemTask, runnerID string) {
					started <- ctx
					select {
					case <-release:
					case <-ctx.Done():
					}
					if mode == "panic" {
						panic("private value must not enter terminal error")
					}
					status := model.SystemTaskStatusSucceeded
					if ctx.Err() != nil {
						status = model.SystemTaskStatusFailed
					}
					finished <- model.FinishSystemTask(task.TaskID, runnerID, status, nil, "")
				},
			}
			withSystemTaskRegistry(t, handler)
			first, err := model.CreateSystemTask(handler.Type(), nil, nil)
			require.NoError(t, err)
			laterID, err := model.GenerateSystemTaskID()
			require.NoError(t, err)
			later := &model.SystemTask{TaskID: laterID, Type: handler.Type(), Status: model.SystemTaskStatusPending}
			require.NoError(t, model.DB.Create(later).Error)
			workers := lifecycle.NewGroup()
			t.Cleanup(func() {
				workers.BeginDrain()
				workers.ForceCancel()
				releaseOnce.Do(func() { close(release) })
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				require.NoError(t, workers.Wait(ctx), "fixture must join before restoring the registry or database")
			})
			require.True(t, workers.Go("system-task-runner", RunSystemTaskRunner))
			var handlerContext context.Context
			select {
			case handlerContext = <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("owned runner did not dispatch the pending task")
			}
			workers.BeginDrain()
			assert.NoError(t, handlerContext.Err(), "quiescing must not cancel accepted work")
			waitCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			assert.ErrorIs(t, workers.Wait(waitCtx), context.DeadlineExceeded, "an active handler is not a completed join")
			cancel()
			if mode == "forced" {
				workers.ForceCancel()
			} else {
				releaseOnce.Do(func() { close(release) })
			}
			joinCtx, joinCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer joinCancel()
			require.NoError(t, workers.Wait(joinCtx))
			if mode != "panic" {
				require.NoError(t, <-finished, "handler terminal write must succeed independently of the runner join")
			}
			reloaded, err := model.GetSystemTaskByTaskID(first.TaskID)
			require.NoError(t, err)
			require.NotNil(t, reloaded)
			if mode == "graceful" {
				assert.Equal(t, model.SystemTaskStatusSucceeded, reloaded.Status)
			} else {
				assert.Equal(t, model.SystemTaskStatusFailed, reloaded.Status)
			}
			if mode == "panic" {
				assert.Equal(t, "system task handler panicked", reloaded.Error)
			}
			pending, err := model.GetSystemTaskByTaskID(later.TaskID)
			require.NoError(t, err)
			require.NotNil(t, pending)
			assert.Equal(t, model.SystemTaskStatusPending, pending.Status, "draining must not claim the next task")
			var locks int64
			require.NoError(t, model.DB.Model(&model.SystemTaskLock{}).Where("task_id = ?", first.TaskID).Count(&locks).Error)
			assert.Zero(t, locks, "terminal handler must release its lease")
		})
	}
}

func TestOwnedSystemTaskRunnerWaitsForInFlightHeartbeatReturn(t *testing.T) {
	truncate(t)
	wasMaster := common.IsMasterNode
	common.IsMasterNode = true
	t.Cleanup(func() { common.IsMasterNode = wasMaster })
	started := make(chan context.Context, 1)
	finished := make(chan error, 1)
	releaseHandler, releaseHeartbeat := make(chan struct{}), make(chan struct{})
	heartbeatEntered := make(chan struct{})
	var handlerOnce, heartbeatOnce, enteredOnce sync.Once
	handler := &stubScheduledHandler{
		taskType: "owned_heartbeat_join",
		onRun: func(ctx context.Context, task *model.SystemTask, runnerID string) {
			started <- ctx
			select {
			case <-releaseHandler:
			case <-ctx.Done():
			}
			finished <- model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusSucceeded, nil, "")
		},
	}
	withSystemTaskRegistry(t, handler)
	task, err := model.CreateSystemTask(handler.Type(), nil, nil)
	require.NoError(t, err)
	workers := lifecycle.NewGroup()
	const callback = "owned_runtime_heartbeat_return"
	t.Cleanup(func() {
		workers.BeginDrain()
		workers.ForceCancel()
		handlerOnce.Do(func() { close(releaseHandler) })
		heartbeatOnce.Do(func() { close(releaseHeartbeat) })
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, workers.Wait(ctx), "release and join before removing the callback or restoring database state")
		require.NoError(t, model.DB.Callback().Update().Remove(callback))
	})
	// Run the real 20-second heartbeat. Block after its SQL transaction releases
	// the fixture's only connection, but before RenewSystemTaskLock returns.
	// This models a global-DB call that cancellation cannot itself join.
	require.NoError(t, model.DB.Callback().Update().After("gorm:commit_or_rollback_transaction").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "system_task_locks" && tx.Error == nil && tx.RowsAffected == 1 {
			enteredOnce.Do(func() { close(heartbeatEntered) })
			<-releaseHeartbeat
		}
	}))
	require.True(t, workers.Go("system-task-runner", RunSystemTaskRunner))
	var handlerContext context.Context
	select {
	case handlerContext = <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("owned runner did not dispatch heartbeat fixture")
	}
	var originalLock model.SystemTaskLock
	require.NoError(t, model.DB.Where("task_id = ?", task.TaskID).First(&originalLock).Error)
	workers.BeginDrain()
	select {
	case <-heartbeatEntered:
	case <-time.After(30 * time.Second):
		t.Fatal("real lease heartbeat did not renew while accepted work was draining")
	}
	assert.NoError(t, handlerContext.Err())
	var renewedLock model.SystemTaskLock
	require.NoError(t, model.DB.Where("task_id = ?", task.TaskID).First(&renewedLock).Error)
	assert.Greater(t, renewedLock.LockedUntil, originalLock.LockedUntil)
	handlerOnce.Do(func() { close(releaseHandler) })
	select {
	case err := <-finished:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("handler terminal write did not finish")
	}
	for _, force := range []bool{false, true} {
		if force {
			workers.ForceCancel()
		}
		waitCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		assert.ErrorIs(t, workers.Wait(waitCtx), context.DeadlineExceeded, "even a terminal handler cannot prove join while its heartbeat call is still in flight")
		cancel()
	}
	heartbeatOnce.Do(func() { close(releaseHeartbeat) })
	joinCtx, joinCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer joinCancel()
	require.NoError(t, workers.Wait(joinCtx))
	reloaded, err := model.GetSystemTaskByTaskID(task.TaskID)
	require.NoError(t, err)
	require.NotNil(t, reloaded)
	assert.Equal(t, model.SystemTaskStatusSucceeded, reloaded.Status)
	var locks int64
	require.NoError(t, model.DB.Model(&model.SystemTaskLock{}).Where("task_id = ?", task.TaskID).Count(&locks).Error)
	assert.Zero(t, locks)
}
