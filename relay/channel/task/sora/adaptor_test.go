package sora

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

<<<<<<< HEAD
func TestSoraBuildRequestBodyReturnsReplayablePassThroughBody(t *testing.T) {
=======
func TestSoraBuildRequestBodyBindsReplayMetadataForPassThrough(t *testing.T) {
>>>>>>> fix(relay): set Request.GetBody so the HTTP/2 transport can transparently retry after an upstream stream reset (#6249)
	payload := []byte("opaque-sora-request-body")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewReader(payload))
	c.Request.Header.Set("Content-Type", "application/octet-stream")
	defer common.CleanupBodyStorage(c)

	info := &relaycommon.RelayInfo{}
	body, err := (&TaskAdaptor{}).BuildRequestBody(c, info)
	require.NoError(t, err)
<<<<<<< HEAD
	replayable, ok := body.(common.ReplayableBody)
	require.True(t, ok)
=======
>>>>>>> fix(relay): set Request.GetBody so the HTTP/2 transport can transparently retry after an upstream stream reset (#6249)

	sent, err := io.ReadAll(body)
	require.NoError(t, err)
	assert.Equal(t, payload, sent)
<<<<<<< HEAD
	assert.EqualValues(t, len(payload), replayable.Size())

	replayBody, err := replayable.NewReader()
=======
	assert.EqualValues(t, len(payload), info.UpstreamRequestBodySize)
	require.NotNil(t, info.UpstreamRequestGetBody)

	replayBody, err := info.UpstreamRequestGetBody()
>>>>>>> fix(relay): set Request.GetBody so the HTTP/2 transport can transparently retry after an upstream stream reset (#6249)
	require.NoError(t, err)
	replay, err := io.ReadAll(replayBody)
	require.NoError(t, err)
	require.NoError(t, replayBody.Close())
	assert.Equal(t, payload, replay)
}
