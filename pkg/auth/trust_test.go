// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTrustAuthenticator_WithOrgID(t *testing.T) {
	auth := NewTrust()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Scope-OrgID", "tenant1")

	result, err := auth.Authenticate(req.Context(), req, ScopeMetricsRead)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got := result.TenantIDs(); len(got) != 1 || got[0] != "tenant1" {
		t.Errorf("expected TenantIDs=[tenant1], got %v", got)
	}
}

func TestTrustAuthenticator_MissingHeader(t *testing.T) {
	auth := NewTrust()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	_, err := auth.Authenticate(req.Context(), req, ScopeMetricsRead)
	if !IsUnauthorized(err) {
		t.Errorf("expected ErrUnauthorized, got %v", err)
	}
}

// TestTrustAuthenticator_RejectsMetadata: dskit-style tenant metadata
// (e.g. "tenant1:product=k6") is not supported by db-auth-gateway. Even
// though dskit's ExtractOrgID returns the raw header verbatim, the Trust
// path must surface an ErrInvalidTenant rather than silently strip and
// accept the metadata. Locked in here so a future swap to a
// metadata-aware dskit helper (tenant.TenantID, tenant.TrimMetadata)
// would visibly fail this test.
func TestTrustAuthenticator_RejectsMetadata(t *testing.T) {
	cases := []string{
		"tenant1:product=k6",
		"tenant1:k=v|tenant2:k=v",
		"tenant1|tenant2:k=v",
	}
	for _, orgID := range cases {
		t.Run(orgID, func(t *testing.T) {
			a := NewTrust()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("X-Scope-OrgID", orgID)

			_, err := a.Authenticate(req.Context(), req, ScopeMetricsRead)
			if !errors.Is(err, ErrInvalidTenant) {
				t.Errorf("expected ErrInvalidTenant, got %v", err)
			}
		})
	}
}

func TestTrustAuthenticator_AnyScope(t *testing.T) {
	auth := NewTrust()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Scope-OrgID", "my-tenant")

	// TrustAuthenticator ignores scope — any scope should succeed with the header present.
	for _, scope := range []string{ScopeMetricsRead, ScopeMetricsWrite, ScopeRulesRead, ""} {
		result, err := auth.Authenticate(req.Context(), req, scope)
		if err != nil {
			t.Errorf("scope=%q: unexpected error %v", scope, err)
		}
		if got := result.TenantIDs(); len(got) != 1 || got[0] != "my-tenant" {
			t.Errorf("scope=%q: got TenantIDs=%v, want [my-tenant]", scope, got)
		}
	}
}
