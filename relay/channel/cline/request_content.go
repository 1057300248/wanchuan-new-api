package cline

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const clineEmptyTextContent = `[{"type":"text","text":""}]`

func clineContentError(index int, reason string) *types.NewAPIError {
	return types.WithOpenAIError(types.OpenAIError{
		Message: fmt.Sprintf("Invalid Cline messages[%d].content: %s", index, reason),
		Type:    "invalid_request_error",
		Param:   "content",
		Code:    "invalid_content",
	}, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
}

func clineRequiresContent(role string) bool {
	switch role {
	case "user", "system", "developer":
		return true
	default:
		return false
	}
}

// Normalize raw JSON rather than round-tripping through MediaContent.Text,
// whose omitempty tag would remove an explicitly empty string again. Only the
// content being repaired is replaced; media, cache metadata and numeric values
// elsewhere in the request are preserved without a float64 conversion.
func normalizeClineContent(role string, content gjson.Result, index int) ([]byte, bool, error) {
	switch content.Type {
	case gjson.Null:
		if clineRequiresContent(role) {
			return []byte(clineEmptyTextContent), true, nil
		}
		// Assistant tool-call messages may legitimately have null content.
		return nil, false, nil
	case gjson.String:
		if content.Str == "" && clineRequiresContent(role) {
			return []byte(clineEmptyTextContent), true, nil
		}
		return nil, false, nil
	}
	if !content.IsArray() {
		return nil, false, clineContentError(index, "expected a string or an array of content parts")
	}
	parts := content.Array()
	if len(parts) == 0 && clineRequiresContent(role) {
		return []byte(clineEmptyTextContent), true, nil
	}
	result := []byte(content.Raw)
	changed := false
	for partIndex, part := range parts {
		if !part.IsObject() || part.Get("type").Type != gjson.String || part.Get("type").Str == "" {
			return nil, false, clineContentError(index, "each content part must be an object with a type")
		}
		if part.Get("type").Str != "text" {
			continue
		}
		text := part.Get("text")
		if text.Type == gjson.String {
			continue
		}
		if text.Type != gjson.Null {
			return nil, false, clineContentError(index, "text parts must contain a string")
		}
		var err error
		result, err = sjson.SetRawBytes(result, fmt.Sprintf("%d.text", partIndex), []byte(`""`))
		if err != nil {
			return nil, false, clineContentError(index, "could not normalize an empty text part")
		}
		changed = true
	}
	return result, changed, nil
}

func normalizeClineMessageContent(request *dto.GeneralOpenAIRequest) error {
	messages := append([]dto.Message(nil), request.Messages...)
	changed := false
	for index, message := range messages {
		encoded, err := common.Marshal(message.Content)
		if err != nil {
			return clineContentError(index, "content is not JSON serializable")
		}
		normalized, contentChanged, err := normalizeClineContent(message.Role, gjson.ParseBytes(encoded), index)
		if err != nil {
			return err
		}
		if contentChanged {
			messages[index].Content = json.RawMessage(normalized)
			changed = true
		}
	}
	if changed {
		request.Messages = messages
	}
	return nil
}

func normalizeClineWireRequest(reader io.Reader) (io.Reader, error) {
	if reader == nil {
		return nil, clineContentError(0, "request body is required")
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadRequestBodyFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	if !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() {
		return nil, clineContentError(0, "request body must be a JSON object")
	}
	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() {
		return nil, clineContentError(0, "messages must be an array")
	}
	for index, message := range messages.Array() {
		if !message.IsObject() {
			return nil, clineContentError(index, "message must be an object")
		}
		normalized, changed, err := normalizeClineContent(message.Get("role").Str, message.Get("content"), index)
		if err != nil {
			return nil, err
		}
		if changed {
			body, err = sjson.SetRawBytes(body, fmt.Sprintf("messages.%d.content", index), normalized)
			if err != nil {
				return nil, clineContentError(index, "could not normalize content")
			}
		}
	}
	return bytes.NewReader(body), nil
}
