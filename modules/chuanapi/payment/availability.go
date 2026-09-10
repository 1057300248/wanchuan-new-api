package payment

import (
	"strings"

	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

func isWaffoTopUpEnabled() bool {
	if !operation_setting.IsPaymentComplianceConfirmed() || !setting.WaffoEnabled {
		return false
	}
	return isWaffoWebhookConfigured()
}

func isWaffoWebhookConfigured() bool {
	if setting.WaffoSandbox {
		return strings.TrimSpace(setting.WaffoSandboxApiKey) != "" &&
			strings.TrimSpace(setting.WaffoSandboxPrivateKey) != "" &&
			strings.TrimSpace(setting.WaffoSandboxPublicCert) != ""
	}
	return strings.TrimSpace(setting.WaffoApiKey) != "" &&
		strings.TrimSpace(setting.WaffoPrivateKey) != "" &&
		strings.TrimSpace(setting.WaffoPublicCert) != ""
}

func isWaffoWebhookEnabled() bool { return isWaffoTopUpEnabled() }

func isWaffoPancakeTopUpEnabled() bool {
	if !operation_setting.IsPaymentComplianceConfirmed() {
		return false
	}
	return strings.TrimSpace(setting.WaffoPancakeMerchantID) != "" &&
		strings.TrimSpace(setting.WaffoPancakePrivateKey) != "" &&
		strings.TrimSpace(setting.WaffoPancakeProductID) != ""
}

func isWaffoPancakeWebhookConfigured() bool { return isWaffoPancakeTopUpEnabled() }
func isWaffoPancakeWebhookEnabled() bool    { return isWaffoPancakeTopUpEnabled() }
