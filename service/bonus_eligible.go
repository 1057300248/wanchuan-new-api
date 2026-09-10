package service

import (
	"fmt"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	"github.com/QuantumNous/new-api/pkg/walletport"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// ---------------------------------------------------------------------------
// 钱包计费账户顺序（TokenMix P0-5）
//
// 规则（fail-closed）：只有模型元数据显式 bonus_eligible=true 时才允许使用
// 赠金，扣减顺序 bonus -> paid；其余模型（false/null/缺失）只允许 paid。
// 预扣、追扣（reserve）、结算差额补扣、异步任务、PostConsume 回退路径都必须
// 使用同一套顺序；退款必须精确退回本次原始扣款账户。
// ---------------------------------------------------------------------------

// ErrModelBonusNotSupported 表示请求模型不支持赠金支付且付费余额不足。
// 不得静默扣减赠金；错误信息面向用户且可在测试中断言。
var ErrModelBonusNotSupported = moneyport.ErrBonusFundingNotSupported

// WalletDebitOrder 返回请求钱包计费使用的账户扣减顺序。
// 资金身份必须使用 GetBillingModelName()（价格/倍率身份），禁止直接使用
// OriginModelName：reasoning suffix / 模型映射可能让两者不同。
func WalletDebitOrder(relayInfo *relaycommon.RelayInfo) []model.LedgerAccount {
	billingModel := ""
	if relayInfo != nil {
		billingModel = relayInfo.GetBillingModelName()
	}
	policy, err := walletport.Default()
	if err != nil {
		return []model.LedgerAccount{model.AccountPaid}
	}
	rawOrder := policy.AccountOrder(billingModel)
	order := make([]model.LedgerAccount, 0, len(rawOrder))
	for _, raw := range rawOrder {
		account := model.LedgerAccount(raw)
		switch account {
		case model.AccountBonus, model.AccountPaid, model.AccountAffiliate:
			order = append(order, account)
		}
	}
	if len(order) == 0 {
		return []model.LedgerAccount{model.AccountPaid}
	}
	return order
}

// WalletBonusEligible 返回请求模型是否允许使用赠金（与 WalletDebitOrder 同源）。
func WalletBonusEligible(relayInfo *relaycommon.RelayInfo) bool {
	if relayInfo == nil {
		return false
	}
	policy, err := walletport.Default()
	if err != nil {
		return false
	}
	return policy.BonusEligible(relayInfo.GetBillingModelName())
}

// LedgerSharesFromRelayShares 将 relay 层份额（无 model 依赖）转换为账本份额。
func LedgerSharesFromRelayShares(shares []relaycommon.WalletDebitShare) []model.LedgerShare {
	if len(shares) == 0 {
		return nil
	}
	out := make([]model.LedgerShare, 0, len(shares))
	for _, share := range shares {
		out = append(out, model.LedgerShare{Account: model.LedgerAccount(share.Account), Amount: share.Amount})
	}
	return out
}

// splitFromRelayShares 按 relay 层记录的扣减份额拆分退款；无记录返回 nil。
func splitFromRelayShares(shares []relaycommon.WalletDebitShare, amount int) []model.LedgerShare {
	if len(shares) == 0 || amount <= 0 {
		return nil
	}
	return SplitRefund(LedgerSharesFromRelayShares(shares), amount)
}

// appendShares 把一次扣减结果（各账户负 delta）累计进 relay 层份额列表。
func appendShares(current []relaycommon.WalletDebitShare, result *model.LedgerResult) []relaycommon.WalletDebitShare {
	if result == nil {
		return current
	}
	index := make(map[string]int, len(current))
	for i, share := range current {
		index[share.Account] = i
	}
	next := append([]relaycommon.WalletDebitShare(nil), current...)
	for _, row := range result.Rows {
		if row.Delta >= 0 {
			continue
		}
		if i, ok := index[row.Account]; ok {
			next[i].Amount += -row.Delta
		} else {
			index[row.Account] = len(next)
			next = append(next, relaycommon.WalletDebitShare{Account: row.Account, Amount: -row.Delta})
		}
	}
	return next
}

// SplitRefund 把 refundAmount 按原始扣款份额比例拆分为各账户退款份额：
// 每个账户份额向下取整，余数归顺序第一位的账户；份额总和恒等于 refundAmount。
// 任意多次部分退款与最终全额退款叠加后，各账户累计退款与原始扣款精确一致。
func SplitRefund(original []model.LedgerShare, refundAmount int) []model.LedgerShare {
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
	if refundAmount >= total {
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
	// 余数归顺序第一位账户，保证份额总和精确等于 refundAmount
	if remaining > 0 {
		if len(out) > 0 {
			out[0].Amount += remaining
		} else {
			out = append(out, model.LedgerShare{Account: original[0].Account, Amount: remaining})
		}
	}
	return out
}

// DescribeWalletShares 生成份额的调试/日志描述。
func DescribeWalletShares(shares []model.LedgerShare) string {
	desc := ""
	for _, share := range shares {
		if desc != "" {
			desc += ","
		}
		desc += fmt.Sprintf("%s=%d", share.Account, share.Amount)
	}
	return desc
}
