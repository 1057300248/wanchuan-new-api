package controller

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestTaskPluginImageErrorHonorsPrivacyBoundary(t *testing.T) {
	for _, role := range []int{common.RoleCommonUser, common.RoleAdminUser, common.RoleRootUser} {
		c, recorder := newImageProtocolTestContext("")
		c.Set("role", role)
		c.Set(common.RequestIdKey, "local-image-request")
		taskErr := service.TaskErrorWrapper(errors.New(`{"code":"private-vendor-code","message":"https://private.example/api?key=secret-key quota_account=private-account","request_id":"vendor-request"}`), "fail_to_fetch_task", http.StatusBadGateway)
		respondTaskPluginImageError(c, taskErr)
		require.Equal(t, http.StatusBadGateway, recorder.Code)
		assert.Contains(t, recorder.Body.String(), "upstream request failed")
		assert.Contains(t, recorder.Body.String(), "local-image-request")
		for _, secret := range []string{"private.example", "secret-key", "private-account", "private-vendor-code", "vendor-request"} {
			assert.NotContains(t, recorder.Body.String(), secret)
		}
	}
	c, recorder := newImageProtocolTestContext("")
	common.SetContextKey(c, constant.ContextKeyChannelTest, true)
	taskErr := service.TaskErrorWrapper(errors.New(`{"code":"InvalidParameter","message":"n must be 1"}`), "fail_to_fetch_task", http.StatusBadRequest)
	respondTaskPluginImageError(c, taskErr)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "InvalidParameter")
	assert.Contains(t, recorder.Body.String(), "n must be 1")
}

// Exercise the real render/HTTP path, not only the standalone sanitizer.
func TestTaskPluginImageSuccessProjectsOnlyRecognizedModelFields(t *testing.T) {
	pinned := imageProtocolTestEndpoint(t)
	engine, err := pluginruntime.Compile(strings.Replace(imageProtocolTestPlugin,
		`return {data: data, vendor: "test"};`,
		`return {data: data, vendor: "test", model: "private-provider-model", unknown: {model:"extension-owned-model", exact:"9007199254740993"}};`, 1),
		pluginruntime.Options{Key: "image-bridge", Version: "1.0.0", Concurrency: 1})
	require.NoError(t, err)
	pinned.Plugin.Engine = engine
	c, recorder := newImageProtocolTestContext("")
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "image-model")
	deps := pluginProtocolTestDeps()
	deps.submit = func(_ *gin.Context, info *relaycommon.RelayInfo) (*taskSubmissionOutcome, *dto.TaskError) {
		return imageProtocolTestOutcome(info, model.TaskStatusSuccess, "https://public-artifact.example/result.png"), nil
	}
	serveTaskPluginImageProtocol(c, pinned, deps)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	body := recorder.Body.Bytes()
	assert.Equal(t, "image-model", gjson.GetBytes(body, "model").String())
	assert.Equal(t, "extension-owned-model", gjson.GetBytes(body, "unknown.model").String())
	assert.Equal(t, "9007199254740993", gjson.GetBytes(body, "unknown.exact").String())
	assert.Equal(t, "test", gjson.GetBytes(body, "vendor").String())
	assert.Equal(t, "https://public-artifact.example/result.png", gjson.GetBytes(body, "data.0.url").String())
	assert.Equal(t, "1710000000", gjson.GetBytes(body, "created").Raw)
}
