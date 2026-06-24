// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/grafana/db-auth-gateway/pkg/middleware/clientip"
)

func newForwardServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv, srv.URL
}

func TestForwardAuthenticator_200(t *testing.T) {
	_, url := newForwardServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Scope-OrgID", "t1|t2")
		w.Header().Set("X-Prom-Label-Policy", `t1:%7Benv%3D%22prod%22%7D`)
		w.WriteHeader(http.StatusOK)
	})

	a := NewForwardAuthenticator(ForwardConfig{URL: url, Timeout: 5 * time.Second})
	req, _ := http.NewRequest(http.MethodGet, "/metrics", nil)
	req.RemoteAddr = "1.2.3.4:5678"

	result, err := a.Authenticate(context.Background(), req, ScopeMetricsRead)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	require.ElementsMatch(t, []string{"t1", "t2"}, result.TenantIDs())
	require.ElementsMatch(t, []LabelPolicy{{TenantID: "t1", Selector: `{env="prod"}`}}, result.LabelPolicies())
}

func TestForwardAuthenticator_401(t *testing.T) {
	_, url := newForwardServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})

	a := NewForwardAuthenticator(ForwardConfig{URL: url, Timeout: 5 * time.Second})
	req, _ := http.NewRequest(http.MethodGet, "/metrics", nil)
	req.RemoteAddr = "1.2.3.4:5678"

	_, err := a.Authenticate(context.Background(), req, ScopeMetricsRead)
	if !IsUnauthorized(err) {
		t.Errorf("expected unauthorized error, got %v", err)
	}
}

func TestForwardAuthenticator_403(t *testing.T) {
	_, url := newForwardServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})

	a := NewForwardAuthenticator(ForwardConfig{URL: url, Timeout: 5 * time.Second})
	req, _ := http.NewRequest(http.MethodGet, "/metrics", nil)
	req.RemoteAddr = "1.2.3.4:5678"

	_, err := a.Authenticate(context.Background(), req, ScopeMetricsRead)
	if !IsForbidden(err) {
		t.Errorf("expected forbidden error, got %v", err)
	}
}

func TestForwardAuthenticator_Timeout(t *testing.T) {
	_, url := newForwardServer(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})

	a := NewForwardAuthenticator(ForwardConfig{URL: url, Timeout: 1 * time.Millisecond})
	req, _ := http.NewRequest(http.MethodGet, "/metrics", nil)
	req.RemoteAddr = "1.2.3.4:5678"

	_, err := a.Authenticate(context.Background(), req, ScopeMetricsRead)
	if err == nil {
		t.Error("expected error due to timeout, got nil")
	}
}

func TestForwardAuthenticator_UnexpectedStatus(t *testing.T) {
	_, url := newForwardServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	a := NewForwardAuthenticator(ForwardConfig{URL: url, Timeout: 5 * time.Second})
	req, _ := http.NewRequest(http.MethodGet, "/metrics", nil)
	req.RemoteAddr = "1.2.3.4:5678"

	_, err := a.Authenticate(context.Background(), req, ScopeMetricsRead)
	if err == nil {
		t.Error("expected error for 500 status")
	}
	if IsUnauthorized(err) || IsForbidden(err) {
		t.Errorf("expected generic error, got %v", err)
	}
}

func TestForwardAuthenticator_CacheHit(t *testing.T) {
	var callCount atomic.Int32
	_, url := newForwardServer(t, func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.Header().Set("X-Scope-OrgID", "t1")
		w.WriteHeader(http.StatusOK)
	})

	a := NewForwardAuthenticator(ForwardConfig{URL: url, Timeout: 5 * time.Second, CacheTTL: time.Minute})
	req, _ := http.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer token123")
	req.RemoteAddr = "1.2.3.4:5678"

	_, err := a.Authenticate(context.Background(), req, ScopeMetricsRead)
	if err != nil {
		t.Fatalf("first call: unexpected error: %v", err)
	}
	_, err = a.Authenticate(context.Background(), req, ScopeMetricsRead)
	if err != nil {
		t.Fatalf("second call: unexpected error: %v", err)
	}

	if callCount.Load() != 1 {
		t.Errorf("expected 1 server call due to cache hit, got %d", callCount.Load())
	}
}

func TestForwardAuthenticator_CacheDisabled(t *testing.T) {
	var callCount atomic.Int32
	_, url := newForwardServer(t, func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.Header().Set("X-Scope-OrgID", "t1")
		w.WriteHeader(http.StatusOK)
	})

	a := NewForwardAuthenticator(ForwardConfig{URL: url, Timeout: 5 * time.Second, CacheTTL: 0})
	req, _ := http.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer token123")
	req.RemoteAddr = "1.2.3.4:5678"

	_, err := a.Authenticate(context.Background(), req, ScopeMetricsRead)
	if err != nil {
		t.Fatalf("first call: unexpected error: %v", err)
	}
	_, err = a.Authenticate(context.Background(), req, ScopeMetricsRead)
	if err != nil {
		t.Fatalf("second call: unexpected error: %v", err)
	}

	if callCount.Load() != 2 {
		t.Errorf("expected 2 server calls with cache disabled, got %d", callCount.Load())
	}
}

func TestForwardAuthenticator_MissingOrgID(t *testing.T) {
	_, url := newForwardServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	a := NewForwardAuthenticator(ForwardConfig{URL: url, Timeout: 5 * time.Second})
	req, _ := http.NewRequest(http.MethodGet, "/metrics", nil)
	req.RemoteAddr = "1.2.3.4:5678"

	_, err := a.Authenticate(context.Background(), req, ScopeMetricsRead)
	require.ErrorIs(t, err, ErrInvalidTenant)
}

func TestForwardAuthenticator_EmptyOrgIDEntry(t *testing.T) {
	_, url := newForwardServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Scope-OrgID", "t1||t2")
		w.WriteHeader(http.StatusOK)
	})

	a := NewForwardAuthenticator(ForwardConfig{URL: url, Timeout: 5 * time.Second})
	req, _ := http.NewRequest(http.MethodGet, "/metrics", nil)
	req.RemoteAddr = "1.2.3.4:5678"

	_, err := a.Authenticate(context.Background(), req, ScopeMetricsRead)
	require.ErrorIs(t, err, ErrInvalidTenant)
}

func TestForwardAuthenticator_MalformedLabelPolicy(t *testing.T) {
	_, url := newForwardServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Scope-OrgID", "t1")
		w.Header().Set("X-Prom-Label-Policy", "notavalidpolicy")
		w.WriteHeader(http.StatusOK)
	})

	a := NewForwardAuthenticator(ForwardConfig{URL: url, Timeout: 5 * time.Second})
	req, _ := http.NewRequest(http.MethodGet, "/metrics", nil)
	req.RemoteAddr = "1.2.3.4:5678"

	_, err := a.Authenticate(context.Background(), req, ScopeMetricsRead)
	require.ErrorIs(t, err, ErrInvalidPolicy)
}

func TestForwardAuthenticator_PolicyUnknownTenant(t *testing.T) {
	_, url := newForwardServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Scope-OrgID", "t1")
		w.Header().Set("X-Prom-Label-Policy", `t2:%7Benv%3D%22prod%22%7D`)
		w.WriteHeader(http.StatusOK)
	})

	a := NewForwardAuthenticator(ForwardConfig{URL: url, Timeout: 5 * time.Second})
	req, _ := http.NewRequest(http.MethodGet, "/metrics", nil)
	req.RemoteAddr = "1.2.3.4:5678"

	_, err := a.Authenticate(context.Background(), req, ScopeMetricsRead)
	require.ErrorIs(t, err, ErrInvalidTenant)
}

func TestForwardAuthenticator_ExtraResponseHeadersPassthrough(t *testing.T) {
	_, url := newForwardServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Scope-OrgID", "t1")
		w.Header().Set("X-Custom-User-Id", "u-42")
		w.Header().Add("X-Audit-Tag", "tag-a")
		w.Header().Add("X-Audit-Tag", "tag-b")
		// Transport-level headers that must NOT be passed through.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
	})

	a := NewForwardAuthenticator(ForwardConfig{URL: url, Timeout: 5 * time.Second})
	req, _ := http.NewRequest(http.MethodGet, "/metrics", nil)
	req.RemoteAddr = "1.2.3.4:5678"

	result, err := a.Authenticate(context.Background(), req, ScopeMetricsRead)
	require.NoError(t, err)

	extras := result.ExtraHeaders()
	require.Equal(t, "u-42", extras.Get("X-Custom-User-Id"))
	require.ElementsMatch(t, []string{"tag-a", "tag-b"}, extras.Values("X-Audit-Tag"))

	// Transport headers Go's server adds (or that describe the auth response
	// body) must be stripped so they don't corrupt the proxied upstream request.
	for _, k := range []string{"Content-Length", "Content-Type", "Date"} {
		require.Empty(t, extras.Values(k), "expected %s to be stripped", k)
	}
}

func TestForwardAuthenticator_HeadersForwarded(t *testing.T) {
	var gotAuth, gotXFF string
	_, url := newForwardServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotXFF = r.Header.Get(clientip.XForwardedForHeader)
		w.Header().Set("X-Scope-OrgID", "t1")
		w.WriteHeader(http.StatusOK)
	})

	a := NewForwardAuthenticator(ForwardConfig{URL: url, Timeout: 5 * time.Second})
	req, _ := http.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer mytoken")
	req.RemoteAddr = "10.0.0.1:9090"

	_, err := a.Authenticate(context.Background(), req, ScopeMetricsRead)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAuth != "Bearer mytoken" {
		t.Errorf("expected Authorization header 'Bearer mytoken', got %q", gotAuth)
	}
	if gotXFF != "10.0.0.1" {
		t.Errorf("expected X-Forwarded-For '10.0.0.1', got %q", gotXFF)
	}
}
