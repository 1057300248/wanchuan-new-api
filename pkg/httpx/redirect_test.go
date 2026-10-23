package httpx

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type policyTransport func(*http.Request) (*http.Response, error)

func (f policyTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type policyBody struct {
	io.Reader
	closed bool
}

func (b *policyBody) Close() error { b.closed = true; return nil }

func TestDoWithoutRedirectsPreservesClientAndResponseOwnership(t *testing.T) {
	origin, err := url.Parse("https://origin.invalid/")
	require.NoError(t, err)
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	jar.SetCookies(origin, []*http.Cookie{{Name: "session", Value: "fixture-only"}})
	body := &policyBody{Reader: strings.NewReader("first response")}
	calls, policyCalls := 0, 0
	transport := policyTransport(func(request *http.Request) (*http.Response, error) {
		calls++
		assert.Equal(t, "origin.invalid", request.URL.Host)
		cookie, err := request.Cookie("session")
		require.NoError(t, err)
		assert.Equal(t, "fixture-only", cookie.Value)
		_, deadline := request.Context().Deadline()
		assert.True(t, deadline)
		return &http.Response{StatusCode: http.StatusTemporaryRedirect, Header: http.Header{"Location": {"https://redirect.invalid/"}}, Body: body, Request: request}, nil
	})
	client := &http.Client{Transport: transport, Jar: jar, Timeout: time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { policyCalls++; return nil }}
	request, err := http.NewRequest(http.MethodGet, origin.String(), nil)
	require.NoError(t, err)
	response, err := DoWithoutRedirects(client, request)
	require.NoError(t, err)
	require.NotNil(t, response)
	assert.Equal(t, http.StatusTemporaryRedirect, response.StatusCode)
	assert.Equal(t, 1, calls)
	assert.Zero(t, policyCalls)
	assert.False(t, body.closed)
	contents, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	assert.Equal(t, "first response", string(contents))
	require.NoError(t, response.Body.Close())
	assert.True(t, body.closed)
	assert.Same(t, jar, client.Jar)
	assert.Equal(t, time.Minute, client.Timeout)
	require.NoError(t, client.CheckRedirect(nil, nil))
	assert.Equal(t, 1, policyCalls)
}

func TestDoWithoutRedirectsRejectsNilClient(t *testing.T) {
	response, err := DoWithoutRedirects(nil, nil)
	assert.Nil(t, response)
	assert.ErrorContains(t, err, "client must not be nil")
}
