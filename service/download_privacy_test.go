package service

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type downloadPrivacyLogBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *downloadPrivacyLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}
func (b *downloadPrivacyLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

type downloadPrivacyTransport func(*http.Request) (*http.Response, error)

func (f downloadPrivacyTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDownloadLogsHideOriginWithoutChangingRequest(t *testing.T) {
	const origin = "https://private-image-fixture.example/private/path.png?signature=private-signature"
	for _, worker := range []bool{false, true} {
		name := "direct"
		if worker {
			name = "worker"
		}
		t.Run(name, func(t *testing.T) {
			oldWorker, oldKey := system_setting.WorkerUrl, system_setting.WorkerValidKey
			oldFetch := *system_setting.GetFetchSetting()
			oldHTTP, oldSSRF := httpClient, ssrfProtectedHTTPClient
			*system_setting.GetFetchSetting() = system_setting.FetchSetting{EnableSSRFProtection: false}
			system_setting.WorkerUrl = ""
			if worker {
				system_setting.WorkerUrl = "https://worker-fixture.invalid/"
			}
			system_setting.WorkerValidKey = "synthetic-worker-key"
			var captured downloadPrivacyLogBuffer
			common.LogWriterMu.Lock()
			oldWriter := gin.DefaultWriter
			gin.DefaultWriter = &captured
			common.LogWriterMu.Unlock()
			t.Cleanup(func() {
				system_setting.WorkerUrl, system_setting.WorkerValidKey = oldWorker, oldKey
				*system_setting.GetFetchSetting() = oldFetch
				httpClient, ssrfProtectedHTTPClient = oldHTTP, oldSSRF
				common.LogWriterMu.Lock()
				gin.DefaultWriter = oldWriter
				common.LogWriterMu.Unlock()
			})
			calls := 0
			transportFailure := errors.New("synthetic transport failure")
			transport := downloadPrivacyTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				if worker {
					assert.Equal(t, "POST", req.Method)
					assert.Equal(t, "https://worker-fixture.invalid/", req.URL.String())
					var payload WorkerRequest
					body, err := io.ReadAll(req.Body)
					require.NoError(t, err)
					require.NoError(t, common.Unmarshal(body, &payload))
					assert.Equal(t, origin, payload.URL)
					assert.Equal(t, "synthetic-worker-key", payload.Key)
				} else {
					assert.Equal(t, "GET", req.Method)
					assert.Equal(t, origin, req.URL.String())
				}
				if calls == 2 {
					return nil, transportFailure
				}
				return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader("public image bytes")), Header: make(http.Header), Request: req}, nil
			})
			httpClient = &http.Client{Transport: transport}
			ssrfProtectedHTTPClient = httpClient
			resp, err := DoDownloadRequest(origin, "image-result", "bounded-fixture")
			require.NoError(t, err)
			require.Equal(t, http.StatusAccepted, resp.StatusCode)
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			assert.Equal(t, "public image bytes", string(body))
			_, err = DoDownloadRequest(origin, "image-result", "bounded-fixture")
			require.ErrorIs(t, err, transportFailure)
			assert.Equal(t, 2, calls, "both real request paths and failure propagation execute")
			logged := captured.String()
			assert.Contains(t, logged, "reason: image-result, bounded-fixture")
			for _, private := range []string{"private-image-fixture.example", "private/path.png", "private-signature", "synthetic-worker-key"} {
				assert.NotContains(t, logged, private)
			}
		})
	}
}
