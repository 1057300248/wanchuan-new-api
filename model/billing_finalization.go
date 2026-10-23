package model

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	BillingRefundPending = "refund_pending"
	BillingRefunded      = "refunded"
	BillingSettled       = "settled"
	BillingTaskOwned     = "task_owned"
	// Only these versioned intents are eligible for the new refund executor.
	// Historical refund_pending rows always require operator review.
	BillingRefundReady  = "refund_ready_v2"
	BillingRefundReview = "refund_review_v2"
)

var ErrBillingFinalized = errors.New("request billing already has a different terminal owner")

// This stage adds only the receipt data and ledger-evidence helpers.
// BillingFinalization is not registered for migration or connected to an owner
// workflow here; claim/reserve/settle/refund transitions are added as one later group.
//
// BillingFinalization is the unique request financial owner. New versioned
// held/refund-ready receipts are executable only through their explicit state
// transitions. Historical refund_pending rows remain read-only.
// Never delete historical rows while their original financial receipts survive.
type BillingFinalization struct {
	RequestID string `gorm:"type:varchar(64);primaryKey;index:idx_billing_refund_due,priority:3"`
	UserID    int    `gorm:"not null;index"`
	Source    string `gorm:"type:varchar(16);not null"`
	TokenID   int    `gorm:"not null"`
	// VARCHAR preserves the empty no-token identity; PostgreSQL CHAR pads it.
	TokenFingerprint string `json:"-" gorm:"type:varchar(64);not null"`
	// Legacy source and token reservations need not be equal. Never fabricate a
	// token credit from the source total or vice versa.
	SourceHeld         int    `gorm:"type:bigint;not null"`
	TokenHeld          int    `gorm:"type:bigint;not null"`
	PreConsumed        int    `gorm:"type:bigint;not null"`
	Paid               int    `gorm:"type:bigint;not null"`
	Bonus              int    `gorm:"type:bigint;not null"`
	Reward             int    `gorm:"type:bigint;not null"`
	SubscriptionID     int    `gorm:"not null"`
	SubscriptionPeriod int64  `gorm:"type:bigint;not null"`
	State              string `gorm:"type:varchar(24);not null;index:idx_billing_refund_queue,priority:1;index:idx_billing_refund_due,priority:1"`
	ActualQuota        int    `gorm:"type:bigint;not null"`
	TaskID             int64  `gorm:"not null"`
	TaskPublicID       string `gorm:"type:varchar(191);not null"`
	// Operational metadata never changes refund eligibility or money ownership.
	// Historical retry fields remain readable; no current worker schedules them.
	Attempts        int    `gorm:"not null"` // Failed recovery attempts, retained after recovery.
	FirstErrorCode  string `gorm:"type:varchar(48);not null;default:''"`
	FirstErrorClass string `gorm:"type:varchar(24);not null;default:''"`
	LastErrorCode   string `gorm:"type:varchar(48);not null;default:''"`
	LastErrorClass  string `gorm:"type:varchar(24);not null;default:''"`
	FirstFailedAt   int64  `gorm:"not null;default:0"`
	LastAttemptAt   int64  `gorm:"not null;default:0"`
	NextRetryAt     int64  `gorm:"not null;default:0;index:idx_billing_refund_due,priority:2"`
	EscalatedAt     int64  `gorm:"not null;default:0"`
	CreatedAt       int64  `gorm:"autoCreateTime"`
	UpdatedAt       int64  `gorm:"autoUpdateTime;index:idx_billing_refund_queue,priority:2"`
}

func BillingTokenFingerprint(key string) string {
	if key == "" {
		return ""
	}
	hash := sha256.Sum256([]byte(key))
	return hex.EncodeToString(hash[:])
}

func (r BillingFinalization) Shares() []LedgerShare {
	var shares []LedgerShare
	for _, s := range []LedgerShare{{AccountPaid, r.Paid}, {AccountBonus, r.Bonus}, {AccountReward, r.Reward}} {
		if s.Amount > 0 {
			shares = append(shares, s)
		}
	}
	return shares
}

func (r BillingFinalization) validate() error {
	if r.RequestID == "" || strings.TrimSpace(r.RequestID) != r.RequestID || len(r.RequestID) > 64 || r.UserID <= 0 {
		return billingRefundReviewError("receipt_invalid", "invalid billing finalization identity")
	}
	for _, n := range []int{r.SourceHeld, r.TokenHeld, r.PreConsumed, r.Paid, r.Bonus, r.Reward} {
		if n < 0 || n > common.MaxQuota {
			return billingRefundReviewError("receipt_invalid", "invalid billing held amount")
		}
	}
	if r.TokenID < 0 || r.TokenID == 0 && (r.TokenHeld != 0 || r.TokenFingerprint != "") || r.TokenID > 0 && len(r.TokenFingerprint) != 64 {
		return billingRefundReviewError("receipt_invalid", "invalid original billing token identity")
	}
	switch r.Source {
	case "wallet":
		if r.SourceHeld != r.Paid+r.Bonus+r.Reward || r.SubscriptionID != 0 || r.SubscriptionPeriod != 0 {
			return billingRefundReviewError("receipt_invalid", "wallet reservation shares mismatch")
		}
	case "subscription":
		if r.SubscriptionID <= 0 || r.SubscriptionPeriod <= 0 || r.Paid+r.Bonus+r.Reward != 0 {
			return billingRefundReviewError("receipt_invalid", "invalid subscription reservation identity")
		}
	default:
		return billingRefundReviewError("receipt_invalid", "unsupported durable billing source")
	}
	return nil
}

func sameBillingIdentity(a, b BillingFinalization) bool {
	return a.RequestID == b.RequestID && a.UserID == b.UserID && a.Source == b.Source && a.TokenID == b.TokenID && subtle.ConstantTimeCompare([]byte(a.TokenFingerprint), []byte(b.TokenFingerprint)) == 1 && a.SourceHeld == b.SourceHeld && a.TokenHeld == b.TokenHeld && a.PreConsumed == b.PreConsumed && a.Paid == b.Paid && a.Bonus == b.Bonus && a.Reward == b.Reward && a.SubscriptionID == b.SubscriptionID && a.SubscriptionPeriod == b.SubscriptionPeriod
}

// Independent fees retain their legacy key/signature and carry explicit new
// provenance. Realtime segments already have a dedicated canonical wss:N key.
// Neither is part of the original hold; never broadly ignore settle rows.
func verifiedIndependentBillingDebitTx(tx *gorm.DB, r *BillingFinalization, row BalanceLedger) (bool, error) {
	if row.Reason != LedgerReasonBillingSettle || row.Delta >= 0 {
		return false, nil
	}
	isFee := func(meta string) bool {
		var value struct {
			IndependentFee string `json:"independent_fee"`
		}
		return common.UnmarshalJsonStr(meta, &value) == nil && value.IndependentFee == "violation_fee"
	}
	fee := isFee(row.Metadata)
	var sequence uint64
	if !fee {
		prefix := "billing:settle:" + r.RequestID + ":wss:"
		if !strings.HasPrefix(row.IdempotencyKey, prefix) {
			return false, nil
		}
		parts := strings.Split(strings.TrimPrefix(row.IdempotencyKey, prefix), ":")
		if len(parts) != 2 {
			return false, billingRefundReviewError("source_evidence", "invalid realtime debit identity")
		}
		var err error
		sequence, err = strconv.ParseUint(parts[0], 10, 64)
		if err != nil || sequence == 0 {
			return false, billingRefundReviewError("source_evidence", "invalid realtime debit sequence")
		}
	}
	op, details, err := loadLedgerOperation(lockForUpdate(tx), row.IdempotencyKey)
	if err != nil {
		return false, err
	}
	if op.UserId != r.UserID || op.Operation != "debit" || op.Id != row.OperationId || len(details) == 0 {
		return false, billingRefundReviewError("source_evidence", "independent billing operation mismatch")
	}
	total := 0
	for _, d := range details {
		validPurpose := fee && isFee(d.Metadata) || !fee && d.Metadata == ""
		if d.UserId != r.UserID || d.RequestId != r.RequestID || d.ReferenceType != "relay" || d.ReferenceId != r.RequestID || d.Reason != LedgerReasonBillingSettle || d.IdempotencyKey != row.IdempotencyKey || d.TotalBefore != op.TotalBefore || d.TotalAfter != op.TotalAfter || d.Delta >= 0 || d.Delta < -common.MaxQuota || !validAccount(LedgerAccount(d.Account)) || !validPurpose {
			return false, billingRefundReviewError("source_evidence", "independent billing rows mismatch")
		}
		total -= d.Delta
		if total > common.MaxQuota {
			return false, billingRefundReviewError("source_evidence", "independent billing amount invalid")
		}
	}
	expectedKey := fmt.Sprintf("billing:settle:%s:%d", r.RequestID, total)
	if !fee {
		expectedKey = fmt.Sprintf("billing:settle:%s:wss:%d:%d", r.RequestID, sequence, total)
	}
	if row.IdempotencyKey != expectedKey || op.TotalBefore-op.TotalAfter != total {
		return false, billingRefundReviewError("source_evidence", "independent billing key mismatch")
	}
	return true, nil
}
