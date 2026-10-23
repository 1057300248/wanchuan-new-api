// Package httpx provides small per-request HTTP policies shared by relay callers.
package httpx

import (
	"errors"
	"net/http"
)

// DoWithoutRedirects returns the first upstream response, including 3xx bodies.
// The shallow copy preserves timeout, cookie jar, transport and connection pools
// without mutating the shared client's policy. The caller owns response.Body.
func DoWithoutRedirects(client *http.Client, request *http.Request) (*http.Response, error) {
	if client == nil {
		return nil, errors.New("http client must not be nil")
	}
	requestClient := *client
	requestClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return requestClient.Do(request)
}
