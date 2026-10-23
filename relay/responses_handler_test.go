package relay

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestPrepareResponsesPassThroughBodyPreservesRawBodyWithoutOverride(t *testing.T) {
	rawBody := "{\n  \"model\": \"grok-4.6\",\n  \"include\": [\"reasoning.encrypted_content\"]\n}\n"
	c := newResponsesPassThroughTestContext(t, rawBody)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	body, closer, relayErr := prepareResponsesPassThroughBody(c, info)
	require.Nil(t, relayErr)
	require.Nil(t, closer)

	got, err := io.ReadAll(body)
	require.NoError(t, err)
	assert.Equal(t, rawBody, string(got))
}

func TestPrepareResponsesPassThroughBodyAppliesOverrideAndPreservesReasoningState(t *testing.T) {
	rawBody := `{
		"model":"grok-4.6",
		"reasoning_effort":"none",
		"reasoning":{"effort":"high","summary":"auto"},
		"include":["reasoning.encrypted_content"],
		"previous_response_id":"resp_previous",
		"prompt_cache_key":"session-123",
		"input":[{"type":"reasoning","id":"rs_123","encrypted_content":"ENCRYPTED_FIXTURE","summary":[]}]
	}`
	c := newResponsesPassThroughTestContext(t, rawBody)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ParamOverride: map[string]any{
				"operations": []any{
					map[string]any{"mode": "delete", "path": "reasoning_effort"},
				},
			},
		},
	}

	body, closer, relayErr := prepareResponsesPassThroughBody(c, info)
	require.Nil(t, relayErr)
	require.NotNil(t, closer)
	t.Cleanup(func() { require.NoError(t, closer.Close()) })

	got, err := io.ReadAll(body)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"model":"grok-4.6",
		"reasoning":{"effort":"high","summary":"auto"},
		"include":["reasoning.encrypted_content"],
		"previous_response_id":"resp_previous",
		"prompt_cache_key":"session-123",
		"input":[{"type":"reasoning","id":"rs_123","encrypted_content":"ENCRYPTED_FIXTURE","summary":[]}]
	}`, string(got))
	assert.Contains(t, info.ParamOverrideAudit, "delete reasoning_effort")
}

func TestPrepareResponsesPassThroughBodyCleansDiskBackedStorage(t *testing.T) {
	previousConfig := common.GetDiskCacheConfig()
	cacheDir := t.TempDir()
	common.SetDiskCacheConfig(common.DiskCacheConfig{
		Enabled:     true,
		ThresholdMB: 0,
		MaxSizeMB:   64,
		Path:        cacheDir,
	})
	t.Cleanup(func() { common.SetDiskCacheConfig(previousConfig) })

	encryptedContent := strings.Repeat("A+/=\\\"", 200_000)
	rawBody := `{"model":"grok-4.6","reasoning_effort":"none","include":["reasoning.encrypted_content"],"input":[{"type":"reasoning","encrypted_content":"` + encryptedContent + `"}]}`
	c := newResponsesPassThroughTestContext(t, rawBody)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ParamOverride: map[string]any{
				"operations": []any{map[string]any{"mode": "delete", "path": "reasoning_effort"}},
			},
		},
	}

	originalStorage, err := common.GetBodyStorage(c)
	require.NoError(t, err)
	require.True(t, originalStorage.IsDisk())

	body, closer, relayErr := prepareResponsesPassThroughBody(c, info)
	require.Nil(t, relayErr)
	require.NotNil(t, closer)
	outboundStorage, ok := closer.(common.BodyStorage)
	require.True(t, ok)
	require.True(t, outboundStorage.IsDisk())

	got, err := io.ReadAll(body)
	require.NoError(t, err)
	assert.NotContains(t, string(got), `"reasoning_effort"`)
	assert.Contains(t, string(got), encryptedContent)

	require.NoError(t, closer.Close())
	_, err = body.(common.ReplayableBody).NewReader()
	require.ErrorIs(t, err, common.ErrStorageClosed)

	common.CleanupBodyStorage(c)
	_, err = originalStorage.Bytes()
	require.ErrorIs(t, err, common.ErrStorageClosed)

	var cachedFiles []string
	err = filepath.WalkDir(filepath.Clean(cacheDir), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			cachedFiles = append(cachedFiles, path)
		}
		return nil
	})
	require.NoError(t, err)
	assert.Empty(t, cachedFiles)
}

func newResponsesPassThroughTestContext(t *testing.T, body string) *gin.Context {
	t.Helper()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	t.Cleanup(func() { common.CleanupBodyStorage(c) })
	return c
}

func TestPrepareResponsesPassThroughBodyPreservesOpaquePrecision(t *testing.T) {
	const opaque = `{"opaque_id":9007199254740993,"negative_id":-9007199254740993,"decimal":0.12345678901234567890123456789,"unknown":[{"future_flag":true}]}`
	const raw = `{"model":"client-visible","reasoning_effort":"none","future":` + opaque + `,"input":[]}`
	cases := []struct {
		name     string
		override map[string]any
	}{
		{"raw", nil},
		{"legacy", map[string]any{"temperature": 0.2}},
		{"operations", map[string]any{"operations": []any{map[string]any{"mode": "delete", "path": "reasoning_effort"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newResponsesPassThroughTestContext(t, raw)
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ParamOverride: tc.override}}
			body, closer, apiErr := prepareResponsesPassThroughBody(c, info)
			require.Nil(t, apiErr)
			if closer != nil {
				t.Cleanup(func() { require.NoError(t, closer.Close()) })
			}
			got, err := io.ReadAll(body)
			require.NoError(t, err)
			require.True(t, gjson.ValidBytes(got))
			assert.Equal(t, opaque, gjson.GetBytes(got, "future").Raw, "unknown fields and number lexemes must remain byte-exact")
			if tc.override == nil {
				assert.Equal(t, raw, string(got))
			}
		})
	}
}
