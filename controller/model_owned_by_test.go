package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelOwnerNameUsesAdaptorChannelName(t *testing.T) {
	tests := []struct {
		name        string
		channelType int
		expected    string
	}{
		{
			name:        "openai",
			channelType: constant.ChannelTypeOpenAI,
			expected:    "openai",
		},
		{
			name:        "codex",
			channelType: constant.ChannelTypeCodex,
			expected:    "codex",
		},
		{
			name:        "openrouter",
			channelType: constant.ChannelTypeOpenRouter,
			expected:    "openrouter",
		},
		{
			name:        "azure fallback",
			channelType: constant.ChannelTypeAzure,
			expected:    "azure",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, channelOwnerName(tt.channelType))
		})
	}
}

func TestBuildOpenAIModelUsesPublicOwner(t *testing.T) {
	modelItem := buildOpenAIModel("gpt-5.4", map[string]string{"gpt-5.4": "cline"})
	require.Equal(t, "gpt-5.4", modelItem.Id)
	assert.Equal(t, "chuanapi", modelItem.OwnedBy)
}

func TestBuildOpenAIModelUsesPublicOwnerForUnknownModels(t *testing.T) {
	modelItem := buildOpenAIModel("custom-test-model", nil)
	require.Equal(t, "custom-test-model", modelItem.Id)
	assert.Equal(t, "chuanapi", modelItem.OwnedBy)
}

func TestGetModelListGroupsUsesUserGroupWhenTokenGroupIsEmpty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")

	groups, err := getModelListGroups(ctx)
	require.NoError(t, err)

	require.Equal(t, "default", groups.userGroup)
	require.Empty(t, groups.tokenGroup)
	require.Equal(t, []string{"default"}, groups.ownerGroups)
}

func TestGetModelListGroupsUsesExplicitTokenGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(ctx, constant.ContextKeyTokenGroup, "vip")

	groups, err := getModelListGroups(ctx)
	require.NoError(t, err)

	require.Equal(t, "default", groups.userGroup)
	require.Equal(t, "vip", groups.tokenGroup)
	require.Equal(t, []string{"vip"}, groups.ownerGroups)
}

func TestGetModelListGroupsUsesFilteredTokenAutoGroupsSnapshot(t *testing.T) {
	originalMax := setting.GetMaxTokenAutoGroups()
	originalUsableGroups := setting.UserUsableGroups2JSONString()
	originalRatios := ratio_setting.GroupRatio2JSONString()
	require.NoError(t, setting.UpdateMaxTokenAutoGroups("1"))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":1}`))
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateMaxTokenAutoGroups(fmt.Sprintf("%d", originalMax)))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(originalUsableGroups))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalRatios))
	})

	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(ctx, constant.ContextKeyTokenGroup, "auto")
	common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, []string{"vip", "default"})

	groups, err := getModelListGroups(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"vip"}, groups.ownerGroups)

	common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, []string{"vip"})
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default"}`))
	groups, err = getModelListGroups(ctx)
	require.NoError(t, err)
	require.Empty(t, groups.ownerGroups)
}

func TestListModelsUsesPublicOwnerForEveryModel(t *testing.T) {
	withSelfUseModeEnabled(t)
	db := setupModelListControllerTestDB(t)
	previousCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = previousCache })
	require.NoError(t, db.Create(&[]model.Channel{
		{Id: 901, Type: constant.ChannelTypeCline, Status: common.ChannelStatusEnabled, Name: "private-cline-route", Models: "deepseek-v4.1-flash", Group: "default"},
		{Id: 902, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Name: "private-openai-route", Models: "gpt-5.4,custom-test-model", Group: "default"},
	}).Error)
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "default", Model: "deepseek-v4.1-flash", ChannelId: 901, Enabled: true},
		{Group: "default", Model: "gpt-5.4", ChannelId: 902, Enabled: true},
		{Group: "default", Model: "custom-test-model", ChannelId: 902, Enabled: true},
	}).Error)
	for _, path := range []string{"/v1/models", "/v1beta/openai/models"} {
		t.Run(path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodGet, path, nil)
			common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
			ListModels(ctx, constant.ChannelTypeOpenAI)
			payload := decodeListModelsPayload(t, recorder)
			require.Len(t, payload.Data, 3)
			for _, item := range payload.Data {
				assert.Equal(t, "chuanapi", item.OwnedBy, item.Id)
			}
		})
	}
}

func TestRetrieveModelUsesPublicOwnerWithoutChangingAdminMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, modelName := range []string{"gpt-5.4", "deepseek/deepseek-v4-flash"} {
		t.Run(modelName, func(t *testing.T) {
			_, exists := openAIModelsMap[modelName]
			if !exists {
				openAIModelsMap[modelName] = dto.OpenAIModels{Id: modelName, Object: "model", OwnedBy: "cline"}
				t.Cleanup(func() { delete(openAIModelsMap, modelName) })
			}
			metadata := openAIModelsMap[modelName]
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Params = gin.Params{{Key: "model", Value: modelName}}
			RetrieveModel(ctx, constant.ChannelTypeOpenAI)
			require.Equal(t, http.StatusOK, recorder.Code)
			var payload dto.OpenAIModels
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
			assert.Equal(t, modelName, payload.Id)
			assert.Equal(t, "chuanapi", payload.OwnedBy)
			assert.Equal(t, metadata, openAIModelsMap[modelName])
		})
	}
}
