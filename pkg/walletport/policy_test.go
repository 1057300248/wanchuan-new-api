package walletport

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fixturePolicy struct{ bonusModel string }

func (p *fixturePolicy) AccountOrder(billingModel string) []string {
	if p.BonusEligible(billingModel) {
		return []string{AccountBonus, AccountPaid, AccountReward}
	}
	return []string{AccountPaid, AccountReward}
}

func (p *fixturePolicy) BonusEligible(billingModel string) bool {
	return billingModel == p.bonusModel
}

func TestDefaultPolicyFailsClosedAndCannotBeReplaced(t *testing.T) {
	// The production policy is composition-time-only. This non-parallel test
	// restores the package state so repeated test runs do not reconfigure it.
	mu.Lock()
	previous := defaultPolicy
	defaultPolicy = nil
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		defaultPolicy = previous
		mu.Unlock()
	})

	policy, err := Default()
	require.ErrorIs(t, err, ErrNotConfigured)
	assert.Nil(t, policy)
	require.Error(t, SetDefault(nil))
	policy, err = Default()
	require.ErrorIs(t, err, ErrNotConfigured)
	assert.Nil(t, policy, "a rejected registration must not install a fallback")

	first := &fixturePolicy{bonusModel: "eligible"}
	require.NoError(t, SetDefault(first))
	policy, err = Default()
	require.NoError(t, err)
	assert.Same(t, first, policy)
	assert.True(t, policy.BonusEligible("eligible"))
	assert.False(t, policy.BonusEligible("other"))
	assert.Equal(t, []string{AccountBonus, AccountPaid, AccountReward}, policy.AccountOrder("eligible"))

	require.Error(t, SetDefault(&fixturePolicy{bonusModel: "other"}))
	policy, err = Default()
	require.NoError(t, err)
	assert.Same(t, first, policy, "later registration must not change the active commercial policy")
	assert.Equal(t, []string{AccountPaid, AccountReward}, policy.AccountOrder("other"))
}
