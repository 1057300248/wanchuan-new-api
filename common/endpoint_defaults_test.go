package common

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAIVideoDefaultEndpoint(t *testing.T) {
	endpoint, ok := GetDefaultEndpointInfo(constant.EndpointTypeOpenAIVideo)
	require.True(t, ok)
	assert.Equal(t, "/v1/videos", endpoint.Path)
	assert.Equal(t, "POST", endpoint.Method)
}
