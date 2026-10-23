package common

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFinancialStrictJSONBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		valid     bool
	}{
		{"single", `{"amount":1}`, true},
		{"whitespace", "{\"amount\":1}\n\t ", true},
		{"unknown", `{"amount":1,"operator_id":99}`, false},
		{"second_value", `{"amount":1} {"amount":2}`, false},
		{"trailing", `{"amount":1} garbage`, false},
		{"empty", ``, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var value struct {
				Amount int `json:"amount"`
			}
			err := DecodeJsonStrict(strings.NewReader(tc.raw), &value)
			if tc.valid {
				require.NoError(t, err)
				require.Equal(t, 1, value.Amount)
			} else {
				require.Error(t, err)
			}
		})
	}
}
