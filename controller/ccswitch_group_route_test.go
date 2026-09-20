package controller

import (
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func requestCCSwitchGroupModels(t *testing.T, tokenID, userID int, group string) *httptest.ResponseRecorder {
	t.Helper()
	query := url.Values{"app": {"codex"}, "group": {group}}
	ctx, recorder := newAuthenticatedContext(t, http.MethodGet, "/api/user/tokens/"+strconv.Itoa(tokenID)+"/models?"+query.Encode(), nil, userID)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(tokenID)}}
	GetTokenModels(ctx)
	return recorder
}

func initializeCCSwitchTokenLookup(t *testing.T) {
	t.Helper()
	// Reuse the isolated SQLite fixture, but run the normal column-name
	// initialization required by GetTokenByKey. No connection or migration is
	// created: an empty log DSN reuses model.DB and non-master skips migrations.
	t.Setenv("LOG_SQL_DSN", "")
	previousMaster := common.IsMasterNode
	common.IsMasterNode = false
	defer func() { common.IsMasterNode = previousMaster }()
	require.NoError(t, model.InitLogDB())
}

func TestCCSwitchGroupsNarrowModelsWithoutWritingToken(t *testing.T) {
	db, user := setupTokenModelsControllerTest(t)
	token := seedTokenModelsToken(t, db, user.Id, "group-models", "auto", []string{"a", "b"}, "gpt-4o", "gpt-4.1")
	seedTokenModelsAbility(t, db, "a", "gpt-4o", constant.ChannelTypeOpenAI, true)
	seedTokenModelsAbility(t, db, "b", "gpt-4.1", constant.ChannelTypeOpenAI, true)
	before, err := common.Marshal(token)
	require.NoError(t, err)

	for _, tc := range []struct {
		group  string
		models string
	}{
		{group: "", models: `["gpt-4.1","gpt-4o"]`},
		{group: "a", models: `["gpt-4o"]`},
		{group: "b", models: `["gpt-4.1"]`},
	} {
		t.Run("group="+tc.group, func(t *testing.T) {
			response := requestCCSwitchGroupModels(t, token.Id, user.Id, tc.group)
			require.True(t, gjson.Get(response.Body.String(), "success").Bool(), response.Body.String())
			assert.JSONEq(t, tc.models, gjson.Get(response.Body.String(), "data").Raw)
			assert.JSONEq(t, `["a","b"]`, gjson.Get(response.Body.String(), "groups").Raw)
		})
	}
	var after model.Token
	require.NoError(t, db.First(&after, token.Id).Error)
	encoded, err := common.Marshal(&after)
	require.NoError(t, err)
	assert.JSONEq(t, string(before), string(encoded))
}

func TestCCSwitchGroupsPreserveWhitelistAndRejectWiderGroups(t *testing.T) {
	db, user := setupTokenModelsControllerTest(t)
	seedTokenModelsAbility(t, db, "a", "gpt-4o", constant.ChannelTypeOpenAI, true)
	seedTokenModelsAbility(t, db, "a", "gpt-4.1", constant.ChannelTypeOpenAI, true)
	seedTokenModelsAbility(t, db, "b", "gpt-4.1", constant.ChannelTypeOpenAI, true)

	for _, token := range []*model.Token{
		seedTokenModelsToken(t, db, user.Id, "fixed", "a", nil, "gpt-4o"),
		seedTokenModelsToken(t, db, user.Id, "snapshot", "auto", []string{"a"}, "gpt-4o"),
	} {
		t.Run(token.Name, func(t *testing.T) {
			allowed := requestCCSwitchGroupModels(t, token.Id, user.Id, "a")
			require.True(t, gjson.Get(allowed.Body.String(), "success").Bool())
			assert.JSONEq(t, `["gpt-4o"]`, gjson.Get(allowed.Body.String(), "data").Raw)
			assert.JSONEq(t, `["a"]`, gjson.Get(allowed.Body.String(), "groups").Raw)
			for _, group := range []string{"b", "unknown", "auto"} {
				denied := requestCCSwitchGroupModels(t, token.Id, user.Id, group)
				assert.False(t, gjson.Get(denied.Body.String(), "success").Bool(), denied.Body.String())
			}
			otherUser := requestCCSwitchGroupModels(t, token.Id, user.Id+1, "a")
			assert.False(t, gjson.Get(otherUser.Body.String(), "success").Bool())
		})
	}
}

func TestCCSwitchGroupsRejectRevokedGroup(t *testing.T) {
	db, user := setupTokenModelsControllerTest(t)
	token := seedTokenModelsToken(t, db, user.Id, "revoked", "auto", []string{"a", "b"})
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","a":"A"}`))
	denied := requestCCSwitchGroupModels(t, token.Id, user.Id, "b")
	assert.False(t, gjson.Get(denied.Body.String(), "success").Bool())
	inherited := requestCCSwitchGroupModels(t, token.Id, user.Id, "")
	require.True(t, gjson.Get(inherited.Body.String(), "success").Bool())
	assert.JSONEq(t, `["a"]`, gjson.Get(inherited.Body.String(), "groups").Raw)
}

func TestCCSwitchGroupHintRoutesOnlyWithinAuthenticatedToken(t *testing.T) {
	db, user := setupTokenModelsControllerTest(t)
	initializeCCSwitchTokenLookup(t)
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","auto":"Auto","a":"A","b":"B"}`))
	token := seedTokenModelsToken(t, db, user.Id, "routing", "auto", []string{"a", "b"}, "gpt-4o")
	key, err := common.GenerateKey()
	require.NoError(t, err)
	token.Key = key
	token.UnlimitedQuota = false
	token.RemainQuota = 123
	token.CrossGroupRetry = true
	require.NoError(t, db.Save(token).Error)

	for _, tc := range []struct {
		path   string
		header string
		prefix string
	}{
		{path: "/v1/responses", header: "Authorization", prefix: "Bearer "},
		{path: "/v1/messages", header: "x-api-key"},
		{path: "/v1beta/models/gemini:generateContent", header: "x-goog-api-key"},
	} {
		t.Run(tc.header, func(t *testing.T) {
			router := gin.New()
			router.POST(tc.path, middleware.TokenAuth(), func(c *gin.Context) {
				assert.Equal(t, "b", common.GetContextKeyString(c, constant.ContextKeyUsingGroup))
				assert.Equal(t, "b", common.GetContextKeyString(c, constant.ContextKeyTokenGroup))
				crossRetry, _ := common.GetContextKey(c, constant.ContextKeyTokenCrossGroupRetry)
				assert.Equal(t, false, crossRetry)
				assert.Equal(t, token.Id, c.GetInt("token_id"))
				assert.Equal(t, token.Key, c.GetString("token_key"))
				assert.Equal(t, 123, c.GetInt("token_quota"))
				assert.True(t, c.GetBool("token_model_limit_enabled"))
				limits, _ := c.Get("token_model_limit")
				assert.Equal(t, token.GetModelLimitsMap(), limits)
				c.Status(http.StatusNoContent)
			})
			request := httptest.NewRequest(http.MethodPost, tc.path, nil)
			request.Header.Set(tc.header, tc.prefix+"sk-"+key+"-grp-62")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			assert.Equal(t, http.StatusNoContent, response.Code, response.Body.String())
		})
	}

	var unchanged model.Token
	require.NoError(t, db.First(&unchanged, token.Id).Error)
	assert.Equal(t, "auto", unchanged.Group)
	assert.Equal(t, token.AutoGroups, unchanged.AutoGroups)
	assert.Equal(t, token.ModelLimits, unchanged.ModelLimits)
	assert.True(t, unchanged.ModelLimitsEnabled)
	assert.True(t, unchanged.CrossGroupRetry)
	assert.Equal(t, 123, unchanged.RemainQuota)
}

func TestCCSwitchGroupHintRejectsInvalidOrUnauthorizedRouting(t *testing.T) {
	db, user := setupTokenModelsControllerTest(t)
	initializeCCSwitchTokenLookup(t)
	token := seedTokenModelsToken(t, db, user.Id, "fixed-route", "a", nil)
	key, err := common.GenerateKey()
	require.NoError(t, err)
	token.Key = key
	require.NoError(t, db.Save(token).Error)

	router := gin.New()
	router.POST("/v1/responses", middleware.TokenAuth(), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	for _, tc := range []struct {
		suffix string
		status int
	}{
		{suffix: "", status: http.StatusNoContent},
		{suffix: "-grp-61", status: http.StatusNoContent},
		{suffix: "-grp-62", status: http.StatusForbidden},
		{suffix: "-grp-" + hex.EncodeToString([]byte("unknown")), status: http.StatusForbidden},
		{suffix: "-grp", status: http.StatusBadRequest},
		{suffix: "-grp-", status: http.StatusBadRequest},
		{suffix: "-grp-zz", status: http.StatusBadRequest},
		{suffix: "-grp-61-123", status: http.StatusBadRequest},
		{suffix: "-123", status: http.StatusForbidden},
	} {
		t.Run(tc.suffix, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			request.Header.Set("Authorization", "Bearer sk-"+key+tc.suffix)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			assert.Equal(t, tc.status, response.Code, response.Body.String())
		})
	}
}

func TestCCSwitchGroupHintKeepsReadOnlyUsageWorking(t *testing.T) {
	db, user := setupTokenModelsControllerTest(t)
	initializeCCSwitchTokenLookup(t)
	token := seedTokenModelsToken(t, db, user.Id, "usage", "a", nil, "gpt-4o")
	key, err := common.GenerateKey()
	require.NoError(t, err)
	token.Key = key
	token.RemainQuota = 123
	require.NoError(t, db.Save(token).Error)

	router := gin.New()
	router.GET("/api/usage/token/", middleware.TokenAuthReadOnly(), GetTokenUsage)
	request := httptest.NewRequest(http.MethodGet, "/api/usage/token/", nil)
	request.Header.Set("Authorization", "Bearer sk-"+key+"-grp-61")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.True(t, gjson.Get(response.Body.String(), "code").Bool())
	assert.EqualValues(t, 123, gjson.Get(response.Body.String(), "data.total_available").Int())
	assert.True(t, gjson.Get(response.Body.String(), "data.model_limits_enabled").Bool())
}
