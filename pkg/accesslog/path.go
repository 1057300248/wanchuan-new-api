// Package accesslog defines the gateway's URL display policy. It never changes
// the request used by authentication, capability checks or payment signatures.
package accesslog

import (
	"net/url"
	"strings"
)

// Path returns a redacted log value from distinct URL path/query components.
// Call before downstream handlers can rewrite the URL, but after the host's
// existing artifact capability preprocessing. Never split Gin's display string:
// its decoded path may itself contain a question mark.
func Path(requestURL *url.URL) string {
	if requestURL == nil {
		return "[unavailable]"
	}
	path := requestURL.EscapedPath()
	cleanPath := requestURL.Path
	switch {
	case strings.HasPrefix(cleanPath, "/api/oauth/"), strings.HasPrefix(cleanPath, "/oauth/"),
		cleanPath == "/api/stripe/webhook", cleanPath == "/api/creem/webhook", cleanPath == "/api/waffo/webhook",
		strings.HasPrefix(cleanPath, "/api/waffo-pancake/webhook/"), cleanPath == "/api/user/epay/notify",
		cleanPath == "/api/subscription/epay/notify", cleanPath == "/api/subscription/epay/return":
		// Provider-defined callback fields may carry credentials under arbitrary
		// names. Omit their entire query from the display, not from the request.
		return path
	}
	if requestURL.RawQuery == "" {
		return path
	}
	return path + "?" + credentialQuery(requestURL.RawQuery)
}

// credentialQuery retains non-secret diagnostic fields byte-for-byte. The
// aliases are an explicit supported set, not a claim that every arbitrary query
// parameter can be classified as safe. Unparseable queries fail closed.
func credentialQuery(rawQuery string) string {
	if _, err := url.ParseQuery(rawQuery); err != nil {
		return "[REDACTED]"
	}
	fields := strings.Split(rawQuery, "&")
	for i, field := range fields {
		name, _, _ := strings.Cut(field, "=")
		decodedName, err := url.QueryUnescape(name)
		if err != nil {
			return "[REDACTED]"
		}
		if IsSensitiveURLQueryKey(decodedName) {
			fields[i] = name + "=[REDACTED]"
		}
	}
	return strings.Join(fields, "&")
}

// IsSensitiveURLQueryKey classifies decoded query names for access and relay logs.
// Callers sanitize a log copy; this must not change the original request.
func IsSensitiveURLQueryKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	normalized = strings.ReplaceAll(strings.ReplaceAll(normalized, "-", ""), "_", "")
	switch normalized {
	case "key", "apikey", "xapikey", "xgoogapikey", "mjapisecret",
		"token", "apitoken", "accesstoken", "refreshtoken", "bearertoken",
		"authorization", "auth", "bearer", "password", "passwd",
		"secret", "secretkey", "clientsecret", "apisecret",
		"session", "sessionid", "sid", "signature", "sig", "sign",
		"awsaccesskeyid", "xamzsignature", "xamzcredential", "xamzsecuritytoken":
		return true
	}
	return strings.Contains(normalized, "token") ||
		strings.Contains(normalized, "secret") ||
		strings.Contains(normalized, "signature")
}
