// SPDX-License-Identifier: AGPL-3.0-only

package router

import (
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"

	"github.com/grafana/db-auth-gateway/pkg/auth"
)

// backendRoutes maps backend name to its route slice, mirroring the backends
// that NewAPI wires up in pkg/proxy.
var backendRoutes = map[string][]Route{
	"query":        MimirQueryRoutes,
	"distributor":  MimirWriteRoutes,
	"ruler":        MimirRulerRoutes,
	"alertmanager": MimirAlertmanagerRoutes,
	"compactor":    MimirCompactorRoutes,
}

// findRoute returns the first Route in routes that matches method+path using
// gorilla/mux, and reports whether a match was found.
func findRoute(routes []Route, method, path string) (Route, bool) {
	req := httptest.NewRequest(method, path, nil)
	for _, route := range routes {
		sub := mux.NewRouter()
		if route.PathPrefix {
			sub.PathPrefix(route.Path).Methods(route.Methods...)
		} else {
			sub.Path(route.Path).Methods(route.Methods...)
		}
		var m mux.RouteMatch
		if sub.Match(req, &m) {
			return route, true
		}
	}
	return Route{}, false
}

func TestMimirRoutes(t *testing.T) {
	tests := []struct {
		method        string
		path          string
		expectedScope string
		expectedBack  string
		expectMatch   bool
	}{
		// Query — legacy /api/prom routes
		{"GET", "/api/prom/api/v1/read", auth.ScopeMetricsRead, "query", true},
		{"GET", "/api/prom/api/v1/query", auth.ScopeMetricsRead, "query", true},
		{"POST", "/api/prom/api/v1/query_range", auth.ScopeMetricsRead, "query", true},
		{"GET", "/api/prom/api/v1/labels", auth.ScopeMetricsRead, "query", true},
		{"GET", "/api/prom/api/v1/label/job/values", auth.ScopeMetricsRead, "query", true},
		{"GET", "/api/prom/api/v1/series", auth.ScopeMetricsRead, "query", true},
		{"GET", "/api/prom/api/v1/metadata", auth.ScopeMetricsRead, "query", true},
		{"GET", "/api/prom/api/v1/query_exemplars", auth.ScopeMetricsRead, "query", true},
		{"GET", "/api/prom/api/v1/cardinality/label_names", auth.ScopeMetricsRead, "query", true},
		{"POST", "/api/prom/api/v1/cardinality/label_values", auth.ScopeMetricsRead, "query", true},

		// Query — Cortex v1.0.0+ routes
		{"GET", "/prometheus/api/v1/read", auth.ScopeMetricsRead, "query", true},
		{"GET", "/prometheus/api/v1/query", auth.ScopeMetricsRead, "query", true},
		{"GET", "/prometheus/api/v1/query_range", auth.ScopeMetricsRead, "query", true},
		{"GET", "/prometheus/api/v1/labels", auth.ScopeMetricsRead, "query", true},
		{"GET", "/prometheus/api/v1/label/job/values", auth.ScopeMetricsRead, "query", true},
		{"GET", "/prometheus/api/v1/series", auth.ScopeMetricsRead, "query", true},
		{"GET", "/prometheus/api/v1/metadata", auth.ScopeMetricsRead, "query", true},
		{"GET", "/prometheus/api/v1/query_exemplars", auth.ScopeMetricsRead, "query", true},
		{"GET", "/prometheus/api/v1/cardinality/active_series", auth.ScopeMetricsExport, "query", true},
		{"GET", "/prometheus/api/v1/cardinality/active_native_histogram_metrics", auth.ScopeMetricsRead, "query", true},
		{"GET", "/prometheus/api/v1/cardinality/label_names", auth.ScopeMetricsRead, "query", true},
		{"POST", "/prometheus/api/v1/cardinality/label_values", auth.ScopeMetricsRead, "query", true},

		// Distributor — push
		{"POST", "/api/prom/push", auth.ScopeMetricsWrite, "distributor", true},
		{"POST", "/api/v1/push", auth.ScopeMetricsWrite, "distributor", true},
		{"POST", "/otlp/v1/metrics", auth.ScopeMetricsWrite, "distributor", true},

		// Compactor
		{"POST", "/api/v1/upload/block/some-block-id", auth.ScopeMetricsExport, "compactor", true},

		// Ruler — /api/prom/rules/...
		{"GET", "/api/prom/rules/my-namespace", auth.ScopeRulesRead, "ruler", true},
		{"POST", "/api/prom/rules/my-namespace", auth.ScopeRulesWrite, "ruler", true},
		{"DELETE", "/api/prom/rules/my-namespace", auth.ScopeRulesWrite, "ruler", true},
		{"GET", "/api/prom/rules/my-namespace/my-group", auth.ScopeRulesRead, "ruler", true},
		{"DELETE", "/api/prom/rules/my-namespace/my-group", auth.ScopeRulesWrite, "ruler", true},

		// Ruler — /api/prom/api/v1/...
		{"GET", "/api/prom/api/v1/rules", auth.ScopeRulesRead, "ruler", true},
		{"GET", "/api/prom/api/v1/alerts", auth.ScopeRulesRead, "ruler", true},
		{"GET", "/api/prom/api/v1/status/buildinfo", "", "ruler", true},

		// Ruler — /api/prom/config/v1/...
		{"GET", "/api/prom/config/v1/rules/my-namespace", auth.ScopeRulesRead, "ruler", true},
		{"POST", "/api/prom/config/v1/rules/my-namespace", auth.ScopeRulesWrite, "ruler", true},
		{"DELETE", "/api/prom/config/v1/rules/my-namespace", auth.ScopeRulesWrite, "ruler", true},

		// Ruler — /api/v1/rules/...
		{"GET", "/api/v1/rules/my-namespace", auth.ScopeRulesRead, "ruler", true},
		{"POST", "/api/v1/rules/my-namespace", auth.ScopeRulesWrite, "ruler", true},
		{"DELETE", "/api/v1/rules/my-namespace", auth.ScopeRulesWrite, "ruler", true},

		// Ruler — /prometheus/rules/...
		{"GET", "/prometheus/rules/my-namespace", auth.ScopeRulesRead, "ruler", true},
		{"POST", "/prometheus/rules/my-namespace", auth.ScopeRulesWrite, "ruler", true},
		{"DELETE", "/prometheus/rules/my-namespace", auth.ScopeRulesWrite, "ruler", true},

		// Ruler — /prometheus/config/v1/rules/...
		{"GET", "/prometheus/config/v1/rules/my-namespace", auth.ScopeRulesRead, "ruler", true},
		{"POST", "/prometheus/config/v1/rules/my-namespace", auth.ScopeRulesWrite, "ruler", true},
		{"DELETE", "/prometheus/config/v1/rules/my-namespace", auth.ScopeRulesWrite, "ruler", true},
		{"GET", "/prometheus/config/v1/rules/my-namespace/my-group", auth.ScopeRulesRead, "ruler", true},
		{"DELETE", "/prometheus/config/v1/rules/my-namespace/my-group", auth.ScopeRulesWrite, "ruler", true},

		// Ruler — /prometheus/api/v1/...
		{"GET", "/prometheus/api/v1/rules", auth.ScopeRulesRead, "ruler", true},
		{"GET", "/prometheus/api/v1/alerts", auth.ScopeRulesRead, "ruler", true},
		{"GET", "/prometheus/api/v1/status/buildinfo", "", "ruler", true},

		// Alertmanager
		{"GET", "/alertmanager/api/v2/alerts", auth.ScopeAlertsRead, "alertmanager", true},
		{"GET", "/api/v1/alerts", auth.ScopeAlertsRead, "alertmanager", true},
		{"POST", "/api/v1/alerts", auth.ScopeAlertsWrite, "alertmanager", true},

		// No match
		{"GET", "/unknown/path", "", "", false},
		{"DELETE", "/api/v1/push", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			var matchedRoute *Route
			var matchedBack string

			for backName, routes := range backendRoutes {
				if r, ok := findRoute(routes, tt.method, tt.path); ok {
					matchedRoute = &r
					matchedBack = backName
					break
				}
			}

			if tt.expectMatch && matchedRoute == nil {
				t.Errorf("Match(%s %s): expected match but got none", tt.method, tt.path)
				return
			}
			if !tt.expectMatch && matchedRoute != nil {
				t.Errorf("Match(%s %s): expected no match but got one in %q", tt.method, tt.path, matchedBack)
				return
			}
			if !tt.expectMatch {
				return
			}
			if matchedRoute.Permission != tt.expectedScope {
				t.Errorf("Match(%s %s): got scope=%q, want %q", tt.method, tt.path, matchedRoute.Permission, tt.expectedScope)
			}
			if matchedBack != tt.expectedBack {
				t.Errorf("Match(%s %s): got backend=%q, want %q", tt.method, tt.path, matchedBack, tt.expectedBack)
			}
		})
	}
}

func TestMimirRouteMiddlewarePresence(t *testing.T) {
	tests := []struct {
		method           string
		path             string
		expectMiddleware bool
	}{
		{"GET", "/api/prom/api/v1/query", true},
		{"POST", "/api/prom/push", true},
		{"GET", "/api/v1/rules/ns", true},
		{"GET", "/prometheus/api/v1/query", false},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			var matchedRoute *Route
			for _, routes := range backendRoutes {
				if r, ok := findRoute(routes, tt.method, tt.path); ok {
					matchedRoute = &r
					break
				}
			}
			if matchedRoute == nil {
				t.Fatalf("expected match for %s %s", tt.method, tt.path)
			}
			hasMiddleware := matchedRoute.Middleware != nil
			if hasMiddleware != tt.expectMiddleware {
				t.Errorf("got middleware=%v, want %v", hasMiddleware, tt.expectMiddleware)
			}
		})
	}
}

func TestMimirRouteGETvsPOSTForPush(t *testing.T) {
	// GET /api/v1/push should NOT match (only POST is valid).
	_, matched := findRoute(MimirWriteRoutes, "GET", "/api/v1/push")
	if matched {
		t.Error("GET /api/v1/push should not match")
	}
}
