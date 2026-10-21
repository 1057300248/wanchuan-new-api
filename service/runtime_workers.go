package service

import (
	"context"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
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
	err := billingBackground.Wait(ctx)
	if err != nil && ctx.Err() != nil {
		// An unavailable intent store must not leave retry timers running after
		// the bounded shutdown deadline. Each unpersisted job reports the gap;
		// only committed refund_pending rows are restart-recoverable.
		billingBackground.ForceCancel()
	}
	return err
}

func StartRuntimeWorkers(group *lifecycle.Group) {
	group.Go("system-instance-reporter", func(schedule, work context.Context) {
		lifecycle.Periodic(schedule, work, func() time.Duration { return systemInstanceReportInterval }, true, func(context.Context) { reportSystemInstanceWithLog() })
	})
	group.Go("billing-refund-recovery", func(schedule, work context.Context) {
		lifecycle.Periodic(schedule, work, func() time.Duration { return 30 * time.Second }, true, func(ctx context.Context) {
			if _, err := model.RecoverPendingBillingRefunds(ctx, 100); err != nil {
				code, class := model.BillingRefundErrorCode(err)
				common.SysError(fmt.Sprintf("billing refund recovery: code=%s class=%s; inspect pending refund operations", code, class))
			}
		})
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
