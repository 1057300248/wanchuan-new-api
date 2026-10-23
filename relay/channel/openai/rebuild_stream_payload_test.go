package openai

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestRawStreamBoundaryPreservesUnknownFieldsAndNumericLexemes(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "public-model")
	payload := `{"id":"stream-1","model":"private-provider-model","vendor_extension":{"count":9007199254740993,"content":"unaltered"},"choices":[{"index":0,"delta":{"content":"hello"}}]}`
	require.NoError(t, sendStreamData(c, &relaycommon.RelayInfo{}, payload, false, false))
	body := strings.TrimSpace(strings.TrimPrefix(recorder.Body.String(), "data: "))
	require.Equal(t, "public-model", gjson.Get(body, "model").String())
	require.Equal(t, "9007199254740993", gjson.Get(body, "vendor_extension.count").Raw)
	require.Equal(t, "unaltered", gjson.Get(body, "vendor_extension.content").String())
	require.Equal(t, "hello", gjson.Get(body, "choices.0.delta.content").String())
}
