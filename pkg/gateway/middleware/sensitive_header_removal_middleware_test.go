// SPDX-License-Identifier: AGPL-3.0-only

package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSensitiveHeaderRemovalMiddleware(t *testing.T) {
	var requestSentToHandler *http.Request
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestSentToHandler = r
		w.WriteHeader(http.StatusTeapot)
	})

	middleware := NewSensitiveHeaderRemovalMiddleware([]string{"My-Custom-Header"})
	pipeline := middleware.Wrap(handler)

	originalRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	originalRequest.Header.Set("User-Agent", "foo/1.2.3")
	originalRequest.Header.Set("X-Access-Token", "super secret")
	originalRequest.Header.Set("X-Grafana-Id", "another secret")
	originalRequest.Header.Set("My-Custom-Header", "third secret")

	writer := httptest.NewRecorder()
	pipeline.ServeHTTP(writer, originalRequest)
	response := writer.Result()
	require.Equal(t, http.StatusTeapot, response.StatusCode)
	require.NotNil(t, requestSentToHandler, "expected handler to be called")
	require.NotEmpty(t, requestSentToHandler.Header.Values("User-Agent"))
	require.Empty(t, requestSentToHandler.Header.Values("X-Access-Token"))
	require.Empty(t, requestSentToHandler.Header.Values("X-Grafana-Id"))
	require.Empty(t, requestSentToHandler.Header.Values("My-Custom-Header"))
}
