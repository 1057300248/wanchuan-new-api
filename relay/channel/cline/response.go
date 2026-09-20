package cline

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type clineErrorDetail struct {
	Message    string
	Type       string
	Code       string
	Param      string
	StatusCode int
}

func newClineEnvelopeError(detail clineErrorDetail) *types.NewAPIError {
	if strings.TrimSpace(detail.Type) == "" {
		detail.Type = "api_error"
	}
	if strings.TrimSpace(detail.Message) == "" {
		detail.Message = "Cline request failed"
	}
	var code any
	if detail.Code != "" {
		code = detail.Code
	}
	statusCode := detail.StatusCode
	if statusCode < 400 || statusCode > 599 {
		statusCode = http.StatusBadGateway
	}
	return types.WithOpenAIError(types.OpenAIError{
		Message: detail.Message,
		Type:    detail.Type,
		Code:    code,
		Param:   detail.Param,
	}, statusCode)
}

func clineErrorDetailFromValue(value gjson.Result) clineErrorDetail {
	return clineErrorDetailAtDepth(value, 0)
}

func clineErrorDetailFromBody(body []byte) clineErrorDetail {
	return clineErrorDetailFromValue(gjson.ParseBytes(body))
}

func normalizeClineResponseBody(body []byte) ([]byte, *types.NewAPIError) {
	if len(body) == 0 {
		return nil, types.NewOpenAIError(fmt.Errorf("empty response body"), types.ErrorCodeEmptyResponse, http.StatusBadGateway)
	}
	payload := body
	if gjson.ValidBytes(body) && gjson.ParseBytes(body).IsObject() {
		success := gjson.GetBytes(body, "success")
		switch {
		case success.Type == gjson.False:
			return nil, newClineEnvelopeError(clineErrorDetailFromBody(body))
		case success.Type == gjson.True:
			data := gjson.GetBytes(body, "data")
			switch {
			case data.IsObject():
				payload = []byte(data.Raw)
			case hasClineCompletionShape(body):
			default:
				return nil, newClineEnvelopeError(clineErrorDetailFromBody(body))
			}
		}
	}
	return normalizeClinePayload(payload)
}

func hasClineCompletionShape(body []byte) bool {
	for _, path := range []string{"choices", "usage", "id"} {
		if gjson.GetBytes(body, path).Exists() {
			return true
		}
	}
	return false
}

func normalizeClinePayload(payload []byte) ([]byte, *types.NewAPIError) {
	if !gjson.ValidBytes(payload) || !gjson.ParseBytes(payload).IsObject() {
		return payload, nil
	}
	if errorsValue := gjson.GetBytes(payload, "errors"); errorsValue.Exists() && errorsValue.Type != gjson.Null &&
		!gjson.GetBytes(payload, "error").Exists() && !gjson.GetBytes(payload, "choices").Exists() {
		return nil, newClineEnvelopeError(clineErrorDetailFromBody(payload))
	}
	return normalizeClineReasoning(payload), nil
}

func normalizeClineReasoning(payload []byte) []byte {
	choices := gjson.GetBytes(payload, "choices")
	if !choices.IsArray() {
		return payload
	}
	result := payload
	for index := range choices.Array() {
		for _, field := range []string{"message", "delta"} {
			path := fmt.Sprintf("choices.%d.%s", index, field)
			message := gjson.GetBytes(result, path)
			if !message.IsObject() {
				continue
			}
			reasoning, ok := clineReasoningText(message)
			if ok {
				if updated, err := sjson.SetBytes(result, path+".reasoning_content", reasoning); err == nil {
					result = updated
				}
			}
			for _, private := range []string{".reasoning", ".reasoning_details", ".provider_metadata"} {
				if !gjson.GetBytes(result, path+private).Exists() {
					continue
				}
				if updated, err := sjson.DeleteBytes(result, path+private); err == nil {
					result = updated
				}
			}
		}
	}
	return result
}

func clineReasoningText(message gjson.Result) (string, bool) {
	details := message.Get("reasoning_details")
	if details.IsArray() && len(details.Array()) > 0 {
		var builder strings.Builder
		for _, detail := range details.Array() {
			builder.WriteString(gjson.Get(detail.Raw, "text").Str)
		}
		return builder.String(), true
	}
	reasoning := message.Get("reasoning")
	if reasoning.Type == gjson.String {
		return reasoning.Str, true
	}
	return "", false
}

func prepareClineJSONResponse(resp *http.Response) *types.NewAPIError {
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	normalized, apiErr := normalizeClineResponseBody(body)
	if apiErr != nil {
		return apiErr
	}
	resp.Body = io.NopCloser(bytes.NewReader(normalized))
	resp.ContentLength = int64(len(normalized))
	return nil
}

func normalizeClineStreamPayload(eventType, payload string) ([]byte, *types.NewAPIError) {
	if !gjson.Valid(payload) {
		if eventType == "error" {
			return nil, newClineEnvelopeError(clineErrorDetail{Message: payload})
		}
		return []byte(payload), nil
	}
	value := gjson.Parse(payload)
	if !value.IsObject() {
		if eventType == "error" {
			return nil, newClineEnvelopeError(clineErrorDetailFromValue(value))
		}
		return []byte(payload), nil
	}
	success := value.Get("success")
	if success.Type == gjson.False {
		return nil, newClineEnvelopeError(clineErrorDetailFromBody([]byte(payload)))
	}
	if success.Type == gjson.True {
		data := value.Get("data")
		if data.Type == gjson.String && strings.TrimSpace(data.Str) == "[DONE]" {
			return []byte("[DONE]"), nil
		}
		if !data.IsObject() {
			return nil, newClineEnvelopeError(clineErrorDetailFromBody([]byte(payload)))
		}
		return normalizeClineStreamObject(eventType, []byte(data.Raw))
	}
	return normalizeClineStreamObject(eventType, []byte(payload))
}

func normalizeClineStreamObject(eventType string, payload []byte) ([]byte, *types.NewAPIError) {
	if detail, ok := clineStreamErrorDetail(eventType, payload); ok {
		return nil, newClineEnvelopeError(detail)
	}
	return normalizeClineReasoning(payload), nil
}

func clineStreamErrorDetail(eventType string, payload []byte) (clineErrorDetail, bool) {
	value := gjson.ParseBytes(payload)
	if nested := value.Get("data.error"); nested.Exists() && nested.Type != gjson.Null {
		return clineErrorDetailFromBody(payload), true
	}
	if errValue := value.Get("error"); errValue.Exists() && errValue.Type != gjson.Null {
		return clineErrorDetailFromBody(payload), true
	}
	if errorsValue := value.Get("errors"); errorsValue.Exists() && errorsValue.Type != gjson.Null {
		return clineErrorDetailFromBody(payload), true
	}
	if eventType == "error" || value.Get("event").Str == "error" {
		return clineErrorDetailFromValue(value), true
	}
	return clineErrorDetail{}, false
}

func clineErrorFrame(apiErr *types.NewAPIError) string {
	if apiErr == nil {
		return ""
	}
	openAIError := apiErr.ToOpenAIError()
	frame := map[string]any{
		"error": map[string]any{
			"message": openAIError.Message,
			"type":    openAIError.Type,
		},
	}
	if code, ok := openAIError.Code.(string); ok && code != "" {
		frame["error"].(map[string]any)["code"] = code
	}
	if openAIError.Param != "" {
		frame["error"].(map[string]any)["param"] = openAIError.Param
	}
	encoded, err := common.Marshal(frame)
	if err != nil {
		return ""
	}
	return "data: " + string(encoded) + "\n"
}
