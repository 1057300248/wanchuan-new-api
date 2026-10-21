package model

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

var (
	ErrSubscriptionPeriodUnknown = errors.New("subscription allowance period is unknown; preserve the receipt for reconciliation")
	ErrSubscriptionPeriodClosed  = errors.New("subscription allowance period has closed; a new reservation is required")
)

// lockSubscriptionReservationTx uses subscription -> receipt ordering, including
// for reservations first created while another transaction was waiting. The
// initial read only locates immutable identity; the locked read is authoritative.
func lockSubscriptionReservationTx(tx *gorm.DB, requestId string, subscriptionId int) (*SubscriptionPreConsumeRecord, *UserSubscription, error) {
	if tx == nil || strings.TrimSpace(requestId) == "" {
		return nil, nil, errors.New("invalid subscription receipt")
	}
	var snapshot SubscriptionPreConsumeRecord
	if err := tx.Where("request_id = ?", requestId).First(&snapshot).Error; err != nil {
		return nil, nil, err
	}
	if subscriptionId > 0 && snapshot.UserSubscriptionId != subscriptionId {
		return nil, nil, errors.New("subscription receipt identity mismatch")
	}
	var sub UserSubscription
	err := lockForUpdate(tx).Where("id = ?", snapshot.UserSubscriptionId).First(&sub).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, err
	}
	var record SubscriptionPreConsumeRecord
	if err := lockForUpdate(tx).Where("request_id = ?", requestId).First(&record).Error; err != nil {
		return nil, nil, err
	}
	if record.UserSubscriptionId != snapshot.UserSubscriptionId || record.UserId != snapshot.UserId {
		return nil, nil, errors.New("subscription receipt identity changed")
	}
	if record.QuotaPeriod <= 0 {
		return nil, nil, ErrSubscriptionPeriodUnknown
	}
	if record.PreConsumed < 0 || record.ReservedAmount < record.PreConsumed || record.ReservedAmount > common.MaxQuota {
		return nil, nil, errors.New("invalid subscription reservation amount")
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &record, nil, nil
	}
	if sub.UserId != record.UserId {
		return nil, nil, errors.New("subscription receipt owner mismatch")
	}
	if sub.QuotaPeriod < record.QuotaPeriod {
		return nil, nil, errors.New("subscription allowance period regressed")
	}
	return &record, &sub, nil
}

func ReserveSubscriptionPreConsume(requestId string, subscriptionId int, target int64) error {
	return DB.Transaction(func(tx *gorm.DB) error { return ReserveSubscriptionPreConsumeTx(tx, requestId, subscriptionId, target) })
}

// ReserveSubscriptionPreConsumeTx records the absolute reserved amount, making
// top-ups retryable and ensuring a failed request refunds every committed hold.
func ReserveSubscriptionPreConsumeTx(tx *gorm.DB, requestId string, subscriptionId int, target int64) error {
	if target <= 0 || target > common.MaxQuota {
		return errors.New("invalid subscription reserve target")
	}
	record, sub, err := lockSubscriptionReservationTx(tx, requestId, subscriptionId)
	if err != nil {
		return err
	}
	return reserveSubscriptionReceiptTx(tx, record, sub, target)
}

func ReserveSubscriptionPreConsumeForRequestTx(tx *gorm.DB, requestId string, expectedUserId, subscriptionId int, expectedReserved, target int64) error {
	if target <= 0 || target > common.MaxQuota {
		return errors.New("invalid subscription reserve target")
	}
	record, sub, err := lockSubscriptionReservationTx(tx, requestId, subscriptionId)
	if err != nil {
		return err
	}
	if expectedUserId <= 0 || subscriptionId <= 0 || record.UserId != expectedUserId || record.ReservedAmount != expectedReserved {
		return errors.New("subscription reserve does not match the request reservation")
	}
	return reserveSubscriptionReceiptTx(tx, record, sub, target)
}

func reserveSubscriptionReceiptTx(tx *gorm.DB, record *SubscriptionPreConsumeRecord, sub *UserSubscription, target int64) error {
	if record.Status != "consumed" {
		return errors.New("subscription reservation is already finalized")
	}
	if sub == nil {
		return gorm.ErrRecordNotFound
	}
	now := GetDBTimestamp(tx)
	if sub.Status != "active" || sub.EndTime <= now {
		return errors.New("no active subscription")
	}
	plan, err := getSubscriptionPlanByIdTx(tx, sub.PlanId)
	if err != nil {
		return err
	}
	if err = maybeResetUserSubscriptionWithPlanTx(tx, sub, plan, now); err != nil {
		return err
	}
	if sub.QuotaPeriod != record.QuotaPeriod {
		return ErrSubscriptionPeriodClosed
	}
	if target <= record.ReservedAmount {
		return nil
	}
	delta := target - record.ReservedAmount
	if err := applySubscriptionPeriodDeltaTx(tx, sub, record.QuotaPeriod, delta); err != nil {
		return err
	}
	record.ReservedAmount = target
	return tx.Select("reserved_amount", "updated_at").Updates(record).Error
}

func SettleSubscriptionPreConsume(requestId string, subscriptionId int, actual int64) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		_, err := SettleSubscriptionPreConsumeTx(tx, requestId, subscriptionId, actual)
		return err
	})
}

// SettleSubscriptionPreConsumeTx finalizes exactly one actual amount. A closed
// allowance cannot grant current-period quota: its receipt is finalized without
// changing the surviving subscription's current usage. This does not authorize
// usage above a reserved amount; strict-prepaid callers enforce that separately.
func SettleSubscriptionPreConsumeTx(tx *gorm.DB, requestId string, subscriptionId int, actual int64) (bool, error) {
	if actual < 0 || actual > common.MaxQuota {
		return false, errors.New("invalid subscription settlement amount")
	}
	record, sub, err := lockSubscriptionReservationTx(tx, requestId, subscriptionId)
	if err != nil {
		return false, err
	}
	return settleSubscriptionReceiptTx(tx, record, sub, actual)
}

func SettleSubscriptionPreConsumeForRequestTx(tx *gorm.DB, requestId string, expectedUserId, subscriptionId int, expectedReserved, actual int64) (bool, error) {
	if actual < 0 || actual > common.MaxQuota {
		return false, errors.New("invalid subscription settlement amount")
	}
	record, sub, err := lockSubscriptionReservationTx(tx, requestId, subscriptionId)
	if err != nil {
		return false, err
	}
	if expectedUserId <= 0 || subscriptionId <= 0 || record.UserId != expectedUserId || record.ReservedAmount != expectedReserved {
		return false, errors.New("subscription settlement does not match the request reservation")
	}
	return settleSubscriptionReceiptTx(tx, record, sub, actual)
}

func settleSubscriptionReceiptTx(tx *gorm.DB, record *SubscriptionPreConsumeRecord, sub *UserSubscription, actual int64) (bool, error) {
	if record.Status == "settled" {
		if record.SettledAmount != actual {
			return false, errors.New("subscription settlement replay amount mismatch")
		}
		return true, nil
	}
	if record.Status != "consumed" {
		return false, errors.New("subscription reservation is already refunded")
	}
	delta := actual - record.ReservedAmount
	// Legacy non-strict callers may settle upward only in the original live
	// allowance and only within its remaining quota. Once closed there is no
	// source left to charge; never silently borrow from the new period.
	if delta > 0 && (sub == nil || sub.QuotaPeriod != record.QuotaPeriod) {
		return false, ErrSubscriptionPeriodClosed
	}
	if sub != nil {
		if err := applySubscriptionPeriodDeltaTx(tx, sub, record.QuotaPeriod, delta); err != nil {
			return false, err
		}
	}
	record.Status = "settled"
	record.SettledAmount = actual
	return false, tx.Select("status", "settled_amount", "updated_at").Updates(record).Error
}

func RefundSubscriptionPreConsumeTx(tx *gorm.DB, requestId string) error {
	record, sub, err := lockSubscriptionReservationTx(tx, requestId, 0)
	if err != nil {
		return err
	}
	if record.Status == "refunded" {
		return nil
	}
	return refundSubscriptionReservationTx(tx, record, sub)
}

// RefundSubscriptionPreConsumeForRequestTx validates the journal's source facts
// after acquiring the authoritative locks. An unjournaled concurrent legacy
// reserve/settle cannot turn a stale pre-read into a mismatched token refund.
func RefundSubscriptionPreConsumeForRequestTx(tx *gorm.DB, requestId string, expectedUserId, expectedSubscriptionId int, expectedReserved int64) error {
	record, sub, err := lockSubscriptionReservationTx(tx, requestId, expectedSubscriptionId)
	if err != nil {
		return err
	}
	if expectedUserId <= 0 || expectedSubscriptionId <= 0 || record.UserId != expectedUserId || record.ReservedAmount != expectedReserved {
		return errors.New("subscription refund does not match the request reservation")
	}
	return refundSubscriptionReservationTx(tx, record, sub)
}

func refundSubscriptionReservationTx(tx *gorm.DB, record *SubscriptionPreConsumeRecord, sub *UserSubscription) error {
	if record.Status != "consumed" {
		return errors.New("subscription reservation is already finalized")
	}
	if sub != nil {
		if err := applySubscriptionPeriodDeltaTx(tx, sub, record.QuotaPeriod, -record.ReservedAmount); err != nil {
			return err
		}
	}
	record.Status = "refunded"
	record.SettledAmount = 0
	return tx.Select("status", "settled_amount", "updated_at").Updates(record).Error
}

// PostConsumeUserSubscriptionPeriodDeltaTx is for durable task adjustments whose
// task receipt already provides exactly-once settlement in this transaction.
// Missing/legacy periods remain pending, rather than crediting a guessed cycle.
func PostConsumeUserSubscriptionPeriodDeltaTx(tx *gorm.DB, subscriptionId int, period int64, delta int64) error {
	if tx == nil || subscriptionId <= 0 {
		return errors.New("invalid subscription adjustment")
	}
	if period <= 0 {
		return ErrSubscriptionPeriodUnknown
	}
	var sub UserSubscription
	err := lockForUpdate(tx).Where("id = ?", subscriptionId).First(&sub).Error
	if err != nil {
		return err
	}
	return applySubscriptionPeriodDeltaTx(tx, &sub, period, delta)
}

func applySubscriptionPeriodDeltaTx(tx *gorm.DB, sub *UserSubscription, period int64, delta int64) error {
	if period <= 0 {
		return ErrSubscriptionPeriodUnknown
	}
	if delta < -common.MaxQuota || delta > common.MaxQuota {
		return errors.New("invalid subscription adjustment amount")
	}
	if sub.QuotaPeriod < period {
		return errors.New("subscription allowance period regressed")
	}
	if sub.QuotaPeriod != period {
		if delta > 0 {
			return ErrSubscriptionPeriodClosed
		}
		return nil
	}
	if sub.AmountUsed < 0 || sub.AmountTotal < 0 || delta < 0 && sub.AmountUsed < -delta {
		return errors.New("subscription adjustment exceeds recorded period usage")
	}
	if delta > 0 && sub.AmountUsed > math.MaxInt64-delta {
		return errors.New("subscription usage overflow")
	}
	used := sub.AmountUsed + delta
	if sub.AmountTotal > 0 && used > sub.AmountTotal {
		return fmt.Errorf("subscription used exceeds total, used=%d total=%d", used, sub.AmountTotal)
	}
	if delta == 0 {
		return nil
	}
	sub.AmountUsed = used
	return tx.Select("amount_used", "updated_at").Updates(sub).Error
}
