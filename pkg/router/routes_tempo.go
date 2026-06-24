// SPDX-License-Identifier: AGPL-3.0-only

package router

import "github.com/grafana/db-auth-gateway/pkg/auth"

var TempoQueryFrontendRoutes = []Route{
	NewRoute("/tempo/api/echo",
		[]string{"GET"},
		auth.ScopeTracesRead),
	NewRoute("/tempo/api/traces/{traceID}",
		[]string{"GET"},
		auth.ScopeTracesRead),
	NewRoute("/tempo/api/search",
		[]string{"GET"},
		auth.ScopeTracesRead),
	NewRoute("/tempo/api/search/tags",
		[]string{"GET"},
		auth.ScopeTracesRead),
	NewRoute("/tempo/api/search/tag/{tagName}/values",
		[]string{"GET"},
		auth.ScopeTracesRead),
	NewRoute("/tempo/api/mcp",
		[]string{"GET", "POST", "DELETE"},
		auth.ScopeTracesRead),
	// Metrics
	NewRoute("/tempo/api/metrics/summary",
		[]string{"GET"},
		auth.ScopeTracesRead),
	NewRoute("/tempo/api/metrics/query_range",
		[]string{"GET"},
		auth.ScopeTracesRead),
	NewRoute("/tempo/api/metrics/query",
		[]string{"GET"},
		auth.ScopeTracesRead),
	// v2 api
	NewRoute("/tempo/api/v2/traces/{traceID}",
		[]string{"GET"},
		auth.ScopeTracesRead),
	NewRoute("/tempo/api/v2/search/tag/{tagName:.*}/values",
		[]string{"GET"},
		auth.ScopeTracesRead),
	NewRoute("/tempo/api/v2/search/tags",
		[]string{"GET"},
		auth.ScopeTracesRead),
	NewRoute("/tempo/api/status/buildinfo",
		[]string{"GET"},
		auth.ScopeTracesRead),
}

// TempoUserConfigurableOverridesRoutes contains the user-configurable overrides API routes.
// These routes can be disabled via the -tempo.overrides.api-enabled flag.
// When disabled, these routes are not registered and requests will return 404 Not Found.
var TempoUserConfigurableOverridesRoutes = []Route{
	NewRoute("/tempo/api/overrides",
		[]string{"GET"},
		auth.ScopeTracesRead),
	NewRoute("/tempo/api/overrides",
		[]string{"POST", "PATCH", "DELETE"},
		auth.ScopeTracesWrite),
}

var TempoGRPCReadRoutes = []Route{
	NewRoute("/tempopb.StreamingQuerier/Search",
		[]string{"POST"},
		auth.ScopeTracesRead),
	NewRoute("/tempopb.StreamingQuerier/SearchTags",
		[]string{"POST"},
		auth.ScopeTracesRead),
	NewRoute("/tempopb.StreamingQuerier/SearchTagsV2",
		[]string{"POST"},
		auth.ScopeTracesRead),
	NewRoute("/tempopb.StreamingQuerier/SearchTagValues",
		[]string{"POST"},
		auth.ScopeTracesRead),
	NewRoute("/tempopb.StreamingQuerier/SearchTagValuesV2",
		[]string{"POST"},
		auth.ScopeTracesRead),
	NewRoute("/tempopb.StreamingQuerier/MetricsQueryRange",
		[]string{"POST"},
		auth.ScopeTracesRead),
	NewRoute("/tempopb.StreamingQuerier/MetricsQueryInstant",
		[]string{"POST"},
		auth.ScopeTracesRead),
}

var TempoGRPCWriteRoutes = []Route{
	NewRoute("/opentelemetry.proto.collector.trace.v1.TraceService/Export",
		[]string{"POST"},
		auth.ScopeTracesWrite),
}

var TempoHTTPWriteRoutes = []Route{
	NewRoute("/tempo/api/v1/traces",
		[]string{"POST"},
		auth.ScopeTracesWrite,
	),
	NewRoute("/otlp/v1/traces",
		[]string{"POST"},
		auth.ScopeTracesWrite,
		TrimPrefix("/otlp"),
	),
}
