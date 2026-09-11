package payment

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const (
	OrderBindingKindTopUp        = "topup"
	OrderBindingKindSubscription = "subscription"
)

// PaymentOrderBinding is the provider-owned settlement snapshot for one local
// payment order. Keeping it in the payment module avoids adding provider fields
// to root TopUp/SubscriptionOrder while still making webhook settlement depend
// on immutable server-side facts captured when the order is created.
type PaymentOrderBinding struct {
	TradeNo           string `gorm:"primaryKey;type:varchar(255)"`
	Provider          string `gorm:"type:varchar(64);not null;index"`
	OrderKind         string `gorm:"type:varchar(32);not null"`
	Environment       string `gorm:"type:varchar(32);not null"`
	StoreID           string `gorm:"type:varchar(128);not null;default:''"`
	MerchantID        string `gorm:"type:varchar(128);not null;default:''"`
	PaymentRequestID  string `gorm:"type:varchar(255);not null;default:''"`
	ProviderSessionID string `gorm:"type:varchar(255);not null;default:''"`
	ProviderOrderID   string `gorm:"type:varchar(255);not null;default:''"`
	ExpectedAmount    string `gorm:"type:varchar(64);not null"`
	ExpectedCurrency  string `gorm:"type:varchar(16);not null"`
	CreatedAt         int64  `gorm:"autoCreateTime"`
	UpdatedAt         int64  `gorm:"autoUpdateTime"`
}

func (PaymentOrderBinding) TableName() string { return "chuanapi_payment_order_bindings" }

type paymentCallbackFacts struct {
	Provider         string
	OrderKind        string
	Environment      string
	StoreID          string
	MerchantID       string
	PaymentRequestID string
	ProviderOrderID  string
	ActualAmount     string
	ActualCurrency   string
}

func configuredWaffoPancakeEnvironment() (string, error) {
	return setting.NormalizeWaffoPancakeEnvironment(setting.WaffoPancakeEnvironment)
}

func currentWaffoEnvironment() string {
	if setting.WaffoSandbox {
		return "sandbox"
	}
	return "production"
}

func newWaffoPancakeBinding(tradeNo, orderKind, amount string) (PaymentOrderBinding, error) {
	environment, err := configuredWaffoPancakeEnvironment()
	if err != nil {
		return PaymentOrderBinding{}, err
	}
	storeID := strings.TrimSpace(setting.WaffoPancakeStoreID)
	if storeID == "" {
		return PaymentOrderBinding{}, errors.New("Waffo Pancake store id is not configured")
	}
	return newPaymentOrderBinding(
		tradeNo,
		model.PaymentProviderWaffoPancake,
		orderKind,
		environment,
		storeID,
		"",
		"",
		amount,
		"USD",
	)
}

func newWaffoBinding(tradeNo, amount, currency string) (PaymentOrderBinding, error) {
	return newPaymentOrderBinding(
		tradeNo,
		model.PaymentProviderWaffo,
		OrderBindingKindTopUp,
		currentWaffoEnvironment(),
		"",
		strings.TrimSpace(setting.WaffoMerchantId),
		tradeNo,
		amount,
		currency,
	)
}

func newPaymentOrderBinding(tradeNo, provider, orderKind, environment, storeID, merchantID, paymentRequestID, amount, currency string) (PaymentOrderBinding, error) {
	tradeNo = strings.TrimSpace(tradeNo)
	provider = strings.TrimSpace(provider)
	orderKind = strings.TrimSpace(orderKind)
	environment = strings.ToLower(strings.TrimSpace(environment))
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if tradeNo == "" || provider == "" || orderKind == "" || environment == "" || currency == "" {
		return PaymentOrderBinding{}, errors.New("payment order binding is incomplete")
	}
	canonicalAmount, err := canonicalPaymentAmount(amount)
	if err != nil {
		return PaymentOrderBinding{}, err
	}
	return PaymentOrderBinding{
		TradeNo:          tradeNo,
		Provider:         provider,
		OrderKind:        orderKind,
		Environment:      environment,
		StoreID:          strings.TrimSpace(storeID),
		MerchantID:       strings.TrimSpace(merchantID),
		PaymentRequestID: strings.TrimSpace(paymentRequestID),
		ExpectedAmount:   canonicalAmount,
		ExpectedCurrency: currency,
	}, nil
}

func canonicalPaymentAmount(raw string) (string, error) {
	value, err := decimal.NewFromString(strings.TrimSpace(raw))
	if err != nil || value.LessThanOrEqual(decimal.Zero) {
		return "", fmt.Errorf("invalid payment amount %q", raw)
	}
	return value.String(), nil
}

func insertTopUpWithBinding(topUp *model.TopUp, binding PaymentOrderBinding) error {
	if topUp == nil || model.DB == nil {
		return errors.New("invalid topup binding insert")
	}
	return model.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(topUp).Error; err != nil {
			return err
		}
		return tx.Create(&binding).Error
	})
}

func insertSubscriptionOrderWithBinding(order *model.SubscriptionOrder, binding PaymentOrderBinding) error {
	if order == nil || model.DB == nil {
		return errors.New("invalid subscription binding insert")
	}
	return model.DB.Transaction(func(tx *gorm.DB) error {
		if order.CreateTime == 0 {
			order.CreateTime = time.Now().Unix()
		}
		if err := tx.Create(order).Error; err != nil {
			return err
		}
		return tx.Create(&binding).Error
	})
}

func getPaymentOrderBinding(tradeNo string) (*PaymentOrderBinding, error) {
	if model.DB == nil {
		return nil, errors.New("payment database is not initialized")
	}
	var binding PaymentOrderBinding
	if err := model.DB.Where("trade_no = ?", strings.TrimSpace(tradeNo)).First(&binding).Error; err != nil {
		return nil, err
	}
	return &binding, nil
}

func bindProviderSessionID(tradeNo, sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return errors.New("provider session id is empty")
	}
	result := model.DB.Model(&PaymentOrderBinding{}).
		Where("trade_no = ? AND (provider_session_id = '' OR provider_session_id = ?)", tradeNo, sessionID).
		Update("provider_session_id", sessionID)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 {
		return nil
	}
	binding, err := getPaymentOrderBinding(tradeNo)
	if err != nil {
		return err
	}
	if binding.ProviderSessionID != sessionID {
		return fmt.Errorf("provider session id mismatch for tradeNo=%s", tradeNo)
	}
	return nil
}

func bindProviderOrderID(tradeNo, providerOrderID string) error {
	providerOrderID = strings.TrimSpace(providerOrderID)
	if providerOrderID == "" {
		return errors.New("provider order id is empty")
	}
	result := model.DB.Model(&PaymentOrderBinding{}).
		Where("trade_no = ? AND (provider_order_id = '' OR provider_order_id = ?)", tradeNo, providerOrderID).
		Update("provider_order_id", providerOrderID)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 {
		return nil
	}
	binding, err := getPaymentOrderBinding(tradeNo)
	if err != nil {
		return err
	}
	if binding.ProviderOrderID != providerOrderID {
		return fmt.Errorf("provider order id mismatch for tradeNo=%s", tradeNo)
	}
	return nil
}

func validatePaymentOrderBinding(binding *PaymentOrderBinding, facts paymentCallbackFacts) error {
	if binding == nil {
		return errors.New("missing payment order binding")
	}
	if binding.Provider != strings.TrimSpace(facts.Provider) || binding.OrderKind != strings.TrimSpace(facts.OrderKind) {
		return fmt.Errorf("payment binding provider/kind mismatch for tradeNo=%s", binding.TradeNo)
	}
	if binding.Environment != strings.ToLower(strings.TrimSpace(facts.Environment)) {
		return fmt.Errorf("payment binding environment mismatch for tradeNo=%s", binding.TradeNo)
	}
	if binding.StoreID != "" && binding.StoreID != strings.TrimSpace(facts.StoreID) {
		return fmt.Errorf("payment binding store mismatch for tradeNo=%s", binding.TradeNo)
	}
	if actualMerchantID := strings.TrimSpace(facts.MerchantID); binding.MerchantID != "" && actualMerchantID != "" && binding.MerchantID != actualMerchantID {
		return fmt.Errorf("payment binding merchant mismatch for tradeNo=%s", binding.TradeNo)
	}
	if binding.PaymentRequestID != "" && binding.PaymentRequestID != strings.TrimSpace(facts.PaymentRequestID) {
		return fmt.Errorf("payment request id mismatch for tradeNo=%s", binding.TradeNo)
	}
	actualCurrency := strings.ToUpper(strings.TrimSpace(facts.ActualCurrency))
	if actualCurrency != binding.ExpectedCurrency {
		return fmt.Errorf("payment currency mismatch for tradeNo=%s: expected=%s actual=%s", binding.TradeNo, binding.ExpectedCurrency, actualCurrency)
	}
	actualAmount, err := canonicalPaymentAmount(facts.ActualAmount)
	if err != nil {
		return err
	}
	if actualAmount != binding.ExpectedAmount {
		return fmt.Errorf("payment amount mismatch for tradeNo=%s: expected=%s actual=%s", binding.TradeNo, binding.ExpectedAmount, actualAmount)
	}
	providerOrderID := strings.TrimSpace(facts.ProviderOrderID)
	if providerOrderID == "" {
		return fmt.Errorf("provider order id missing for tradeNo=%s", binding.TradeNo)
	}
	if binding.ProviderOrderID != "" && binding.ProviderOrderID != providerOrderID {
		return fmt.Errorf("provider order id mismatch for tradeNo=%s", binding.TradeNo)
	}
	return nil
}
