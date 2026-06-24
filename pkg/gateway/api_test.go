// SPDX-License-Identifier: AGPL-3.0-only

package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gokitlog "github.com/go-kit/log"
	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/grafana/db-auth-gateway/pkg/auth"
	gatewaymiddleware "github.com/grafana/db-auth-gateway/pkg/gateway/middleware"
	"github.com/grafana/db-auth-gateway/pkg/middleware/clientip"
	"github.com/grafana/db-auth-gateway/pkg/router"
)

// mustAuthResult builds an AuthResult for tests, failing the test on any
// validation error. Multiple tenant IDs are pipe-joined as a single
// X-Scope-OrgID header value (dskit convention).
func mustAuthResult(t *testing.T, tenants ...string) auth.AuthResult {
	t.Helper()
	result, err := auth.NewAuthResult([]string{strings.Join(tenants, "|")}, nil, nil)
	require.NoError(t, err)
	return result
}

// mockAuthenticator is a configurable Authenticator for tests.
type mockAuthenticator struct {
	result auth.AuthResult
	err    error
}

func (m *mockAuthenticator) Authenticate(_ context.Context, _ *http.Request, _ string) (auth.AuthResult, error) {
	return m.result, m.err
}

// newTestAPI creates a mux with the given routes registered against backendHandler,
// using a as the authenticator. Mirrors the internal wiring of API.registerRoutes.
func newTestAPI(a auth.Authenticator, backendHandler http.Handler, routes []router.Route) http.Handler {
	if backendHandler == nil {
		backendHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
	}
	r := mux.NewRouter()
	api := &API{auth: a, logger: gokitlog.NewNopLogger()}
	if err := api.registerRoutes(r, backendHandler, routes); err != nil {
		panic(err)
	}
	return r
}

func TestAPI_404OnUnmatchedRoute(t *testing.T) {
	h := newTestAPI(&mockAuthenticator{result: mustAuthResult(t, "t1")}, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/unknown", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rec.Code)
	}
}

func TestAPI_401OnAuthUnauthorized(t *testing.T) {
	routes := []router.Route{
		router.NewRoute("/prometheus/api/v1/query", []string{"GET"}, auth.ScopeMetricsRead),
	}
	h := newTestAPI(&mockAuthenticator{err: auth.ErrUnauthorized}, nil, routes)

	req := httptest.NewRequest(http.MethodGet, "/prometheus/api/v1/query", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", rec.Code)
	}
}

func TestAPI_403OnAuthForbidden(t *testing.T) {
	routes := []router.Route{
		router.NewRoute("/prometheus/api/v1/query", []string{"GET"}, auth.ScopeMetricsRead),
	}
	h := newTestAPI(&mockAuthenticator{err: auth.ErrForbidden}, nil, routes)

	req := httptest.NewRequest(http.MethodGet, "/prometheus/api/v1/query", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("got %d, want 403", rec.Code)
	}
}

func TestAPI_OrgIDInjection(t *testing.T) {
	var capturedOrgID string
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedOrgID = r.Header.Get("X-Scope-OrgID")
		w.WriteHeader(http.StatusOK)
	})

	routes := []router.Route{
		router.NewRoute("/prometheus/api/v1/query", []string{"GET"}, auth.ScopeMetricsRead),
	}
	a := &mockAuthenticator{result: mustAuthResult(t, "my-tenant")}
	h := newTestAPI(a, backend, routes)

	req := httptest.NewRequest(http.MethodGet, "/prometheus/api/v1/query", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if capturedOrgID != "my-tenant" {
		t.Errorf("got X-Scope-OrgID=%q, want %q", capturedOrgID, "my-tenant")
	}
}

func TestAPI_AuthHeadersStripped(t *testing.T) {
	var capturedAuth, capturedOrgID string
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		capturedOrgID = r.Header.Get("X-Scope-OrgID")
		w.WriteHeader(http.StatusOK)
	})

	routes := []router.Route{
		router.NewRoute("/prometheus/api/v1/query", []string{"GET"}, auth.ScopeMetricsRead),
	}
	a := &mockAuthenticator{result: mustAuthResult(t, "tenant1")}
	h := newTestAPI(a, backend, routes)

	req := httptest.NewRequest(http.MethodGet, "/prometheus/api/v1/query", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	req.Header.Set("X-Scope-OrgID", "original-tenant")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if capturedAuth != "" {
		t.Errorf("Authorization header should be stripped, got %q", capturedAuth)
	}
	if capturedOrgID != "tenant1" {
		t.Errorf("X-Scope-OrgID should be injected from auth result, got %q", capturedOrgID)
	}
}

func TestAPI_MultiTenantOrgID(t *testing.T) {
	var capturedOrgID string
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedOrgID = r.Header.Get("X-Scope-OrgID")
		w.WriteHeader(http.StatusOK)
	})

	routes := []router.Route{
		router.NewRoute("/prometheus/api/v1/query", []string{"GET"}, auth.ScopeMetricsRead),
	}
	a := &mockAuthenticator{result: mustAuthResult(t, "tenant1", "tenant2")}
	h := newTestAPI(a, backend, routes)

	req := httptest.NewRequest(http.MethodGet, "/prometheus/api/v1/query", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if capturedOrgID != "tenant1|tenant2" {
		t.Errorf("got X-Scope-OrgID=%q, want %q", capturedOrgID, "tenant1|tenant2")
	}
}

func TestAPI_PathRewrite(t *testing.T) {
	var capturedPath string
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})

	routes := []router.Route{
		router.NewRoute("/api/prom/api/v1/query", []string{"GET"}, auth.ScopeMetricsRead, router.TrimPrefix("/api/prom")),
	}
	a := &mockAuthenticator{result: mustAuthResult(t, "t1")}
	h := newTestAPI(a, backend, routes)

	req := httptest.NewRequest(http.MethodGet, "/api/prom/api/v1/query", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if capturedPath != "/api/v1/query" {
		t.Errorf("got path=%q, want %q", capturedPath, "/api/v1/query")
	}
}

func TestAPI_GRPCAllowed(t *testing.T) {
	routes := []router.Route{
		router.NewRoute("/tempopb.StreamingQuerier/Search", []string{"POST"}, auth.ScopeTracesRead),
	}
	h := newTestAPI(&mockAuthenticator{result: mustAuthResult(t, "t1")}, nil, routes)

	req := httptest.NewRequest(http.MethodPost, "/tempopb.StreamingQuerier/Search", nil)
	req.Header.Set("Content-Type", "application/grpc")
	req.Header.Set("X-Scope-OrgID", "t1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("got %d, want 200: gRPC requests should be allowed", rec.Code)
	}
}

func TestAPI_GRPCInjectsLowercaseOrgID(t *testing.T) {
	var capturedHeader http.Header
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeader = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	})

	routes := []router.Route{
		router.NewRoute("/tempopb.StreamingQuerier/Search", []string{"POST"}, auth.ScopeTracesRead),
	}
	a := &mockAuthenticator{result: mustAuthResult(t, "mytenant")}
	h := newTestAPI(a, backend, routes)

	req := httptest.NewRequest(http.MethodPost, "/tempopb.StreamingQuerier/Search", nil)
	req.Header.Set("Content-Type", "application/grpc")
	req.Header.Set("X-Scope-OrgID", "incoming-tenant")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("got %d, want 200", rec.Code)
	}
	// The lowercase key must be set in the header map directly for gRPC/HTTP2.
	vals, ok := capturedHeader["x-scope-orgid"] //nolint:staticcheck // SA1008: intentional non-canonical key — gRPC/HTTP2 requires lowercase headers
	if !ok || len(vals) == 0 || vals[0] != "mytenant" {
		t.Errorf("expected x-scope-orgid=mytenant, got %v (present=%v)", vals, ok)
	}
	// The incoming X-Scope-OrgID must have been stripped.
	if capturedHeader.Get("X-Scope-OrgID") != "" {
		t.Errorf("X-Scope-OrgID should be stripped for gRPC, got %q", capturedHeader.Get("X-Scope-OrgID"))
	}
}

func TestAPI_WebSocketAllowedOnWebsocketRoute(t *testing.T) {
	routes := []router.Route{
		router.NewRoute("/loki/api/v1/tail", []string{"GET"}, auth.ScopeLogsRead, router.Websocket()),
	}
	h := newTestAPI(&mockAuthenticator{result: mustAuthResult(t, "t1")}, nil, routes)

	req := httptest.NewRequest(http.MethodGet, "/loki/api/v1/tail", nil)
	req.Header.Set("Upgrade", "websocket")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, rec.Code, http.StatusOK)
}

func TestValidScope_TraceScopes(t *testing.T) {
	if !validScope(auth.ScopeTracesRead) {
		t.Errorf("ScopeTracesRead (%q) should be a valid scope", auth.ScopeTracesRead)
	}
	if !validScope(auth.ScopeTracesWrite) {
		t.Errorf("ScopeTracesWrite (%q) should be a valid scope", auth.ScopeTracesWrite)
	}
}

func TestAPI_TempoOverridesAPIEnabled(t *testing.T) {
	a := &mockAuthenticator{result: mustAuthResult(t, "t1")}

	t.Run("enabled returns 200", func(t *testing.T) {
		h := newTestAPI(a, nil, router.TempoUserConfigurableOverridesRoutes)

		req := httptest.NewRequest(http.MethodGet, "/tempo/api/overrides", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("got %d, want 200: overrides route should match when enabled", rec.Code)
		}
	})

	t.Run("disabled returns 404", func(t *testing.T) {
		h := newTestAPI(a, nil, []router.Route{})

		req := httptest.NewRequest(http.MethodGet, "/tempo/api/overrides", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("got %d, want 404: overrides route should not match when disabled", rec.Code)
		}
	})
}

// sentinelMiddleware is an identifiable no-op middleware used in tests to verify
// that the correct middleware instance is selected.
type sentinelMiddleware struct{ name string }

func (s *sentinelMiddleware) Wrap(h http.Handler) http.Handler { return h }

func TestTimeoutMiddleware(t *testing.T) {
	read := &sentinelMiddleware{"read"}
	write := &sentinelMiddleware{"write"}
	export := &sentinelMiddleware{"export"}
	request := &sentinelMiddleware{"request"}

	a := &API{
		readTimeoutMiddleware:    read,
		writeTimeoutMiddleware:   write,
		exportTimeoutMiddleware:  export,
		requestTimeoutMiddleware: request,
	}

	t.Run("spot-check groupings", func(t *testing.T) {
		tests := []struct {
			scope    string
			expected *sentinelMiddleware
		}{
			{auth.ScopeLogsRead, read},
			{auth.ScopeLogsWrite, write},
			{auth.ScopeLogsDelete, write},
			{auth.ScopeMetricsRead, read},
			{auth.ScopeMetricsWrite, write},
			{auth.ScopeMetricsExport, export},
			{"", request},
		}
		for _, tt := range tests {
			got := a.timeoutMiddleware(router.Route{Permission: tt.scope})
			if got != tt.expected {
				t.Errorf("scope %q: got middleware %q, want %q",
					tt.scope, got.(*sentinelMiddleware).name, tt.expected.name)
			}
		}
	})

	t.Run("completeness", func(t *testing.T) {
		allScopes := []string{
			auth.ScopeMetricsRead, auth.ScopeMetricsWrite, auth.ScopeMetricsExport,
			auth.ScopeLogsRead, auth.ScopeLogsWrite, auth.ScopeLogsDelete,
			auth.ScopeTracesRead, auth.ScopeTracesWrite,
			auth.ScopeProfilesRead, auth.ScopeProfilesWrite,
			auth.ScopeRulesRead, auth.ScopeRulesWrite,
			auth.ScopeAlertsRead, auth.ScopeAlertsWrite,
		}
		for _, scope := range allScopes {
			got := a.timeoutMiddleware(router.Route{Permission: scope})
			if got == request {
				t.Errorf("scope %q fell through to requestTimeoutMiddleware — add it to the switch", scope)
			}
		}
	})
}

func TestAPI_GRPCMultiTenant(t *testing.T) {
	var capturedHeader http.Header
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeader = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	})

	routes := []router.Route{
		router.NewRoute("/tempopb.StreamingQuerier/Search", []string{"POST"}, auth.ScopeTracesRead),
	}
	a := &mockAuthenticator{result: mustAuthResult(t, "tenant1", "tenant2")}
	h := newTestAPI(a, backend, routes)

	req := httptest.NewRequest(http.MethodPost, "/tempopb.StreamingQuerier/Search", nil)
	req.Header.Set("Content-Type", "application/grpc")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("got %d, want 200", rec.Code)
	}
	// gRPC/HTTP2 requires lowercase x-scope-orgid with multi-tenant value joined by "|".
	vals, ok := capturedHeader["x-scope-orgid"] //nolint:staticcheck // SA1008: intentional non-canonical key — gRPC/HTTP2 requires lowercase headers
	if !ok || len(vals) == 0 || vals[0] != "tenant1|tenant2" {
		t.Errorf("expected x-scope-orgid=tenant1|tenant2, got %v (present=%v)", vals, ok)
	}
}

func TestAPI_ClientIPMiddlewareChain(t *testing.T) {
	logger := gokitlog.NewNopLogger()

	// Configure a clientIPMiddleware that resolves the IP from RemoteAddr.
	clientIPConfig := clientip.Config{
		Enabled: true,
		Type:    map[clientip.ExtractorType]bool{clientip.ExtractorTypeRemoteAddr: true},
	}
	clientIPMw, err := clientip.New(clientIPConfig, prometheus.NewRegistry(), logger)
	if err != nil {
		t.Fatalf("creating clientIPMiddleware: %v", err)
	}

	routes := []router.Route{
		router.NewRoute("/prometheus/api/v1/query", []string{"GET"}, auth.ScopeMetricsRead),
	}
	a := &mockAuthenticator{result: mustAuthResult(t, "t1")}

	r := mux.NewRouter()
	api := &API{
		auth:                    a,
		logger:                  logger,
		clientIPMiddleware:      clientIPMw,
		xForwardedForMiddleware: gatewaymiddleware.NewXForwardedForMiddleware(logger),
	}
	if err := api.registerRoutes(r, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), routes); err != nil {
		t.Fatalf("registerRoutes: %v", err)
	}

	t.Run("client IP is extracted and X-Forwarded-For is set", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/prometheus/api/v1/query", nil)
		req.RemoteAddr = "1.2.3.4:12345"
		req.Header.Set("Authorization", "Bearer token")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if got := req.Header.Get(clientip.XForwardedForHeader); got != "1.2.3.4" {
			t.Errorf("X-Forwarded-For = %q, want %q", got, "1.2.3.4")
		}
	})

	t.Run("existing X-Forwarded-For header is preserved", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/prometheus/api/v1/query", nil)
		req.RemoteAddr = "1.2.3.4:12345"
		req.Header.Set(clientip.XForwardedForHeader, "5.6.7.8")
		req.Header.Set("Authorization", "Bearer token")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if got := req.Header.Get(clientip.XForwardedForHeader); got != "5.6.7.8" {
			t.Errorf("X-Forwarded-For = %q, want %q", got, "5.6.7.8")
		}
	})
}
