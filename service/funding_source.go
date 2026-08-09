package service

import (
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
// ---------------------------------------------------------------------------

type WalletFunding struct {
	userId    int
	requestId string
	consumed  int // 实际预扣的用户额度
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
	if _, err := model.DebitQuota(entry, model.AccountPaid); err != nil {
		return err
	}
	w.consumed = amount
	return nil
}

func (w *WalletFunding) Settle(delta int) error {
	if delta == 0 {
		return nil
	}
	// 补扣与退款方向使用不同幂等键，同一 request 两个方向互不冲突
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
		entry.Delta = delta
		entry.IdempotencyKey = "billing:settle:" + w.requestId
		_, err = model.DebitQuota(entry, model.AccountPaid)
	} else {
		entry.Delta = -delta
		entry.IdempotencyKey = "billing:settle_refund:" + w.requestId
		_, err = model.CreditQuota(entry)
	}
	return err
}

func (w *WalletFunding) Refund() error {
	if w.consumed <= 0 {
		return nil
	}
	// 退款通过账本幂等键（request_id + refund）保证重放安全，可安全重试。
	_, err := model.CreditQuota(model.LedgerEntry{
		UserId:         w.userId,
		Account:        model.AccountPaid,
		Delta:          w.consumed,
		Reason:         model.LedgerReasonBillingRefund,
		ReferenceType:  "relay",
		ReferenceId:    w.requestId,
		RequestId:      w.requestId,
		IdempotencyKey: "billing:refund:" + w.requestId,
	})
	return err
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
