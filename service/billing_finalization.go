package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"gorm.io/gorm"
)

func (s *BillingSession) durableTerminalSource() bool {
	capability, ok := s.funding.(moneyport.HostBillingFinalizationSource)
	return ok && capability.UsesHostBillingFinalization() && (s.funding.Source() == BillingSourceWallet || s.funding.Source() == BillingSourceSubscription)
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
		// SubscriptionFunding owns the exact original receipt/period. Do not guess a
		// period from today's subscription or mutable request metadata.
		sub, ok := s.funding.(*SubscriptionFunding)
		if !ok {
			return r, fmt.Errorf("subscription source %T lacks original receipt", s.funding)
		}
		r.SourceHeld = sub.OutstandingReserve()
		r.SubscriptionID = sub.subscriptionId
		r.SubscriptionPeriod = sub.quotaPeriod
	}
	s.terminalReceipt = &r
	return r, nil
}

func (s *BillingSession) settleDurableLocked(actual int) error {
	if s.refundRequested || s.taskOwned {
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
	applied, err := model.SettleBillingFinalization(context.Background(), receipt, actual, func(tx *gorm.DB) error {
		// Wallet delta zero has no money operation; subscription delta zero MUST
		// finalize its real source receipt, including when all token use is zero.
		if delta != 0 || receipt.Source == BillingSourceSubscription {
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
	if receipt.Source == BillingSourceWallet {
		model.RefreshUserQuotaCache(receipt.UserID, 0)
	}
	if err != nil {
		s.settlementUncertain = true
		return err
	}
	s.settlementUncertain = false
	if applied && delta != 0 {
		funding.ApplySettlementResult(delta, result)
	}
	if receipt.Source == BillingSourceSubscription {
		s.relayInfo.SubscriptionPostDelta += int64(delta)
	}
	s.settled, s.fundingSettled = true, true
	s.actualSettled = actual
	return nil
}

func (s *BillingSession) requestDurableRefundLocked() (model.BillingFinalization, error) {
	// Close admission at the pre-existing Refund decision even if the pending
	// write's ACK is uncertain. This is NOT a successful-refund flag.
	s.refundRequested = true
	receipt, err := s.terminalReceiptLocked()
	if err != nil {
		return receipt, err
	}
	if receipt.RequestID == "" || len(receipt.RequestID) > 64 || strings.TrimSpace(receipt.RequestID) != receipt.RequestID {
		return receipt, errors.New("invalid billing refund request identity")
	}
	// Do not let a short ID sliced from a large request buffer retain that
	// buffer throughout an outage. The worker owns only this exact compact ID.
	receipt.RequestID = strings.Clone(receipt.RequestID)
	s.refundInFlight = true
	// Freeze the original receipt before unlocking. No persistence, semaphore
	// wait or background submission is allowed under the request mutex.
	return receipt, nil
}

func (s *BillingSession) finishDurableRefund(state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refundInFlight = false
	switch state {
	case model.BillingRefunded:
		s.refunded = true
	case model.BillingTaskOwned:
		s.taskOwned = true
	case model.BillingSettled:
		s.fundingSettled = true
	}
}

// PersistTaskWithBillingOwner changes only the successful INSERT seam. Custom
// BillingSettler implementations keep their original persistence contract, and
// omitted result columns remain omitted without discarding inline response data.
func PersistTaskWithBillingOwner(ctx context.Context, info *relaycommon.RelayInfo, task *model.Task, omitColumns ...string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s, ok := info.Billing.(*BillingSession)
	if !ok || !s.durableTerminalSource() {
		return task.InsertWithContext(ctx, omitColumns...)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refundRequested || s.refunded || s.settled && !s.taskOwned {
		return model.ErrBillingFinalized
	}
	receipt, err := s.terminalReceiptLocked()
	if err != nil {
		return err
	}
	err = model.InsertTaskWithBillingFinalization(ctx, receipt, task, omitColumns...)
	if err != nil {
		// Read back only the original destination. Never insert another task or
		// infer upstream success merely because the first COMMIT ACK was lost.
		readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if readErr := model.ReadCommittedBillingTask(readCtx, receipt, task); readErr != nil {
			if !errors.Is(readErr, model.ErrBillingFinalized) {
				// Preserve the existing failure/refund policy. This is correlation
				// evidence, not an accepted task or a decision about another submit.
				return &TaskPersistenceUncertainError{receipt: receipt, taskPublicID: task.TaskID, cause: err}
			}
			return err
		}
	}

	s.taskOwned = true
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
		s.refundRequested = true
	case model.BillingRefundPending:
		s.refundRequested = true
	default:
		return errors.New("unknown billing terminal owner")
	}
	return model.ErrBillingFinalized
}
