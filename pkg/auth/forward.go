// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/grafana/dskit/user"
	gocache "github.com/patrickmn/go-cache"

	"github.com/grafana/db-auth-gateway/pkg/middleware/clientip"
)

// ForwardConfig holds configuration for the ForwardAuthenticator.
type ForwardConfig struct {
	URL      string
	Timeout  time.Duration
	CacheTTL time.Duration
}

// RegisterFlags registers CLI flags for ForwardConfig.
func (c *ForwardConfig) RegisterFlags(f *flag.FlagSet) {
	f.StringVar(&c.URL, "forward-auth.url", "", "URL of the external auth service (required when auth.type=forward_auth)")
	f.DurationVar(&c.Timeout, "forward-auth.timeout", 5*time.Second, "Timeout for requests to the external auth service")
	f.DurationVar(&c.CacheTTL, "forward-auth.cache-ttl", 0, "TTL for caching auth results (0 disables caching)")
}

// ForwardAuthenticator calls an external HTTP service to authenticate requests.
type ForwardAuthenticator struct {
	cfg    ForwardConfig
	client *http.Client
	cache  *gocache.Cache
}

// NewForwardAuthenticator creates a new ForwardAuthenticator.
func NewForwardAuthenticator(cfg ForwardConfig) *ForwardAuthenticator {
	a := &ForwardAuthenticator{
		cfg: cfg,
		client: &http.Client{
			Timeout: cfg.Timeout,
		},
	}
	if cfg.CacheTTL > 0 {
		a.cache = gocache.New(cfg.CacheTTL, cfg.CacheTTL*2)
	}
	return a
}

// responseHeadersToStrip lists headers added by Go's HTTP server stack (or
// describing the auth response body) that must not be forwarded onto the
// upstream proxied request. X-Scope-OrgID and X-Prom-Label-Policy are not
// stripped here because AuthResultHeaders overwrites them with the parsed
// versions.
var responseHeadersToStrip = []string{
	"Content-Length",
	"Content-Type",
	"Content-Encoding",
	"Transfer-Encoding",
	"Date",
	"Server",
}

type authRequestBody struct {
	Path          string `json:"path"`
	Method        string `json:"method"`
	RequiredScope string `json:"requiredScope"`
}

// Authenticate calls the external auth service and returns an AuthResult.
func (a *ForwardAuthenticator) Authenticate(ctx context.Context, req *http.Request, requiredScope string) (AuthResult, error) {
	cacheKey := req.Header.Get("Authorization") + "|" + requiredScope

	if a.cache != nil {
		if cached, found := a.cache.Get(cacheKey); found {
			return cached.(AuthResult), nil
		}
	}

	bodyBytes, err := json.Marshal(authRequestBody{
		Path:          req.URL.Path,
		Method:        req.Method,
		RequiredScope: requiredScope,
	})
	if err != nil {
		return AuthResult{}, fmt.Errorf("forward auth: marshal request body: %w", err)
	}

	authReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.URL, bytes.NewReader(bodyBytes))
	if err != nil {
		return AuthResult{}, fmt.Errorf("forward auth: create request: %w", err)
	}

	// Copy all original request headers.
	for key, vals := range req.Header {
		for _, v := range vals {
			authReq.Header.Add(key, v)
		}
	}

	// Set X-Forwarded-For from the resolved client IP (set by clientIPMiddleware if enabled),
	// falling back to the raw TCP remote address.
	clientIP := clientip.ExtractClientIP(ctx)
	if clientIP == "" {
		host, _, err := net.SplitHostPort(req.RemoteAddr)
		if err != nil {
			host = req.RemoteAddr
		}
		clientIP = host
	}
	authReq.Header.Set(clientip.XForwardedForHeader, clientIP)

	// Set Content-Type after copying headers so it isn't overwritten.
	authReq.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(authReq)
	if err != nil {
		return AuthResult{}, fmt.Errorf("forward auth: request failed: %w", err)
	}
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return AuthResult{}, ErrUnauthorized
	case http.StatusForbidden:
		return AuthResult{}, ErrForbidden
	case http.StatusOK:
		// handled below
	default:
		return AuthResult{}, fmt.Errorf("forward auth: unexpected status %d", resp.StatusCode)
	}

	extra := resp.Header.Clone()
	for _, k := range responseHeadersToStrip {
		extra.Del(k)
	}
	result, err := NewAuthResult(
		resp.Header.Values(user.OrgIDHeaderName),
		resp.Header.Values(LabelPolicyHeader),
		extra,
	)
	if err != nil {
		return AuthResult{}, fmt.Errorf("forward auth: %w", err)
	}

	if a.cache != nil {
		a.cache.Set(cacheKey, result, gocache.DefaultExpiration)
	}

	return result, nil
}
