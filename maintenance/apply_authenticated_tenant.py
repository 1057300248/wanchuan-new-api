#!/usr/bin/env python3
"""Exact-source migration for the dedicated tenant-isolation review branch."""
from pathlib import Path
p = Path('relay/channel/api_request.go')
s = p.read_text(encoding='utf-8')
old = 'func applyHeaderOverridePlaceholders(template string, c *gin.Context, apiKey string) (string, bool, error) {\n'
new = old + '\tif value, handled, err := resolveAuthenticatedTenant(template, c, apiKey, false); handled {\n\t\treturn value, err == nil, err\n\t}\n'
if new not in s:
    assert s.count(old) == 1
    s = s.replace(old, new)
old = '\t\tvalue, include, err := applyHeaderOverridePlaceholders(str, c, info.ApiKey)\n'
new = '''		if value, handled, err := resolveAuthenticatedTenant(str, c, info.ApiKey, info.IsChannelTest); handled {
			if err != nil {
				return nil, types.NewError(err, types.ErrorCodeChannelHeaderOverrideInvalid)
			}
			headerOverride[key] = value
			continue
		}
''' + old
if new not in s:
    assert s.count(old) == 1
    s = s.replace(old, new)
s = s.replace('//   - {api_key}: resolved to the channel API key', '//   - {authenticated_tenant}: opaque scope from verified user/token identity\n//   - {api_key}: resolved to the channel API key') if '//   - {authenticated_tenant}:' not in s else s
p.write_text(s, encoding='utf-8')
