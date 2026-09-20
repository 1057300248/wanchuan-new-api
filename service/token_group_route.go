package service

import (
	"encoding/hex"
	"fmt"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/model"
)

// ParseTokenGroupRoute recognizes an optional routing hint in an API key:
// <canonical-key>-grp-<hex-encoded UTF-8 group>. This is NOT a new credential or
// delegated permission. Authentication still uses the canonical key, and the
// requested group must be checked against current token permissions each time.
// Legacy administrator numeric channel suffixes remain a separate protocol.
func ParseTokenGroupRoute(parts []string) (group string, present bool, err error) {
	if len(parts) < 2 || parts[1] != "grp" {
		return "", false, nil
	}
	if len(parts) != 3 || parts[0] == "" || len(parts[2]) == 0 || len(parts[2]) > 2048 {
		return "", true, fmt.Errorf("invalid API key group routing hint")
	}
	decoded, err := hex.DecodeString(parts[2])
	if err != nil || !utf8.Valid(decoded) || len(decoded) == 0 || string(decoded) == "auto" {
		return "", true, fmt.Errorf("invalid API key group routing hint")
	}
	return string(decoded), true, nil
}

// GetTokenSelectedGroups can only narrow the key's effective groups. An empty
// selection keeps its original routing behavior; invalid selections fail closed.
func GetTokenSelectedGroups(userGroup string, token *model.Token, selected string) (effective, selectedGroups []string, err error) {
	effective, err = GetTokenEffectiveGroups(userGroup, token)
	if err != nil {
		return nil, nil, err
	}
	if selected == "" {
		return effective, effective, nil
	}
	for _, group := range effective {
		if group == selected {
			return effective, []string{group}, nil
		}
	}
	return effective, nil, fmt.Errorf("group is not available to this API key")
}
