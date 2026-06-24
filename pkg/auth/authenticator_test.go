// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var _ Authenticator = NoopAuthenticator{}

func TestIsUnauthorized(t *testing.T) {
	if !IsUnauthorized(ErrUnauthorized) {
		t.Error("IsUnauthorized(ErrUnauthorized) should be true")
	}

	wrapped := fmt.Errorf("wrapped: %w", ErrUnauthorized)
	if !IsUnauthorized(wrapped) {
		t.Error("IsUnauthorized(wrapped ErrUnauthorized) should be true")
	}

	if IsUnauthorized(ErrForbidden) {
		t.Error("IsUnauthorized(ErrForbidden) should be false")
	}
}

func TestIsForbidden(t *testing.T) {
	if !IsForbidden(ErrForbidden) {
		t.Error("IsForbidden(ErrForbidden) should be true")
	}

	wrapped := fmt.Errorf("wrapped: %w", ErrForbidden)
	if !IsForbidden(wrapped) {
		t.Error("IsForbidden(wrapped ErrForbidden) should be true")
	}

	if IsForbidden(ErrUnauthorized) {
		t.Error("IsForbidden(ErrUnauthorized) should be false")
	}
}

// TestNewAuthResult_Success exercises every shape of well-formed input the
// constructor must accept.
func TestNewAuthResult_Success(t *testing.T) {
	tests := []struct {
		name          string
		orgIDValues   []string
		policyValues  []string
		extra         http.Header
		wantTenantIDs []string
		wantPolicies  []LabelPolicy
	}{
		{
			name:          "single tenant, no policies",
			orgIDValues:   []string{"tenant1"},
			wantTenantIDs: []string{"tenant1"},
		},
		{
			name:          "pipe-separated tenants, no policies",
			orgIDValues:   []string{"tenant1|tenant2"},
			wantTenantIDs: []string{"tenant1", "tenant2"},
		},
		{
			name:          "single tenant, single policy",
			orgIDValues:   []string{"t1"},
			policyValues:  []string{`t1:%7Benv%3D%22prod%22%7D`},
			wantTenantIDs: []string{"t1"},
			wantPolicies:  []LabelPolicy{{TenantID: "t1", Selector: `{env="prod"}`}},
		},
		{
			name:          "comma-separated policies",
			orgIDValues:   []string{"t1|t2"},
			policyValues:  []string{`t1:%7Benv%3D%22prod%22%7D,t2:%7Benv%3D%22staging%22%7D`},
			wantTenantIDs: []string{"t1", "t2"},
			wantPolicies: []LabelPolicy{
				{TenantID: "t1", Selector: `{env="prod"}`},
				{TenantID: "t2", Selector: `{env="staging"}`},
			},
		},
		{
			name:          "repeated label-policy headers",
			orgIDValues:   []string{"t1|t2"},
			policyValues:  []string{`t1:%7Benv%3D%22prod%22%7D`, `t2:%7Benv%3D%22staging%22%7D`},
			wantTenantIDs: []string{"t1", "t2"},
			wantPolicies: []LabelPolicy{
				{TenantID: "t1", Selector: `{env="prod"}`},
				{TenantID: "t2", Selector: `{env="staging"}`},
			},
		},
		{
			name:        "OR'd policies per tenant",
			orgIDValues: []string{"t1"},
			policyValues: []string{
				`t1:%7Benv=%22dev%22%2C%20classification!=%22secret%22%7D`,
				`t1:%7Bclassification=~%22secre.*%22%7D`,
			},
			wantTenantIDs: []string{"t1"},
			wantPolicies: []LabelPolicy{
				{TenantID: "t1", Selector: `{env="dev", classification!="secret"}`},
				{TenantID: "t1", Selector: `{classification=~"secre.*"}`},
			},
		},
		{
			name:          "with extra headers",
			orgIDValues:   []string{"t1"},
			extra:         http.Header{"X-Custom": {"value"}},
			wantTenantIDs: []string{"t1"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := NewAuthResult(tc.orgIDValues, tc.policyValues, tc.extra)
			require.NoError(t, err)
			assert.Equal(t, tc.wantTenantIDs, result.TenantIDs())
			assert.Equal(t, tc.wantPolicies, result.LabelPolicies())
			assert.Equal(t, tc.extra, result.ExtraHeaders())
		})
	}
}

// TestNewAuthResult_TenantErrors covers structurally-malformed X-Scope-OrgID
// inputs and the empty-input case. All must wrap ErrInvalidTenant.
func TestNewAuthResult_TenantErrors(t *testing.T) {
	tests := []struct {
		name        string
		orgIDValues []string
	}{
		{"nil orgID values", nil},
		{"empty orgID values slice", []string{}},
		{"single empty header value", []string{""}},
		{"trailing pipe", []string{"tenant1|"}},
		{"double pipe", []string{"tenant1||tenant2"}},
		{"comma not valid", []string{"tenant1,tenant2"}},
		{"slash not valid", []string{"tenant/escape"}},
		{"space not valid", []string{"tenant 1"}},
		{"unsafe . path component", []string{"."}},
		{"unsafe .. path component", []string{".."}},
		{"exceeds 150-byte limit", []string{strings.Repeat("a", 151)}},
		{"duplicate tenant", []string{"tenant1|tenant1"}},
		{"repeated header (must not be set twice)", []string{"tenant1", "tenant2"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewAuthResult(tc.orgIDValues, nil, nil)
			require.ErrorIs(t, err, ErrInvalidTenant, "input: %q", tc.orgIDValues)
		})
	}
}

// TestNewAuthResult_PolicyErrors covers structurally-malformed
// X-Prom-Label-Policy inputs. All must wrap ErrInvalidPolicy.
func TestNewAuthResult_PolicyErrors(t *testing.T) {
	tests := []struct {
		name         string
		policyValues []string
	}{
		{"empty header value", []string{""}},
		{"trailing comma", []string{`t1:%7Benv%3D%22prod%22%7D,`}},
		{"double comma", []string{`t1:%7Benv%3D%22prod%22%7D,,t1:%7Benv%3D%22staging%22%7D`}},
		{"unencoded comma in selector", []string{`t1:{env="prod", cluster="us-east"}`}},
		{"empty tenant ID", []string{`:%7Benv%3D%22prod%22%7D`}},
		{"missing colon", []string{`notavalidpolicy`}},
		{"bad percent encoding", []string{`t1:%zz`}},
		{"invalid PromQL selector", []string{`t1:%7Benv%3D%7D`}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewAuthResult([]string{"t1"}, tc.policyValues, nil)
			require.ErrorIs(t, err, ErrInvalidPolicy, "input: %q", tc.policyValues)
		})
	}
}

// TestNewAuthResult_RejectsMetadata locks in the policy that db-auth-gateway
// does not accept dskit-style tenant metadata. dskit ships helpers
// (tenant.TrimMetadata, tenant.TenantID, tenant.TenantIDs) that silently strip
// metadata before validating; this gateway intentionally bypasses those and
// relies on tenant.ValidTenantID rejecting the ':' separator. These cases
// guard against a future refactor accidentally calling a metadata-aware
// helper and silently accepting `tenant:k=v`-shaped IDs.
func TestNewAuthResult_RejectsMetadata(t *testing.T) {
	tests := []struct {
		name        string
		orgIDValues []string
	}{
		{"single tenant with metadata", []string{"tenant1:product=k6"}},
		{"multiple metadata key-value pairs", []string{"tenant1:k1=v1:k2=v2"}},
		{"first of two has metadata", []string{"tenant1:k=v|tenant2"}},
		{"second of two has metadata", []string{"tenant1|tenant2:k=v"}},
		{"both have metadata", []string{"tenant1:k=v|tenant2:k=v"}},
		{"trailing bare colon", []string{"tenant1:"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewAuthResult(tc.orgIDValues, nil, nil)
			require.ErrorIs(t, err, ErrInvalidTenant, "input: %q", tc.orgIDValues)
		})
	}
}

// TestNewAuthResult_UnauthorisedTenantPolicy: a policy whose TenantID is
// not in TenantIDs must be rejected.
func TestNewAuthResult_UnauthorisedTenantPolicy(t *testing.T) {
	_, err := NewAuthResult(
		[]string{"t1"},
		[]string{`t1:%7Benv%3D%22prod%22%7D`, `t2:%7Benv%3D%22staging%22%7D`},
		nil,
	)
	require.ErrorIs(t, err, ErrInvalidTenant)
}

// TestAuthResultHeaders covers the full matrix: tenant joining, label-policy
// emission, ExtraHeaders forwarding, and extras-vs-derived precedence.
func TestAuthResultHeaders(t *testing.T) {
	t.Run("single tenant, no policies, no extras", func(t *testing.T) {
		result, err := NewAuthResult([]string{"t1"}, nil, nil)
		require.NoError(t, err)

		h := AuthResultHeaders(result)
		assert.Equal(t, "t1", h.Get("X-Scope-OrgID"))
		assert.Empty(t, h.Values("X-Prom-Label-Policy"))
	})

	t.Run("multi-tenant pipe-joined", func(t *testing.T) {
		result, err := NewAuthResult([]string{"t1|t2"}, nil, nil)
		require.NoError(t, err)

		h := AuthResultHeaders(result)
		assert.Equal(t, "t1|t2", h.Get("X-Scope-OrgID"))
	})

	t.Run("emits one label-policy header per policy", func(t *testing.T) {
		result, err := NewAuthResult(
			[]string{"t1|t2"},
			[]string{`t1:%7Benv%3D%22prod%22%7D`, `t2:%7Benv%3D%22staging%22%7D`},
			nil,
		)
		require.NoError(t, err)

		h := AuthResultHeaders(result)
		got := h.Values("X-Prom-Label-Policy")
		require.Len(t, got, 2)
		assert.Equal(t, `t1:%7Benv=%22prod%22%7D`, got[0])
		assert.Equal(t, `t2:%7Benv=%22staging%22%7D`, got[1])
	})

	t.Run("policy order preserved", func(t *testing.T) {
		result, err := NewAuthResult(
			[]string{"t1|t2"},
			[]string{`t2:%7Benv%3D%22staging%22%7D`, `t1:%7Benv%3D%22prod%22%7D`},
			nil,
		)
		require.NoError(t, err)

		got := AuthResultHeaders(result).Values("X-Prom-Label-Policy")
		require.Len(t, got, 2)
		assert.Equal(t, `t2:%7Benv=%22staging%22%7D`, got[0])
		assert.Equal(t, `t1:%7Benv=%22prod%22%7D`, got[1])
	})

	t.Run("extras pass through", func(t *testing.T) {
		extras := http.Header{
			"X-Custom-1": {"a"},
			"X-Custom-2": {"b", "c"},
		}
		result, err := NewAuthResult([]string{"t1"}, nil, extras)
		require.NoError(t, err)

		h := AuthResultHeaders(result)
		assert.Equal(t, "a", h.Get("X-Custom-1"))
		assert.Equal(t, []string{"b", "c"}, h.Values("X-Custom-2"))
	})

	t.Run("derived headers override extras on key collision", func(t *testing.T) {
		extras := http.Header{
			"X-Scope-OrgID":       {"hostile"},
			"X-Prom-Label-Policy": {"hostile:policy"},
		}
		result, err := NewAuthResult(
			[]string{"t1"},
			[]string{`t1:%7Benv%3D%22prod%22%7D`},
			extras,
		)
		require.NoError(t, err)

		h := AuthResultHeaders(result)
		assert.Equal(t, "t1", h.Get("X-Scope-OrgID"))
		got := h.Values("X-Prom-Label-Policy")
		require.Len(t, got, 1)
		assert.Equal(t, `t1:%7Benv=%22prod%22%7D`, got[0])
	})

	t.Run("round-trip through NewAuthResult", func(t *testing.T) {
		original, err := NewAuthResult(
			[]string{"t1|t2"},
			[]string{`t1:%7Benv%3D%22prod%22%7D`, `t2:%7Benv%3D%22staging%22%7D`},
			nil,
		)
		require.NoError(t, err)

		h := AuthResultHeaders(original)
		decoded, err := NewAuthResult(
			h.Values("X-Scope-OrgID"),
			h.Values("X-Prom-Label-Policy"),
			nil,
		)
		require.NoError(t, err)

		assert.Equal(t, original.TenantIDs(), decoded.TenantIDs())
		assert.Equal(t, original.LabelPolicies(), decoded.LabelPolicies())
	})
}

func TestNoopAuthenticator(t *testing.T) {
	noop := NoopAuthenticator{}
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	_, err := noop.Authenticate(context.Background(), req, ScopeMetricsRead)
	require.True(t, IsForbidden(err))
}
