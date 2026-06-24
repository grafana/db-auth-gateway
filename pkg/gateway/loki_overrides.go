// SPDX-License-Identifier: AGPL-3.0-only

package gateway

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/grafana/dskit/user"
	"go.yaml.in/yaml/v3"

	"github.com/grafana/db-auth-gateway/pkg/router"
)

const (
	queryTagsHTTPHeaderKey            = "X-Query-Tags"
	lokiHintQueriesHTTPHeaderValue    = "Source=datasample"
	logVolumeHistogramHTTPHeaderValue = "Source=logvolhist"
)

var defaultLogVolumeHistogramTimeout = 10 * time.Second
var defaultHintQueryTimeout = 3 * time.Second

// LokiOverrides defines Loki-specific per-tenant overrides.
type LokiOverrides struct {
	BlockQueries        bool   `yaml:"block_queries" json:"block_queries"`
	BlockQueriesMessage string `yaml:"block_queries_message" json:"block_queries_message"`
	BlockWrites         bool   `yaml:"block_writes" json:"block_writes"`
	BlockWritesMessage  string `yaml:"block_writes_message" json:"block_writes_message"`

	BlockHintQueries   BlockHintQueriesOverrides  `yaml:"block_hint_queries"`
	LogVolumeHistogram LogVolmeHistogramOverrides `yaml:"log_volume_histogram"`

	// Insights related config
	LogUnhashedQueries bool `yaml:"log_unhashed_queries" json:"log_unhashed_queries"`
}

type BlockHintQueriesOverrides struct {
	Enabled bool          `yaml:"enabled" json:"enabled"`
	Timeout time.Duration `yaml:"timeout" json:"timeout"`

	// HeaderKey:HeaderValue are the HTTP header key and values that identifies hint queries.
	HTTPHeaderKey   string `yaml:"http_header_key" json:"http_header_key"`
	HTTPHeaderValue string `yaml:"http_header_value" json:"http_header_value"`

	Message string `yaml:"message" json:"message"`
}

// LogVolmeHistogramOverrides is per-tenant config for Log Volume Histogram queries.
type LogVolmeHistogramOverrides struct {
	Enabled bool          `yaml:"enabled" json:"enabled"`
	Timeout time.Duration `yaml:"timeout" json:"timeout"`

	// HeaderKey:HeaderValue are the HTTP header key and values that identifies Log Volume Histogram queries.
	HTTPHeaderKey   string `yaml:"http_header_key" json:"http_header_key"`
	HTTPHeaderValue string `yaml:"http_header_value" json:"http_header_value"`
}

var defaultLokiOverrides = LokiOverrides{
	BlockWrites:         false,
	BlockWritesMessage:  "Loki writes are blocked for this tenant, please contact your system administrator",
	BlockQueries:        false,
	BlockQueriesMessage: "Loki queries are blocked for this tenant, please contact your system administrator",

	BlockHintQueries: BlockHintQueriesOverrides{
		Enabled:         false,
		Timeout:         defaultHintQueryTimeout,
		HTTPHeaderKey:   queryTagsHTTPHeaderKey,
		HTTPHeaderValue: lokiHintQueriesHTTPHeaderValue,
		Message:         "Loki hint queries are blocked for this tenant, please contact your system administrator",
	},

	// Default log volume histogram overrides independent of tenants.
	LogVolumeHistogram: LogVolmeHistogramOverrides{
		Enabled:         true,
		Timeout:         defaultLogVolumeHistogramTimeout,
		HTTPHeaderKey:   queryTagsHTTPHeaderKey,
		HTTPHeaderValue: logVolumeHistogramHTTPHeaderValue,
	},

	LogUnhashedQueries: false,
}

// UnmarshalYAML implements the yaml.Unmarshaler interface.
func (l *LokiOverrides) UnmarshalYAML(value *yaml.Node) error {
	// We want to set l to the defaults and then overwrite it with the input.
	// To make unmarshal fill the plain data struct rather than calling UnmarshalYAML
	// again, we have to hide it using a type indirection.  See prometheus/config.
	*l = defaultLokiOverrides
	type plain LokiOverrides
	b, err := yaml.Marshal(value)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	return dec.Decode((*plain)(l))
}

type LokiOverridesMiddleware struct {
	cfg *RuntimeConfigTenantOverrides
}

// NewLokiOverridesMiddleware creates a new loki overrides middleware
func NewLokiOverridesMiddleware(cfg *RuntimeConfigTenantOverrides) *LokiOverridesMiddleware {
	return &LokiOverridesMiddleware{
		cfg: cfg,
	}
}

func (l LokiOverridesMiddleware) getLokiOverridesFromRequest(r *http.Request) LokiOverrides {
	u, _, err := user.ExtractOrgIDFromHTTPRequest(r)
	if err != nil {
		return defaultLokiOverrides
	}
	return l.cfg.GetLokiConfig(u)
}

// Wrap returns the loki overrides middleware function
func (l LokiOverridesMiddleware) Wrap(next http.Handler) http.Handler {
	queryRoutes := buildRoutesMap(router.LokiQueryRoutes)
	writeRoutes := buildRoutesMap(router.LokiWriteRoutes)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		switch {
		case queryRoutes[r.URL.Path]:
			o := l.getLokiOverridesFromRequest(r)
			if o.BlockQueries {
				// Query is blocked for this tenant ID
				writeError(ctx, w, o.BlockQueriesMessage, http.StatusUnauthorized)
				return
			}

			// If it's a Loki log volume histogram query, either drop it completely or tweak the timeout.
			if isLogVolumeHistogramQuery(r, o.LogVolumeHistogram.HTTPHeaderKey, o.LogVolumeHistogram.HTTPHeaderValue) {
				// if not enabled, drop the complete query.
				if !o.LogVolumeHistogram.Enabled {
					writeError(
						ctx,
						w,
						"Log volume histogram is disabled for this tenant",
						http.StatusForbidden,
					)
					return
				}
				// if it's enabled, then set the configured timeout
				ctx, cancel := context.WithTimeout(ctx, o.LogVolumeHistogram.Timeout)
				defer cancel()
				r = r.WithContext(ctx)
			}

			// If it's a Loki hint query check if it's need to be blocked.
			if isLokiHintQuery(r, o.BlockHintQueries.HTTPHeaderKey, o.BlockHintQueries.HTTPHeaderValue) {
				// if enabled, drop the complete query.
				if o.BlockHintQueries.Enabled {
					writeError(ctx, w, o.BlockHintQueries.Message, http.StatusUnauthorized)
					return
				}
				// if blocking is not enabled, then set the configured timeout
				ctx, cancel := context.WithTimeout(ctx, o.BlockHintQueries.Timeout)
				defer cancel()
				r = r.WithContext(ctx)
			}
		case writeRoutes[r.URL.Path]:
			o := l.getLokiOverridesFromRequest(r)
			if o.BlockWrites {
				// Writes is blocked for this tenant ID
				writeError(ctx, w, o.BlockWritesMessage, http.StatusUnauthorized)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// isLogVolumeHistogramQuery checks if incoming query is log volume histogram query based on
// HTTP headers.
func isLogVolumeHistogramQuery(r *http.Request, key, value string) bool {
	return strings.Contains(strings.ToLower(r.Header.Get(key)), strings.ToLower(value))
}

func isLokiHintQuery(r *http.Request, key, value string) bool {
	k := strings.TrimSpace(r.Header.Get(key))
	if k != "" {
		return strings.Contains(strings.ToLower(k), strings.ToLower(value))
	}

	return false
}
