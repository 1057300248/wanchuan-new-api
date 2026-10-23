package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/chuanapi"
	"github.com/QuantumNous/new-api/extension"
	"github.com/QuantumNous/new-api/modules/imagegen"
	"github.com/QuantumNous/new-api/pkg/lifecycle"
)

// drainCustomRuntime owns only HTTP admissions and the explicitly registered
// workers, including the system-task runner and its handler/heartbeat children.
// Other upstream background services retain their upstream startup/lifetime
// contracts; this must not be presented as joining those jobs. A blocked
// global-DB call can outlive cancellation and must keep the join unproven.
// A true result proves these two owned domains can no longer use SQL pools.
func drainCustomRuntime(ctx context.Context, forceTimeout time.Duration, server *http.Server, requests *lifecycle.Gate, workers *lifecycle.Group, cancelRequests context.CancelFunc) (bool, error) {
	requests.BeginDrain()
	workers.BeginDrain()

	httpErr := server.Shutdown(ctx)
	requestErr := requests.Wait(ctx)
	workerErr := workers.Wait(ctx)
	// Group.Wait returns a context error only when completion is unproven;
	// its other possible error is the retained failure of a joined worker.
	joined := func(err error) bool {
		return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
	}
	if requestErr == nil && joined(workerErr) {
		return true, errors.Join(httpErr, workerErr)
	}

	// Accepted work is cancelled only after the original graceful budget.
	// Close wakes ordinary HTTP sockets, not hijacked connections. Hijacked
	// handlers must honor request cancellation and close their own socket;
	// Gate requires their return instead of treating Close as a join proof.
	drainErr := errors.Join(httpErr, requestErr, workerErr)
	cancelRequests()
	workers.ForceCancel()
	closeErr := server.Close()
	forceCtx, cancel := context.WithTimeout(context.Background(), forceTimeout)
	defer cancel()
	requestErr = requests.Wait(forceCtx)
	workerErr = workers.Wait(forceCtx)
	return requestErr == nil && joined(workerErr), errors.Join(drainErr, closeErr, requestErr, workerErr)
}

// The same explicit composition is used by startup and route wiring tests.
func newRuntimeRegistry(workers *lifecycle.Group) (*extension.Registry, error) {
	registry := extension.NewRegistry()
	if err := imagegen.Register(registry, workers); err != nil {
		return nil, err
	}
	if err := chuanapi.Register(registry); err != nil {
		return nil, err
	}
	return registry, nil
}
