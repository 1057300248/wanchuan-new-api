package service

import (
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/model"
)

// ---------------------------------------------------------------------------
// FundingSource — 资金来源接口（钱包 or 订阅）
// ---------------------------------------------------------------------------

// FundingSource 抽象了预扣费的资金来源。
type FundingSource interface {
	// Source 返回资金来源标识："wallet" 或 "subscription"
	Source() string
	// PreConsume 从该资金来源预扣 amount 额度
	PreConsume(amount int) error
	// Settle 根据差额调整资金来源（正数补扣，负数退还）
	Settle(delta int) error
	// Refund 退还所有预扣费
	Refund() error
}

// ---------------------------------------------------------------------------
// WalletFunding — 钱包资金来源实现
//
// 所有余额变动通过模型层账本入口（model.CreditQuota / model.DebitQuota）执行，
// 使用 request_id 派生幂等键；预扣/结算/退款各使用独立键，互不冲突。
// 余额扣减不允许任一账户或合计为负：预扣前已做余额校验，
// 结算/补充预扣在余额不足时返回错误（不再产生负余额欠费）。
//
// P0-5：扣减顺序由 debitOrder 决定（eligible 模型 bonus->paid，否则仅 paid），
// 预扣实际份额记录在 debitShares，结算退款与全额退款按原账户精确退回。
// ---------------------------------------------------------------------------

type WalletFunding struct {
	userId    int
	requestId string
	consumed  int // 实际预扣的用户额度
	// debitOrder 账户扣减顺序（NewBillingSession 依据模型赠金资格构造）。
	debitOrder []model.LedgerAccount
	// debitShares 本次请求各账户实际扣减份额（预扣 + 追扣 + 结算补扣累计），
	// 退款必须按此精确退回，不得把 bonus 退进 paid。
	debitShares map[model.LedgerAccount]int
}

func (w *WalletFunding) Source() string { return BillingSourceWallet }

func (w *WalletFunding) PreConsume(amount int) error {
	if amount <= 0 {
		return nil
	}
	entry := model.LedgerEntry{
		UserId:         w.userId,
		Delta:          amount,
		Reason:         model.LedgerReasonBillingPreConsume,
		ReferenceType:  "relay",
		ReferenceId:    w.requestId,
		RequestId:      w.requestId,
		IdempotencyKey: "billing:preconsume:" + w.requestId,
	}
	result, err := model.DebitQuota(entry, w.debitOrder...)
	if err != nil {
		if !w.bonusEligible() && errors.Is(err, model.ErrLedgerInsufficientBalance) {
			return ErrModelBonusNotSupported
		}
		return err
	}
	w.consumed = amount
	if !result.Replayed {
		w.addSharesFromResult(result)
	}
	return nil
}

func (w *WalletFunding) Settle(delta int) error {
	if delta == 0 {
		return nil
	}
	// 补扣与退款方向使用不同幂等键；同一 request 同方向可能按不同差额多次结算
	// （WSS 分段计费、测试场景），幂等键包含目标差额，避免同键不同参数冲突。
	entry := model.LedgerEntry{
		UserId:        w.userId,
		Account:       model.AccountPaid,
		Reason:        model.LedgerReasonBillingSettle,
		ReferenceType: "relay",
		ReferenceId:   w.requestId,
		RequestId:     w.requestId,
	}
	var err error
	if delta > 0 {
		// 补扣与预扣同一顺序；份额计入累计，保证后续（如有）退款精确
		entry.Delta = delta
		entry.IdempotencyKey = fmt.Sprintf("billing:settle:%s:%d", w.requestId, delta)
		var result *model.LedgerResult
		result, err = model.DebitQuota(entry, w.debitOrder...)
		if err == nil && !result.Replayed {
			w.addSharesFromResult(result)
		}
	} else {
		refund := -delta
		if shares := w.refundShares(refund); len(shares) > 0 {
			entry.IdempotencyKey = fmt.Sprintf("billing:settle_refund:%s:%d", w.requestId, refund)
			_, err = model.CreditQuotaShares(entry, shares)
		} else {
			// 兼容旧路径：无份额记录时退回 paid 账户
			entry.Delta = refund
			entry.IdempotencyKey = fmt.Sprintf("billing:settle_refund:%s:%d", w.requestId, refund)
			_, err = model.CreditQuota(entry)
		}
	}
	return err
}

func (w *WalletFunding) Refund() error {
	if w.consumed <= 0 {
		return nil
	}
	entry := model.LedgerEntry{
		UserId:         w.userId,
		Account:        model.AccountPaid,
		Reason:         model.LedgerReasonBillingRefund,
		ReferenceType:  "relay",
		ReferenceId:    w.requestId,
		RequestId:      w.requestId,
		IdempotencyKey: "billing:refund:" + w.requestId,
	}
	if shares := w.refundShares(w.consumed); len(shares) > 0 {
		_, err := model.CreditQuotaShares(entry, shares)
		return err
	}
	// 兼容旧路径：无份额记录时退回 paid 账户
	entry.Delta = w.consumed
	_, err := model.CreditQuota(entry)
	return err
}

// bonusEligible 返回本次请求是否允许使用赠金（依据 debitOrder）。
func (w *WalletFunding) bonusEligible() bool {
	for _, account := range w.debitOrder {
		if account == model.AccountBonus {
			return true
		}
	}
	return false
}

// addSharesFromResult 把一次扣减结果的各账户份额累计进 debitShares。
func (w *WalletFunding) addSharesFromResult(result *model.LedgerResult) {
	if result == nil {
		return
	}
	if w.debitShares == nil {
		w.debitShares = make(map[model.LedgerAccount]int)
	}
	for _, row := range result.Rows {
		if row.Delta >= 0 {
			continue
		}
		account := model.LedgerAccount(row.Account)
		w.debitShares[account] += -row.Delta
	}
}

// refundShares 按累计扣减份额拆分退款；无记录时返回 nil（由调用方退回 paid）。
func (w *WalletFunding) refundShares(amount int) []model.LedgerShare {
	if len(w.debitShares) == 0 {
		return nil
	}
	shares := w.debitSharesInOrder()
	return SplitRefund(shares, amount)
}

// debitSharesInOrder 按 debitOrder 顺序输出非零份额（未配置顺序时按 map 顺序）。
func (w *WalletFunding) debitSharesInOrder() []model.LedgerShare {
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

// DebitShares 返回累计扣减份额（供异步任务持久化，退款按原账户精确退回）。
func (w *WalletFunding) DebitShares() []model.LedgerShare {
	return w.debitSharesInOrder()
}

// Reserve 补充预扣份额（幂等键已含目标预扣值）。
func (w *WalletFunding) Reserve(targetQuota int) error {
	if targetQuota <= w.consumed {
		return nil
	}
	delta := targetQuota - w.consumed
	entry := model.LedgerEntry{
		UserId:         w.userId,
		Delta:          delta,
		Reason:         model.LedgerReasonBillingReserve,
		ReferenceType:  "relay",
		ReferenceId:    w.requestId,
		RequestId:      w.requestId,
		IdempotencyKey: fmt.Sprintf("billing:reserve:%s:%d", w.requestId, targetQuota),
	}
	result, err := model.DebitQuota(entry, w.debitOrder...)
	if err != nil {
		if !w.bonusEligible() && errors.Is(err, model.ErrLedgerInsufficientBalance) {
			return ErrModelBonusNotSupported
		}
		return err
	}
	w.consumed = targetQuota
	if !result.Replayed {
		w.addSharesFromResult(result)
	}
	return nil
}

// ---------------------------------------------------------------------------
// SubscriptionFunding — 订阅资金来源实现
// ---------------------------------------------------------------------------

type SubscriptionFunding struct {
	requestId      string
	userId         int
	modelName      string
	amount         int64 // 预扣的订阅额度（subConsume）
	subscriptionId int
	preConsumed    int64
	// 以下字段在 PreConsume 成功后填充，供 RelayInfo 同步使用
	AmountTotal     int64
	AmountUsedAfter int64
	PlanId          int
	PlanTitle       string
}

func (s *SubscriptionFunding) Source() string { return BillingSourceSubscription }

func (s *SubscriptionFunding) PreConsume(_ int) error {
	// amount 参数被忽略，使用内部 s.amount（已在构造时根据 preConsumedQuota 计算）
	res, err := model.PreConsumeUserSubscription(s.requestId, s.userId, s.modelName, 0, s.amount)
	if err != nil {
		return err
	}
	s.subscriptionId = res.UserSubscriptionId
	s.preConsumed = res.PreConsumed
	s.AmountTotal = res.AmountTotal
	s.AmountUsedAfter = res.AmountUsedAfter
	// 获取订阅计划信息
	if planInfo, err := model.GetSubscriptionPlanInfoByUserSubscriptionId(res.UserSubscriptionId); err == nil && planInfo != nil {
		s.PlanId = planInfo.PlanId
		s.PlanTitle = planInfo.PlanTitle
	}
	return nil
}

func (s *SubscriptionFunding) Settle(delta int) error {
	if delta == 0 {
		return nil
	}
	return model.PostConsumeUserSubscriptionDelta(s.subscriptionId, int64(delta))
}

func (s *SubscriptionFunding) Refund() error {
	if s.preConsumed <= 0 {
		return nil
	}
	return refundWithRetry(func() error {
		return model.RefundSubscriptionPreConsume(s.requestId)
	})
}

// refundWithRetry 尝试多次执行退款操作以提高成功率，只能用于基于事务的退款函数！！！！！！
// try to refund with retries, only for refund functions based on transactions!!!
func refundWithRetry(fn func() error) error {
	if fn == nil {
		return nil
	}
	const maxAttempts = 3
	var lastErr error
	for i := 0; i < maxAttempts; i++ {
		if err := fn(); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if i < maxAttempts-1 {
			time.Sleep(time.Duration(200*(i+1)) * time.Millisecond)
		}
	}
	return lastErr
}
