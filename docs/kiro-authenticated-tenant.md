# Kiro Responses continuation tenant isolation

## Configuration

Configure **Kiro's config.json**, not the New API body override:

```json
{"responseStoreTenantHeader":"X-Kiro2CC-Tenant"}
```

The equivalent Kiro environment variable is `RESPONSE_STORE_TENANT_HEADER=X-Kiro2CC-Tenant`.

In the **New API channel Header Override**, use:

```json
{"X-Kiro2CC-Tenant":"{authenticated_tenant}"}
```

Keep the normal channel Authorization using the Kiro channel credential. Do not copy an end-user credential into that Authorization header. The new placeholder must occupy the complete value. The channel must route Responses requests to the Kiro Responses endpoint without forcing `store=false`; clients must use `store=true` for responses they intend to resume. Use the actual reachable Kiro listen port; `5678` is not an application default.

Do **not** use `{client_header:Authorization}`, `{client_header:x-api-key}`, or a client-supplied tenant header as an authorization boundary. Raw headers can differ from the authentication method that actually succeeded, and can have alternate spellings or unrelated client-controlled values.

## Identity contract

The placeholder reads `id` and `token_id` from the server-side context established by successful API token authentication. HMAC-SHA256 with a domain-separated message and the configured channel credential generates an opaque per-token scope. The raw downstream credential, numeric identity and channel credential are not transmitted in the tenant header.

Different authenticated API tokens do not share continuation state, including two tokens owned by the same user. Alternate authentication-header formats for one authenticated token keep the same scope. Rotating the channel credential changes the scope; rotating/replacing the downstream token to a new token ID also requires a new conversation or replaying the full input history.

Channel self-tests use a distinct HMAC domain, never a real user's scope. Ordinary requests lacking an authenticated token fail closed instead of using that self-test scope. Generic existing header placeholders retain their behavior; the trusted scope is strictly opt-in. Wildcard passthrough happens before explicit overrides, so this explicit tenant override replaces a hostile same-name incoming header.

The receiver must only be reachable through the trusted gateway/private network or authenticated TLS transport. The tenant value is a namespace, not independent authentication or a signed request. Do not expose the Kiro channel credential to users. With a shared Kiro key, usage/RPM/quota still aggregate on Kiro; New API remains responsible for downstream billing and limits. No billing or database behavior is changed here.

## Validation and security references

Regression tests exercise the actual header override resolver and final HTTP header application, including alternative-authentication raw headers, user/token separation, spoofed tenant headers, missing verified context, missing channel credential, full-value template enforcement, self-test separation and key rotation.

Applicable guidance reviewed: OWASP Authentication Cheat Sheet (server-verified identity, alternate paths must not bypass assurance) and Session Management Cheat Sheet (server-side ownership binding, non-secret opaque identifiers, secure transport):

- https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html
- https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html

This is a focused source-level security improvement, not an assertion that the entire deployment has been certified against ASVS. Runtime network exposure, log redaction and end-to-end deployment configuration require operator verification. No real account credentials or live model calls are used in tests.
