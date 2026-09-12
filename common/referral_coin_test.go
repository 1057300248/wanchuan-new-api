package common

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReferralCoinConversionEnforcesExactWalletUnits(t *testing.T) {
	old := QuotaPerUnit
	t.Cleanup(func() { QuotaPerUnit = old })
	for _, tc := range []struct {
		name              string
		rate              float64
		coins, want, step int
		invalid           bool
	}{
		{"whole Coin", 500000, 1000000, 500000, 2, false},
		{"smallest exact amount", 500000, 2, 1, 2, false},
		{"fractional quota", 500000, 1, 0, 2, true},
		{"fractional rate", 500000.5, 2000000, 1000001, 2000000, false},
		{"fractional rate rejected", 500000.5, 1000000, 0, 2000000, true},
		{"above input range", 500000, MaxWalletQuota + 1, 0, 2, true},
		{"above output range", 2000000, MaxWalletQuota, 0, 1, true},
		{"NaN", math.NaN(), 1000000, 0, 0, true},
		{"infinite", math.Inf(1), 1000000, 0, 0, true},
		{"zero rate", 0, 1000000, 0, 0, true},
		{"zero amount", 500000, 0, 0, 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			QuotaPerUnit = tc.rate
			got, err := ReferralCoinsToQuota(tc.coins)
			if tc.invalid {
				require.ErrorIs(t, err, ErrReferralCoinAmount)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			step, err := ReferralCoinRedemptionStep()
			require.NoError(t, err)
			assert.Equal(t, tc.step, step)
		})
	}
}
