package model

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	billingRefundRetryable = "retryable"
	billingRefundReview    = "review_required"
)

type billingRefundIssue struct {
	code    string
	message string
}

func (e *billingRefundIssue) Error() string { return e.message }

// Called only at locally known invariant checks, never on arbitrary error text.
func billingRefundReviewError(code, message string) error {
	return &billingRefundIssue{code: code, message: message}
}

type billingRefundMetadataError struct{ cause error }

func (e *billingRefundMetadataError) Error() string {
	return "billing refund retry metadata was not persisted"
}
func (e *billingRefundMetadataError) Unwrap() error { return e.cause }

// BillingRefundErrorCode returns bounded operational labels, never SQL, driver
// messages, token identities or credentials. Unknown/uncertain errors retry.
func BillingRefundErrorCode(err error) (code, class string) {
	if err == nil {
		return "", ""
	}
	var metadata *billingRefundMetadataError
	if errors.As(err, &metadata) {
		return "metadata_write_failed", billingRefundRetryable
	}
	var issue *billingRefundIssue
	if errors.As(err, &issue) {
		return issue.code, billingRefundReview
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded", billingRefundRetryable
	case errors.Is(err, context.Canceled):
		return "canceled", billingRefundRetryable
	case errors.Is(err, gorm.ErrRecordNotFound):
		return "missing_record", billingRefundReview
	case errors.Is(err, ErrBillingFinalized):
		return "terminal_owner_conflict", billingRefundReview
	case errors.Is(err, ErrSubscriptionPeriodUnknown), errors.Is(err, ErrSubscriptionPeriodClosed):
		return "subscription_period", billingRefundReview
	case errors.Is(err, ErrLedgerIdempotencyConflict), errors.Is(err, ErrLedgerInvalidEntry):
		return "ledger_evidence", billingRefundReview
	default:
		return "recovery_error", billingRefundRetryable
	}
}

func billingRefundRetryAt(now int64, attempts int, class string) int64 {
	// Review-required is not a dead-money state: the exact pending owner is
	// rechecked hourly so repaired authoritative evidence can recover normally.
	delay := int64(3600)
	if class != billingRefundReview {
		delay = min(int64(30)<<min(max(attempts, 1)-1, 7), 3600)
	}
	if now > math.MaxInt64-delay {
		return math.MaxInt64
	}
	return now + delay
}

func recordBillingRefundFailure(ctx context.Context, requestID string, failure error, now int64) error {
	code, class := BillingRefundErrorCode(failure)
	var escalation *BillingFinalization
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var r BillingFinalization
		if err := lockForUpdate(tx).Where("request_id = ?", requestID).First(&r).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil // Never manufacture a monetary owner from an error.
			}
			return err
		}
		if r.State != BillingRefundPending {
			return nil // A concurrent successful refund/lost ACK remains terminal.
		}
		now = max(now, r.LastAttemptAt)
		attempts := min(max(r.Attempts, 0), 999999) + 1
		updates := map[string]any{
			"attempts": attempts, "last_attempt_at": now,
			"last_error_code": code, "last_error_class": class,
			"next_retry_at": billingRefundRetryAt(now, attempts, class),
		}
		if r.FirstFailedAt == 0 {
			updates["first_failed_at"], updates["first_error_code"], updates["first_error_class"] = now, code, class
		}
		if r.EscalatedAt == 0 && (class == billingRefundReview || attempts >= 5 || r.CreatedAt > 0 && now >= r.CreatedAt && now-r.CreatedAt >= 3600) {
			updates["escalated_at"] = now
			r.Attempts, r.NextRetryAt = attempts, updates["next_retry_at"].(int64)
			escalation = &r
		}
		return tx.Model(&BillingFinalization{}).Where("request_id = ? AND state = ?", requestID, BillingRefundPending).Updates(updates).Error
	})
	if err == nil && escalation != nil {
		// A best-effort local operational signal, NOT exactly-once alert delivery.
		// The durable escalated_at marker is queryable even if this log is lost.
		r := escalation
		source := r.Source
		if source != "wallet" && source != "subscription" {
			source = "unknown"
		}
		common.SysError(fmt.Sprintf("billing refund escalation: request_id=%q user_id=%d source=%s code=%s class=%s attempts=%d next_retry_at=%d source_held=%d token_held=%d", r.RequestID[:min(len(r.RequestID), 64)], r.UserID, source, code, class, r.Attempts, r.NextRetryAt, r.SourceHeld, r.TokenHeld))
	}
	return err
}
