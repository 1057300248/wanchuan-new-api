package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSumUsedQuotaReturnsRangeTotalsAndCurrentMinuteRates(t *testing.T) {
	truncateTables(t)
	now := time.Now().Unix()
	logs := []Log{
		{
			UserId: 1, Username: "range-user", Type: LogTypeConsume,
			CreatedAt: now - 120, Quota: 100, PromptTokens: 10, CompletionTokens: 5,
		},
		{
			UserId: 1, Username: "range-user", Type: LogTypeConsume,
			CreatedAt: now - 10, Quota: 200, PromptTokens: 20, CompletionTokens: 7,
		},
		{
			UserId: 1, Username: "range-user", Type: LogTypeConsume,
			CreatedAt: now - 1000, Quota: 900, PromptTokens: 90, CompletionTokens: 90,
		},
		{
			UserId: 1, Username: "range-user", Type: LogTypeError,
			CreatedAt: now - 5, Quota: 500, PromptTokens: 50, CompletionTokens: 50,
		},
	}
	require.NoError(t, LOG_DB.Create(&logs).Error)

	stat, err := SumUsedQuota(0, now-300, now, "", "range-user", "", 0, "")
	require.NoError(t, err)
	assert.Equal(t, 300, stat.Quota)
	assert.Equal(t, 2, stat.RequestCount)
	assert.Equal(t, 42, stat.TokenCount)
	assert.Equal(t, 1, stat.Rpm)
	assert.Equal(t, 27, stat.Tpm)
}
