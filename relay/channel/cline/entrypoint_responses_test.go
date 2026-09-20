package cline_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const responsesChatCompletionFixture = `{
	"success": true,
	"data": {
		"id": "chatcmpl-1",
		"object": "chat.completion",
		"created": 123,
		"model": "cline-free/upstream-model",
		"choices": [{
			"index": 0,
			"message": {
				"role": "assistant",
				"content": "hello from cline",
				"tool_calls": [{"id": "call_1", "type": "function", "function": {"name": "read_file", "arguments": "{\"path\":\"a.txt\"}"}}]
			},
			"finish_reason": "stop"
		}],
		"usage": {"prompt_tokens": 10, "completion_tokens": 2, "total_tokens": 12}
	}
}`

const responsesChatStreamFixture = `data: {"success":true,"data":{"id":"chatcmpl-1","object":"chat.completion.chunk","created":123,"model":"cline-free/upstream-model","choices":[{"index":0,"delta":{"role":"assistant"}}]}}

data: {"success":true,"data":{"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":"hello "}}]}}

data: {"success":true,"data":{"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":"world"}}]}}

data: {"success":true,"data":{"id":"chatcmpl-1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{\"path\":"}}]}}]}}

data: {"success":true,"data":{"id":"chatcmpl-1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"a.txt\"}"}}]},"finish_reason":"tool_calls"}]}}

data: {"success":true,"data":{"id":"chatcmpl-1","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}}

data: [DONE]

`

func newResponsesRequest(requestedModel string) *dto.OpenAIResponsesRequest {
	return &dto.OpenAIResponsesRequest{
		Model: requestedModel,
		Input: []byte(`"hello"`),
	}
}

func newResponsesStreamRequest(requestedModel string) *dto.OpenAIResponsesRequest {
	stream := true
	request := newResponsesRequest(requestedModel)
	request.Stream = &stream
	return request
}

func TestClineResponsesRequestConversionStringInput(t *testing.T) {
	env := newClineTestEnv(t, types.RelayFormatOpenAIResponses, relayconstant.RelayModeResponses, false, "cline-free/deepseek-v4.1-flash", "http://127.0.0.1:1")
	request := newResponsesRequest(env.info.OriginModelName)
	request.Instructions = []byte(`"be brief"`)
	maxOutput := uint(128)
	request.MaxOutputTokens = &maxOutput

	converted, err := env.adaptor.ConvertOpenAIResponsesRequest(env.context, env.info, *request)
	require.NoError(t, err)
	body, err := common.Marshal(converted)
	require.NoError(t, err)

	assert.Equal(t, env.info.OriginModelName, gjson.GetBytes(body, "model").String())
	assert.Equal(t, "system", gjson.GetBytes(body, "messages.0.role").String())
	assert.Equal(t, "be brief", gjson.GetBytes(body, "messages.0.content").String())
	assert.Equal(t, "user", gjson.GetBytes(body, "messages.1.role").String())
	assert.Equal(t, "hello", gjson.GetBytes(body, "messages.1.content").String())
	assert.Equal(t, int64(128), gjson.GetBytes(body, "max_completion_tokens").Int())
	assert.True(t, env.info.SupportStreamOptions)
	require.Equal(t, []types.RelayFormat{types.RelayFormatOpenAIResponses, types.RelayFormatOpenAI}, env.info.RequestConversionChain)
}

func TestClineResponsesRequestConversionMessageArrayWithTools(t *testing.T) {
	env := newClineTestEnv(t, types.RelayFormatOpenAIResponses, relayconstant.RelayModeResponses, false, "cline-pass/kimi-k3", "http://127.0.0.1:1")
	request := &dto.OpenAIResponsesRequest{
		Model: env.info.OriginModelName,
		Input: []byte(`[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"read the file"}]},
			{"type":"function_call","call_id":"call_1","name":"read_file","arguments":"{\"path\":\"a.txt\"}"},
			{"type":"function_call_output","call_id":"call_1","output":"file contents"}
		]`),
		Tools: []byte(`[{"type":"function","name":"read_file","description":"Read a file","parameters":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}}]`),
	}

	converted, err := env.adaptor.ConvertOpenAIResponsesRequest(env.context, env.info, *request)
	require.NoError(t, err)
	body, err := common.Marshal(converted)
	require.NoError(t, err)

	assert.Equal(t, "read the file", gjson.GetBytes(body, "messages.0.content").String())
	assert.Equal(t, "assistant", gjson.GetBytes(body, "messages.1.role").String())
	assert.Equal(t, "read_file", gjson.GetBytes(body, "messages.1.tool_calls.0.function.name").String())
	assert.Equal(t, "call_1", gjson.GetBytes(body, "messages.1.tool_calls.0.id").String())
	assert.Equal(t, `{"path":"a.txt"}`, gjson.GetBytes(body, "messages.1.tool_calls.0.function.arguments").String())
	assert.Equal(t, "tool", gjson.GetBytes(body, "messages.2.role").String())
	assert.Equal(t, "call_1", gjson.GetBytes(body, "messages.2.tool_call_id").String())
	assert.Equal(t, "read_file", gjson.GetBytes(body, "tools.0.function.name").String())
	assert.Equal(t, "Read a file", gjson.GetBytes(body, "tools.0.function.description").String())
}

func TestClineResponsesRequestMaxOutputTokensExplicitZero(t *testing.T) {
	env := newClineTestEnv(t, types.RelayFormatOpenAIResponses, relayconstant.RelayModeResponses, false, "cline-free/deepseek-v4.1-flash", "http://127.0.0.1:1")
	request := newResponsesRequest(env.info.OriginModelName)
	zero := uint(0)
	request.MaxOutputTokens = &zero

	converted, err := env.adaptor.ConvertOpenAIResponsesRequest(env.context, env.info, *request)
	require.NoError(t, err)
	body, err := common.Marshal(converted)
	require.NoError(t, err)
	assert.True(t, gjson.GetBytes(body, "max_completion_tokens").Exists(), "explicit zero must survive conversion")
	assert.Equal(t, int64(0), gjson.GetBytes(body, "max_completion_tokens").Int())
}

func TestClineResponsesRequestMaxOutputTokensBounds(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		valid bool
	}{
		{name: "within bound", value: "4096", valid: true},
		{name: "zero", value: "0", valid: true},
		{name: "above int32 half", value: "4294967295"},
		{name: "wrapped negative uint", value: "18446744073686646784"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			body := `{"model":"cline-free/deepseek-v4.1-flash","input":"hello","max_output_tokens":` + tc.value + `}`
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")

			request, err := helper.GetAndValidateResponsesRequest(c)
			if tc.valid {
				require.NoError(t, err)
				require.NotNil(t, request)
				return
			}
			require.Error(t, err)
		})
	}
}

func TestClineResponsesJSONThroughAdaptor(t *testing.T) {
	upstream, received := newClineUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(responsesChatCompletionFixture))
	})
	env := newClineTestEnv(t, types.RelayFormatOpenAIResponses, relayconstant.RelayModeResponses, false, "cline-free/deepseek-v4.1-flash", upstream.URL+"/api/v1")
	request := newResponsesRequest(env.info.OriginModelName)
	env.mapModel(t, request, "cline-free/upstream-model")

	converted, err := env.adaptor.ConvertOpenAIResponsesRequest(env.context, env.info, *request)
	require.NoError(t, err)
	resp := env.send(t, converted)
	usage, apiErr := env.receive(t, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 12, usage.(*dto.Usage).TotalTokens)

	upstreamRequest := <-received
	assert.Equal(t, "/api/v1/chat/completions", upstreamRequest.path)
	assert.Equal(t, "cline-free/upstream-model", gjson.GetBytes(upstreamRequest.body, "model").String())

	body := env.recorder.Body.String()
	assert.Equal(t, "completed", gjson.Get(body, "status").String())
	assert.Equal(t, "cline-free/deepseek-v4.1-flash", gjson.Get(body, "model").String())
	assert.Equal(t, "hello from cline", gjson.Get(body, `output.#(type=="message").content.0.text`).String())
	assert.Equal(t, "function_call", gjson.Get(body, `output.#(type=="function_call").type`).String())
	assert.Equal(t, "read_file", gjson.Get(body, `output.#(type=="function_call").name`).String())
	assert.Equal(t, `{"path":"a.txt"}`, gjson.Get(body, `output.#(type=="function_call").arguments`).String())
	assert.Equal(t, int64(12), gjson.Get(body, "usage.total_tokens").Int())
}

func TestClineResponsesSSEThroughAdaptor(t *testing.T) {
	upstream, received := newClineUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(responsesChatStreamFixture))
	})
	env := newClineTestEnv(t, types.RelayFormatOpenAIResponses, relayconstant.RelayModeResponses, true, "cline-free/deepseek-v4.1-flash", upstream.URL+"/api/v1")
	request := newResponsesStreamRequest(env.info.OriginModelName)
	env.mapModel(t, request, "cline-free/upstream-model")

	converted, err := env.adaptor.ConvertOpenAIResponsesRequest(env.context, env.info, *request)
	require.NoError(t, err)
	resp := env.send(t, converted)
	usage, apiErr := env.receive(t, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 7, usage.(*dto.Usage).TotalTokens)

	upstreamRequest := <-received
	assert.True(t, gjson.GetBytes(upstreamRequest.body, "stream").Bool())
	assert.True(t, gjson.GetBytes(upstreamRequest.body, "stream_options.include_usage").Bool())

	body := env.recorder.Body.String()
	events := streamEventTypes(body)
	require.NotEmpty(t, events)
	assert.Equal(t, "response.created", events[0])
	assert.Equal(t, "response.completed", events[len(events)-1])
	assert.Equal(t, 1, countEvents(events, "response.completed"))
	assert.Equal(t, 0, countEvents(events, "response.failed"))

	payloads := dataPayloads(body)
	var text strings.Builder
	var arguments strings.Builder
	for _, payload := range payloads {
		if !gjson.Valid(payload) {
			continue
		}
		switch gjson.Get(payload, "type").String() {
		case "response.output_text.delta":
			text.WriteString(gjson.Get(payload, "delta").String())
		case "response.function_call_arguments.delta":
			arguments.WriteString(gjson.Get(payload, "delta").String())
		}
	}
	assert.Equal(t, "hello world", text.String())
	assert.Equal(t, `{"path":"a.txt"}`, arguments.String())

	completed := findEventPayload(payloads, "response.completed")
	require.NotEmpty(t, completed)
	assert.Equal(t, "completed", gjson.Get(completed, "response.status").String())
	assert.Equal(t, "cline-free/deepseek-v4.1-flash", gjson.Get(completed, "response.model").String())
	assert.Equal(t, int64(7), gjson.Get(completed, "response.usage.total_tokens").Int())
}

func TestClineResponsesLeadingStreamErrorReturnsBeforeOutput(t *testing.T) {
	upstream, _ := newClineUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"success\":false,\"error\":\"responses not enabled for free tier\"}\n\n"))
	})
	env := newClineTestEnv(t, types.RelayFormatOpenAIResponses, relayconstant.RelayModeResponses, true, "cline-free/deepseek-v4.1-flash", upstream.URL+"/api/v1")
	request := newResponsesStreamRequest(env.info.OriginModelName)
	converted, err := env.adaptor.ConvertOpenAIResponsesRequest(env.context, env.info, *request)
	require.NoError(t, err)
	resp := env.send(t, converted)
	usage, apiErr := env.receive(t, resp)
	require.Nil(t, usage)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusBadGateway, apiErr.StatusCode)
	assert.Contains(t, apiErr.Error(), "responses not enabled for free tier")
	assert.Empty(t, env.recorder.Body.String())
}

func TestClineResponsesMidStreamErrorEmitsFailedTerminalAndKeepsDiagnosticsInternal(t *testing.T) {
	stream := "data: " + `{"success":true,"data":{"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":"partial"}}]}}` + "\n\n" +
		"data: " + `{"success":false,"error":{"message":"internal account id 42 rejected"}}` + "\n\n"

	upstream, _ := newClineUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(stream))
	})
	env := newClineTestEnv(t, types.RelayFormatOpenAIResponses, relayconstant.RelayModeResponses, true, "cline-free/deepseek-v4.1-flash", upstream.URL+"/api/v1")
	request := newResponsesStreamRequest(env.info.OriginModelName)
	converted, err := env.adaptor.ConvertOpenAIResponsesRequest(env.context, env.info, *request)
	require.NoError(t, err)
	resp := env.send(t, converted)
	_, apiErr := env.receive(t, resp)
	require.Nil(t, apiErr)

	body := env.recorder.Body.String()
	events := streamEventTypes(body)
	require.Contains(t, events, "response.failed")
	assert.Equal(t, 0, countEvents(events, "response.completed"), "a partial error must not become a fake completion")
	assert.Equal(t, 1, countEvents(events, "response.failed"))
	assert.Contains(t, body, "upstream request failed")
	assert.NotContains(t, body, "internal account id 42 rejected")
	require.NotNil(t, env.info.StreamStatus)
	assert.True(t, env.info.StreamStatus.ResponseFailed())
	require.NotEmpty(t, env.info.StreamStatus.Errors)
	assert.Contains(t, env.info.StreamStatus.Errors[0].Message, "internal account id 42 rejected",
		"upstream diagnostics stay available internally and in logs")
}

func TestClineResponsesMidStreamErrorKeepsDiagnosticsForChannelTest(t *testing.T) {
	stream := "data: " + `{"success":true,"data":{"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":"partial"}}]}}` + "\n\n" +
		"data: " + `{"success":false,"error":{"message":"internal account id 42 rejected"}}` + "\n\n"

	upstream, _ := newClineUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(stream))
	})
	env := newClineTestEnv(t, types.RelayFormatOpenAIResponses, relayconstant.RelayModeResponses, true, "cline-free/deepseek-v4.1-flash", upstream.URL+"/api/v1")
	common.SetContextKey(env.context, constant.ContextKeyChannelTest, true)
	request := newResponsesStreamRequest(env.info.OriginModelName)
	converted, err := env.adaptor.ConvertOpenAIResponsesRequest(env.context, env.info, *request)
	require.NoError(t, err)
	resp := env.send(t, converted)
	_, apiErr := env.receive(t, resp)
	require.Nil(t, apiErr)

	body := env.recorder.Body.String()
	assert.Contains(t, body, "response.failed")
	assert.Contains(t, body, "internal account id 42 rejected",
		"the administrator channel test keeps upstream diagnostics")
}

func TestClineResponsesCompactIsRejectedBeforeUpstream(t *testing.T) {
	upstream, received := newClineUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	env := newClineTestEnv(t, types.RelayFormatOpenAIResponsesCompaction, relayconstant.RelayModeResponsesCompact, false, "cline-free/deepseek-v4.1-flash", upstream.URL+"/api/v1")
	request := newResponsesRequest(env.info.OriginModelName)
	body, err := common.Marshal(request)
	require.NoError(t, err)
	_, requestErr := env.adaptor.DoRequest(env.context, env.info, strings.NewReader(string(body)))
	require.Error(t, requestErr)
	apiErr, ok := requestErr.(*types.NewAPIError)
	require.True(t, ok)
	assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	assert.Equal(t, types.ErrorCodeInvalidApiType, apiErr.GetErrorCode())
	assert.True(t, types.IsSkipRetryError(apiErr))
	assert.Empty(t, received, "unsupported compact requests must never reach a Cline endpoint")
}

func countEvents(events []string, target string) int {
	count := 0
	for _, event := range events {
		if event == target {
			count++
		}
	}
	return count
}

func findEventPayload(payloads []string, eventType string) string {
	for _, payload := range payloads {
		if gjson.Valid(payload) && gjson.Get(payload, "type").String() == eventType {
			return payload
		}
	}
	return ""
}
