// SPDX-License-Identifier: AGPL-3.0-only

package router

import "github.com/grafana/db-auth-gateway/pkg/auth"

var (
	rewriteAPIPromToPrometheus            = ReplacePrefix("/api/prom", "/prometheus")
	rewriteAPIPromToAPIV1                 = ReplacePrefix("/api/prom", "/api/v1")
	rewriteAPIPromToPrometheusConfigV1    = ReplacePrefix("/api/prom", "/prometheus/config/v1")
	rewriteAPIToPrometheusConfig          = ReplacePrefix("/api", "/prometheus/config")
	rewritePrometheusToPrometheusConfigV1 = ReplacePrefix("/prometheus", "/prometheus/config/v1")
)

var MimirQueryRoutes = []Route{
	// Legacy cortex routes
	// TODO: check if we still need these
	NewRoute("/api/prom/api/v1/read", []string{"GET", "POST"}, auth.ScopeMetricsRead, rewriteAPIPromToPrometheus),
	NewRoute("/api/prom/api/v1/query", []string{"GET", "POST"}, auth.ScopeMetricsRead, rewriteAPIPromToPrometheus),
	NewRoute("/api/prom/api/v1/query_range", []string{"GET", "POST"}, auth.ScopeMetricsRead, rewriteAPIPromToPrometheus),
	NewRoute("/api/prom/api/v1/labels", []string{"GET", "POST"}, auth.ScopeMetricsRead, rewriteAPIPromToPrometheus),
	NewRoute("/api/prom/api/v1/label/{name}/values", []string{"GET", "POST"}, auth.ScopeMetricsRead, rewriteAPIPromToPrometheus),
	NewRoute("/api/prom/api/v1/series", []string{"GET", "POST"}, auth.ScopeMetricsRead, rewriteAPIPromToPrometheus),
	NewRoute("/api/prom/api/v1/metadata", []string{"GET"}, auth.ScopeMetricsRead, rewriteAPIPromToPrometheus),
	NewRoute("/api/prom/api/v1/query_exemplars", []string{"GET", "POST"}, auth.ScopeMetricsRead, rewriteAPIPromToPrometheus),
	NewRoute("/api/prom/api/v1/cardinality/label_names", []string{"GET", "POST"}, auth.ScopeMetricsRead, rewriteAPIPromToPrometheus),
	NewRoute("/api/prom/api/v1/cardinality/label_values", []string{"GET", "POST"}, auth.ScopeMetricsRead, rewriteAPIPromToPrometheus),

	// Cortex v1.0.0+ routes
	NewRoute("/prometheus/api/v1/read", []string{"GET", "POST"}, auth.ScopeMetricsRead),
	NewRoute("/prometheus/api/v1/query", []string{"GET", "POST"}, auth.ScopeMetricsRead),
	NewRoute("/prometheus/api/v1/query_range", []string{"GET", "POST"}, auth.ScopeMetricsRead),
	NewRoute("/prometheus/api/v1/labels", []string{"GET", "POST"}, auth.ScopeMetricsRead),
	NewRoute("/prometheus/api/v1/label/{name}/values", []string{"GET", "POST"}, auth.ScopeMetricsRead),
	NewRoute("/prometheus/api/v1/series", []string{"GET", "POST"}, auth.ScopeMetricsRead),
	NewRoute("/prometheus/api/v1/metadata", []string{"GET"}, auth.ScopeMetricsRead),
	NewRoute("/prometheus/api/v1/query_exemplars", []string{"GET", "POST"}, auth.ScopeMetricsRead),
	NewRoute("/prometheus/api/v1/cardinality/active_series", []string{"GET", "POST"}, auth.ScopeMetricsExport),
	NewRoute("/prometheus/api/v1/cardinality/active_native_histogram_metrics", []string{"GET", "POST"}, auth.ScopeMetricsRead),
	NewRoute("/prometheus/api/v1/cardinality/label_names", []string{"GET", "POST"}, auth.ScopeMetricsRead),
	NewRoute("/prometheus/api/v1/cardinality/label_values", []string{"GET", "POST"}, auth.ScopeMetricsRead),
}

var MimirWriteRoutes = []Route{
	NewRoute("/api/prom/push", []string{"POST"}, auth.ScopeMetricsWrite, rewriteAPIPromToAPIV1),

	// Cortex v1.0.0+ routes
	NewRoute("/api/v1/push", []string{"POST"}, auth.ScopeMetricsWrite),
	NewRoute("/otlp/v1/metrics", []string{"POST"}, auth.ScopeMetricsWrite),
}

var MimirCompactorRoutes = []Route{
	// TSDB block upload routes.
	NewRoute("/api/v1/upload/block/", []string{"GET", "POST"}, auth.ScopeMetricsExport, PathPrefix()),
}

var MimirRulerRoutes = []Route{
	NewRoute("/api/prom/rules", []string{"GET"}, auth.ScopeRulesRead, rewriteAPIPromToPrometheusConfigV1),
	NewRoute("/api/prom/rules/{namespace}", []string{"GET"}, auth.ScopeRulesRead, rewriteAPIPromToPrometheusConfigV1),
	NewRoute("/api/prom/rules/{namespace}", []string{"POST"}, auth.ScopeRulesWrite, rewriteAPIPromToPrometheusConfigV1),
	NewRoute("/api/prom/rules/{namespace}/{groupName}", []string{"GET"}, auth.ScopeRulesRead, rewriteAPIPromToPrometheusConfigV1),
	NewRoute("/api/prom/rules/{namespace}/{groupName}", []string{"DELETE"}, auth.ScopeRulesWrite, rewriteAPIPromToPrometheusConfigV1),
	NewRoute("/api/prom/rules/{namespace}", []string{"DELETE"}, auth.ScopeRulesWrite, rewriteAPIPromToPrometheusConfigV1),

	NewRoute("/api/prom/api/v1/rules", []string{"GET"}, auth.ScopeRulesRead, rewriteAPIPromToPrometheus),
	NewRoute("/api/prom/api/v1/alerts", []string{"GET"}, auth.ScopeRulesRead, rewriteAPIPromToPrometheus),
	NewRoute("/api/prom/api/v1/status/buildinfo", []string{"GET"}, "", rewriteAPIPromToPrometheus),

	// These routes exist to cater for the following situation:
	// - When new versions of Grafana detect the endpoint as Mimir, it adds /config/v1 to ruler routes.
	// - But the user may have /api/prom configured as their prefix, and have not updated it to /prometheus.
	NewRoute("/api/prom/config/v1/rules", []string{"GET"}, auth.ScopeRulesRead, rewriteAPIPromToPrometheus),
	NewRoute("/api/prom/config/v1/rules/{namespace}", []string{"GET"}, auth.ScopeRulesRead, rewriteAPIPromToPrometheus),
	NewRoute("/api/prom/config/v1/rules/{namespace}", []string{"POST"}, auth.ScopeRulesWrite, rewriteAPIPromToPrometheus),
	NewRoute("/api/prom/config/v1/rules/{namespace}/{groupName}", []string{"GET"}, auth.ScopeRulesRead, rewriteAPIPromToPrometheus),
	NewRoute("/api/prom/config/v1/rules/{namespace}/{groupName}", []string{"DELETE"}, auth.ScopeRulesWrite, rewriteAPIPromToPrometheus),
	NewRoute("/api/prom/config/v1/rules/{namespace}", []string{"DELETE"}, auth.ScopeRulesWrite, rewriteAPIPromToPrometheus),

	NewRoute("/api/v1/rules", []string{"GET"}, auth.ScopeRulesRead, rewriteAPIToPrometheusConfig),
	NewRoute("/api/v1/rules/{namespace}", []string{"GET"}, auth.ScopeRulesRead, rewriteAPIToPrometheusConfig),
	NewRoute("/api/v1/rules/{namespace}", []string{"POST"}, auth.ScopeRulesWrite, rewriteAPIToPrometheusConfig),
	NewRoute("/api/v1/rules/{namespace}/{groupName}", []string{"GET"}, auth.ScopeRulesRead, rewriteAPIToPrometheusConfig),
	NewRoute("/api/v1/rules/{namespace}/{groupName}", []string{"DELETE"}, auth.ScopeRulesWrite, rewriteAPIToPrometheusConfig),
	NewRoute("/api/v1/rules/{namespace}", []string{"DELETE"}, auth.ScopeRulesWrite, rewriteAPIToPrometheusConfig),

	NewRoute("/prometheus/rules", []string{"GET"}, auth.ScopeRulesRead, rewritePrometheusToPrometheusConfigV1),
	NewRoute("/prometheus/rules/{namespace}", []string{"GET"}, auth.ScopeRulesRead, rewritePrometheusToPrometheusConfigV1),
	NewRoute("/prometheus/rules/{namespace}", []string{"POST"}, auth.ScopeRulesWrite, rewritePrometheusToPrometheusConfigV1),
	NewRoute("/prometheus/rules/{namespace}/{groupName}", []string{"GET"}, auth.ScopeRulesRead, rewritePrometheusToPrometheusConfigV1),
	NewRoute("/prometheus/rules/{namespace}/{groupName}", []string{"DELETE"}, auth.ScopeRulesWrite, rewritePrometheusToPrometheusConfigV1),
	NewRoute("/prometheus/rules/{namespace}", []string{"DELETE"}, auth.ScopeRulesWrite, rewritePrometheusToPrometheusConfigV1),

	NewRoute("/prometheus/config/v1/rules", []string{"GET"}, auth.ScopeRulesRead),
	NewRoute("/prometheus/config/v1/rules/{namespace}", []string{"GET"}, auth.ScopeRulesRead),
	NewRoute("/prometheus/config/v1/rules/{namespace}", []string{"POST"}, auth.ScopeRulesWrite),
	NewRoute("/prometheus/config/v1/rules/{namespace}/{groupName}", []string{"GET"}, auth.ScopeRulesRead),
	NewRoute("/prometheus/config/v1/rules/{namespace}/{groupName}", []string{"DELETE"}, auth.ScopeRulesWrite),
	NewRoute("/prometheus/config/v1/rules/{namespace}", []string{"DELETE"}, auth.ScopeRulesWrite),

	NewRoute("/prometheus/api/v1/rules", []string{"GET"}, auth.ScopeRulesRead),
	NewRoute("/prometheus/api/v1/alerts", []string{"GET"}, auth.ScopeRulesRead),
	// buildinfo exposes non-sensitive information about the ruler.
	// It is used by Grafana to determine what type of system this is: Prometheus, Mimir or Cortex.
	NewRoute("/prometheus/api/v1/status/buildinfo", []string{"GET"}, ""),
}

var MimirAlertmanagerRoutes = []Route{
	NewRoute("/alertmanager", []string{"GET"}, auth.ScopeAlertsRead, PathPrefix()),
	NewRoute("/alertmanager", []string{"POST", "PUT", "DELETE"}, auth.ScopeAlertsWrite, PathPrefix()),

	NewRoute("/api/v1/alerts", []string{"GET"}, auth.ScopeAlertsRead),
	NewRoute("/api/v1/alerts", []string{"POST", "DELETE"}, auth.ScopeAlertsWrite),
}

var MimirInfluxWriteRoutes = []Route{
	NewRoute("/api/v1/push/influx/write", []string{"POST"}, auth.ScopeMetricsWrite),
}

var MimirOpenTSDBWriteRoutes = []Route{
	NewRoute("/opentsdb/api/put", []string{"POST", "PUT"}, auth.ScopeMetricsWrite),
}
