package model

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	BillingRefundPending = "refund_pending"
	BillingRefunded      = "refunded"
	BillingSettled       = "settled"
	BillingTaskOwned     = "task_owned"
)

var ErrBillingFinalized = errors.New("request billing already has a different terminal owner")

// BillingFinalization is created at the EXISTING refund/settlement/task-insert
// decision, not before upstream work. It does not change refund eligibility,
// reservation policy, pricing or retries. Only refund_pending is recoverable.
// A crash before this row commits still requires source-ledger reconciliation.
// Never delete terminal rows while their original financial receipts survive.
type BillingFinalization struct {
	RequestID        string `gorm:"type:varchar(64);primaryKey"`
	UserID           int    `gorm:"not null;index"`
	Source           string `gorm:"type:varchar(16);not null"`
	TokenID          int    `gorm:"not null"`
	TokenFingerprint string `json:"-" gorm:"type:char(64);not null"`
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
	State              string `gorm:"type:varchar(24);not null;index:idx_billing_refund_queue,priority:1"`
	ActualQuota        int    `gorm:"type:bigint;not null"`
	TaskID             int64  `gorm:"not null"`
	TaskPublicID       string `gorm:"type:varchar(191);not null"`
	Attempts           int    `gorm:"not null"`
	CreatedAt          int64  `gorm:"autoCreateTime"`
	UpdatedAt          int64  `gorm:"autoUpdateTime;index:idx_billing_refund_queue,priority:2"`
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
		return errors.New("invalid billing finalization identity")
	}
	for _, n := range []int{r.SourceHeld, r.TokenHeld, r.PreConsumed, r.Paid, r.Bonus, r.Reward} {
		if n < 0 || n > common.MaxQuota {
			return errors.New("invalid billing held amount")
		}
	}
	if r.TokenID < 0 || r.TokenID == 0 && (r.TokenHeld != 0 || r.TokenFingerprint != "") || r.TokenID > 0 && len(r.TokenFingerprint) != 64 {
		return errors.New("invalid original billing token identity")
	}
	switch r.Source {
	case "wallet":
		if r.SourceHeld != r.Paid+r.Bonus+r.Reward || r.SubscriptionID != 0 || r.SubscriptionPeriod != 0 {
			return errors.New("wallet reservation shares mismatch")
		}
	case "subscription":
		if r.SubscriptionID <= 0 || r.SubscriptionPeriod <= 0 || r.Paid+r.Bonus+r.Reward != 0 {
			return errors.New("invalid subscription reservation identity")
		}
	default:
		return errors.New("unsupported durable billing source")
	}
	return nil
}

func sameBillingIdentity(a, b BillingFinalization) bool {
	return a.RequestID == b.RequestID && a.UserID == b.UserID && a.Source == b.Source && a.TokenID == b.TokenID && subtle.ConstantTimeCompare([]byte(a.TokenFingerprint), []byte(b.TokenFingerprint)) == 1 && a.SourceHeld == b.SourceHeld && a.TokenHeld == b.TokenHeld && a.PreConsumed == b.PreConsumed && a.Paid == b.Paid && a.Bonus == b.Bonus && a.Reward == b.Reward && a.SubscriptionID == b.SubscriptionID && a.SubscriptionPeriod == b.SubscriptionPeriod
}

// INSERT ... ON CONFLICT avoids poisoning a PostgreSQL transaction on first-owner
// races. The following current/locking read observes the committed winner on
// MySQL as well; no absent consistent snapshot is established first.
func claimBillingFinalizationTx(tx *gorm.DB, receipt BillingFinalization) (*BillingFinalization, error) {
	if err := receipt.validate(); err != nil {
		return nil, err
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	receipt.State = "b:" + hex.EncodeToString(nonce[:]) // private, never committed
	receipt.ActualQuota, receipt.TaskID, receipt.Attempts = 0, 0, 0
	receipt.TaskPublicID = ""
	if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "request_id"}}, DoNothing: true}).Create(&receipt).Error; err != nil {
		return nil, err
	}
	var stored BillingFinalization
	if err := lockForUpdate(tx).Where("request_id = ?", receipt.RequestID).First(&stored).Error; err != nil {
		return nil, err
	}
	if !sameBillingIdentity(stored, receipt) {
		return nil, errors.New("billing finalization identity mismatch")
	}
	if stored.State == receipt.State {
		stored.State = "building"
	} else {
		switch stored.State {
		case BillingRefundPending, BillingRefunded, BillingSettled, BillingTaskOwned:
		default:
			return nil, errors.New("invalid persisted billing finalization state")
		}
	}
	return &stored, nil
}

// validateBillingSourceTx verifies ORIGINAL held evidence under authoritative
// source locks, never from a current balance. Terminal-owner replays bypass it:
// successful settlement/task processing legitimately changes the source later.
func validateBillingSourceTx(tx *gorm.DB, r *BillingFinalization) error {
	if r.Source == "subscription" {
		record, _, err := lockSubscriptionReservationTx(tx, r.RequestID, r.SubscriptionID)
		if err != nil {
			return err
		}
		if record.UserId != r.UserID || record.ReservedAmount != int64(r.SourceHeld) || record.QuotaPeriod != r.SubscriptionPeriod || record.Status != "consumed" {
			return errors.New("subscription held evidence mismatch")
		}
		return nil
	}
	var user User
	if err := lockForUpdate(tx).First(&user, r.UserID).Error; err != nil {
		return err
	}
	var rows []BalanceLedger
	if err := lockForUpdate(tx).Where("user_id = ? AND reference_type = ? AND reference_id = ?", r.UserID, "relay", r.RequestID).Order("id").Find(&rows).Error; err != nil {
		return err
	}
	paid, bonus, reward := 0, 0, 0
	checked := map[string]bool{}
	for _, row := range rows {
		if independent, err := verifiedIndependentBillingDebitTx(tx, r, row); err != nil {
			return err
		} else if independent {
			continue
		}
		if row.RequestId != r.RequestID || row.Delta >= 0 || (row.Reason != LedgerReasonBillingPreConsume && row.Reason != LedgerReasonBillingReserve) {
			return errors.New("wallet reservation already adjusted or inconsistent")
		}
		if !checked[row.IdempotencyKey] {
			op, details, err := loadLedgerOperation(lockForUpdate(tx), row.IdempotencyKey)
			if err != nil {
				return err
			}
			if op.UserId != r.UserID || op.Operation != "debit" || op.Id != row.OperationId || len(details) == 0 {
				return errors.New("wallet reservation operation mismatch")
			}
			total := 0
			for _, detail := range details {
				if detail.UserId != r.UserID || detail.ReferenceType != "relay" || detail.ReferenceId != r.RequestID || detail.RequestId != r.RequestID || detail.IdempotencyKey != row.IdempotencyKey || detail.Delta >= 0 || detail.Delta < -common.MaxQuota || detail.Reason != row.Reason || !validAccount(LedgerAccount(detail.Account)) || detail.TotalBefore != op.TotalBefore || detail.TotalAfter != op.TotalAfter {
					return errors.New("wallet reservation operation rows mismatch")
				}
				total -= detail.Delta
				if total > common.MaxQuota {
					return errors.New("wallet reservation amount invalid")
				}
			}
			if op.TotalBefore-op.TotalAfter != total {
				return errors.New("wallet reservation operation amount mismatch")
			}
			expectedKey := "billing:preconsume:" + r.RequestID
			if row.Reason == LedgerReasonBillingReserve {
				prefix := "billing:reserve:" + r.RequestID + ":"
				if !strings.HasPrefix(row.IdempotencyKey, prefix) {
					return errors.New("wallet reserve key mismatch")
				}
				target, err := strconv.ParseInt(strings.TrimPrefix(row.IdempotencyKey, prefix), 10, 64)
				if err != nil || target <= 0 || target > common.MaxQuota {
					return errors.New("invalid wallet reserve target")
				}
				expectedKey = fmt.Sprintf("%s%d", prefix, target)
			}
			if row.IdempotencyKey != expectedKey {
				return errors.New("wallet reservation key mismatch")
			}
			checked[row.IdempotencyKey] = true
		}
		switch LedgerAccount(row.Account) {
		case AccountPaid:
			paid -= row.Delta
		case AccountBonus:
			bonus -= row.Delta
		case AccountReward:
			reward -= row.Delta
		default:
			return errors.New("unknown wallet reservation account")
		}
	}
	if paid != r.Paid || bonus != r.Bonus || reward != r.Reward {
		return errors.New("wallet held evidence mismatch")
	}
	return nil
}

// PersistBillingRefund records an already-made refund decision. A failed return
// may be a lost commit ACK: callers must keep their local admission closed and
// retry this exact identity, not permit another reserve/settlement.
func PersistBillingRefund(ctx context.Context, receipt BillingFinalization) (string, error) {
	var state string
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r, err := claimBillingFinalizationTx(tx, receipt)
		if err != nil {
			return err
		}
		state = r.State
		if state == BillingRefundPending || state == BillingRefunded {
			return nil
		}
		if state != "building" {
			return ErrBillingFinalized
		}
		if err = validateBillingSourceTx(tx, r); err != nil {
			return err
		}
		state = BillingRefundPending
		return tx.Model(r).Updates(map[string]any{"state": state}).Error
	})
	return state, err
}

// ApplyBillingTokenDeltaTx restores/debits ONLY the original authorized token.
// Soft deletion does not revive authentication; hard deletion leaves no counter.
func ApplyBillingTokenDeltaTx(tx *gorm.DB, r *BillingFinalization, delta int) error {
	// No token mutation is requested: a replacement cannot receive credit here.
	if delta == 0 {
		return nil
	}
	if r.TokenID == 0 {
		return nil
	}
	var token Token
	err := lockForUpdate(tx.Unscoped()).First(&token, r.TokenID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if token.UserId != r.UserID || subtle.ConstantTimeCompare([]byte(BillingTokenFingerprint(token.Key)), []byte(r.TokenFingerprint)) != 1 {
		return errors.New("original billing token identity mismatch")
	}
	return ApplyTokenQuotaDeltaTx(tx.Unscoped(), token.Id, token.Key, delta)
}

func RecoverBillingRefund(ctx context.Context, requestID string) error {
	var userID int
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var r BillingFinalization
		if err := lockForUpdate(tx).Where("request_id = ?", requestID).First(&r).Error; err != nil {
			return err
		}
		if err := r.validate(); err != nil {
			return err
		}
		userID = r.UserID
		if r.State == BillingRefunded {
			return nil
		}
		if r.State != BillingRefundPending {
			return ErrBillingFinalized
		}
		if err := validateBillingSourceTx(tx, &r); err != nil {
			return err
		}
		if r.Source == "subscription" {
			if err := RefundSubscriptionPreConsumeForRequestTx(tx, r.RequestID, r.UserID, r.SubscriptionID, int64(r.SourceHeld)); err != nil {
				return err
			}
		} else if r.SourceHeld > 0 {
			result, err := CreditQuotaSharesTx(tx, LedgerEntry{UserId: r.UserID, Delta: r.SourceHeld, Reason: LedgerReasonBillingRefund, ReferenceType: "relay", ReferenceId: r.RequestID, RequestId: r.RequestID, IdempotencyKey: "billing:refund:" + r.RequestID}, r.Shares())
			if err != nil {
				return err
			}
			if result.Replayed {
				return errors.New("refund operation exists without committed finalization")
			}
		}
		if err := ApplyBillingTokenDeltaTx(tx, &r, r.TokenHeld); err != nil {
			return err
		}
		return tx.Model(&r).Updates(map[string]any{"state": BillingRefunded}).Error
	})
	// Cache invalidation is safe even with an uncertain COMMIT acknowledgement.
	if userID > 0 {
		RefreshUserQuotaCache(userID, 0)
	}
	return err
}

// RecoverPendingBillingRefunds never selects settled/task_owned receipts and
// never infers refund eligibility from age. Bounded scans retain failed work.
func RecoverPendingBillingRefunds(ctx context.Context, limit int) (int, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	var rows []BillingFinalization
	if err := DB.WithContext(ctx).Where("state = ?", BillingRefundPending).Order("updated_at, request_id").Limit(limit).Find(&rows).Error; err != nil {
		return 0, err
	}
	completed := 0
	var errs []error
	for _, r := range rows {
		if err := RecoverBillingRefund(ctx, r.RequestID); err != nil {
			errs = append(errs, fmt.Errorf("refund %s: %w", r.RequestID, err))
			if noteErr := DB.WithContext(ctx).Model(&BillingFinalization{}).Where("request_id = ? AND state = ?", r.RequestID, BillingRefundPending).Updates(map[string]any{"attempts": gorm.Expr("attempts + 1"), "updated_at": common.GetTimestamp()}).Error; noteErr != nil {
				errs = append(errs, noteErr)
			}
		} else {
			completed++
		}
	}
	return completed, errors.Join(errs...)
}

// SettleBillingFinalization runs the existing source/token arithmetic under one
// terminal owner. The callback must reject source-only legacy replays: this row
// is the only proof that both sides and the terminal marker committed together.
func SettleBillingFinalization(ctx context.Context, receipt BillingFinalization, actual int, apply func(*gorm.DB) error) (bool, error) {
	if actual < 0 || actual > common.MaxQuota {
		return false, errors.New("invalid billing actual quota")
	}
	applied := false
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r, err := claimBillingFinalizationTx(tx, receipt)
		if err != nil {
			return err
		}
		if r.State == BillingSettled {
			if r.ActualQuota != actual {
				return errors.New("billing settlement replay amount mismatch")
			}
			return nil
		}
		if r.State != "building" {
			return ErrBillingFinalized
		}
		if err = validateBillingSourceTx(tx, r); err != nil {
			return err
		}
		if err = apply(tx); err != nil {
			return err
		}
		applied = true
		return tx.Model(r).Updates(map[string]any{"state": BillingSettled, "actual_quota": actual}).Error
	})
	return applied && err == nil, err
}

// InsertTaskWithBillingFinalization transfers ownership, not money. The task
// INSERT and destination-bound owner commit together. Task workers subsequently
// use their existing task/source/token order and never lock this row.
func InsertTaskWithBillingFinalization(ctx context.Context, receipt BillingFinalization, task *Task, omitColumns ...string) error {
	if task == nil || task.TaskID == "" || len(task.TaskID) > 191 {
		return errors.New("invalid billing task destination")
	}
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r, err := claimBillingFinalizationTx(tx, receipt)
		if err != nil {
			return err
		}
		if r.State == BillingTaskOwned {
			if r.TaskPublicID != task.TaskID || task.ID != 0 && task.ID != r.TaskID {
				return errors.New("billing task destination mismatch")
			}
			var stored Task
			if err = lockForUpdate(tx).First(&stored, r.TaskID).Error; err != nil {
				return err
			}
			if !billingTaskIdentityMatches(r, &stored) {
				return errors.New("billing task identity mismatch")
			}
			// Do not replace inline Data with the omitted persisted value. Quota may
			// already have evolved in a worker; it is not original-held evidence.
			task.ID = stored.ID
			return nil
		}
		if r.State != "building" {
			return ErrBillingFinalized
		}
		if !billingTaskIdentityMatches(r, task) || task.Quota != r.PreConsumed || !task.SettlementPending || task.PrivateData.PendingSettlement == nil || task.PrivateData.PendingSettlement.Phase != "submit" {
			return errors.New("billing task initial receipt mismatch")
		}
		if r.Source == "wallet" {
			var paid, bonus, reward int
			for _, s := range task.PrivateData.WalletDebit {
				switch s.Account {
				case AccountPaid:
					paid += s.Amount
				case AccountBonus:
					bonus += s.Amount
				case AccountReward:
					reward += s.Amount
				default:
					return errors.New("invalid task wallet account")
				}
			}
			if paid != r.Paid || bonus != r.Bonus || reward != r.Reward {
				return errors.New("billing task wallet shares mismatch")
			}
		}
		if err = validateBillingSourceTx(tx, r); err != nil {
			return err
		}
		if err = tx.Omit(omitColumns...).Create(task).Error; err != nil {
			return err
		}
		return tx.Model(r).Updates(map[string]any{"state": BillingTaskOwned, "task_id": task.ID, "task_public_id": task.TaskID}).Error
	})
}

func billingTaskIdentityMatches(r *BillingFinalization, t *Task) bool {
	return t.TaskID != "" && t.UserId == r.UserID && t.PrivateData.EffectiveRequestID() == r.RequestID && t.PrivateData.BillingSource == r.Source && t.PrivateData.TokenId == r.TokenID && t.PrivateData.SubscriptionId == r.SubscriptionID && t.PrivateData.SubscriptionQuotaPeriod == r.SubscriptionPeriod
}

// ProbeBillingFinalization waits for an uncertain COMMIT through the unique
// owner key. An absent owner is proved by inserting then ROLLING BACK a private
// building row; it never creates a reservation journal. This is only used when
// a previous terminal attempt's ACK was uncertain, before allowing more reserve.
func ProbeBillingFinalization(ctx context.Context, receipt BillingFinalization) (*BillingFinalization, error) {
	var stored *BillingFinalization
	absent := errors.New("billing finalization absent")
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r, err := claimBillingFinalizationTx(tx, receipt)
		if err != nil {
			return err
		}
		if r.State == "building" {
			return absent
		}
		stored = r
		return nil
	})
	if errors.Is(err, absent) {
		return nil, nil
	}
	return stored, err
}

// ReadCommittedBillingTask proves the SAME destination after lost INSERT ACK.
// An absent owner rolls back its private probe; it must not insert a second task.
func ReadCommittedBillingTask(ctx context.Context, receipt BillingFinalization, task *Task) error {
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r, err := claimBillingFinalizationTx(tx, receipt)
		if err != nil {
			return err
		}
		if r.State != BillingTaskOwned {
			return ErrBillingFinalized
		}
		if r.TaskPublicID != task.TaskID || task.ID != 0 && task.ID != r.TaskID {
			return errors.New("billing task destination mismatch")
		}
		var stored Task
		if err = lockForUpdate(tx).First(&stored, r.TaskID).Error; err != nil {
			return err
		}
		if !billingTaskIdentityMatches(r, &stored) {
			return errors.New("billing task identity mismatch")
		}
		task.ID = stored.ID
		return nil
	})
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
			return false, errors.New("invalid realtime debit identity")
		}
		var err error
		sequence, err = strconv.ParseUint(parts[0], 10, 64)
		if err != nil || sequence == 0 {
			return false, errors.New("invalid realtime debit sequence")
		}
	}
	op, details, err := loadLedgerOperation(lockForUpdate(tx), row.IdempotencyKey)
	if err != nil {
		return false, err
	}
	if op.UserId != r.UserID || op.Operation != "debit" || op.Id != row.OperationId || len(details) == 0 {
		return false, errors.New("independent billing operation mismatch")
	}
	total := 0
	for _, d := range details {
		validPurpose := fee && isFee(d.Metadata) || !fee && d.Metadata == ""
		if d.UserId != r.UserID || d.RequestId != r.RequestID || d.ReferenceType != "relay" || d.ReferenceId != r.RequestID || d.Reason != LedgerReasonBillingSettle || d.IdempotencyKey != row.IdempotencyKey || d.TotalBefore != op.TotalBefore || d.TotalAfter != op.TotalAfter || d.Delta >= 0 || d.Delta < -common.MaxQuota || !validAccount(LedgerAccount(d.Account)) || !validPurpose {
			return false, errors.New("independent billing rows mismatch")
		}
		total -= d.Delta
		if total > common.MaxQuota {
			return false, errors.New("independent billing amount invalid")
		}
	}
	expectedKey := fmt.Sprintf("billing:settle:%s:%d", r.RequestID, total)
	if !fee {
		expectedKey = fmt.Sprintf("billing:settle:%s:wss:%d:%d", r.RequestID, sequence, total)
	}
	if row.IdempotencyKey != expectedKey || op.TotalBefore-op.TotalAfter != total {
		return false, errors.New("independent billing key mismatch")
	}
	return true, nil
}
