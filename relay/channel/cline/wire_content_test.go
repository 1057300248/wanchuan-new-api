package cline_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestClineWireNormalizesEveryEmptyTextPart(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content any
	}{
		{
			name: "typed mixed text and image",
			content: []dto.MediaContent{
				{Type: "text", CacheControl: []byte(`{"type":"ephemeral"}`)},
				{Type: "text", Text: "keep this text"},
				{Type: "image_url", ImageUrl: map[string]any{"url": "https://example.invalid/image.png"}},
			},
		},
		{
			name: "JSON null text in a multi-part message",
			content: []any{
				map[string]any{"type": "text", "text": nil, "cache_control": map[string]any{"type": "ephemeral"}},
				map[string]any{"type": "text", "text": "keep this text"},
			},
		},
		{
			name: "map slice with a missing text field",
			content: []map[string]any{
				{"type": "text", "cache_control": map[string]any{"type": "ephemeral"}},
				{"type": "text", "text": "keep this text"},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream, received := newClineUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"wire-test","choices":[]}`))
			})
			env := newClineTestEnv(t, types.RelayFormatOpenAI, relayconstant.RelayModeChatCompletions, false, "cline-test-model", upstream.URL)
			request := &dto.GeneralOpenAIRequest{
				Model:    "cline-test-model",
				Messages: []dto.Message{{Role: "user", Content: tc.content}},
			}
			before, err := common.Marshal(tc.content)
			require.NoError(t, err)
			converted, err := env.adaptor.ConvertOpenAIRequest(env.context, env.info, request)
			require.NoError(t, err)
			response := env.send(t, converted)
			require.NoError(t, response.Body.Close())
			body := (<-received).body
			text := gjson.GetBytes(body, "messages.0.content.0.text")
			assert.Equal(t, gjson.String, text.Type, "the actual HTTP body must contain an explicit text string")
			assert.Empty(t, text.Str)
			assert.Equal(t, "ephemeral", gjson.GetBytes(body, "messages.0.content.0.cache_control.type").Str)
			assert.Equal(t, "keep this text", gjson.GetBytes(body, "messages.0.content.1.text").Str)
			if tc.name == "typed mixed text and image" {
				assert.Equal(t, "https://example.invalid/image.png", gjson.GetBytes(body, "messages.0.content.2.image_url.url").Str)
			}
			after, err := common.Marshal(tc.content)
			require.NoError(t, err)
			assert.JSONEq(t, string(before), string(after), "normalizing the outgoing body must not mutate a reusable input")
		})
	}
}

func TestClineWireNormalizesTheFinalBodyAfterOverrides(t *testing.T) {
	upstream, received := newClineUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		w.WriteHeader(http.StatusOK)
	})
	env := newClineTestEnv(t, types.RelayFormatOpenAI, relayconstant.RelayModeChatCompletions, false, "cline-test-model", upstream.URL)
	body := `{"model":"cline-test-model","temperature":0,"metadata":{"large_id":9007199254740993},"messages":[{"role":"user","content":[{"type":"text"},{"type":"text","text":"hello"}]}]}`
	response, err := env.adaptor.DoRequest(env.context, env.info, strings.NewReader(body))
	require.NoError(t, err)
	require.NoError(t, response.(*http.Response).Body.Close())
	captured := (<-received).body
	assert.Equal(t, `""`, gjson.GetBytes(captured, "messages.0.content.0.text").Raw)
	assert.Equal(t, "9007199254740993", gjson.GetBytes(captured, "metadata.large_id").Raw)
	assert.Equal(t, "0", gjson.GetBytes(captured, "temperature").Raw)
}

func TestClineWireRejectsInvalidContentBeforeSending(t *testing.T) {
	for _, content := range []string{
		`42`,
		`{"type":"text","text":"not-an-array"}`,
		`[{"type":"text","text":{"private":"do-not-echo-this-content"}}]`,
		`[{"type":"text","text":42}]`,
		`[null]`,
	} {
		t.Run(content, func(t *testing.T) {
			upstream, received := newClineUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
				w.WriteHeader(http.StatusOK)
			})
			env := newClineTestEnv(t, types.RelayFormatOpenAI, relayconstant.RelayModeChatCompletions, false, "cline-test-model", upstream.URL)
			body := `{"model":"cline-test-model","messages":[{"role":"user","content":` + content + `}]}`
			response, err := env.adaptor.DoRequest(env.context, env.info, strings.NewReader(body))
			if response != nil {
				_ = response.(*http.Response).Body.Close()
			}
			require.Error(t, err)
			var apiErr *types.NewAPIError
			require.ErrorAs(t, err, &apiErr)
			assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
			assert.Equal(t, "content", apiErr.ToOpenAIError().Param)
			assert.NotContains(t, err.Error(), "do-not-echo-this-content")
			assert.Empty(t, received, "invalid content must not reach any upstream")
		})
	}
}

func TestClineWirePreservesValidToolAndMultimodalHistory(t *testing.T) {
	upstream, received := newClineUpstream(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		w.WriteHeader(http.StatusOK)
	})
	env := newClineTestEnv(t, types.RelayFormatOpenAI, relayconstant.RelayModeChatCompletions, false, "cline-test-model", upstream.URL)
	body := `{"model":"cline-test-model","messages":[{"role":"system","content":"instructions"},{"role":"user","content":[{"type":"text","text":"   "},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA","detail":"low"}}]},{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_1","content":"result"}]}`
	response, err := env.adaptor.DoRequest(env.context, env.info, strings.NewReader(body))
	require.NoError(t, err)
	require.NoError(t, response.(*http.Response).Body.Close())
	assert.JSONEq(t, body, string((<-received).body))
}
