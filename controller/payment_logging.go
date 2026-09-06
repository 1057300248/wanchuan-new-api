package controller

import (
	"crypto/sha256"
	"fmt"
)

// paymentPayloadSummary gives operators a stable correlation value without
// persisting payment payloads, customer data, or webhook signatures in logs.
func paymentPayloadSummary(payload []byte) string {
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("bytes=%d sha256=%x", len(payload), digest[:8])
}
