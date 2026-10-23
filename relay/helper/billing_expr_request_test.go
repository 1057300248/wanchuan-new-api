package helper

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestResolveIncomingBillingExprRequestInput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("Content-Type", "application/json")

	body := []byte(`{"service_tier":"fast"}`)
	ctx.Request.Body = io.NopCloser(bytes.NewReader(body))
	ctx.Set(common.KeyRequestBody, body)

	info := &relaycommon.RelayInfo{
		RequestHeaders: map[string]string{"Content-Type": "application/json"},
	}

	input, err := ResolveIncomingBillingExprRequestInput(ctx, info)
	require.NoError(t, err)
	require.Equal(t, body, input.Body)
	require.Equal(t, "application/json", input.Headers["Content-Type"])
}

func TestBuildBillingExprRequestInputFromRequest(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{
		Model:  "gemini-3.1-pro-preview",
		Stream: lo.ToPtr(true),
		Messages: []dto.Message{
			{
				Role:    "user",
				Content: "hi",
			},
		},
		MaxTokens: lo.ToPtr(uint(3000)),
	}

	input, err := BuildBillingExprRequestInputFromRequest(request, map[string]string{
		"Content-Type": "application/json",
		"X-Test":       "1",
	})
	require.NoError(t, err)
	require.Equal(t, "application/json", input.Headers["Content-Type"])
	require.Equal(t, "1", input.Headers["X-Test"])
	require.True(t, gjson.GetBytes(input.Body, "stream").Bool())
	require.Equal(t, "user", gjson.GetBytes(input.Body, "messages.0.role").String())
	require.Equal(t, float64(3000), gjson.GetBytes(input.Body, "max_tokens").Float())
}

func TestResolveBillingRequestSnapshotKeepsOriginalInputs(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("X-Plan", "http")
	ctx.Request.Header.Set("Authorization", "Bearer never-persist")
	ctx.Set(common.KeyRequestBody, []byte(`{"quality":"hd","prompt":"never-persist"}`))
	started := time.Date(2026, time.October, 1, 3, 0, 0, 0, time.UTC)
	info := &relaycommon.RelayInfo{
		StartTime:      started,
		RequestHeaders: map[string]string{"X-Plan": "fast"},
	}
	input, err := ResolveIncomingBillingExprRequestInput(ctx, info)
	require.NoError(t, err)
	require.Equal(t, started.Unix(), input.Timestamp)
	require.Equal(t, "fast", input.Headers["X-Plan"])

	const expression = `tier("base", p) * (header("x-plan") == "fast" ? 2 : 1) * (param("quality") == "hd" ? 3 : 1) * (hour("UTC") == 3 ? 5 : 1)`
	frozen, err := billingexpr.FreezeRequestInput(expression, input)
	require.NoError(t, err)
	encoded, err := common.Marshal(frozen)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "never-persist")
	require.NotContains(t, string(encoded), "authorization")
	require.Empty(t, frozen.Body)
	require.Empty(t, frozen.Usage)
	var restored billingexpr.RequestInput
	require.NoError(t, common.Unmarshal(encoded, &restored))
	cost, _, err := billingexpr.RunExprWithRequest(expression, billingexpr.TokenParams{P: 1}, restored)
	require.NoError(t, err)
	require.Equal(t, float64(30), cost)

	// A retry preserves the prior exact-key override and original timestamp.
	info.StartTime = started.Add(10 * time.Hour)
	info.BillingRequestInput = &input
	ctx.Request.Header.Set("X-Plan", "changed-http")
	info.RequestHeaders["X-Plan"] = "changed-relay"
	retry, err := ResolveIncomingBillingExprRequestInput(ctx, info)
	require.NoError(t, err)
	require.Equal(t, started.Unix(), retry.Timestamp)
	require.Equal(t, "fast", retry.Headers["X-Plan"])
	retry.Headers["X-Plan"] = "mutated-copy"
	require.Equal(t, "fast", input.Headers["X-Plan"])
}
