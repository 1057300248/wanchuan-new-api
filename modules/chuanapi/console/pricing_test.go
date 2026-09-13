package console

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type publicPricingPayload struct {
	Success bool `json:"success"`
	Data    []struct {
		ModelName   string   `json:"model_name"`
		EnableGroup []string `json:"enable_groups"`
	} `json:"data"`
	GroupRatio     map[string]float64 `json:"group_ratio"`
	UsableGroup    map[string]string  `json:"usable_group"`
	AutoGroups     []string           `json:"auto_groups"`
	PricingVersion string             `json:"pricing_version"`
}

func performPublicPricingRequest(t *testing.T, decorate func(*gin.Context)) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/public-api/pricing", nil)
	if decorate != nil {
		decorate(context)
	}
	GetPublicPricing(context)
	require.Equal(t, http.StatusOK, recorder.Code)
	return recorder
}

func TestGetPublicPricingProjectsAnonymousPublicGroupsOnly(t *testing.T) {
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousRedis := common.RedisEnabled
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	db, err := gorm.Open(sqlite.Open("file:console-public-pricing?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	model.DB, model.LOG_DB = db, db
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.Model{}, &model.Vendor{}))
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled = previousRedis
		common.SetDatabaseTypes(previousMainType, previousLogType)
		model.InvalidatePricingCache()
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})

	originalUsableGroups := setting.UserUsableGroups2JSONString()
	originalGroupRatios := ratio_setting.GroupRatio2JSONString()
	originalAutoGroups := setting.AutoGroups2JsonString()
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"private":9}`))
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["default","private"]`))
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(originalUsableGroups))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalGroupRatios))
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(originalAutoGroups))
	})

	require.NoError(t, db.Create(&model.Channel{
		Id: 801, Type: constant.ChannelTypeOpenAI, Key: "fixture-key",
		Status: common.ChannelStatusEnabled, Name: "fixture-channel",
		Models: "public-model,private-model", Group: "default,private",
	}).Error)
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "default", Model: "public-model", ChannelId: 801, Enabled: true},
		{Group: "private", Model: "private-model", ChannelId: 801, Enabled: true},
	}).Error)
	model.InvalidatePricingCache()

	anonymous := performPublicPricingRequest(t, nil)
	var payload publicPricingPayload
	require.NoError(t, common.Unmarshal(anonymous.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	require.Len(t, payload.Data, 1, "only public-group models may appear in the anonymous catalogue")
	assert.Equal(t, "public-model", payload.Data[0].ModelName)
	assert.Equal(t, []string{"default"}, payload.Data[0].EnableGroup)
	assert.Equal(t, map[string]float64{"default": 1}, payload.GroupRatio)
	assert.Equal(t, map[string]string{"default": "Default"}, payload.UsableGroup)
	assert.Equal(t, []string{"default"}, payload.AutoGroups)
	assert.Equal(t, pricingVersion, payload.PricingVersion)
	assert.NotContains(t, anonymous.Body.String(), "private")

	forged := performPublicPricingRequest(t, func(c *gin.Context) { c.Set("id", 4242) })
	assert.Equal(t, anonymous.Body.String(), forged.Body.String(),
		"an identity in the context must not personalize the anonymous catalogue")
}
