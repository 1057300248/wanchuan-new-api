package service

import (
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// FundingSource — 资金来源接口（钱包 or 订阅）
// ---------------------------------------------------------------------------

// FundingSource is retained as a service compatibility alias while ownership
// of the runtime contract lives in the stdlib-only moneyport package.
type FundingSource = moneyport.FundingSource

// 编译期能力断言。BillingSession 按 moneyport 能力而非实现类型分派，
// 这里锁定各资金来源的能力集：
//   - 钱包按账户拆分扣减，故提供扣减回执；预扣状态由令牌额度反映，
//     不报告独立的待退预扣（否则 needsRefund 语义会改变）。
//   - 订阅不按账户拆分，故无回执；但可能在 tokenConsumed=0 时仍有预扣，
//     故必须报告待退预扣。
var (
	_ FundingSource                        = (*WalletFunding)(nil)
	_ transactionalSettlementFunding       = (*WalletFunding)(nil)
	_ moneyport.ReservableFunding          = (*WalletFunding)(nil)
	_ moneyport.FundingReceipt             = (*WalletFunding)(nil)
	_ FundingSource                        = (*SubscriptionFunding)(nil)
	_ moneyport.ReservableFunding          = (*SubscriptionFunding)(nil)
	_ moneyport.OutstandingReserveReporter = (*SubscriptionFunding)(nil)
)

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

// ErrInsufficientWalletQuota 钱包原子预扣失败（余额不足），未发生任何扣减。
// BillingSession 据此映射为 ErrorCodeInsufficientUserQuota，
// 使 wallet_first 等计费偏好可以回退到订阅。
var ErrInsufficientWalletQuota = errors.New("wallet quota insufficient")

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
		if errors.Is(err, model.ErrLedgerInsufficientBalance) {
			// P0-5：赠金资格 fail-closed——bonus_eligible=false/null 时不得
			// 静默扣减赠金；否则映射为官方"余额不足"哨兵错误，由
			// BillingSession 统一转成"用户额度不足"响应（未发生任何扣减）。
			if !w.bonusEligible() {
				return ErrModelBonusNotSupported
			}
			return ErrInsufficientWalletQuota
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
		return err
	}
	w.ApplySettlementResult(delta, result)
	return nil
}

// SettleInTx applies only the wallet side of settlement inside a caller-owned
// transaction and returns a host-independent result. It deliberately does not
// mutate in-memory receipt state or cache; callers invoke ApplySettlementResult
// only after the transaction commits.
func (w *WalletFunding) SettleInTx(tx *gorm.DB, delta int) (moneyport.SettlementResult, error) {
	if tx == nil {
		return moneyport.SettlementResult{}, errors.New("wallet settlement transaction is nil")
	}
	if delta == 0 {
		return moneyport.SettlementResult{}, nil
	}
	// 补扣与退款方向使用不同幂等键；同一 request 同方向可能按不同差额多次结算。
	entry := model.LedgerEntry{
		UserId:        w.userId,
		Account:       model.AccountPaid,
		Reason:        model.LedgerReasonBillingSettle,
		ReferenceType: "relay",
		ReferenceId:   w.requestId,
		RequestId:     w.requestId,
	}
	if delta > 0 {
		entry.Delta = delta
		entry.IdempotencyKey = fmt.Sprintf("billing:settle:%s:%d", w.requestId, delta)
		result, err := model.DebitQuotaTx(tx, entry, w.debitOrder...)
		return settlementResultFromLedger(result), err
	}

	refund := -delta
	entry.IdempotencyKey = fmt.Sprintf("billing:settle_refund:%s:%d", w.requestId, refund)
	if shares := w.refundShares(refund); len(shares) > 0 {
		result, err := model.CreditQuotaSharesTx(tx, entry, shares)
		return settlementResultFromLedger(result), err
	}
	// 兼容旧路径：无份额记录时退回 paid 账户。
	entry.Delta = refund
	result, err := model.CreditQuotaTx(tx, entry)
	return settlementResultFromLedger(result), err
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

func (w *WalletFunding) ApplySettlementResult(delta int, result moneyport.SettlementResult) {
	model.RefreshUserQuotaCache(w.userId, result.TotalAfter)
	if delta > 0 && !result.Replayed {
		w.addMoneyportShares(result.DebitShares)
	}
}

func (w *WalletFunding) addMoneyportShares(shares []moneyport.AccountShare) {
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

// Receipt 返回累计扣减份额（预扣 + 追扣 + 结算补扣），供异步任务持久化，
// 退款按原账户精确退回。实现 moneyport.FundingReceipt：每次调用重新构造切片，
// 调用方改写返回值不会影响 debitShares。
func (w *WalletFunding) Receipt() []moneyport.AccountShare {
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
		if errors.Is(err, model.ErrLedgerInsufficientBalance) {
			if !w.bonusEligible() {
				return ErrModelBonusNotSupported
			}
			return ErrInsufficientWalletQuota
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

// Reserve 把订阅预扣抬升到 target（实现 moneyport.ReservableFunding）。
// 订阅侧只接受增量，因此这里把绝对目标值换算成相对当前预扣基线的差额；
// target 不高于基线（重复或下降的目标）时为 no-op，不触碰订阅用量。
func (s *SubscriptionFunding) Reserve(target int) error {
	reserved := int(s.amount) + s.reserveDelta
	if target <= reserved {
		return nil
	}
	delta := target - reserved
	if err := model.PostConsumeUserSubscriptionDelta(s.subscriptionId, int64(delta)); err != nil {
		return err
	}
	s.reserveDelta += delta
	return nil
}

// OutstandingReserve 返回订阅侧仍处于预扣状态的额度
// （实现 moneyport.OutstandingReserveReporter）。
// 订阅可能在令牌额度未扣减（tokenConsumed=0）时仍已预扣，
// BillingSession 据此判断失败请求是否仍需退还预扣费。
//
// 只报告 preConsumed，不含 reserveDelta，这两点缺一不可：
//  1. 归属：reserveDelta 由 BillingSession.extraReserved 独立回滚
//     （见 BillingSession.Refund），而 Refund() 只退 preConsumed。
//     把它计入会让同一笔额度被两条退款路径各算一次。
//  2. 无遗漏：reserveDelta 只在 BillingSession.Reserve 成功时增长，
//     而那里 tokenConsumed 同步增长同一个 delta，故 reserveDelta>0 时
//     needsRefundLocked 早已在 tokenConsumed>0 处返回 true。
func (s *SubscriptionFunding) OutstandingReserve() int {
	return int(s.preConsumed)
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
