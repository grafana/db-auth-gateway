// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"context"
	"net/http"

	"github.com/grafana/dskit/user"
)

// TrustAuthenticator trusts the X-Scope-OrgID header without validation.
// Only safe for in-cluster traffic.
type TrustAuthenticator struct{}

// NewTrust returns a TrustAuthenticator.
func NewTrust() TrustAuthenticator {
	return TrustAuthenticator{}
}

// Authenticate extracts the tenant ID from X-Scope-OrgID header.
// Returns ErrUnauthorized if the header is missing.
func (TrustAuthenticator) Authenticate(_ context.Context, req *http.Request, _ string) (AuthResult, error) {
	_, ctx, err := user.ExtractOrgIDFromHTTPRequest(req)
	if err != nil {
		return AuthResult{}, ErrUnauthorized
	}
	orgID, err := user.ExtractOrgID(ctx)
	if err != nil {
		return AuthResult{}, ErrUnauthorized
	}
	return NewAuthResult([]string{orgID}, nil, nil)
}
