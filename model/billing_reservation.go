package model

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	BillingHeld            = "held_v2"
	BillingDeliveryUnknown = "delivery_unknown_v2"
)

func sameBillingAuthorization(a, b BillingFinalization) bool {
	return a.RequestID == b.RequestID && a.UserID == b.UserID && a.Source == b.Source && a.TokenID == b.TokenID && subtle.ConstantTimeCompare([]byte(a.TokenFingerprint), []byte(b.TokenFingerprint)) == 1
}

// ReserveBillingFinalization creates/extends only a live v2 hold. The callback
// must apply the source and original token and fill the exact resulting held
// amounts in this SAME transaction. A zero trusted hold still saves identity.
// Held/unknown rows are evidence for review, never a refund recovery queue.
func ReserveBillingFinalization(ctx context.Context, authorization BillingFinalization, previous *BillingFinalization, target int, apply func(*gorm.DB, *BillingFinalization) error, inspect ...func(*gorm.DB, BillingFinalization) error) (BillingFinalization, bool, error) {
	var result BillingFinalization
	if authorization.RequestID == "" || authorization.UserID <= 0 || target < 0 || target > common.MaxQuota || apply == nil {
		return result, false, errors.New("invalid billing reservation")
	}
	applied := false
	publish := func(tx *gorm.DB, stored BillingFinalization) error {
		for _, read := range inspect {
			if read != nil {
				if err := read(tx, stored); err != nil {
					return err
				}
			}
		}
		result = stored
		return nil
	}
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var nonce [8]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return err
		}
		claim := authorization
		claim.State = "h:" + hex.EncodeToString(nonce[:])
		claim.PreConsumed = target
		if previous == nil {
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "request_id"}}, DoNothing: true}).Create(&claim).Error; err != nil {
				return err
			}
		}
		var stored BillingFinalization
		if err := lockForUpdate(tx).Where("request_id = ?", authorization.RequestID).First(&stored).Error; err != nil {
			return err
		}
		if !sameBillingAuthorization(stored, authorization) {
			return billingRefundReviewError("identity_mismatch", "billing reservation authorization mismatch")
		}
		if err := LockFinancialUserTx(tx, stored.UserID); err != nil {
			return err
		}
		fresh := stored.State == claim.State
		if !fresh {
			if stored.State != BillingHeld {
				return ErrBillingFinalized
			}
			if err := stored.validate(); err != nil {
				return err
			}
			if previous == nil {
				if stored.PreConsumed != target {
					return billingRefundReviewError("identity_mismatch", "billing reservation replay amount mismatch")
				}
				return publish(tx, stored)
			}
			if !sameBillingIdentity(stored, *previous) {
				return billingRefundReviewError("identity_mismatch", "billing reservation changed since last receipt")
			}
			if target <= stored.PreConsumed {
				return publish(tx, stored)
			}
		}
		if err := apply(tx, &stored); err != nil {
			return err
		}
		stored.PreConsumed, stored.State = target, BillingHeld
		if err := stored.validate(); err != nil {
			return err
		}
		if err := tx.Save(&stored).Error; err != nil {
			return err
		}
		applied = true
		return publish(tx, stored)
	})
	return result, applied && err == nil, err
}

// ReserveBillingTokenTx uses the original authorized token identity, and keeps
// the existing unlimited-token rule while enforcing balance for limited tokens.
func ReserveBillingTokenTx(tx *gorm.DB, r *BillingFinalization, amount int, unlimited bool) error {
	if amount < 0 || amount > common.MaxQuota {
		return errors.New("invalid token reservation")
	}
	if r.TokenID == 0 || amount == 0 {
		return nil
	}
	var token Token
	if err := lockForUpdate(tx).First(&token, r.TokenID).Error; err != nil {
		return err
	}
	if token.UserId != r.UserID || subtle.ConstantTimeCompare([]byte(BillingTokenFingerprint(token.Key)), []byte(r.TokenFingerprint)) != 1 {
		return billingRefundReviewError("identity_mismatch", "billing reservation token identity mismatch")
	}
	if !unlimited && token.RemainQuota < amount {
		return ErrBillingTokenInsufficient
	}
	return ApplyTokenQuotaDeltaTx(tx, token.Id, token.Key, -amount)
}

var ErrBillingTokenInsufficient = errors.New("token quota is not enough")

// MarkBillingDeliveryUnknown freezes a hold after an ambiguous upstream send.
// It records no refund decision and cannot overwrite any winning terminal owner.
func MarkBillingDeliveryUnknown(ctx context.Context, receipt BillingFinalization) error {
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var stored BillingFinalization
		if err := lockForUpdate(tx).Where("request_id = ?", receipt.RequestID).First(&stored).Error; err != nil {
			return err
		}
		if !sameBillingIdentity(stored, receipt) {
			return billingRefundReviewError("identity_mismatch", "unknown delivery receipt mismatch")
		}
		if stored.State == BillingDeliveryUnknown {
			return nil
		}
		if stored.State != BillingHeld {
			return ErrBillingFinalized
		}
		return tx.Model(&stored).Updates(map[string]any{"state": BillingDeliveryUnknown, "last_error_code": "upstream_delivery_unknown", "last_error_class": billingRefundReview}).Error
	})
}

// ReadBillingReservation serializes with an in-flight reservation transaction.
// Only authorization is compared because an uncertain Reserve may have safely
// increased the exact held amounts in the committed row.
func ReadBillingReservation(ctx context.Context, authorization BillingFinalization, inspect ...func(*gorm.DB, BillingFinalization) error) (*BillingFinalization, error) {
	var stored BillingFinalization
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).Where("request_id = ?", authorization.RequestID).First(&stored).Error; err != nil {
			return err
		}
		if !sameBillingAuthorization(stored, authorization) {
			return billingRefundReviewError("identity_mismatch", "billing reservation authorization mismatch")
		}
		if err := stored.validate(); err != nil {
			return err
		}
		if err := LockFinancialUserTx(tx, stored.UserID); err != nil {
			return err
		}
		for _, read := range inspect {
			if read != nil {
				if err := read(tx, stored); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &stored, nil
}
