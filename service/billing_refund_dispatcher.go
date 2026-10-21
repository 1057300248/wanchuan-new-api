package service

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"
	"weak"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/lifecycle"
	"github.com/bytedance/gopkg/util/gopool"
)

// These bounds control operational work, not charging or refund eligibility.
// Accepted jobs retain only an already-made Refund decision and its immutable
// original receipt. Memory is NOT a journal: a crash before the first committed
// intent still requires operator reconciliation, never an inferred refund.
var billingRefundIntents = newBillingRefundDispatcher(billingBackground, 256, 4, 2*time.Second, time.Second, 30*time.Second)

type billingRefundDispatcher struct {
	group          *lifecycle.Group
	slots          chan struct{}
	database       chan struct{}
	attemptTimeout time.Duration
	minBackoff     time.Duration
	maxBackoff     time.Duration
}

func newBillingRefundDispatcher(group *lifecycle.Group, capacity, concurrency int, timeout, minBackoff, maxBackoff time.Duration) *billingRefundDispatcher {
	if group == nil || capacity <= 0 || concurrency <= 0 || timeout <= 0 || minBackoff <= 0 || maxBackoff < minBackoff || maxBackoff > time.Hour {
		panic("invalid billing refund dispatcher bounds")
	}
	return &billingRefundDispatcher{group: group, slots: make(chan struct{}, capacity), database: make(chan struct{}, concurrency), attemptTimeout: timeout, minBackoff: minBackoff, maxBackoff: maxBackoff}
}

func (d *billingRefundDispatcher) submitOrPersist(s *BillingSession, receipt model.BillingFinalization) {
	select {
	case d.slots <- struct{}{}:
		// An outage must not retain the whole request/stream/credential graph.
		// Only observe completion if the caller still owns a live session; the
		// exact financial receipt and retry job survive independently of it.
		observer := weak.Make(s)
		if d.group.Submit("billing-refund-intent", gopool.Go, func(_, work context.Context) {
			defer func() { <-d.slots }()
			d.complete(observer, receipt, work)
		}) {
			return
		}
		<-d.slots
	default:
	}

	// Overflow/late producers do not spawn or wait for a queue slot. One bounded
	// persistence-only fallback outside s.mu lets a healthy DB accept the intent
	// even when the memory queue is full. Semaphore wait and DB work share ONE
	// independent deadline; no monetary refund is run on the request goroutine.
	state, err := d.persist(context.Background(), receipt)
	if errors.Is(err, model.ErrBillingFinalized) {
		reportBillingRefundIntent("terminal_owned", receipt, 1, err)
	} else if err != nil {
		state = ""
		reportBillingRefundIntent("admission_unconfirmed", receipt, 1, err)
	}
	s.finishDurableRefund(state)
}

func (d *billingRefundDispatcher) persist(ctx context.Context, receipt model.BillingFinalization) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, d.attemptTimeout)
	defer cancel()
	select {
	case d.database <- struct{}{}:
		defer func() { <-d.database }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return model.PersistBillingRefund(ctx, receipt)
}

func (d *billingRefundDispatcher) refund(ctx context.Context, requestID string) error {
	ctx, cancel := context.WithTimeout(ctx, d.attemptTimeout)
	defer cancel()
	select {
	case d.database <- struct{}{}:
		defer func() { <-d.database }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return model.RecoverBillingRefund(ctx, requestID)
}

func (d *billingRefundDispatcher) complete(observer weak.Pointer[BillingSession], receipt model.BillingFinalization, work context.Context) {
	state := ""
	defer func() {
		if session := observer.Value(); session != nil {
			session.finishDurableRefund(state)
		}
		if value := recover(); value != nil {
			// Never include an arbitrary panic value or raw DB error in audit logs.
			reportBillingRefundIntent("worker_interrupted", receipt, 0, nil)
			panic(value)
		}
	}()
	backoff := d.minBackoff
	attempts := 0
	for {
		var err error
		state, err = d.persist(work, receipt)
		if errors.Is(err, model.ErrBillingFinalized) {
			reportBillingRefundIntent("terminal_owned", receipt, attempts+1, err)
			break
		}
		if err == nil {
			break
		}
		state = "" // An error/unknown ACK is not a proved terminal state.
		attempts = min(attempts+1, 1<<30)
		if work.Err() != nil {
			reportBillingRefundIntent("shutdown_unconfirmed", receipt, attempts, err)
			return
		}
		// Report first failure and exponentially spaced reminders, not every
		// failing DB call during a long outage. The receipt stays owned in memory.
		if attempts == 1 || attempts&(attempts-1) == 0 {
			reportBillingRefundIntent("retrying_unconfirmed", receipt, attempts, err)
		}
		delay := min(d.maxBackoff, backoff+time.Duration(rand.Int64N(int64(backoff/4)+1)))
		timer := time.NewTimer(delay)
		select {
		case <-work.Done():
			timer.Stop()
			reportBillingRefundIntent("shutdown_unconfirmed", receipt, attempts, work.Err())
			return
		case <-timer.C:
		}
		backoff = min(d.maxBackoff, backoff*2)
	}
	if state != model.BillingRefundPending {
		return
	}
	// From here the database, rather than this process, owns retryability. A
	// failed immediate refund is picked up by the durable pending-row worker;
	// an unknown successful COMMIT ACK may already have made the row refunded.
	if err := d.refund(work, receipt.RequestID); err != nil {
		reportBillingRefundIntent("durable_recovery_unconfirmed", receipt, 0, err)
		return
	}
	state = model.BillingRefunded
}

func reportBillingRefundIntent(event string, receipt model.BillingFinalization, attempts int, err error) {
	// Request IDs are quoted to prevent log injection. No key, fingerprint,
	// provider payload, raw driver error or credential is included. A known
	// Refund decision in logs is investigation evidence, NOT durable ownership.
	requestID := receipt.RequestID
	if len(requestID) > 64 {
		requestID = requestID[:64]
	}
	source := receipt.Source
	if source != BillingSourceWallet && source != BillingSourceSubscription {
		source = "invalid"
	}
	code, class := model.BillingRefundErrorCode(err)
	needsReview := event == "capture_failed" || event == "admission_unconfirmed" || event == "shutdown_unconfirmed" || event == "worker_interrupted" || event == "retrying_unconfirmed" && class == "review_required"
	common.SysError(fmt.Sprintf("billing_refund_intent event=%s request_id=%q user_id=%d source=%q source_held=%d token_held=%d attempts=%d code=%q class=%q reconciliation_required=%t", event, requestID, receipt.UserID, source, receipt.SourceHeld, receipt.TokenHeld, attempts, code, class, needsReview))
}
