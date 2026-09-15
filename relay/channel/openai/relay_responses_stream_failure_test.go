package openai

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func responsesStreamFixture(t *testing.T, reader io.Reader) (*gin.Context, *httptest.ResponseRecorder, *relaycommon.RelayInfo, *http.Response) {
	t.Helper()
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Set(common.RequestIdKey, "responses-stream-regression")
	info := &relaycommon.RelayInfo{
		IsStream: true, DisablePing: true, StartTime: time.Now(),
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "grok-4.6"},
	}
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(reader), Header: http.Header{"Content-Type": {"text/event-stream"}}}
	return c, w, info, resp
}

func responsesStreamEvents(t *testing.T, body string) []dto.ResponsesStreamResponse {
	t.Helper()
	var events []dto.ResponsesStreamResponse
	for _, frame := range strings.Split(strings.TrimSuffix(body, "\n\n"), "\n\n") {
		lines := strings.Split(frame, "\n")
		require.Len(t, lines, 2, "all output must be framed SSE, not a JSON HTTP error: %q", frame)
		require.True(t, strings.HasPrefix(lines[1], "data: "))
		var event dto.ResponsesStreamResponse
		require.NoError(t, common.UnmarshalJsonStr(strings.TrimPrefix(lines[1], "data: "), &event))
		assert.Equal(t, "event: "+event.Type, lines[0])
		events = append(events, event)
	}
	return events
}

func TestOaiResponsesStreamHandlerReportsUpstreamFailure(t *testing.T) {
	cases := []struct {
		name, event, message, code string
		wantEvents                 int
	}{
		{"grok model error with null code", `{"code":null,"message":"Internal server error from model.","param":null,"type":"error"}`, "Internal server error from model.", "server_error", 2},
		{"provider error code", `{"type":"error","code":"rate_limit_exceeded","message":"Slow down"}`, "Slow down", "rate_limit_exceeded", 2},
		{"native failed without error type", `{"type":"response.failed","response":{"id":"resp_failed","status":"failed","error":{"code":"server_error","message":"Generation failed"}}}`, "Generation failed", "server_error", 1},
		{"cancelled response", `{"type":"response.cancelled","response":{"status":"cancelled"}}`, "upstream response stream failed: response.cancelled", "server_error", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, w, info, resp := responsesStreamFixture(t, strings.NewReader("data: "+tc.event+"\n\n"))
			usage, apiErr := OaiResponsesStreamHandler(c, info, resp)
			require.NotNil(t, apiErr)
			assert.Equal(t, tc.message, apiErr.Error())
			assert.Equal(t, types.ErrorCode(tc.code), apiErr.GetErrorCode())
			assert.True(t, types.IsSkipRetryError(apiErr))
			assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyResponsesStreamErrorHandled))
			require.NotNil(t, info.StreamStatus)
			assert.True(t, info.StreamStatus.HasErrors())
			assert.Equal(t, 0, usage.TotalTokens, "error-only responses must not fabricate billable usage")
			assert.Equal(t, 1, info.ReceivedResponseCount)
			assert.NotContains(t, w.Body.String(), "event: response.completed")
			events := responsesStreamEvents(t, w.Body.String())
			require.Len(t, events, tc.wantEvents)
			assert.Contains(t, w.Body.String(), "data: "+tc.event+"\n\n", "preserve the original error payload")
			if tc.wantEvents == 2 {
				last := events[1]
				assert.Equal(t, "response.failed", last.Type)
				require.NotNil(t, last.Response)
				assert.JSONEq(t, `"failed"`, string(last.Response.Status))
				assert.Equal(t, tc.message, last.Response.GetOpenAIError().Message)
			}
		})
	}
}

type responsesReadError struct{ err error }

func (r responsesReadError) Read([]byte) (int, error) { return 0, r.err }

func TestOaiResponsesStreamHandlerRejectsMissingTerminal(t *testing.T) {
	created := "data: {\"type\":\"response.created\",\"sequence_number\":7,\"response\":{\"id\":\"resp_original\",\"created_at\":123,\"model\":\"grok-4.6\",\"status\":\"in_progress\"}}\n\n"
	partialTool := "data: {\"type\":\"response.function_call_arguments.delta\",\"sequence_number\":8,\"delta\":\"{\\\"path\\\":\"}\n\n"
	cases := []struct {
		name string
		tail io.Reader
	}{
		{"EOF after a partial tool argument", strings.NewReader("")},
		{"DONE without a Responses terminal", strings.NewReader("data: [DONE]\n\n")},
		{"malformed terminal JSON", strings.NewReader("data: {\"type\":\"response.completed\"\n\n")},
		{"socket read error", responsesReadError{io.ErrUnexpectedEOF}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, w, info, resp := responsesStreamFixture(t, io.MultiReader(strings.NewReader(created+partialTool), tc.tail))
			_, apiErr := OaiResponsesStreamHandler(c, info, resp)
			require.NotNil(t, apiErr)
			assert.True(t, info.StreamStatus.HasErrors())
			assert.True(t, types.IsSkipRetryError(apiErr))
			assert.NotContains(t, w.Body.String(), "event: response.completed")
			assert.NotContains(t, w.Body.String(), "event: response.function_call_arguments.done")
			events := responsesStreamEvents(t, w.Body.String())
			require.Len(t, events, 3)
			last := events[2]
			assert.Equal(t, "response.failed", last.Type)
			require.NotNil(t, last.SequenceNumber)
			assert.Equal(t, 9, *last.SequenceNumber)
			require.NotNil(t, last.Response)
			assert.Equal(t, "resp_original", last.Response.ID)
			assert.Equal(t, dto.IntValue(123), last.Response.CreatedAt)
			assert.Equal(t, "server_error", last.Response.GetOpenAIError().Code)
			assert.Empty(t, last.Response.Output, "do not mark partial tool arguments as a complete output")
		})
	}
}

func TestOaiResponsesStreamHandlerStopsAtTerminalWithoutWaitingForEOF(t *testing.T) {
	for _, kind := range []string{"response.completed", "response.incomplete"} {
		t.Run(kind, func(t *testing.T) {
			pr, pw := io.Pipe()
			t.Cleanup(func() { _ = pr.Close(); _ = pw.Close() })
			c, w, info, resp := responsesStreamFixture(t, pr)
			resp.Body = pr
			status := strings.TrimPrefix(kind, "response.")
			body := `data: {"type":"` + kind + `","response":{"status":"` + status + `","usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}}` + "\n\n"
			writeDone := make(chan error, 1)
			go func() { _, err := io.WriteString(pw, body); writeDone <- err }()
			usage, apiErr := OaiResponsesStreamHandler(c, info, resp)
			require.Nil(t, apiErr)
			require.NoError(t, <-writeDone)
			assert.Equal(t, 5, usage.TotalTokens)
			assert.Equal(t, 3, usage.PromptTokens)
			assert.Equal(t, 2, usage.CompletionTokens)
			assert.False(t, info.StreamStatus.HasErrors())
			events := responsesStreamEvents(t, w.Body.String())
			require.Len(t, events, 1)
			assert.Equal(t, kind, events[0].Type)
		})
	}
}

func TestOaiResponsesStreamHandlerTimeoutSendsFailure(t *testing.T) {
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pr.Close(); _ = pw.Close() })
	c, w, info, resp := responsesStreamFixture(t, pr)
	constant.StreamingTimeout = 1
	resp.Body = pr
	_, apiErr := OaiResponsesStreamHandler(c, info, resp)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusGatewayTimeout, apiErr.StatusCode)
	assert.Equal(t, relaycommon.StreamEndReasonTimeout, info.StreamStatus.EndReason)
	events := responsesStreamEvents(t, w.Body.String())
	require.Len(t, events, 1)
	assert.Equal(t, "response.failed", events[0].Type)
}

func TestOaiResponsesStreamHandlerDoesNotWriteAfterClientCancellation(t *testing.T) {
	c, w, info, resp := responsesStreamFixture(t, strings.NewReader(""))
	ctx, cancel := context.WithCancel(c.Request.Context())
	c.Request = c.Request.WithContext(ctx)
	cancel()
	_, apiErr := OaiResponsesStreamHandler(c, info, resp)
	require.NotNil(t, apiErr)
	assert.ErrorIs(t, apiErr, context.Canceled)
	assert.Empty(t, w.Body.String())
	assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyResponsesStreamErrorHandled))
}

type responsesBrokenWriter struct {
	header http.Header
	writes int
	err    error
}

func (w *responsesBrokenWriter) Header() http.Header { return w.header }
func (w *responsesBrokenWriter) WriteHeader(int)     {}
func (w *responsesBrokenWriter) Flush()              {}
func (w *responsesBrokenWriter) Write([]byte) (int, error) {
	w.writes++
	return 0, w.err
}

func TestOaiResponsesStreamHandlerPropagatesClientWriteFailure(t *testing.T) {
	for _, writeErr := range []error{errors.New("client write failed"), nil} {
		c, _, info, resp := responsesStreamFixture(t, strings.NewReader("data: {\"type\":\"response.completed\"}\n\n"))
		broken := &responsesBrokenWriter{header: make(http.Header), err: writeErr}
		writerContext, _ := gin.CreateTestContext(broken)
		c.Writer = writerContext.Writer
		_, apiErr := OaiResponsesStreamHandler(c, info, resp)
		require.NotNil(t, apiErr)
		if writeErr == nil {
			assert.ErrorIs(t, apiErr, io.ErrShortWrite)
		} else {
			assert.ErrorIs(t, apiErr, writeErr)
		}
		assert.Equal(t, 1, broken.writes, "do not try to append failure events to an already broken writer")
		assert.True(t, info.StreamStatus.HasErrors())
	}
}

type responsesFlushErrorWriter struct {
	*httptest.ResponseRecorder
	err error
}

func (w *responsesFlushErrorWriter) FlushError() error { return w.err }

func TestOaiResponsesStreamHandlerPropagatesClientFlushFailure(t *testing.T) {
	c, _, info, resp := responsesStreamFixture(t, strings.NewReader("data: {\"type\":\"response.completed\"}\n\n"))
	broken := &responsesFlushErrorWriter{ResponseRecorder: httptest.NewRecorder(), err: errors.New("socket flush failed")}
	writerContext, _ := gin.CreateTestContext(broken)
	c.Writer = writerContext.Writer
	_, apiErr := OaiResponsesStreamHandler(c, info, resp)
	require.NotNil(t, apiErr)
	assert.ErrorIs(t, apiErr, broken.err)
	assert.True(t, info.StreamStatus.HasErrors())
	assert.NotContains(t, broken.Body.String(), "event: response.failed", "the socket already failed while flushing")
}
