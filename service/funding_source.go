package service

import (
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	"gorm.io/gorm"
)

// FundingSource is retained as a service compatibility alias while ownership
// of the runtime contract lives in the stdlib-only moneyport package.
type FundingSource = moneyport.FundingSource

var (
	_ FundingSource                        = (*SubscriptionFunding)(nil)
	_ moneyport.ReservableFunding          = (*SubscriptionFunding)(nil)
	_ moneyport.OutstandingReserveReporter = (*SubscriptionFunding)(nil)
)

// ---------------------------------------------------------------------------
// SubscriptionFunding — 订阅资金来源实现
// ---------------------------------------------------------------------------

type SubscriptionFunding struct {
	requestId         string
	userId            int
	modelName         string
	amount            int64 // 预扣的订阅额度（subConsume）
	subscriptionId    int
	preConsumed       int64
	quotaPeriod       int64
	pendingPreConsume *model.SubscriptionPreConsumeResult
	// reserveDelta 通过 Reserve 累计补充预扣的增量；预扣基线是 amount，
	// 故当前预扣目标为 amount + reserveDelta。它只负责把 Reserve 的绝对目标值
	// 换算回订阅侧需要的增量：与它同值的 BillingSession.extraReserved 才是
	// RelayInfo 同步与补充预扣回滚的唯一依据，两者不得叠加。
	reserveDelta int
	// 以下字段在 PreConsume 成功后填充，供 RelayInfo 同步使用
	AmountTotal     int64
	AmountUsedAfter int64
	PlanId          int
	PlanTitle       string
}

func (s *SubscriptionFunding) UsesHostBillingFinalization() bool { return true }

func (s *SubscriptionFunding) Source() string { return BillingSourceSubscription }

func (s *SubscriptionFunding) PreConsume(amount int) error {
	var result moneyport.SettlementResult
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = s.PreConsumeInTx(tx, amount)
		return err
	})
	if err == nil {
		s.ApplyPreConsumeResult(amount, result)
	}
	return err
}

func (s *SubscriptionFunding) PreConsumeInTx(tx *gorm.DB, _ int) (moneyport.SettlementResult, error) {
	res, err := model.PreConsumeUserSubscriptionTx(tx, s.requestId, s.userId, s.modelName, 0, s.amount)
	if err != nil {
		return moneyport.SettlementResult{}, err
	}
	s.pendingPreConsume = res
	return moneyport.SettlementResult{Replayed: res.Replayed}, nil
}

func (s *SubscriptionFunding) ApplyPreConsumeResult(_ int, _ moneyport.SettlementResult) {
	res := s.pendingPreConsume
	if res == nil {
		return
	}
	s.pendingPreConsume = nil
	s.subscriptionId = res.UserSubscriptionId
	s.quotaPeriod = res.QuotaPeriod
	s.preConsumed = res.PreConsumed
	s.reserveDelta = int(res.ReservedAmount - s.amount)
	s.AmountTotal = res.AmountTotal
	s.AmountUsedAfter = res.AmountUsedAfter
	if planInfo, err := model.GetSubscriptionPlanInfoByUserSubscriptionId(res.UserSubscriptionId); err == nil && planInfo != nil {
		s.PlanId = planInfo.PlanId
		s.PlanTitle = planInfo.PlanTitle
	}
}

func (s *SubscriptionFunding) Settle(delta int) error {
	return model.DB.Transaction(func(tx *gorm.DB) error {
		_, err := s.SettleInTx(tx, delta)
		return err
	})
}

func (s *SubscriptionFunding) SettleInTx(tx *gorm.DB, delta int) (moneyport.SettlementResult, error) {
	actual := s.amount + int64(s.reserveDelta) + int64(delta)
	replayed, err := model.SettleSubscriptionPreConsumeForRequestTx(tx, s.requestId, s.userId, s.subscriptionId, s.amount+int64(s.reserveDelta), actual)
	return moneyport.SettlementResult{Replayed: replayed}, err
}

func (s *SubscriptionFunding) ApplySettlementResult(_ int, _ moneyport.SettlementResult) {}

// Reserve uses the durable absolute receipt target, not an unbound delta against
// whichever subscription allowance happens to be current after a reset.
func (s *SubscriptionFunding) Reserve(target int) error {
	var result moneyport.SettlementResult
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = s.ReserveInTx(tx, target)
		return err
	})
	if err == nil {
		s.ApplyReserveResult(target, result)
	}
	return err
}

func (s *SubscriptionFunding) ReserveInTx(tx *gorm.DB, target int) (moneyport.SettlementResult, error) {
	return moneyport.SettlementResult{}, model.ReserveSubscriptionPreConsumeForRequestTx(tx, s.requestId, s.userId, s.subscriptionId, s.amount+int64(s.reserveDelta), int64(target))
}

func (s *SubscriptionFunding) ApplyReserveResult(target int, _ moneyport.SettlementResult) {
	if target > int(s.amount)+s.reserveDelta {
		s.reserveDelta = target - int(s.amount)
	}
}

// Refund owns the entire receipt, including every supplementary reservation.
// The session must not independently roll reserveDelta back a second time.
func (s *SubscriptionFunding) OutstandingReserve() int {
	if s.preConsumed <= 0 {
		return 0
	}
	return max(int(s.preConsumed), int(s.amount)+s.reserveDelta)
}

func (s *SubscriptionFunding) Refund() error {
	if s.preConsumed <= 0 {
		return nil
	}
	return refundWithRetry(func() error { return model.RefundSubscriptionPreConsume(s.requestId) })
}

// refundWithRetry 尝试多次执行退款操作以提高成功率，只能用于基于事务的退款函数！！！！！！
// try to refund with retries, only for refund functions based on transactions!!!
func refundWithRetry(fn func() error) error {
	if fn == nil {
		return nil
	}
	const maxAttempts = 3
	var lastErr error
	for i := range maxAttempts {
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
