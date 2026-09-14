package wallet

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	"gorm.io/gorm"
)

// Funding is the ChuanAPI wallet implementation of the generic moneyport
// runtime contract. Billing orchestration owns token quota; this type owns only
// wallet ledger/account mutations and receipt state.
type Funding struct {
	userID            int
	requestID         string
	consumed          int
	debitOrder        []model.LedgerAccount
	debitShares       map[model.LedgerAccount]int
	receiptOperations map[string]struct{}
}

var (
	_ moneyport.FundingSource     = (*Funding)(nil)
	_ moneyport.ReservableFunding = (*Funding)(nil)
	_ moneyport.FundingReceipt    = (*Funding)(nil)
)

var ErrMissingDebitReceipt = errors.New("wallet funding: consumed quota has no debit receipt")

func NewFunding(request moneyport.FactoryRequest) (*Funding, error) {
	order := make([]model.LedgerAccount, 0, len(request.AccountOrder))
	for _, raw := range request.AccountOrder {
		account := model.LedgerAccount(raw)
		switch account {
		case model.AccountBonus, model.AccountReward, model.AccountPaid:
			order = append(order, account)
		default:
			return nil, fmt.Errorf("chuanapi wallet: unsupported account %q", raw)
		}
	}
	return &Funding{userID: request.UserID, requestID: request.RequestID, debitOrder: order}, nil
}

func RegisterFundingFactory(reg *moneyport.FactoryRegistry) error {
	if reg == nil {
		return errors.New("chuanapi wallet: nil funding factory registry")
	}
	return reg.Register(moneyport.SourceWallet, moneyport.FundingFactoryFunc(func(request moneyport.FactoryRequest) (moneyport.FundingSource, error) {
		return NewFunding(request)
	}))
}

func (w *Funding) Source() string { return moneyport.SourceWallet }

func (w *Funding) PreConsume(amount int) error {
	if amount <= 0 {
		return nil
	}
	operationID := "billing:preconsume:" + w.requestID
	entry := model.LedgerEntry{
		UserId:         w.userID,
		Delta:          amount,
		Reason:         model.LedgerReasonBillingPreConsume,
		ReferenceType:  "relay",
		ReferenceId:    w.requestID,
		RequestId:      w.requestID,
		IdempotencyKey: operationID,
	}
	result, err := model.DebitQuota(entry, w.debitOrder...)
	if err != nil {
		return w.mapInsufficient(err)
	}
	w.consumed = amount
	w.recordLedgerDebitOperation(operationID, result)
	return nil
}

func (w *Funding) Settle(delta int) error {
	if delta == 0 {
		return nil
	}
	var result moneyport.SettlementResult
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		result = moneyport.SettlementResult{}
		err = model.DB.Transaction(func(tx *gorm.DB) error {
			var settleErr error
			result, settleErr = w.SettleInTx(tx, delta)
			return settleErr
		})
		if !errors.Is(err, model.ErrLedgerDuplicate) {
			break
		}
	}
	if err != nil {
		return w.mapInsufficient(err)
	}
	w.ApplySettlementResult(delta, result)
	return nil
}

// SettleInTx participates in the caller-owned transaction used to make wallet
// settlement and token quota atomic. It does not mutate post-commit cache or
// receipt state.
func (w *Funding) SettleInTx(tx *gorm.DB, delta int) (moneyport.SettlementResult, error) {
	if tx == nil {
		return moneyport.SettlementResult{}, errors.New("wallet settlement transaction is nil")
	}
	if delta == 0 {
		return moneyport.SettlementResult{}, nil
	}
	entry := model.LedgerEntry{
		UserId:        w.userID,
		Account:       model.AccountPaid,
		Reason:        model.LedgerReasonBillingSettle,
		ReferenceType: "relay",
		ReferenceId:   w.requestID,
		RequestId:     w.requestID,
	}
	if delta > 0 {
		entry.Delta = delta
		entry.IdempotencyKey = fmt.Sprintf("billing:settle:%s:%d", w.requestID, delta)
		result, err := model.DebitQuotaTx(tx, entry, w.debitOrder...)
		return settlementResultFromLedger(result), w.mapInsufficient(err)
	}

	refund := -delta
	entry.IdempotencyKey = fmt.Sprintf("billing:settle_refund:%s:%d", w.requestID, refund)
	if shares := w.refundShares(refund); len(shares) > 0 {
		result, err := model.CreditQuotaSharesTx(tx, entry, shares)
		return settlementResultFromLedger(result), err
	}
	return moneyport.SettlementResult{}, ErrMissingDebitReceipt
}

func (w *Funding) ApplySettlementResult(delta int, result moneyport.SettlementResult) {
	model.RefreshUserQuotaCache(w.userID, result.TotalAfter)
	if delta > 0 {
		w.recordMoneyportDebitOperation(fmt.Sprintf("billing:settle:%s:%d", w.requestID, delta), result.DebitShares)
	}
}

func (w *Funding) Refund() error {
	if w.consumed <= 0 {
		return nil
	}
	entry := model.LedgerEntry{
		UserId:         w.userID,
		Account:        model.AccountPaid,
		Reason:         model.LedgerReasonBillingRefund,
		ReferenceType:  "relay",
		ReferenceId:    w.requestID,
		RequestId:      w.requestID,
		IdempotencyKey: "billing:refund:" + w.requestID,
	}
	if shares := w.refundShares(w.consumed); len(shares) > 0 {
		_, err := model.CreditQuotaShares(entry, shares)
		return err
	}
	return ErrMissingDebitReceipt
}

func (w *Funding) Reserve(targetQuota int) error {
	if targetQuota <= w.consumed {
		return nil
	}
	delta := targetQuota - w.consumed
	operationID := fmt.Sprintf("billing:reserve:%s:%d", w.requestID, targetQuota)
	entry := model.LedgerEntry{
		UserId:         w.userID,
		Delta:          delta,
		Reason:         model.LedgerReasonBillingReserve,
		ReferenceType:  "relay",
		ReferenceId:    w.requestID,
		RequestId:      w.requestID,
		IdempotencyKey: operationID,
	}
	result, err := model.DebitQuota(entry, w.debitOrder...)
	if err != nil {
		return w.mapInsufficient(err)
	}
	w.consumed = targetQuota
	w.recordLedgerDebitOperation(operationID, result)
	return nil
}

func (w *Funding) Receipt() []moneyport.AccountShare {
	shares := w.debitSharesInOrder()
	if len(shares) == 0 {
		return nil
	}
	out := make([]moneyport.AccountShare, 0, len(shares))
	for _, share := range shares {
		out = append(out, moneyport.AccountShare{Account: string(share.Account), Amount: share.Amount})
	}
	return out
}

// ReservedQuota is read-only observability for tests/admin diagnostics. It is
// not OutstandingReserveReporter: BillingSession intentionally derives wallet
// failure-refund need from tokenConsumed to avoid duplicate refunds.
func (w *Funding) ReservedQuota() int { return w.consumed }

func (w *Funding) mapInsufficient(err error) error {
	if !errors.Is(err, model.ErrLedgerInsufficientBalance) {
		return err
	}
	if !w.bonusEligible() {
		return moneyport.ErrBonusFundingNotSupported
	}
	return moneyport.ErrWalletInsufficientBalance
}

func (w *Funding) bonusEligible() bool {
	for _, account := range w.debitOrder {
		if account == model.AccountBonus {
			return true
		}
	}
	return false
}

func (w *Funding) addSharesFromLedger(result *model.LedgerResult) {
	if result == nil {
		return
	}
	if w.debitShares == nil {
		w.debitShares = make(map[model.LedgerAccount]int)
	}
	for _, row := range result.Rows {
		if row.Delta < 0 {
			w.debitShares[model.LedgerAccount(row.Account)] += -row.Delta
		}
	}
}

func (w *Funding) addMoneyportShares(shares []moneyport.AccountShare) {
	if len(shares) == 0 {
		return
	}
	if w.debitShares == nil {
		w.debitShares = make(map[model.LedgerAccount]int)
	}
	for _, share := range shares {
		if share.Amount > 0 {
			w.debitShares[model.LedgerAccount(share.Account)] += share.Amount
		}
	}
}

func (w *Funding) recordLedgerDebitOperation(operationID string, result *model.LedgerResult) {
	if result == nil || !w.markReceiptOperation(operationID) {
		return
	}
	w.addSharesFromLedger(result)
}

func (w *Funding) recordMoneyportDebitOperation(operationID string, shares []moneyport.AccountShare) {
	if !w.markReceiptOperation(operationID) {
		return
	}
	w.addMoneyportShares(shares)
}

func (w *Funding) markReceiptOperation(operationID string) bool {
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		return false
	}
	if w.receiptOperations == nil {
		w.receiptOperations = make(map[string]struct{})
	}
	if _, exists := w.receiptOperations[operationID]; exists {
		return false
	}
	w.receiptOperations[operationID] = struct{}{}
	return true
}

func (w *Funding) refundShares(amount int) []model.LedgerShare {
	if len(w.debitShares) == 0 {
		return nil
	}
	return splitRefund(w.debitSharesInOrder(), amount)
}

func (w *Funding) debitSharesInOrder() []model.LedgerShare {
	if len(w.debitShares) == 0 {
		return nil
	}
	if len(w.debitOrder) > 0 {
		out := make([]model.LedgerShare, 0, len(w.debitOrder))
		for _, account := range w.debitOrder {
			if amount := w.debitShares[account]; amount > 0 {
				out = append(out, model.LedgerShare{Account: account, Amount: amount})
			}
		}
		return out
	}
	out := make([]model.LedgerShare, 0, len(w.debitShares))
	for account, amount := range w.debitShares {
		if amount > 0 {
			out = append(out, model.LedgerShare{Account: account, Amount: amount})
		}
	}
	return out
}

func splitRefund(original []model.LedgerShare, refundAmount int) []model.LedgerShare {
	if refundAmount <= 0 || len(original) == 0 {
		return nil
	}
	total := 0
	for _, share := range original {
		total += share.Amount
	}
	if total <= 0 {
		return nil
	}
	if refundAmount > total {
		return nil
	}
	if refundAmount == total {
		return append([]model.LedgerShare(nil), original...)
	}
	out := make([]model.LedgerShare, 0, len(original))
	remaining := refundAmount
	for _, share := range original {
		part := int(int64(share.Amount) * int64(refundAmount) / int64(total))
		if part <= 0 {
			continue
		}
		out = append(out, model.LedgerShare{Account: share.Account, Amount: part})
		remaining -= part
	}
	if remaining > 0 {
		if len(out) > 0 {
			out[0].Amount += remaining
		} else {
			out = append(out, model.LedgerShare{Account: original[0].Account, Amount: remaining})
		}
	}
	return out
}

func settlementResultFromLedger(result *model.LedgerResult) moneyport.SettlementResult {
	if result == nil {
		return moneyport.SettlementResult{}
	}
	out := moneyport.SettlementResult{Replayed: result.Replayed, TotalAfter: result.TotalAfter}
	for _, row := range result.Rows {
		if row.Delta < 0 {
			out.DebitShares = append(out.DebitShares, moneyport.AccountShare{Account: row.Account, Amount: -row.Delta})
		}
	}
	return out
}
