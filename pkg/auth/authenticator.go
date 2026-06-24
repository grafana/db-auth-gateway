// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/grafana/dskit/tenant"
	"github.com/grafana/dskit/user"
	"github.com/prometheus/prometheus/promql/parser"
)

var ErrUnauthorized = errors.New("unauthorized")
var ErrForbidden = errors.New("forbidden")
var ErrInvalidPolicy = errors.New("invalid policy")
var ErrInvalidTenant = errors.New("invalid tenant")

const (
	ScopeMetricsRead   = "metrics:read"
	ScopeMetricsWrite  = "metrics:write"
	ScopeMetricsExport = "metrics:export"

	ScopeLogsRead   = "logs:read"
	ScopeLogsWrite  = "logs:write"
	ScopeLogsDelete = "logs:delete"

	ScopeTracesRead  = "traces:read"
	ScopeTracesWrite = "traces:write"

	ScopeProfilesRead  = "profiles:read"
	ScopeProfilesWrite = "profiles:write"

	ScopeRulesRead  = "rules:read"
	ScopeRulesWrite = "rules:write"

	ScopeAlertsRead  = "alerts:read"
	ScopeAlertsWrite = "alerts:write"
)

const LabelPolicyHeader = "X-Prom-Label-Policy"

type LabelPolicy struct {
	TenantID string
	Selector string
}

// AuthResult is the validated outcome of an authentication. Outside this
// package the only way to obtain a populated AuthResult is via NewAuthResult,
// which enforces the cross-tenant invariant: every LabelPolicies entry must
// reference a tenant that appears in TenantIDs.
type AuthResult struct {
	tenantIDs     []string
	labelPolicies []LabelPolicy
	extraHeaders  http.Header
}

func (r AuthResult) TenantIDs() []string          { return r.tenantIDs }
func (r AuthResult) LabelPolicies() []LabelPolicy { return r.labelPolicies }
func (r AuthResult) ExtraHeaders() http.Header    { return r.extraHeaders }

type Authenticator interface {
	Authenticate(ctx context.Context, req *http.Request, requiredScope string) (AuthResult, error)
}

func IsUnauthorized(err error) bool {
	return errors.Is(err, ErrUnauthorized)
}

func IsForbidden(err error) bool {
	return errors.Is(err, ErrForbidden)
}

// NewAuthResult parses the raw X-Scope-OrgID and X-Prom-Label-Policy header
// values, validates them structurally and against each other, and returns the
// resulting AuthResult. Any malformed input or any LabelPolicies entry that
// references a tenant not in TenantIDs is an error.
//
// orgIDHeaderValues must be the raw values returned by Header.Values(...);
// it is required to contain at least one non-empty tenant ID.
// labelPolicyHeaderValues may be nil or empty.
// extra is copied verbatim into the result and is emitted by AuthResultHeaders
// alongside the derived headers.
func NewAuthResult(
	orgIDHeaderValues []string,
	labelPolicyHeaderValues []string,
	extra http.Header,
) (AuthResult, error) {
	tenantIDs, err := parseTenantIDs(orgIDHeaderValues)
	if err != nil {
		return AuthResult{}, err
	}
	if len(tenantIDs) == 0 {
		return AuthResult{}, fmt.Errorf("no tenant ID specified: %w", ErrInvalidTenant)
	}

	policies, err := parseLabelPolicies(labelPolicyHeaderValues)
	if err != nil {
		return AuthResult{}, err
	}

	authorised := make(map[string]struct{}, len(tenantIDs))
	for _, id := range tenantIDs {
		authorised[id] = struct{}{}
	}
	for _, p := range policies {
		if _, ok := authorised[p.TenantID]; !ok {
			return AuthResult{}, fmt.Errorf("label policy tenant %q not in authorised tenant list: %w", p.TenantID, ErrInvalidTenant)
		}
	}

	return AuthResult{
		tenantIDs:     tenantIDs,
		labelPolicies: policies,
		extraHeaders:  extra,
	}, nil
}

// AuthResultHeaders returns the complete set of headers to apply to an
// outbound proxied request. The returned http.Header contains:
//   - X-Scope-OrgID (single value, pipe-joined per dskit convention)
//   - X-Prom-Label-Policy (one repeated value per LabelPolicy)
//   - Every key/value from ExtraHeaders
//
// If ExtraHeaders contains keys that collide with the derived headers, the
// derived headers take precedence (extras applied first, derived overwrite).
// The result uses canonical header keys; transports that require non-canonical
// keys (HTTP/2 / gRPC) must perform their own swap at the transport boundary.
func AuthResultHeaders(r AuthResult) http.Header {
	out := http.Header{}
	for k, vs := range r.extraHeaders {
		out[http.CanonicalHeaderKey(k)] = slices.Clone(vs)
	}
	out.Set(user.OrgIDHeaderName, tenant.JoinTenantIDs(r.tenantIDs))
	out.Del(LabelPolicyHeader)
	for _, p := range r.labelPolicies {
		out.Add(LabelPolicyHeader, p.TenantID+":"+url.PathEscape(p.Selector))
	}
	return out
}

// parseLabelPolicies parses X-Prom-Label-Policy header values into LabelPolicy entries.
// Each value may be comma-separated; each entry is "tenantID:<percent-encoded selector>".
// Any malformed entry causes an error to be returned.
func parseLabelPolicies(headerValues []string) ([]LabelPolicy, error) {
	var policies []LabelPolicy
	promqlParser := parser.NewParser(parser.Options{})
	for _, headerVal := range headerValues {
		for _, v := range strings.Split(headerVal, ",") {
			v = strings.TrimSpace(v)
			if v == "" {
				return nil, fmt.Errorf("auth: invalid X-Prom-Label-Policy: empty entry (stray comma): %w", ErrInvalidPolicy)
			}
			idx := strings.IndexByte(v, ':')
			if idx < 0 {
				return nil, fmt.Errorf("auth: invalid X-Prom-Label-Policy entry %q: missing ':': %w", v, ErrInvalidPolicy)
			}
			tenantID := v[:idx]
			if tenantID == "" {
				return nil, fmt.Errorf("auth: invalid X-Prom-Label-Policy entry %q: empty tenant ID: %w", v, ErrInvalidPolicy)
			}
			selector, err := url.PathUnescape(v[idx+1:])
			if err != nil {
				return nil, fmt.Errorf("auth: invalid X-Prom-Label-Policy entry %q: %w: %w", v, err, ErrInvalidPolicy)
			}
			if _, err := promqlParser.ParseMetricSelector(selector); err != nil {
				return nil, fmt.Errorf("auth: invalid X-Prom-Label-Policy entry %q: invalid selector: %w: %w", v, err, ErrInvalidPolicy)
			}
			policies = append(policies, LabelPolicy{
				TenantID: tenantID,
				Selector: selector,
			})
		}
	}
	return policies, nil
}

// parseTenantIDs parses X-Scope-OrgID header values into a string list.
// X-Scope-OrgID is a single pipe-separated header (dskit/backend-enterprise convention).
// Any malformed entry causes an error to be returned.
func parseTenantIDs(headerValues []string) ([]string, error) {
	if len(headerValues) > 1 {
		return nil, fmt.Errorf("X-Scope-OrgID must not be set more than once: %w", ErrInvalidTenant)
	}
	var tenantIDs []string
	for _, headerVal := range headerValues {
		for _, id := range strings.Split(headerVal, "|") {
			id = strings.TrimSpace(id)

			if id == "" {
				return nil, fmt.Errorf("X-Scope-OrgID contains an empty tenant ID: %w", ErrInvalidTenant)
			}
			if err := tenant.ValidTenantID(id); err != nil {
				return nil, fmt.Errorf("X-Scope-OrgID contains an invalid tenant ID: %w: %w", err, ErrInvalidTenant)
			}
			if slices.Contains(tenantIDs, id) {
				return nil, fmt.Errorf("X-Scope-OrgID contains duplicate tenant ID %q: %w", id, ErrInvalidTenant)
			}
			tenantIDs = append(tenantIDs, id)
		}
	}
	return tenantIDs, nil
}

type NoopAuthenticator struct{}

func (NoopAuthenticator) Authenticate(_ context.Context, _ *http.Request, _ string) (AuthResult, error) {
	return AuthResult{}, ErrForbidden
}
