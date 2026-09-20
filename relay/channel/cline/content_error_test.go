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

func TestClineContentErrorPreservesStructuredFieldsFromErrorArray(t *testing.T) {
	_, apiErr := normalizeClineResponseBody([]byte(`{"success":false,"errors":[{"message":"Invalid input","type":"invalid_request_error","code":"invalid_content","status":400}]}`))
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	openAIError := apiErr.ToOpenAIError()
	assert.Equal(t, "Invalid input", openAIError.Message)
	assert.Equal(t, "invalid_request_error", openAIError.Type)
	assert.Equal(t, "invalid_content", openAIError.Code)
}
