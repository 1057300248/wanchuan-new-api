package service

import (
	"context"
	"errors"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	"gorm.io/gorm"
)

type transactionalReservationFunding interface {
	PreConsumeInTx(*gorm.DB, int) (moneyport.SettlementResult, error)
	ReserveInTx(*gorm.DB, int) (moneyport.SettlementResult, error)
	ApplyReservationResult(int, moneyport.SettlementResult)
}

func (s *BillingSession) reserveDurableLocked(target int, initial bool) error {
	funding, ok := s.funding.(transactionalReservationFunding)
	if !ok {
		return errors.New("durable funding source cannot reserve atomically")
	}
	s.captureBillingIdentity()
	if s.billingIdentity == nil {
		return errors.New("missing billing reservation identity")
	}
	var previous *model.BillingFinalization
	if !initial {
		r, err := s.terminalReceiptLocked()
		if err != nil {
			return err
		}
		previous = &r
	}
	var result moneyport.SettlementResult
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	stored, _, err := model.ReserveBillingFinalization(ctx, *s.billingIdentity, previous, target, func(tx *gorm.DB, receipt *model.BillingFinalization) error {
		var sourceErr error
		if initial {
			result, sourceErr = funding.PreConsumeInTx(tx, target)
		} else {
			result, sourceErr = funding.ReserveInTx(tx, target)
		}
		if sourceErr != nil {
			return sourceErr
		}
		if result.Replayed {
			return errors.New("source reservation exists without its atomic held receipt")
		}
		receipt.SourceHeld = result.SourceHeld
		receipt.Paid, receipt.Bonus, receipt.Reward = 0, 0, 0
		if receipt.Source == BillingSourceWallet {
			if !result.ReceiptKnown {
				return errors.New("wallet reservation has no authoritative receipt")
			}
			for _, share := range result.OutstandingShares {
				switch model.LedgerAccount(share.Account) {
				case model.AccountPaid:
					receipt.Paid += share.Amount
				case model.AccountBonus:
					receipt.Bonus += share.Amount
				case model.AccountReward:
					receipt.Reward += share.Amount
				default:
					return errors.New("unknown wallet reservation account")
				}
			}
		} else {
			receipt.SubscriptionID, receipt.SubscriptionPeriod = result.SourceID, result.SourcePeriod
		}
		tokenDelta := target
		if previous != nil {
			tokenDelta -= previous.PreConsumed
		}
		if err := model.ReserveBillingTokenTx(tx, receipt, tokenDelta, s.relayInfo.TokenUnlimited); err != nil {
			return err
		}
		if receipt.TokenID > 0 {
			receipt.TokenHeld += tokenDelta
		}
		return nil
	}, func(tx *gorm.DB, r model.BillingFinalization) error {
		var readErr error
		result, readErr = s.reservationResultTx(tx, r)
		return readErr
	})
	cancel()
	model.RefreshUserQuotaCacheAfterBillingFinalization(s.relayInfo.UserId)
	if err != nil {
		s.reservationUncertain = true
		return err
	}
	funding.ApplyReservationResult(stored.PreConsumed, result)
	s.applyHeldReceipt(stored)
	return nil
}

func (s *BillingSession) reservationResultTx(tx *gorm.DB, r model.BillingFinalization) (moneyport.SettlementResult, error) {
	result := moneyport.SettlementResult{ReceiptKnown: true, SourceHeld: r.SourceHeld, SourceID: r.SubscriptionID, SourcePeriod: r.SubscriptionPeriod}
	for _, share := range r.Shares() {
		result.OutstandingShares = append(result.OutstandingShares, moneyport.AccountShare{Account: string(share.Account), Amount: share.Amount})
	}
	if sub, ok := s.funding.(*SubscriptionFunding); ok {
		identity, err := model.GetSubscriptionBillingIdentityTx(tx, r.RequestID, r.UserID, r.SubscriptionID, r.SubscriptionPeriod, int64(r.SourceHeld))
		if err != nil {
			return result, err
		}
		if identity.PreConsumed != sub.amount {
			return result, errors.New("subscription reservation replay original amount mismatch")
		}
		result.OriginalAmount = int(identity.PreConsumed)
		// A new period is not an old-request preconsume snapshot. Preserve the
		// frozen monetary authorization but explicitly suppress advisory balance
		// math/notifications when its period has already rolled over.
		var allowance model.UserSubscription
		if err := tx.Where("id = ? AND user_id = ?", r.SubscriptionID, r.UserID).First(&allowance).Error; err != nil {
			return result, err
		}
		result.SnapshotUnknown = allowance.QuotaPeriod != r.SubscriptionPeriod
		if !result.SnapshotUnknown {
			result.AmountTotal, result.AmountUsedAfter = allowance.AmountTotal, allowance.AmountUsed
		}
		result.SourcePlanID = allowance.PlanId
		var plan model.SubscriptionPlan
		if err := tx.Select("id", "title").First(&plan, allowance.PlanId).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return result, err
		}
		result.SourcePlanTitle = plan.Title
	}
	return result, nil
}

func (s *BillingSession) applyHeldReceipt(r model.BillingFinalization) {
	s.terminalReceipt = &r
	s.preConsumedQuota = r.PreConsumed
	s.tokenConsumed = r.TokenHeld
	if s.relayInfo.IsPlayground {
		s.tokenConsumed = r.PreConsumed
	}
	if sub, ok := s.funding.(*SubscriptionFunding); ok {
		s.extraReserved = sub.reserveDelta
	}
	s.reservationUncertain = false
	s.syncRelayInfo()
}

func (s *BillingSession) resolveReservationLocked() error {
	if s.billingIdentity == nil {
		return errors.New("missing uncertain reservation authorization")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var result moneyport.SettlementResult
	r, err := model.ReadBillingReservation(ctx, *s.billingIdentity, func(tx *gorm.DB, r model.BillingFinalization) error {
		var readErr error
		result, readErr = s.reservationResultTx(tx, r)
		return readErr
	})
	if err != nil {
		return err
	}
	if r.State != model.BillingHeld {
		return model.ErrBillingFinalized
	}
	funding, ok := s.funding.(transactionalReservationFunding)
	if !ok {
		return errors.New("unsupported reservation recovery")
	}
	funding.ApplyReservationResult(r.PreConsumed, result)
	s.applyHeldReceipt(*r)
	return nil
}
