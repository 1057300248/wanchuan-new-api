package payment

import (
	"strconv"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/paymentport"
	"github.com/QuantumNous/new-api/setting"
)

const (
	WaffoProviderID        = "chuanapi/waffo"
	WaffoPancakeProviderID = "chuanapi/waffo-pancake"
)

type waffoCatalogue struct{}
type waffoPancakeCatalogue struct{}

// RegisterProviders contributes each concrete payment provider independently so
// the generic registry can detect provider ids and response-surface collisions.
// Pancake is registered first to preserve the historical payment-method order.
func RegisterProviders(registry *paymentport.Registry) error {
	if err := registry.Register(WaffoPancakeProviderID, waffoPancakeCatalogue{}); err != nil {
		return err
	}
	return registry.Register(WaffoProviderID, waffoCatalogue{})
}

func (waffoPancakeCatalogue) Descriptor() paymentport.Descriptor {
	return paymentport.Descriptor{
		MethodTypes: []string{model.PaymentMethodWaffoPancake},
		FieldKeys: []string{
			"enable_waffo_pancake_topup",
			"waffo_pancake_min_topup",
		},
	}
}

func (waffoPancakeCatalogue) Contribution() paymentport.Contribution {
	enabled := isWaffoPancakeTopUpEnabled()
	contribution := paymentport.Contribution{Fields: map[string]any{
		"enable_waffo_pancake_topup": enabled,
		"waffo_pancake_min_topup":    setting.WaffoPancakeMinTopUp,
	}}
	if enabled {
		contribution.Methods = append(contribution.Methods, map[string]string{
			"name":      "Waffo Pancake",
			"type":      model.PaymentMethodWaffoPancake,
			"color":     "#F97316",
			"min_topup": strconv.Itoa(setting.WaffoPancakeMinTopUp),
		})
	}
	return contribution
}

func (waffoCatalogue) Descriptor() paymentport.Descriptor {
	return paymentport.Descriptor{
		MethodTypes: []string{model.PaymentMethodWaffo},
		FieldKeys: []string{
			"enable_waffo_topup",
			"waffo_min_topup",
			"waffo_pay_methods",
		},
	}
}

func (waffoCatalogue) Contribution() paymentport.Contribution {
	enabled := isWaffoTopUpEnabled()
	contribution := paymentport.Contribution{Fields: map[string]any{
		"enable_waffo_topup": enabled,
		"waffo_min_topup":    setting.WaffoMinTopUp,
		"waffo_pay_methods":  nil,
	}}
	if enabled {
		contribution.Methods = append(contribution.Methods, map[string]string{
			"name":      "Waffo (Global Payment)",
			"type":      model.PaymentMethodWaffo,
			"color":     "#3B82F6",
			"min_topup": strconv.Itoa(setting.WaffoMinTopUp),
		})
		contribution.Fields["waffo_pay_methods"] = setting.GetWaffoPayMethods()
	}
	return contribution
}
