package payment

import (
	"strconv"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/paymentport"
	"github.com/QuantumNous/new-api/setting"
)

type providerCatalogue struct{}

func RegisterRuntime() error {
	return paymentport.SetDefault(providerCatalogue{})
}

func (providerCatalogue) Contribution() paymentport.Contribution {
	enableWaffo := isWaffoTopUpEnabled()
	enablePancake := isWaffoPancakeTopUpEnabled()
	contribution := paymentport.Contribution{
		Fields: map[string]any{
			"enable_waffo_topup":         enableWaffo,
			"enable_waffo_pancake_topup": enablePancake,
			"waffo_min_topup":            setting.WaffoMinTopUp,
			"waffo_pancake_min_topup":    setting.WaffoPancakeMinTopUp,
		},
	}
	if enablePancake {
		contribution.Methods = append(contribution.Methods, map[string]string{
			"name":      "Waffo Pancake",
			"type":      model.PaymentMethodWaffoPancake,
			"color":     "#F97316",
			"min_topup": strconv.Itoa(setting.WaffoPancakeMinTopUp),
		})
	}
	if enableWaffo {
		contribution.Methods = append(contribution.Methods, map[string]string{
			"name":      "Waffo (Global Payment)",
			"type":      model.PaymentMethodWaffo,
			"color":     "#3B82F6",
			"min_topup": strconv.Itoa(setting.WaffoMinTopUp),
		})
		contribution.Fields["waffo_pay_methods"] = setting.GetWaffoPayMethods()
	} else {
		contribution.Fields["waffo_pay_methods"] = nil
	}
	return contribution
}
