package payment

import (
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/waffo-com/waffo-go/core"
	"gorm.io/gorm"
)

func withPaymentBindingDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := model.DB
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", name)), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.TopUp{}, &model.SubscriptionOrder{}, &model.Option{}, &PaymentOrderBinding{}))
	model.DB = db
	t.Cleanup(func() {
		model.DB = previousDB
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func withPancakeBindingConfig(t *testing.T, storeID string) {
	t.Helper()
	previousStoreID := setting.WaffoPancakeStoreID
	previousEnvironment := setting.WaffoPancakeEnvironment
	setting.WaffoPancakeStoreID = storeID
	setting.WaffoPancakeEnvironment = setting.WaffoPancakeEnvironmentProd
	t.Cleanup(func() {
		setting.WaffoPancakeStoreID = previousStoreID
		setting.WaffoPancakeEnvironment = previousEnvironment
	})
}

func TestSaveWaffoPancakeConfigPersistsTrustedEnvironment(t *testing.T) {
	db := withPaymentBindingDB(t)
	previousOptionMap := common.OptionMap
	common.OptionMap = map[string]string{}
	previousEnvironment := setting.WaffoPancakeEnvironment
	previousStoreID := setting.WaffoPancakeStoreID
	previousProductID := setting.WaffoPancakeProductID
	t.Cleanup(func() {
		setting.WaffoPancakeEnvironment = previousEnvironment
		common.OptionMap = previousOptionMap
		setting.WaffoPancakeStoreID = previousStoreID
		setting.WaffoPancakeProductID = previousProductID
	})

	err := SaveWaffoPancakeConfig(t.Context(), "merchant", "private", "invalid", "https://example.com", "STORE", "PRODUCT")
	require.Error(t, err)
	var invalidCount int64
	require.NoError(t, db.Model(&model.Option{}).Where("key = ?", "WaffoPancakeEnvironment").Count(&invalidCount).Error)
	assert.Zero(t, invalidCount, "invalid environment must fail before any option persistence")

	require.NoError(t, SaveWaffoPancakeConfig(t.Context(), "merchant", "private", "TEST", "https://example.com", "STORE", "PRODUCT"))
	var environment model.Option
	require.NoError(t, db.Where("key = ?", "WaffoPancakeEnvironment").First(&environment).Error)
	assert.Equal(t, "test", environment.Value)
	assert.Equal(t, "test", setting.WaffoPancakeEnvironment)
	assert.Equal(t, "STORE", setting.WaffoPancakeStoreID)
	assert.Equal(t, "PRODUCT", setting.WaffoPancakeProductID)
}

func TestResolveWaffoPancakeTradeNoRejectsUnboundAndMismatchedCallbacks(t *testing.T) {
	db := withPaymentBindingDB(t)
	withPancakeBindingConfig(t, "STORE_SITE")

	tradeNo := "WAFFO_PANCAKE-1-secure"
	topUp := &model.TopUp{
		UserId:          1,
		Amount:          100,
		Money:           100,
		TradeNo:         tradeNo,
		PaymentMethod:   model.PaymentMethodWaffoPancake,
		PaymentProvider: model.PaymentProviderWaffoPancake,
		Status:          common.TopUpStatusPending,
	}
	binding, err := newWaffoPancakeBinding(tradeNo, OrderBindingKindTopUp, "100.00")
	require.NoError(t, err)
	require.NoError(t, insertTopUpWithBinding(topUp, binding))

	base := WaffoPancakeWebhookEvent{
		StoreID: "STORE_SITE",
		Mode:    "prod",
		Data: WaffoPancakeWebhookData{
			OrderID:                       "ORD_SITE_1",
			OrderMerchantExternalID:       tradeNo,
			Currency:                      "USD",
			Amount:                        "100.00",
			MerchantProvidedBuyerIdentity: WaffoPancakeBuyerIdentityFromUserID(1),
		},
	}

	wrongStore := base
	wrongStore.StoreID = "STORE_OTHER"
	_, err = ResolveWaffoPancakeTradeNo(&wrongStore)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "store mismatch")

	wrongEnv := base
	wrongEnv.Mode = "test"
	_, err = ResolveWaffoPancakeTradeNo(&wrongEnv)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "environment mismatch")

	wrongAmount := base
	wrongAmount.Data.Amount = "0.01"
	_, err = ResolveWaffoPancakeTradeNo(&wrongAmount)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "amount mismatch")

	wrongCurrency := base
	wrongCurrency.Data.Currency = "EUR"
	_, err = ResolveWaffoPancakeTradeNo(&wrongCurrency)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "currency mismatch")

	wrongBuyer := base
	wrongBuyer.Data.MerchantProvidedBuyerIdentity = WaffoPancakeBuyerIdentityFromUserID(999)
	_, err = ResolveWaffoPancakeTradeNo(&wrongBuyer)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "buyer identity mismatch")
	beforeBind, err := getPaymentOrderBinding(tradeNo)
	require.NoError(t, err)
	assert.Empty(t, beforeBind.ProviderOrderID, "failed identity validation must not lock an upstream order id")

	resolved, err := ResolveWaffoPancakeTradeNo(&base)
	require.NoError(t, err)
	assert.Equal(t, tradeNo, resolved)

	stored, err := getPaymentOrderBinding(tradeNo)
	require.NoError(t, err)
	assert.Equal(t, "ORD_SITE_1", stored.ProviderOrderID)

	conflictingOrder := base
	conflictingOrder.Data.OrderID = "ORD_SITE_2"
	_, err = ResolveWaffoPancakeTradeNo(&conflictingOrder)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "provider order id mismatch")

	legacyTradeNo := "WAFFO_PANCAKE-1-legacy-unbound"
	require.NoError(t, db.Create(&model.TopUp{
		UserId: 1, Amount: 100, Money: 100, TradeNo: legacyTradeNo,
		PaymentMethod: model.PaymentMethodWaffoPancake, PaymentProvider: model.PaymentProviderWaffoPancake,
		Status: common.TopUpStatusPending,
	}).Error)
	legacyEvent := base
	legacyEvent.Data.OrderMerchantExternalID = legacyTradeNo
	legacyEvent.Data.OrderID = "ORD_LEGACY"
	_, err = ResolveWaffoPancakeTradeNo(&legacyEvent)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "binding not found")
}

func TestResolveWaffoPancakeSubscriptionRequiresBoundAmountStoreAndEnvironment(t *testing.T) {
	withPaymentBindingDB(t)
	withPancakeBindingConfig(t, "STORE_SUB")

	tradeNo := "WAFFO_PANCAKE_SUB-2-secure"
	order := &model.SubscriptionOrder{
		UserId: 2, PlanId: 7, Money: 29.99, TradeNo: tradeNo,
		PaymentMethod: model.PaymentMethodWaffoPancake, PaymentProvider: model.PaymentProviderWaffoPancake,
		Status: common.TopUpStatusPending,
	}
	binding, err := newWaffoPancakeBinding(tradeNo, OrderBindingKindSubscription, "29.99")
	require.NoError(t, err)
	require.NoError(t, insertSubscriptionOrderWithBinding(order, binding))

	event := &WaffoPancakeWebhookEvent{
		StoreID: "STORE_SUB",
		Mode:    "prod",
		Data: WaffoPancakeWebhookData{
			OrderID:                       "ORD_SUB_1",
			OrderMerchantExternalID:       tradeNo,
			Currency:                      "USD",
			Amount:                        "0.01",
			MerchantProvidedBuyerIdentity: WaffoPancakeBuyerIdentityFromUserID(2),
		},
	}
	_, err = ResolveWaffoPancakeSubscriptionTradeNo(event)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "amount mismatch")

	event.Data.Amount = "29.99"
	resolved, err := ResolveWaffoPancakeSubscriptionTradeNo(event)
	require.NoError(t, err)
	assert.Equal(t, tradeNo, resolved)
}

func TestValidateWaffoPaymentCallbackRejectsAmountCurrencyIdentityMismatch(t *testing.T) {
	withPaymentBindingDB(t)
	previousCurrency := setting.WaffoCurrency
	previousMerchant := setting.WaffoMerchantId
	previousSandbox := setting.WaffoSandbox
	setting.WaffoCurrency = "USD"
	setting.WaffoMerchantId = "MER_SITE"
	setting.WaffoSandbox = false
	t.Cleanup(func() {
		setting.WaffoCurrency = previousCurrency
		setting.WaffoMerchantId = previousMerchant
		setting.WaffoSandbox = previousSandbox
	})

	tradeNo := "WAFFO-3-secure"
	topUp := &model.TopUp{
		UserId: 3, Amount: 100, Money: 100, TradeNo: tradeNo,
		PaymentMethod: model.PaymentMethodWaffo, PaymentProvider: model.PaymentProviderWaffo,
		Status: common.TopUpStatusPending,
	}
	binding, err := newWaffoBinding(tradeNo, "100.00", "USD")
	require.NoError(t, err)
	require.NoError(t, insertTopUpWithBinding(topUp, binding))

	base := &core.PaymentNotificationResult{
		PaymentRequestID: tradeNo,
		MerchantOrderID:  tradeNo,
		AcquiringOrderID: "ACQ_SITE_1",
		OrderAmount:      "100.00",
		OrderCurrency:    "USD",
		MerchantInfo:     map[string]interface{}{"merchantId": "MER_SITE"},
	}

	wrongAmount := *base
	wrongAmount.OrderAmount = "0.01"
	err = validateWaffoPaymentCallback(&wrongAmount)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "amount mismatch")

	wrongCurrency := *base
	wrongCurrency.OrderCurrency = "EUR"
	err = validateWaffoPaymentCallback(&wrongCurrency)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "currency mismatch")

	wrongRequest := *base
	wrongRequest.PaymentRequestID = "REQ_OTHER"
	err = validateWaffoPaymentCallback(&wrongRequest)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "payment request id mismatch")

	wrongMerchant := *base
	wrongMerchant.MerchantInfo = map[string]interface{}{"merchantId": "MER_OTHER"}
	err = validateWaffoPaymentCallback(&wrongMerchant)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "merchant mismatch")

	require.NoError(t, validateWaffoPaymentCallback(base))
	stored, err := getPaymentOrderBinding(tradeNo)
	require.NoError(t, err)
	assert.Equal(t, "ACQ_SITE_1", stored.ProviderOrderID)

	legacyTradeNo := "WAFFO-3-legacy-unbound"
	require.NoError(t, model.DB.Create(&model.TopUp{
		UserId: 3, Amount: 100, Money: 100, TradeNo: legacyTradeNo,
		PaymentMethod: model.PaymentMethodWaffo, PaymentProvider: model.PaymentProviderWaffo,
		Status: common.TopUpStatusPending,
	}).Error)
	legacyCallback := *base
	legacyCallback.PaymentRequestID = legacyTradeNo
	legacyCallback.MerchantOrderID = legacyTradeNo
	legacyCallback.AcquiringOrderID = "ACQ_LEGACY"
	err = validateWaffoPaymentCallback(&legacyCallback)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "binding not found")

	conflictingOrder := *base
	conflictingOrder.AcquiringOrderID = "ACQ_SITE_2"
	err = validateWaffoPaymentCallback(&conflictingOrder)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "provider order id mismatch")
}
