package service

import (
	"context"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/lifecycle"
	"github.com/bytedance/gopkg/util/gopool"
)

// The main request/task group is drained before this group is sealed. Refunds
// admitted by already accepted work are therefore not dropped on SIGTERM or
// client cancellation.
var billingBackground = lifecycle.NewGroup()

func runBillingBackground(fn func()) {
	if !billingBackground.Submit("billing-refund", gopool.Go, func(_, _ context.Context) { fn() }) {
		// Defensive compatibility for embedded hosts; main joins all producers
		// before sealing this group, so a late producer cannot occur there.
		fn()
	}
}

func DrainBillingBackground(ctx context.Context) error {
	billingBackground.BeginDrain()
	return billingBackground.Wait(ctx)
}

func StartRuntimeWorkers(group *lifecycle.Group) {
	group.Go("system-instance-reporter", func(schedule, work context.Context) {
		lifecycle.Periodic(schedule, work, func() time.Duration { return systemInstanceReportInterval }, true, func(context.Context) { reportSystemInstanceWithLog() })
	})
	if !common.IsMasterNode {
		return
	}
	group.Go("codex-credential-refresh", func(schedule, work context.Context) {
		lifecycle.Periodic(schedule, work, func() time.Duration { return codexCredentialRefreshTickInterval }, true, runCodexCredentialAutoRefreshOnceContext)
	})
	group.Go("subscription-maintenance", func(schedule, work context.Context) {
		lifecycle.Periodic(schedule, work, func() time.Duration { return subscriptionResetTickInterval }, true, runSubscriptionQuotaResetOnceContext)
	})
	group.Go("auth-artifact-cleanup", func(schedule, work context.Context) {
		lifecycle.Periodic(schedule, work, func() time.Duration { return authArtifactCleanupInterval }, true, func(context.Context) { cleanupAuthArtifacts() })
	})
	group.Go("system-task-runner", RunSystemTaskRunner)
}
