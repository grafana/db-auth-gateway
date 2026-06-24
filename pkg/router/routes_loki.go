// SPDX-License-Identifier: AGPL-3.0-only

package router

import "github.com/grafana/db-auth-gateway/pkg/auth"

var LokiQueryRoutes = []Route{
	NewRoute("/loki/api/v1/query", []string{"GET", "POST"}, auth.ScopeLogsRead),
	NewRoute("/loki/api/v1/query_range", []string{"GET", "POST"}, auth.ScopeLogsRead),
	NewRoute("/loki/api/v1/label", []string{"GET", "POST"}, auth.ScopeLogsRead),
	NewRoute("/loki/api/v1/labels", []string{"GET", "POST"}, auth.ScopeLogsRead),
	NewRoute("/loki/api/v1/label/{name}/values", []string{"GET", "POST"}, auth.ScopeLogsRead),
	NewRoute("/loki/api/v1/series", []string{"GET", "POST"}, auth.ScopeLogsRead),
	NewRoute("/loki/api/v1/index/stats", []string{"GET", "POST"}, auth.ScopeLogsRead),
	NewRoute("/loki/api/v1/index/volume", []string{"GET", "POST"}, auth.ScopeLogsRead),
	NewRoute("/loki/api/v1/index/volume_range", []string{"GET", "POST"}, auth.ScopeLogsRead),
	NewRoute("/loki/api/v1/format_query", []string{"GET", "POST"}, auth.ScopeLogsRead),
	NewRoute("/loki/api/v1/detected_fields", []string{"GET", "POST"}, auth.ScopeLogsRead),
	NewRoute("/loki/api/v1/detected_field/{name}/values", []string{"GET", "POST"}, auth.ScopeLogsRead),
	NewRoute("/loki/api/v1/detected_labels", []string{"GET", "POST"}, auth.ScopeLogsRead),
	NewRoute("/loki/api/v1/patterns", []string{"GET", "POST"}, auth.ScopeLogsRead),
}

var LokiTailRoutes = []Route{
	// WebSocket tail routes: GET only — WebSocket upgrade requires GET per RFC 6455.
	NewRoute("/loki/api/v1/tail", []string{"GET"}, auth.ScopeLogsRead, Websocket()),
}

var LokiWriteRoutes = []Route{
	NewRoute("/loki/api/v1/push", []string{"POST"}, auth.ScopeLogsWrite),
	NewRoute("/otlp/v1/logs", []string{"POST"}, auth.ScopeLogsWrite),
}

var LokiDeleteRoutes = []Route{
	NewRoute("/loki/api/v1/delete", []string{"GET", "PUT", "POST", "DELETE"}, auth.ScopeLogsDelete),
}

var LokiRulerRoutes = []Route{
	NewRoute("/loki/api/v1/rules", []string{"GET"}, auth.ScopeRulesRead),
	NewRoute("/loki/api/v1/rules/{namespace}", []string{"GET"}, auth.ScopeRulesRead),
	NewRoute("/loki/api/v1/rules/{namespace}", []string{"POST"}, auth.ScopeRulesWrite),
	NewRoute("/loki/api/v1/rules/{namespace}", []string{"DELETE"}, auth.ScopeRulesWrite),
	NewRoute("/loki/api/v1/rules/{namespace}/{groupName}", []string{"GET"}, auth.ScopeRulesRead),
	NewRoute("/loki/api/v1/rules/{namespace}/{groupName}", []string{"DELETE"}, auth.ScopeRulesWrite),

	// Prometheus-format ruler read endpoints
	NewRoute("/prometheus/api/v1/rules", []string{"GET"}, auth.ScopeRulesRead),
	NewRoute("/prometheus/api/v1/alerts", []string{"GET"}, auth.ScopeRulesRead),
}
