package cline

import (
	"bufio"
	"bytes"
	"io"
	"net/http"
	"strings"
	"sync"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/types"
)

const (
	maxClineStreamPeekBytes = 256 << 10
	maxClineStreamPeekLines = 64
)

func prepareClineStreamResponse(resp *http.Response, info *relaycommon.RelayInfo) *types.NewAPIError {
	original := resp.Body
	reader := bufio.NewReader(original)
	prefix, apiErr, readErr := peekClineStreamEvents(reader)
	if apiErr != nil {
		_ = original.Close()
		return apiErr
	}
	if readErr != nil {
		_ = original.Close()
		return types.NewOpenAIError(readErr, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	transformed := newClineStreamReader(io.MultiReader(bytes.NewReader(prefix), reader), info)
	resp.Body = &clineStreamBody{reader: transformed, closer: original}
	return nil
}

// peekClineStreamEvents inspects the first data event before any output is
// written so a leading error envelope is reported as a normal API error instead
// of a truncated stream. The consumed bytes are replayed to the stream
// transformer, and an error after the first event is handled in-band by the
// transformer so partial output is never discarded or duplicated.
func peekClineStreamEvents(reader *bufio.Reader) ([]byte, *types.NewAPIError, error) {
	var prefix bytes.Buffer
	var eventType string
	lines := 0
	for lines < maxClineStreamPeekLines && prefix.Len() < maxClineStreamPeekBytes {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			lines++
			prefix.WriteString(line)
			trimmed := strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(trimmed, "event:"):
				eventType = strings.TrimSpace(strings.TrimPrefix(trimmed, "event:"))
			case strings.HasPrefix(trimmed, "data:"):
				payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
				if payload != "" {
					if payload == "[DONE]" {
						return prefix.Bytes(), nil, nil
					}
					if _, apiErr := normalizeClineStreamPayload(eventType, payload); apiErr != nil {
						return prefix.Bytes(), apiErr, nil
					}
					return prefix.Bytes(), nil, nil
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				return prefix.Bytes(), nil, nil
			}
			return prefix.Bytes(), nil, err
		}
	}
	return prefix.Bytes(), nil, nil
}

type clineStreamBody struct {
	reader io.Reader
	closer io.Closer
	once   sync.Once
}

func (b *clineStreamBody) Read(p []byte) (int, error) {
	return b.reader.Read(p)
}

func (b *clineStreamBody) Close() error {
	var err error
	b.once.Do(func() {
		err = b.closer.Close()
	})
	return err
}

// clineStreamReader rewrites Cline SSE events into plain OpenAI chunks:
// success/data envelopes are unwrapped, reasoning fields are normalized, and
// error events become a single client-safe error frame before the stream stops.
type clineStreamReader struct {
	scanner   *bufio.Scanner
	output    bytes.Buffer
	info      *relaycommon.RelayInfo
	eventType string
	err       error
}

func newClineStreamReader(reader io.Reader, info *relaycommon.RelayInfo) *clineStreamReader {
	return &clineStreamReader{scanner: helper.NewStreamScanner(reader), info: info}
}

func (r *clineStreamReader) Read(p []byte) (int, error) {
	for r.output.Len() == 0 {
		if r.err != nil {
			return 0, r.err
		}
		if !r.scanner.Scan() {
			if err := r.scanner.Err(); err != nil {
				r.err = err
			} else {
				r.err = io.EOF
			}
			continue
		}
		r.processLine(r.scanner.Text())
	}
	return r.output.Read(p)
}

func (r *clineStreamReader) processLine(line string) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, ":") {
		return
	}
	if event, ok := strings.CutPrefix(trimmed, "event:"); ok {
		r.eventType = strings.TrimSpace(event)
		return
	}
	payload, ok := strings.CutPrefix(trimmed, "data:")
	if !ok {
		return
	}
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return
	}
	if payload == "[DONE]" {
		r.emit("data: [DONE]\n")
		return
	}
	eventType := r.eventType
	r.eventType = ""
	normalized, apiErr := normalizeClineStreamPayload(eventType, payload)
	if apiErr != nil {
		r.recordFailure(apiErr)
		// Only the Responses bridge understands an in-band chat error frame;
		// the chat/Claude/Gemini paths re-marshal typed chunks and would drop
		// it into an empty chunk, so there the failed stream simply stops and
		// the failure stays recorded in StreamStatus.
		if r.info != nil && r.info.RelayFormat == types.RelayFormatOpenAIResponses {
			r.emit(clineErrorFrame(apiErr))
		}
		r.err = io.EOF
		return
	}
	if len(normalized) == 0 {
		return
	}
	r.emit("data: " + string(normalized) + "\n")
}

func (r *clineStreamReader) emit(frame string) {
	if frame == "" {
		return
	}
	r.output.WriteString(frame)
}

func (r *clineStreamReader) recordFailure(apiErr *types.NewAPIError) {
	if r.info == nil || r.info.StreamStatus == nil || apiErr == nil {
		return
	}
	status := r.info.StreamStatus
	status.RecordError(apiErr.Error())
	openAIError := apiErr.ToOpenAIError()
	status.MarkFailed(string(apiErr.GetErrorCode()), openAIError.Type, apiErr.StatusCode)
}
