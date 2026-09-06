package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPaymentPayloadSummaryDoesNotExposePayloadOrSignature(t *testing.T) {
	payload := []byte(`{"customer_email":"customer@example.com","secret":"payment-secret"}`)
	summary := paymentPayloadSummary(payload)

	assert.Contains(t, summary, "bytes=")
	assert.Contains(t, summary, "sha256=")
	assert.NotContains(t, summary, string(payload))
	assert.False(t, paymentSignaturePresent(""))
	assert.True(t, paymentSignaturePresent("signature"))
}
