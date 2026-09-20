package cline

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestClineContentErrorPreservesDeclaredStatusAndParam(t *testing.T) {
	for _, payload := range []string{
		`{"error":{"message":"Invalid input","type":"invalid_request_error","param":"content","status":400}}`,
		`{"success":false,"status":400,"error":{"message":"Invalid input","type":"invalid_request_error","param":"content"}}`,
		`{"success":false,"error":{"message":"Failed to create stream","code":"stream_initialization_failed","details":{"status":400,"error":{"message":"Invalid input","type":"invalid_request_error","param":"content"}}}}`,
	} {
		t.Run(payload, func(t *testing.T) {
			_, apiErr := normalizeClineStreamPayload("error", payload)
			require.NotNil(t, apiErr)
			assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
			assert.Equal(t, "Invalid input", apiErr.ToOpenAIError().Message)
			assert.Equal(t, "content", apiErr.ToOpenAIError().Param)
			frame := clineErrorFrame(apiErr)
			assert.Equal(t, "content", gjson.Get(frame[len("data: "):], "error.param").Str)
		})
	}
}

func TestClineContentErrorDoesNotInventAClientStatus(t *testing.T) {
	_, apiErr := normalizeClineStreamPayload("error", `{"error":{"message":"Failed to create stream","code":"stream_initialization_failed"}}`)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusBadGateway, apiErr.StatusCode)
}
