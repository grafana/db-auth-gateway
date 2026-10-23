// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-kit/log"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

// backendHandler reports the protocol each request arrived on, so a client that
// silently falls back to HTTP/1 shows up as a failed assertion rather than a
// connection error.
func backendHandler(proto *atomic.Int32) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proto.Store(int32(r.ProtoMajor))
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("backend response"))
	})
}

func proxyRequest(t *testing.T, cfg Config, enableHTTP2 bool) *httptest.ResponseRecorder {
	t.Helper()

	handler, err := newHTTPProxy(
		"test",
		cfg,
		enableHTTP2,
		log.NewNopLogger(),
		NewMetrics(prometheus.NewPedanticRegistry(), "test"),
	)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/some/path", nil))

	return rec
}

// The h2c:// scheme must reach the backend as cleartext HTTP/2 with prior
// knowledge: no TLS, and no HTTP/1 upgrade dance.
func TestNewHTTPProxy_H2CUsesUnencryptedHTTP2(t *testing.T) {
	var proto atomic.Int32

	// Serve both HTTP/1 and cleartext HTTP/2, so a client-side fallback to
	// HTTP/1 still connects and is caught by the ProtoMajor assertion.
	srv := &http.Server{
		Handler:           backendHandler(&proto),
		ReadHeaderTimeout: 5 * time.Second,
	}
	srv.Protocols = &http.Protocols{}
	srv.Protocols.SetHTTP1(true)
	srv.Protocols.SetUnencryptedHTTP2(true)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	rec := proxyRequest(t, Config{
		URL:         "h2c://" + ln.Addr().String(),
		KeepAlive:   true,
		DialTimeout: 5 * time.Second,
	}, true)

	body, err := io.ReadAll(rec.Result().Body)
	require.NoError(t, err)

	require.Equal(t, http.StatusTeapot, rec.Code)
	require.Equal(t, "backend response", string(body))
	require.Equal(t, int32(2), proto.Load(), "h2c backend should have been reached over HTTP/2")
}

// With enableHTTP2 set, an https:// backend must negotiate HTTP/2 via ALPN.
func TestNewHTTPProxy_TLSNegotiatesHTTP2(t *testing.T) {
	var proto atomic.Int32

	backend := httptest.NewUnstartedServer(backendHandler(&proto))
	backend.EnableHTTP2 = true
	backend.StartTLS()
	t.Cleanup(backend.Close)

	// newHTTPProxy sets InsecureSkipVerify for https://127.0.0.1: URLs, which is
	// what httptest serves on, so the test certificate needs no extra plumbing.
	rec := proxyRequest(t, Config{
		URL:         backend.URL,
		KeepAlive:   true,
		DialTimeout: 5 * time.Second,
	}, true)

	body, err := io.ReadAll(rec.Result().Body)
	require.NoError(t, err)

	require.Equal(t, http.StatusTeapot, rec.Code)
	require.Equal(t, "backend response", string(body))
	require.Equal(t, int32(2), proto.Load(), "TLS backend should have been reached over HTTP/2")
}
