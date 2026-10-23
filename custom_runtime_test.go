package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/extension"
	"github.com/QuantumNous/new-api/pkg/lifecycle"
	"github.com/QuantumNous/new-api/router"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type customDrainResult struct {
	proven bool
	err    error
}

func TestCustomRuntimeQuiescesWithoutCancellingAcceptedWork(t *testing.T) {
	workers, gate := lifecycle.NewGroup(), lifecycle.NewGate()
	defer workers.ForceCancel()
	requestCtx, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()
	workerStarted, quiesced := make(chan struct{}), make(chan struct{})
	releaseWorker, workerContext := make(chan struct{}), make(chan error, 1)
	require.True(t, workers.Go("accepted", func(schedule, work context.Context) {
		close(workerStarted)
		<-schedule.Done()
		close(quiesced)
		<-releaseWorker
		workerContext <- work.Err()
	}))
	<-workerStarted
	requestStarted, releaseRequest := make(chan struct{}), make(chan struct{})
	requestContext := make(chan error, 1)
	server := httptest.NewUnstartedServer(gate.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requestStarted)
		<-releaseRequest
		requestContext <- r.Context().Err()
		w.WriteHeader(http.StatusNoContent)
	})))
	server.Config.BaseContext = func(net.Listener) context.Context { return requestCtx }
	server.Start()
	defer server.Close()
	defer func() {
		closeCustomRuntimeSignal(releaseRequest)
		closeCustomRuntimeSignal(releaseWorker)
		cancelRequests()
		workers.BeginDrain()
		workers.ForceCancel()
	}()
	clientDone := make(chan error, 1)
	go func() {
		resp, err := server.Client().Get(server.URL)
		if resp != nil {
			_ = resp.Body.Close()
		}
		clientDone <- err
	}()
	<-requestStarted
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan customDrainResult, 1)
	go func() {
		proven, err := drainCustomRuntime(ctx, time.Second, server.Config, gate, workers, cancelRequests)
		done <- customDrainResult{proven, err}
	}()
	<-quiesced
	require.False(t, workers.Go("too-late", func(_, _ context.Context) { t.Error("admitted late work") }))
	recorder := httptest.NewRecorder()
	gate.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("admitted late request") })).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	close(releaseRequest)
	close(releaseWorker)
	result := <-done
	require.True(t, result.proven)
	require.NoError(t, result.err)
	require.NoError(t, <-clientDone)
	require.NoError(t, <-requestContext)
	require.NoError(t, <-workerContext)
}

func TestCustomRuntimeForceCancelsThenProvesBothJoins(t *testing.T) {
	workers, gate := lifecycle.NewGroup(), lifecycle.NewGate()
	requestCtx, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()
	workerStarted, workerStopped := make(chan struct{}), make(chan struct{})
	require.True(t, workers.Go("cancellable", func(_, work context.Context) {
		close(workerStarted)
		<-work.Done()
		close(workerStopped)
	}))
	<-workerStarted
	requestStarted := make(chan struct{})
	server := httptest.NewUnstartedServer(gate.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requestStarted)
		<-r.Context().Done()
	})))
	server.Config.BaseContext = func(net.Listener) context.Context { return requestCtx }
	server.Start()
	defer func() {
		cancelRequests()
		workers.BeginDrain()
		workers.ForceCancel()
		server.Close()
	}()
	clientDone := make(chan struct{})
	go func() {
		resp, _ := server.Client().Get(server.URL)
		if resp != nil {
			_ = resp.Body.Close()
		}
		close(clientDone)
	}()
	<-requestStarted
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	proven, err := drainCustomRuntime(ctx, time.Second, server.Config, gate, workers, cancelRequests)
	require.True(t, proven)
	require.ErrorIs(t, err, context.DeadlineExceeded, "forced shutdown must remain observable after a successful join")
	<-workerStopped
	<-clientDone
}

func TestCustomRuntimeUnjoinedWorkCannotAuthorizePoolClose(t *testing.T) {
	workers, gate := lifecycle.NewGroup(), lifecycle.NewGate()
	release := make(chan struct{})
	require.True(t, workers.Go("ignores-cancel", func(_, _ context.Context) { <-release }))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	proven, err := drainCustomRuntime(ctx, 20*time.Millisecond, &http.Server{}, gate, workers, func() {})
	closedPools := false
	if proven {
		closedPools = true
	}
	// Join the intentionally stuck fixture before making any fatal assertion.
	close(release)
	joinCtx, joinCancel := context.WithTimeout(context.Background(), time.Second)
	defer joinCancel()
	require.NoError(t, workers.Wait(joinCtx))
	require.False(t, closedPools)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestCustomRuntimeRetainsLateWorkerFailureAfterJoin(t *testing.T) {
	workers, gate := lifecycle.NewGroup(), lifecycle.NewGate()
	require.True(t, workers.Go("late-failure", func(schedule, _ context.Context) {
		<-schedule.Done()
		panic("private token that must not be returned")
	}))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	proven, err := drainCustomRuntime(ctx, time.Second, &http.Server{}, gate, workers, func() {})
	require.True(t, proven)
	require.Error(t, err)
	require.False(t, errors.Is(err, context.DeadlineExceeded))
	require.Contains(t, err.Error(), "late-failure panicked")
	require.False(t, strings.Contains(err.Error(), "private token"))
}

func TestCustomRuntimeDoesNotTreatHijackedSocketShutdownAsJoin(t *testing.T) {
	workers, gate := lifecycle.NewGroup(), lifecycle.NewGate()
	requestCtx, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewUnstartedServer(gate.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			close(entered)
			return
		}
		defer conn.Close()
		close(entered)
		<-release // deliberately ignores cancellation, as a broken WS reader would
	})))
	server.Config.BaseContext = func(net.Listener) context.Context { return requestCtx }
	server.Start()
	defer func() {
		closeCustomRuntimeSignal(release)
		cancelRequests()
		server.Close()
	}()
	conn, err := net.DialTimeout("tcp", server.Listener.Addr().String(), time.Second)
	require.NoError(t, err)
	defer conn.Close()
	_, err = fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: fixture.local\r\n\r\n")
	require.NoError(t, err)
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	proven, drainErr := drainCustomRuntime(ctx, 20*time.Millisecond, server.Config, gate, workers, cancelRequests)
	close(release)
	joinCtx, joinCancel := context.WithTimeout(context.Background(), time.Second)
	defer joinCancel()
	require.NoError(t, gate.Wait(joinCtx))
	require.False(t, proven)
	require.ErrorIs(t, drainErr, context.DeadlineExceeded)
}

// All fixtures release their own blocked handlers even after a fatal assertion.
func closeCustomRuntimeSignal(ch chan struct{}) {
	select {
	case <-ch:
	default:
		close(ch)
	}
}

func TestCustomRuntimeRegistryWiresAsyncImagesWithExistingAuth(t *testing.T) {
	reg, err := newRuntimeRegistry(nil)
	require.Error(t, err, "startup must not silently register unowned async jobs")
	require.Nil(t, reg)
	workers := lifecycle.NewGroup()
	defer workers.ForceCancel()
	defer workers.BeginDrain()
	reg, err = newRuntimeRegistry(workers)
	require.NoError(t, err)
	require.NoError(t, reg.Freeze())
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	scopes := router.SetApiRouter(engine)
	router.SetRelayRouter(engine)
	require.NoError(t, reg.ApplyRoutesWithScopes(scopes, extension.PriorityBeforePlugin))
	require.NoError(t, reg.ApplyRoutesWithScopes(scopes, extension.PriorityAfterPlugin))
	for _, target := range []struct{ method, path string }{
		{http.MethodPost, "/v1/images/tasks/generations"},
		{http.MethodPost, "/v1/images/tasks/edits"},
		{http.MethodGet, "/v1/images/tasks/opaque-job-id"},
	} {
		t.Run(target.method+target.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, httptest.NewRequest(target.method, target.path, nil))
			require.Equal(t, http.StatusUnauthorized, recorder.Code, "registered route must reject an anonymous request, not fall through to HTML/404")
		})
	}
}

func TestCustomRuntimeWorkerOnlyDeadlineIsNotLostAfterForceJoin(t *testing.T) {
	workers, gate := lifecycle.NewGroup(), lifecycle.NewGate()
	defer workers.ForceCancel()
	defer workers.BeginDrain()
	require.True(t, workers.Go("cancellable-only", func(_, work context.Context) { <-work.Done() }))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	proven, err := drainCustomRuntime(ctx, time.Second, &http.Server{}, gate, workers, func() {})
	require.True(t, proven)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}
