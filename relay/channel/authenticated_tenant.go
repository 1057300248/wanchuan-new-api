package channel

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
)

const authenticatedTenantPlaceholder = "{authenticated_tenant}"

// resolveAuthenticatedTenant scopes upstream state to the server-authenticated
// API token, never to an unverified request header. It is an opt-in header
// override, not another authentication mechanism. The receiving service must
// only accept this header from a trusted gateway using private/TLS transport.
// Channel-key rotation intentionally invalidates the old upstream state scope.
func resolveAuthenticatedTenant(template string, c *gin.Context, channelKey string, channelTest bool) (string, bool, error) {
	trimmed := strings.TrimSpace(template)
	if !strings.Contains(trimmed, "{authenticated_tenant") {
		return "", false, nil
	}
	if trimmed != authenticatedTenantPlaceholder {
		return "", true, fmt.Errorf("authenticated_tenant placeholder must be the full header value")
	}
	if strings.TrimSpace(channelKey) == "" {
		return "", true, fmt.Errorf("authenticated_tenant requires a configured channel credential")
	}

	// Channel self-tests have no end-user token. Use a distinct namespace instead
	// of omitting the tenant header or impersonating an arbitrary real user.
	identity := "new-api:channel-test:v1"
	if !channelTest {
		if c == nil || c.GetInt("id") <= 0 || c.GetInt("token_id") <= 0 {
			return "", true, fmt.Errorf("authenticated_tenant requires a verified user and API token identity")
		}
		identity = fmt.Sprintf("new-api:token:v1:%d:%d", c.GetInt("id"), c.GetInt("token_id"))
	}
	mac := hmac.New(sha256.New, []byte(channelKey))
	_, _ = mac.Write([]byte(identity))
	return "newapi:v1:" + hex.EncodeToString(mac.Sum(nil)), true, nil
}
