// SPDX-License-Identifier: AGPL-3.0-only

package gateway

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/grafana/dskit/user"
	"go.yaml.in/yaml/v3"

	"github.com/grafana/db-auth-gateway/pkg/router"
)

const httpRetryAfter = "Retry-After"

var (
	errInvalidRetryAfter                  = errors.New("invalid retry-after")
	errInvalidResponseCodeForRequestBlock = errors.New("only allow 4xx and 5xx http response code for blocks_writes and block_reads")
)

// MimirGlobalOverrides defines Mimir-specific global (applied to all tenants) overrides.
type MimirGlobalOverrides struct {
	mimirOverrides `yaml:",inline"`
}

func (l *MimirGlobalOverrides) UnmarshalYAML(value *yaml.Node) error {
	return l.unmarshalYAML(value, defaultMimirGlobalOverrides.mimirOverrides)
}

// MimirTenantOverrides defines Mimir-specific per-tenant overrides.
type MimirTenantOverrides struct {
	mimirOverrides `yaml:",inline"`
}

func (l *MimirTenantOverrides) UnmarshalYAML(value *yaml.Node) error {
	return l.unmarshalYAML(value, defaultMimirTenantOverrides.mimirOverrides)
}

// defaultMimirGlobalOverrides are defaults for rejecting requests for all tenants.
// Note the use of 5XX errors since this is intended to be used as part of mitigation
// of an incident (we want requests to be retried later).
var defaultMimirGlobalOverrides = MimirGlobalOverrides{mimirOverrides{
	BlockWrites:                       nil, // nil is false for global overrides
	BlockWritesMessage:                "Writes are temporarily disabled. Please retry later.",
	BlockWritesHTTPResponseCode:       http.StatusServiceUnavailable,
	BlockWritesHTTPRetryAfter:         "",
	BlockReads:                        nil, // nil is false for global overrides
	BlockReadsMessage:                 "Reads are temporarily disabled. Please retry later.",
	BlockReadsHTTPResponseCode:        http.StatusServiceUnavailable,
	BlockReadsHTTPRetryAfter:          "",
	BlockRuler:                        nil, // nil is false for global overrides
	BlockRulerMessage:                 "Ruler API is temporarily disabled. Please retry later.",
	BlockRulerHTTPResponseCode:        http.StatusServiceUnavailable,
	BlockRulerHTTPRetryAfter:          "",
	BlockAggregations:                 nil, // nil is false for global overrides
	BlockAggregationsMessage:          "Aggregations API is temporarily disabled. Please retry later.",
	BlockAggregationsHTTPResponseCode: http.StatusServiceUnavailable,
	BlockAggregationsHTTPRetryAfter:   "",
}}

// defaultMimirTenantOverrides are defaults for rejecting requests for specific tenants.
// Note the use of 4XX errors since this is intended to be used to prevent malicious or
// badly behaved clients from causing issues.
var defaultMimirTenantOverrides = MimirTenantOverrides{mimirOverrides{
	BlockWrites:                       nil, // nil means to use the global value
	BlockWritesMessage:                "Writes to this Hosted Metrics instance are disabled. Please contact support.",
	BlockWritesHTTPResponseCode:       http.StatusUnauthorized,
	BlockWritesHTTPRetryAfter:         "",
	BlockReads:                        nil, // nil means to use the global value
	BlockReadsMessage:                 "Reads from this Hosted Metrics instance are disabled. Please contact support.",
	BlockReadsHTTPResponseCode:        http.StatusUnauthorized,
	BlockReadsHTTPRetryAfter:          "",
	BlockRuler:                        nil, // nil means to use the global value
	BlockRulerMessage:                 "Ruler API on this Hosted Metrics instance is disabled. Please contact support.",
	BlockRulerHTTPResponseCode:        http.StatusUnauthorized,
	BlockRulerHTTPRetryAfter:          "",
	BlockAggregations:                 nil, // nil means to use the global value
	BlockAggregationsMessage:          "Aggregations API on this Hosted Metrics instance is disabled. Please contact support.",
	BlockAggregationsHTTPResponseCode: http.StatusUnauthorized,
	BlockAggregationsHTTPRetryAfter:   "",
}}

type mimirOverrides struct {
	BlockWrites                 *bool  `yaml:"block_writes" json:"block_writes"`
	BlockWritesMessage          string `yaml:"block_writes_message" json:"block_writes_message"`
	BlockWritesHTTPResponseCode int    `yaml:"block_writes_http_response_code" json:"block_writes_http_response_code"`
	// See https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Retry-After
	BlockWritesHTTPRetryAfter string `yaml:"block_writes_http_retry_after" json:"block_writes_http_retry_after"`

	BlockReads                 *bool  `yaml:"block_reads" json:"block_reads"`
	BlockReadsMessage          string `yaml:"block_reads_message" json:"block_reads_message"`
	BlockReadsHTTPResponseCode int    `yaml:"block_reads_http_response_code" json:"block_reads_http_response_code"`
	// See https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Retry-After
	BlockReadsHTTPRetryAfter string `yaml:"block_reads_http_retry_after" json:"block_reads_http_retry_after"`

	BlockRuler                 *bool  `yaml:"block_ruler" json:"block_ruler"`
	BlockRulerMessage          string `yaml:"block_ruler_message" json:"block_ruler_message"`
	BlockRulerHTTPResponseCode int    `yaml:"block_ruler_http_response_code" json:"block_ruler_http_response_code"`
	// See https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Retry-After
	BlockRulerHTTPRetryAfter string `yaml:"block_ruler_http_retry_after" json:"block_ruler_http_retry_after"`

	BlockAggregations                 *bool  `yaml:"block_aggregations" json:"block_aggregations"`
	BlockAggregationsMessage          string `yaml:"block_aggregations_message" json:"block_aggregations_message"`
	BlockAggregationsHTTPResponseCode int    `yaml:"block_aggregations_http_response_code" json:"block_aggregations_http_response_code"`
	// See https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Retry-After
	BlockAggregationsHTTPRetryAfter string `yaml:"block_aggregations_http_retry_after" json:"block_aggregations_http_retry_after"`
}

func (l *mimirOverrides) unmarshalYAML(value *yaml.Node, base mimirOverrides) error {
	*l = base
	type plain mimirOverrides
	if err := value.Decode((*plain)(l)); err != nil {
		return err
	}

	if l.BlockWritesHTTPResponseCode/100 != 4 && l.BlockWritesHTTPResponseCode/100 != 5 {
		return errInvalidResponseCodeForRequestBlock
	}
	if l.BlockReadsHTTPResponseCode/100 != 4 && l.BlockReadsHTTPResponseCode/100 != 5 {
		return errInvalidResponseCodeForRequestBlock
	}
	if l.BlockRulerHTTPResponseCode/100 != 4 && l.BlockRulerHTTPResponseCode/100 != 5 {
		return errInvalidResponseCodeForRequestBlock
	}
	if l.BlockAggregationsHTTPResponseCode/100 != 4 && l.BlockAggregationsHTTPResponseCode/100 != 5 {
		return errInvalidResponseCodeForRequestBlock
	}

	var err error

	if l.BlockReadsHTTPRetryAfter, err = sanitizeHTTPRetryAfter(l.BlockReadsHTTPRetryAfter); err != nil {
		return err
	}
	if l.BlockWritesHTTPRetryAfter, err = sanitizeHTTPRetryAfter(l.BlockWritesHTTPRetryAfter); err != nil {
		return err
	}
	if l.BlockRulerHTTPRetryAfter, err = sanitizeHTTPRetryAfter(l.BlockRulerHTTPRetryAfter); err != nil {
		return err
	}
	if l.BlockAggregationsHTTPRetryAfter, err = sanitizeHTTPRetryAfter(l.BlockAggregationsHTTPRetryAfter); err != nil {
		return err
	}

	return nil
}

// sanitizeHTTPRetryAfter sanitizes http-retry as second or as time. If it couldn't be parsed as any of those two, this will return error.
func sanitizeHTTPRetryAfter(rawHTTPRetryAfter string) (string, error) {
	sanitisedHTTPRetryAfter := ""
	if rawHTTPRetryAfter != "" {
		parsedSecond, err := strconv.ParseInt(rawHTTPRetryAfter, 10, 0)
		if err != nil {
			parsedTime, err := http.ParseTime(rawHTTPRetryAfter)
			if err != nil {
				return "", errInvalidRetryAfter
			}
			sanitisedHTTPRetryAfter = parsedTime.Format(http.TimeFormat)
		} else {
			// if retry-after is 0 or less, then we shouldn't send the response header
			if parsedSecond <= 0 {
				sanitisedHTTPRetryAfter = ""
			} else {
				sanitisedHTTPRetryAfter = strconv.Itoa(int(parsedSecond))
			}
		}
	}
	return sanitisedHTTPRetryAfter, nil
}

type MimirOverridesMiddleware struct {
	cfg *RuntimeConfigTenantOverrides
}

// NewMimirOverridesMiddleware creates a new mimir overrides middleware
func NewMimirOverridesMiddleware(cfg *RuntimeConfigTenantOverrides) *MimirOverridesMiddleware {
	return &MimirOverridesMiddleware{
		cfg: cfg,
	}
}

// Wrap returns the Mimir overrides middleware function
func (l MimirOverridesMiddleware) Wrap(next http.Handler) http.Handler {
	queryRoutes := newRoutesMatcher()
	queryRoutes.addRoutes(router.MimirQueryRoutes)

	writeRoutes := newRoutesMatcher()
	writeRoutes.addRoutes(router.MimirWriteRoutes)
	writeRoutes.addRoutes(router.MimirInfluxWriteRoutes)

	rulerRoutes := newRoutesMatcher()
	rulerRoutes.addRoutes(router.MimirRulerRoutes)

	aggregationRoutes := newRoutesMatcher()
	// AggregationsRoutes (adaptive metrics) is an enterprise-only feature and has no OSS equivalent.

	configRoutes := newRoutesMatcher()
	_ = configRoutes

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		global, perTenant := l.getMimirOverridesForTenant(r)

		// Check if the request should be blocked
		for _, route := range []struct {
			globalBlockSetting *bool
			tenantBlockSetting *bool
			routes             *routesMatcher
			blocker            func(context.Context, http.ResponseWriter, mimirOverrides)
		}{
			{global.BlockReads, perTenant.BlockReads, queryRoutes, blockReads},
			{global.BlockWrites, perTenant.BlockWrites, writeRoutes, blockWrites},
			{global.BlockRuler, perTenant.BlockRuler, rulerRoutes, blockRuler},
			{global.BlockAggregations, perTenant.BlockAggregations, aggregationRoutes, blockAggregations},
		} {
			if route.tenantBlockSetting != nil {
				// If the tenant has an override, use it
				// a false value overrides global settings and means that the request should not be blocked
				if *route.tenantBlockSetting && route.routes.matches(r.URL.EscapedPath()) {
					route.blocker(ctx, w, perTenant.mimirOverrides)
					return
				}
			} else if route.globalBlockSetting != nil && *route.globalBlockSetting && route.routes.matches(r.URL.EscapedPath()) {
				// If the tenant has no override, use the global setting (defaults to not block)
				route.blocker(ctx, w, global.mimirOverrides)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

func blockReads(ctx context.Context, w http.ResponseWriter, cfg mimirOverrides) {
	if cfg.BlockReadsHTTPRetryAfter != "" {
		w.Header().Set(httpRetryAfter, cfg.BlockReadsHTTPRetryAfter)
	}
	writeError(ctx, w, cfg.BlockReadsMessage, cfg.BlockReadsHTTPResponseCode)
}

func blockWrites(ctx context.Context, w http.ResponseWriter, cfg mimirOverrides) {
	if cfg.BlockWritesHTTPRetryAfter != "" {
		w.Header().Set(httpRetryAfter, cfg.BlockWritesHTTPRetryAfter)
	}
	writeError(ctx, w, cfg.BlockWritesMessage, cfg.BlockWritesHTTPResponseCode)
}

func blockRuler(ctx context.Context, w http.ResponseWriter, cfg mimirOverrides) {
	if cfg.BlockRulerHTTPRetryAfter != "" {
		w.Header().Set(httpRetryAfter, cfg.BlockRulerHTTPRetryAfter)
	}
	writeError(ctx, w, cfg.BlockRulerMessage, cfg.BlockRulerHTTPResponseCode)
}

func blockAggregations(ctx context.Context, w http.ResponseWriter, cfg mimirOverrides) {
	if cfg.BlockAggregationsHTTPRetryAfter != "" {
		w.Header().Set(httpRetryAfter, cfg.BlockAggregationsHTTPRetryAfter)
	}
	writeError(ctx, w, cfg.BlockAggregationsMessage, cfg.BlockAggregationsHTTPResponseCode)
}

func (l MimirOverridesMiddleware) getMimirOverridesForTenant(r *http.Request) (MimirGlobalOverrides, MimirTenantOverrides) {
	u, _, err := user.ExtractOrgIDFromHTTPRequest(r)
	if err != nil {
		return defaultMimirGlobalOverrides, defaultMimirTenantOverrides
	}
	return l.cfg.GetMimirConfig(u)
}
