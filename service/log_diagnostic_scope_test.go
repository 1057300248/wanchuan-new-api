package service

import (
	"errors"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestLogDiagnosticWritersRetainAdminScopeAndUserPrivacy(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "logs.db")), &gorm.Config{})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	oldLogDB := model.LOG_DB
	oldMain, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
	t.Cleanup(func() {
		model.LOG_DB = oldLogDB
		common.SetDatabaseTypes(oldMain, oldLogType)
		assert.NoError(t, pool.Close())
	})
	model.LOG_DB = db
	common.SetDatabaseTypes(oldMain, common.DatabaseTypeSQLite)
	require.NoError(t, db.AutoMigrate(&model.Log{}))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	stream := relaycommon.NewStreamStatus()
	stream.SetEndReason(relaycommon.StreamEndReasonTimeout, errors.New("private-stream-end"))
	stream.RecordError("private-stream-error")
	info := &relaycommon.RelayInfo{
		OriginModelName: "requested", IsStream: true, StreamStatus: stream,
		ChannelMeta:            &relaycommon.ChannelMeta{UpstreamModelName: "private-upstream-model", IsModelMapped: true},
		ResponseModel:          &relaycommon.ResponseModel{RequestedModel: "requested", UpstreamModel: "private-upstream-model", ReturnedModel: "private-returned-model"},
		ParamOverrideAudit:     []string{"private-override"},
		RequestConversionChain: []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude},
	}
	other := GenerateTextOtherInfo(c, info, 1, 1, 1, 0, 0, 0.25, 1)
	snapshot := other.Snapshot()
	adminInfo, ok := snapshot["admin_info"].(map[string]any)
	require.True(t, ok)
	keys := []string{"upstream_model_name", "response_model", "po", "stream_status", "request_conversion"}
	for _, key := range keys {
		assert.NotContains(t, snapshot, key)
		require.Contains(t, adminInfo, key)
		assert.False(t, model.NewLogOther().SetPublic(key, "private-bypass"), "reserved key must remain rejected")
	}
	assert.Equal(t, "private-upstream-model", adminInfo["upstream_model_name"])
	assert.Equal(t, *info.ResponseModel, adminInfo["response_model"])
	assert.Equal(t, []string{"private-override"}, adminInfo["po"])
	assert.Equal(t, []string{"OpenAI Compatible", "Claude Messages"}, adminInfo["request_conversion"])
	streamInfo, ok := adminInfo["stream_status"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "error", streamInfo["status"])
	assert.Equal(t, "private-stream-end", streamInfo["end_error"])
	assert.Equal(t, []string{"private-stream-error"}, streamInfo["errors"])
	stored := model.Log{UserId: 7, Type: model.LogTypeConsume, Content: "wallet charged 100", Quota: 100, Other: other.JSONString()}
	require.NoError(t, db.Create(&stored).Error)
	users, total, err := model.GetUserLogs(7, model.LogFilter{}, 0, 10)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, users, 1)
	var userOther map[string]any
	require.NoError(t, common.UnmarshalJsonStr(users[0].Other, &userOther))
	assert.NotContains(t, userOther, "admin_info")
	assert.NotContains(t, users[0].Other, "private-")
	for _, key := range keys {
		assert.NotContains(t, userOther, key)
	}
	assert.Equal(t, 0.25, userOther["model_price"])
	assert.Equal(t, stored.Content, users[0].Content)
	assert.Equal(t, stored.Quota, users[0].Quota)
	adminLog := stored
	model.FormatAdminLogs([]*model.Log{&adminLog})
	var privileged map[string]any
	require.NoError(t, common.UnmarshalJsonStr(adminLog.Other, &privileged))
	visible, ok := privileged["admin_info"].(map[string]any)
	require.True(t, ok)
	for _, key := range keys {
		assert.Contains(t, visible, key)
		assert.NotContains(t, privileged, key)
	}
	assert.Contains(t, adminLog.Other, "private-stream-end")
	var after model.Log
	require.NoError(t, db.First(&after, stored.Id).Error)
	assert.Equal(t, stored, after, "projection leaves persisted administrator diagnostics unchanged")
}
