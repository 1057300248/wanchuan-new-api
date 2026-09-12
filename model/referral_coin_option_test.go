package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateReferralCoinOptionsBoundsPolicy(t *testing.T) {
	require.NoError(t, validateReferralCoinOption("ReferralCoinEnabled", "true"))
	require.NoError(t, validateReferralCoinOption("ReferralCoinRate", "0.01"))
	require.NoError(t, validateReferralCoinOption("ReferralCoinFreezeDays", "30"))
	require.Error(t, validateReferralCoinOption("ReferralCoinRate", "NaN"))
	require.Error(t, validateReferralCoinOption("ReferralCoinRate", "1.01"))
	require.Error(t, validateReferralCoinOption("ReferralCoinFreezeDays", "-1"))
	require.Error(t, validateReferralCoinOption("ReferralCoinFreezeDays", "3651"))
}
