package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	"gorm.io/gorm"
)

func (s *BillingSession) durableTerminalSource() bool {
	capability, ok := s.funding.(moneyport.HostBillingFinalizationSource)
	return ok && capability.UsesHostBillingFinalization() && s.funding.Source() == BillingSourceWallet
}

// Authorization identity is captured before preconsume. The lazy initialization
// supports internal, manually constructed sessions; it never reads a replacement
// credential from the database. RelayInfo is not a source of later mutations.
func (s *BillingSession) captureBillingIdentity() {
	if s.billingIdentity != nil || s.relayInfo == nil {
		return
	}
	info := s.relayInfo
	r := model.BillingFinalization{RequestID: info.RequestId, UserID: info.UserId, Source: s.funding.Source()}
	if !info.IsPlayground && info.TokenId > 0 {
		r.TokenID = info.TokenId
		r.TokenFingerprint = model.BillingTokenFingerprint(info.TokenKey)
	}
	s.billingIdentity = &r
}

func (s *BillingSession) terminalReceiptLocked() (model.BillingFinalization, error) {
	if s.terminalReceipt != nil {
		return *s.terminalReceipt, nil
	}
	s.captureBillingIdentity()
	if s.billingIdentity == nil {
		return model.BillingFinalization{}, errors.New("missing billing authorization identity")
	}
	r := *s.billingIdentity
	if s.relayInfo.RequestId != r.RequestID || s.relayInfo.UserId != r.UserID || s.funding.Source() != r.Source {
		return r, errors.New("billing session identity changed")
	}
	r.PreConsumed = s.preConsumedQuota
	if r.TokenID > 0 {
		r.TokenHeld = s.tokenConsumed
	}
	if r.Source == BillingSourceWallet {
		for _, share := range s.Receipt() {
			if share.Amount < 0 {
				return r, errors.New("invalid wallet held receipt")
			}
			switch model.LedgerAccount(share.Account) {
			case model.AccountPaid:
				r.Paid += share.Amount
			case model.AccountBonus:
				r.Bonus += share.Amount
			case model.AccountReward:
				r.Reward += share.Amount
			default:
				return r, errors.New("unknown wallet held account")
			}
		}
		r.SourceHeld = r.Paid + r.Bonus + r.Reward
	} else {
		return r, errors.New("durable settlement is only used by wallet funding")
	}
	s.terminalReceipt = &r
	return r, nil
}

func (s *BillingSession) settleDurableLocked(actual int) error {
	if s.refunded || s.taskOwned {
		return model.ErrBillingFinalized
	}
	receipt, err := s.terminalReceiptLocked()
	if err != nil {
		return err
	}
	funding, ok := s.funding.(transactionalSettlementFunding)
	if !ok {
		return fmt.Errorf("funding source %T does not support transactional settlement", s.funding)
	}
	delta := actual - s.preConsumedQuota
	var result moneyport.SettlementResult
	// Settlement survives a disconnected client, but its SQL waits and precommit
	// Redis fence share one finite cooperative budget while the session is locked.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	applied, err := model.SettleBillingFinalization(ctx, receipt, actual, func(tx *gorm.DB) error {
		// Wallet delta zero has no money operation.
		if delta != 0 {
			var err error
			result, err = funding.SettleInTx(tx, delta)
			if err != nil {
				return err
			}
			if result.Replayed {
				return errors.New("source settlement exists without atomic terminal owner")
			}
		}
		return model.ApplyBillingTokenDeltaTx(tx, &receipt, -delta)
	})
	if receipt.Source == BillingSourceWallet && (err != nil || !applied || delta == 0) {
		// Cache cleanup has its own bounded budget, including after a lost COMMIT
		// acknowledgement; it cannot classify the financial result as a rollback.
		// Applied nonzero wallet deltas instead clean up in ApplySettlementResult.
		model.RefreshUserQuotaCacheAfterBillingFinalization(receipt.UserID)
	}
	if err != nil {
		// A deadline (like a lost COMMIT ACK) is not proof of rollback. Preserve
		// the frozen receipt for the existing terminal-owner reconciliation.
		s.settlementUncertain = true
		return err
	}
	s.settlementUncertain = false
	if applied && delta != 0 {
		funding.ApplySettlementResult(delta, result)
	}
	s.settled, s.fundingSettled = true, true
	s.actualSettled = actual
	return nil
}

func (s *BillingSession) resolveSettlementBeforeReserveLocked() error {
	if s.terminalReceipt == nil {
		return errors.New("missing uncertain settlement receipt")
	}
	probeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stored, err := model.ProbeBillingFinalization(probeCtx, *s.terminalReceipt)
	if err != nil {
		return err
	}
	s.settlementUncertain = false
	if stored == nil {
		s.terminalReceipt = nil
		return nil
	}
	switch stored.State {
	case model.BillingSettled:
		s.settled = true
		s.fundingSettled = true
		s.actualSettled = stored.ActualQuota
	case model.BillingTaskOwned:
		s.taskOwned = true
	case model.BillingRefunded:
		s.refunded = true
	case model.BillingRefundPending:
	default:
		return errors.New("unknown billing terminal owner")
	}
	return model.ErrBillingFinalized
}
