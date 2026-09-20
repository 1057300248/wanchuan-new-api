package service

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTokenGroupRoute(t *testing.T) {
	for _, tc := range []struct {
		name    string
		key     string
		group   string
		present bool
		invalid bool
	}{
		{name: "canonical key", key: "key"},
		{name: "legacy channel pin", key: "key-123"},
		{name: "legacy invalid channel pin", key: "key-invalid"},
		{name: "ASCII group", key: "key-grp-62", group: "b", present: true},
		{name: "Unicode and delimiter", key: "key-grp-" + hex.EncodeToString([]byte("<国模>-稳定")), group: "<国模>-稳定", present: true},
		{name: "missing group", key: "key-grp", present: true, invalid: true},
		{name: "empty group", key: "key-grp-", present: true, invalid: true},
		{name: "missing key", key: "-grp-62", present: true, invalid: true},
		{name: "invalid hex", key: "key-grp-zz", present: true, invalid: true},
		{name: "odd hex", key: "key-grp-6", present: true, invalid: true},
		{name: "invalid UTF8", key: "key-grp-ff", present: true, invalid: true},
		{name: "auto is not a concrete group", key: "key-grp-6175746f", present: true, invalid: true},
		{name: "combined channel pin", key: "key-grp-62-123", present: true, invalid: true},
		{name: "oversized hint", key: "key-grp-" + strings.Repeat("61", 1025), present: true, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			group, present, err := ParseTokenGroupRoute(strings.Split(tc.key, "-"))
			assert.Equal(t, tc.present, present)
			if tc.invalid {
				require.Error(t, err)
				assert.Empty(t, group)
				assert.NotContains(t, err.Error(), tc.key)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.group, group)
		})
	}
}
