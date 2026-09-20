package cline

import (
	"strings"

	"github.com/tidwall/gjson"
)

// Cline can wrap an upstream validation error in a stream-initialization error.
// Read only structured error fields, with a bounded nesting depth. Never copy
// a raw request/response body or guess an HTTP status from a message string.
func clineErrorDetailAtDepth(value gjson.Result, depth int) clineErrorDetail {
	if depth > 4 {
		return clineErrorDetail{}
	}
	switch {
	case value.Type == gjson.String:
		return clineErrorDetail{Message: strings.TrimSpace(value.Str)}
	case value.IsArray():
		var detail clineErrorDetail
		messages := make([]string, 0, len(value.Array()))
		for _, item := range value.Array() {
			child := clineErrorDetailAtDepth(item, depth+1)
			if child.Message != "" {
				messages = append(messages, child.Message)
			}
			if detail.Type == "" && child.Type != "" {
				detail.Type = child.Type
			}
			if detail.Code == "" && child.Code != "" {
				detail.Code = child.Code
			}
			if detail.Param == "" && child.Param != "" {
				detail.Param = child.Param
			}
			if detail.StatusCode == 0 && child.StatusCode != 0 {
				detail.StatusCode = child.StatusCode
			}
		}
		detail.Message = strings.Join(messages, "; ")
		return detail
	case !value.IsObject():
		return clineErrorDetail{}
	}

	detail := clineErrorDetail{
		Message: strings.TrimSpace(value.Get("message").Str),
		Type:    strings.TrimSpace(value.Get("type").Str),
		Param:   value.Get("param").Str,
	}
	if detail.Message == "" {
		detail.Message = strings.TrimSpace(value.Get("detail").Str)
	}
	code := value.Get("code")
	if code.Type == gjson.String {
		detail.Code = strings.TrimSpace(code.Str)
	} else if code.Type == gjson.Number {
		detail.Code = code.String()
	}
	for _, path := range []string{"status", "status_code", "statusCode"} {
		status := value.Get(path)
		if status.Type == gjson.Number && status.Int() >= 400 && status.Int() <= 599 && status.Float() == float64(status.Int()) {
			detail.StatusCode = int(status.Int())
			break
		}
	}
	for _, path := range []string{"error", "errors", "data.error", "details", "cause"} {
		nested := value.Get(path)
		if !nested.Exists() || nested.Type == gjson.Null {
			continue
		}
		if (path == "details" || path == "cause") && !nested.IsObject() {
			continue
		}
		child := clineErrorDetailAtDepth(nested, depth+1)
		if child.Message == "" && child.Param == "" && child.StatusCode == 0 {
			continue
		}
		if child.Message != "" {
			detail.Message = child.Message
		}
		if child.Type != "" {
			detail.Type = child.Type
		}
		if child.Code != "" {
			detail.Code = child.Code
		}
		if child.Param != "" {
			detail.Param = child.Param
		}
		if child.StatusCode != 0 {
			detail.StatusCode = child.StatusCode
		}
		break
	}
	return detail
}
