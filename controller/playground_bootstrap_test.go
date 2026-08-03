package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type playgroundBootstrapData struct {
	Id                   int     `json:"id"`
	Username             string  `json:"username"`
	DisplayName          string  `json:"display_name"`
	Quota                int     `json:"quota"`
	QuotaPerUnit         float64 `json:"quota_per_unit"`
	QuotaDisplayType     string  `json:"quota_display_type"`
	CurrencySymbol       string  `json:"currency_symbol"`
	CurrencyExchangeRate float64 `json:"currency_exchange_rate"`
	Groups               []struct {
		Name  string  `json:"name"`
		Desc  string  `json:"desc"`
		Ratio float64 `json:"ratio"`
	} `json:"groups"`
	Models        []string            `json:"models"`
	ModelsByGroup map[string][]string `json:"models_by_group"`
}

func setupPlaygroundBootstrapTestDB(t *testing.T) int {
	t.Helper()
	db := setupModelListControllerTestDB(t)
	user := model.User{
		Id:          2201,
		Username:    "bootstrap-user",
		Password:    "password123",
		DisplayName: "Bootstrap User",
		Email:       "bootstrap-secret@example.com",
		Group:       "default",
		Status:      common.UserStatusEnabled,
		Quota:       8888,
	}
	require.NoError(t, db.Create(&user).Error)
	priority := int64(0)
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "default", Model: "zz-b-model", ChannelId: 1, Enabled: true, Priority: &priority},
		{Group: "default", Model: "zz-a-model", ChannelId: 1, Enabled: true, Priority: &priority},
		{Group: "vip", Model: "zz-b-model", ChannelId: 2, Enabled: true, Priority: &priority},
		{Group: "vip", Model: "zz-a-model", ChannelId: 2, Enabled: true, Priority: &priority},
		{Group: "vip", Model: "zz-c-model", ChannelId: 2, Enabled: false, Priority: &priority},
	}).Error)
	return user.Id
}

func callPlaygroundBootstrap(t *testing.T, userId int) (*httptest.ResponseRecorder, []byte) {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("id", userId)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/user/playground/bootstrap", nil)

	PlaygroundBootstrap(ctx)

	return recorder, recorder.Body.Bytes()
}

func TestPlaygroundBootstrapReturnsMinimalSortedFields(t *testing.T) {
	userId := setupPlaygroundBootstrapTestDB(t)
	recorder, raw := callPlaygroundBootstrap(t, userId)

	require.Equal(t, http.StatusOK, recorder.Code)
	var payload struct {
		Success bool                    `json:"success"`
		Data    playgroundBootstrapData `json:"data"`
	}
	require.NoError(t, common.Unmarshal(raw, &payload))
	require.True(t, payload.Success)

	assert.Equal(t, 2201, payload.Data.Id)
	assert.Equal(t, "bootstrap-user", payload.Data.Username)
	assert.Equal(t, "Bootstrap User", payload.Data.DisplayName)
	assert.Equal(t, 8888, payload.Data.Quota)
	assert.Equal(t, common.QuotaPerUnit, payload.Data.QuotaPerUnit)
	assert.NotEmpty(t, payload.Data.QuotaDisplayType)
	assert.Greater(t, payload.Data.CurrencyExchangeRate, 0.0)

	// 可用分组按名称排序，且带 ratio/desc
	require.Len(t, payload.Data.Groups, 2)
	assert.Equal(t, "default", payload.Data.Groups[0].Name)
	assert.Equal(t, 1.0, payload.Data.Groups[0].Ratio)
	assert.NotEmpty(t, payload.Data.Groups[0].Desc)
	assert.Equal(t, "vip", payload.Data.Groups[1].Name)
	assert.Equal(t, 1.0, payload.Data.Groups[1].Ratio)
	assert.NotEmpty(t, payload.Data.Groups[1].Desc)

	// 模型跨分组去重、排除禁用模型、并按名称排序
	assert.Equal(t, []string{"zz-a-model", "zz-b-model"}, payload.Data.Models)
	assert.Equal(t, []string{"zz-a-model", "zz-b-model"}, payload.Data.ModelsByGroup["default"])
	assert.Equal(t, []string{"zz-a-model", "zz-b-model"}, payload.Data.ModelsByGroup["vip"])
}

func TestPlaygroundBootstrapDoesNotLeakSensitiveFields(t *testing.T) {
	userId := setupPlaygroundBootstrapTestDB(t)
	_, raw := callPlaygroundBootstrap(t, userId)

	body := string(raw)
	for _, sensitive := range []string{"bootstrap-secret@example.com", "password123", "access_token", `"email"`, `"role"`, `"status"`, `"setting"`, `"stripe_customer"`} {
		assert.NotContains(t, body, sensitive, "bootstrap 响应不应包含敏感字段 %s", sensitive)
	}
	assert.Contains(t, body, "display_name")
	assert.Contains(t, body, "quota")
}

func TestPlaygroundBootstrapFailsForMissingUser(t *testing.T) {
	setupModelListControllerTestDB(t)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("id", 999999)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/user/playground/bootstrap", nil)

	PlaygroundBootstrap(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	var payload struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	assert.False(t, payload.Success)
	assert.NotEmpty(t, payload.Message)
}

// TestPlaygroundChatNewPathEnforcesQuotaGate 验证独立官网 chat 路由（
// /api/user/playground/chat）复用 controller.Playground 的余额门槛：
// quota <= 0 时返回 403 insufficient_user_quota，且能按新路径识别为
// Chat Completions 中继模式。
func TestPlaygroundChatNewPathEnforcesQuotaGate(t *testing.T) {
	userId := setupPlaygroundControllerTestDB(t, 0)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("id", userId)
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/user/playground/chat",
		strings.NewReader(`{"model":"zz-playground-test-model","messages":[{"role":"user","content":"hi"}]}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	Playground(ctx)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	payload := decodePlaygroundErrorPayload(t, recorder)
	assert.Equal(t, types.ErrorCodeInsufficientUserQuota, payload.Error.Code)
}
