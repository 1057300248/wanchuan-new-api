package service

import (
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/moneyport"
)

// FundingSource is retained as a service compatibility alias while ownership
// of the runtime contract lives in the stdlib-only moneyport package.
type FundingSource = moneyport.FundingSource

var (
	_ FundingSource               = (*SubscriptionFunding)(nil)
	_ moneyport.ReservableFunding = (*SubscriptionFunding)(nil)
)

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

// Reserve adapts the host absolute target to official subscription delta accounting.
// Only the base pre-consumption belongs to Refund; BillingSession returns the extra.
func (s *SubscriptionFunding) Reserve(target int) error {
	current := int(s.amount) + s.reserveDelta
	if target <= current {
		return nil
	}
	if err := model.PostConsumeUserSubscriptionDelta(s.subscriptionId, int64(target-current)); err != nil {
		return err
	}
	s.reserveDelta = target - int(s.amount)
	return nil
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
