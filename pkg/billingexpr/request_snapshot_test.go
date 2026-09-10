package billingexpr

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFreezeRequestInputCapturesOnlyReferencedFacts(t *testing.T) {
	exprStr := `tier("base", 1) * (header("x-plan") == "fast" ? 2 : 1) * (param("quality") == "hd" ? 3 : 1)`
	input := RequestInput{
		Headers: map[string]string{
			"X-Plan":        "fast",
			"X-Unused":      "do-not-store",
			"Authorization": "Bearer do-not-store",
		},
		Body: []byte(`{"quality":"hd","secret":"do-not-store"}`),
	}

	frozen, err := FreezeRequestInput(exprStr, input)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"x-plan": "fast"}, frozen.Headers)
	assert.Equal(t, map[string]any{"quality": "hd"}, frozen.Params)
	assert.Empty(t, frozen.Body)
	assert.NotZero(t, frozen.Timestamp)

	cost, _, err := RunExprWithRequest(exprStr, TokenParams{}, frozen)
	require.NoError(t, err)
	assert.Equal(t, 6.0, cost)

	encoded, err := common.Marshal(frozen)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "do-not-store")
	assert.NotContains(t, string(encoded), "authorization")
}

func TestFreezeRequestInputRejectsSensitiveReferencedHeader(t *testing.T) {
	_, err := FreezeRequestInput(
		`tier("secret", header("authorization") == "Bearer x" ? 2 : 1)`,
		RequestInput{Headers: map[string]string{"Authorization": "Bearer x"}},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sensitive")
}

func TestFrozenRequestTimestampKeepsTimeRuleStable(t *testing.T) {
	exprStr := `tier("time", hour("UTC") == 3 ? 2.0 : 1.0)`
	frozen, err := FreezeRequestInput(exprStr, RequestInput{
		Timestamp: time.Date(2026, time.September, 10, 3, 15, 0, 0, time.UTC).Unix(),
	})
	require.NoError(t, err)

	cost, _, err := RunExprWithRequest(exprStr, TokenParams{}, frozen)
	require.NoError(t, err)
	assert.Equal(t, 2.0, cost)
}
