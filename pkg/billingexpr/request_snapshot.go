package billingexpr

import (
	"fmt"
	"strings"
	"time"

	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"
	"github.com/tidwall/gjson"
)

var sensitiveBillingHeaders = map[string]struct{}{
	"authorization":       {},
	"proxy-authorization": {},
	"cookie":              {},
	"set-cookie":          {},
	"x-api-key":           {},
	"api-key":             {},
	"x-security-proof":    {},
}

// FreezeRequestInput captures only request facts referenced by an expression so
// an async task can settle against the same request state after a restart.
// Raw request bodies and unrelated headers are deliberately never persisted.
func FreezeRequestInput(exprStr string, input RequestInput) (RequestInput, error) {
	_, body := ParseExprVersion(exprStr)
	tree, err := parser.Parse(body)
	if err != nil {
		return RequestInput{}, fmt.Errorf("freeze billing request: %w", err)
	}

	frozen := RequestInput{Timestamp: input.Timestamp}
	if frozen.Timestamp <= 0 {
		frozen.Timestamp = time.Now().Unix()
	}
	normalizedHeaders := normalizeHeaders(input.Headers)

	var freezeErr error
	ast.Find(tree.Node, func(node ast.Node) bool {
		call, ok := node.(*ast.CallNode)
		if !ok || len(call.Arguments) != 1 {
			return false
		}
		callee, ok := call.Callee.(*ast.IdentifierNode)
		if !ok || (callee.Value != "header" && callee.Value != "param") {
			return false
		}
		literal, ok := call.Arguments[0].(*ast.StringNode)
		if !ok {
			freezeErr = fmt.Errorf("freeze billing request: %s() requires a literal key for async task billing", callee.Value)
			return true
		}
		key := strings.TrimSpace(literal.Value)
		if key == "" {
			return false
		}

		if callee.Value == "header" {
			normalizedKey := strings.ToLower(key)
			if _, sensitive := sensitiveBillingHeaders[normalizedKey]; sensitive {
				freezeErr = fmt.Errorf("freeze billing request: header %q is sensitive and cannot be persisted for async task billing", key)
				return true
			}
			if value, exists := normalizedHeaders[normalizedKey]; exists {
				if frozen.Headers == nil {
					frozen.Headers = make(map[string]string)
				}
				frozen.Headers[normalizedKey] = value
			}
			return false
		}

		if value, exists := input.Params[key]; exists {
			if frozen.Params == nil {
				frozen.Params = make(map[string]any)
			}
			frozen.Params[key] = value
			return false
		}
		if len(input.Body) == 0 {
			return false
		}
		result := gjson.GetBytes(input.Body, key)
		if result.Exists() {
			if frozen.Params == nil {
				frozen.Params = make(map[string]any)
			}
			frozen.Params[key] = result.Value()
		}
		return false
	})
	if freezeErr != nil {
		return RequestInput{}, freezeErr
	}
	return frozen, nil
}

// UsesRequestContext reports whether settlement must reuse request-time facts.
func UsesRequestContext(exprStr string) bool {
	vars := UsedVars(exprStr)
	for _, name := range []string{"header", "param", "hour", "minute", "weekday", "month", "day"} {
		if vars[name] {
			return true
		}
	}
	return false
}
