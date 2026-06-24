// SPDX-License-Identifier: AGPL-3.0-only

package router

import (
	"testing"

	"github.com/grafana/db-auth-gateway/pkg/auth"
)

var lokiBackendRoutes = map[string][]Route{
	"query":  LokiQueryRoutes,
	"tail":   LokiTailRoutes,
	"write":  LokiWriteRoutes,
	"delete": LokiDeleteRoutes,
	"ruler":  LokiRulerRoutes,
}

func TestLokiRoutes(t *testing.T) {
	tests := []struct {
		method        string
		path          string
		expectedScope string
		expectedBack  string
		expectMatch   bool
	}{
		// Loki API v1 query routes
		{"GET", "/loki/api/v1/query", auth.ScopeLogsRead, "query", true},
		{"POST", "/loki/api/v1/query", auth.ScopeLogsRead, "query", true},
		{"GET", "/loki/api/v1/query_range", auth.ScopeLogsRead, "query", true},
		{"POST", "/loki/api/v1/query_range", auth.ScopeLogsRead, "query", true},
		{"GET", "/loki/api/v1/labels", auth.ScopeLogsRead, "query", true},
		{"GET", "/loki/api/v1/label", auth.ScopeLogsRead, "query", true},
		{"GET", "/loki/api/v1/label/foo/values", auth.ScopeLogsRead, "query", true},
		{"GET", "/loki/api/v1/series", auth.ScopeLogsRead, "query", true},
		{"GET", "/loki/api/v1/index/stats", auth.ScopeLogsRead, "query", true},
		{"GET", "/loki/api/v1/index/volume", auth.ScopeLogsRead, "query", true},
		{"GET", "/loki/api/v1/index/volume_range", auth.ScopeLogsRead, "query", true},
		{"GET", "/loki/api/v1/format_query", auth.ScopeLogsRead, "query", true},
		{"GET", "/loki/api/v1/detected_fields", auth.ScopeLogsRead, "query", true},
		{"GET", "/loki/api/v1/detected_field/foo/values", auth.ScopeLogsRead, "query", true},
		{"GET", "/loki/api/v1/detected_labels", auth.ScopeLogsRead, "query", true},
		{"GET", "/loki/api/v1/patterns", auth.ScopeLogsRead, "query", true},

		// Tail (WebSocket) routes — GET only per RFC 6455
		{"GET", "/loki/api/v1/tail", auth.ScopeLogsRead, "tail", true},
		{"POST", "/loki/api/v1/tail", "", "", false},

		// Write routes
		{"POST", "/loki/api/v1/push", auth.ScopeLogsWrite, "write", true},
		{"POST", "/otlp/v1/logs", auth.ScopeLogsWrite, "write", true},

		// Delete routes
		{"GET", "/loki/api/v1/delete", auth.ScopeLogsDelete, "delete", true},
		{"PUT", "/loki/api/v1/delete", auth.ScopeLogsDelete, "delete", true},
		{"POST", "/loki/api/v1/delete", auth.ScopeLogsDelete, "delete", true},
		{"DELETE", "/loki/api/v1/delete", auth.ScopeLogsDelete, "delete", true},

		// Ruler routes
		{"GET", "/loki/api/v1/rules", auth.ScopeRulesRead, "ruler", true},
		{"GET", "/loki/api/v1/rules/my-namespace", auth.ScopeRulesRead, "ruler", true},
		{"POST", "/loki/api/v1/rules/my-namespace", auth.ScopeRulesWrite, "ruler", true},
		{"DELETE", "/loki/api/v1/rules/my-namespace", auth.ScopeRulesWrite, "ruler", true},
		{"GET", "/loki/api/v1/rules/my-namespace/my-group", auth.ScopeRulesRead, "ruler", true},
		{"DELETE", "/loki/api/v1/rules/my-namespace/my-group", auth.ScopeRulesWrite, "ruler", true},
		{"GET", "/prometheus/api/v1/rules", auth.ScopeRulesRead, "ruler", true},
		{"GET", "/prometheus/api/v1/alerts", auth.ScopeRulesRead, "ruler", true},

		// No match
		{"GET", "/unknown/path", "", "", false},
		{"GET", "/loki/api/v1/push", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			var matchedRoute *Route
			var matchedBack string

			for backName, routes := range lokiBackendRoutes {
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

func TestLokiTailRoutesWebsocketFlagged(t *testing.T) {
	for _, route := range LokiTailRoutes {
		if !route.Websocket {
			t.Errorf("tail route %s should have Websocket=true", route.Path)
		}
	}
}

func TestLokiNonTailRoutesNotWebsocket(t *testing.T) {
	allNonTail := [][]Route{LokiQueryRoutes, LokiWriteRoutes, LokiDeleteRoutes, LokiRulerRoutes}
	for _, routes := range allNonTail {
		for _, route := range routes {
			if route.Websocket {
				t.Errorf("non-tail route %s should not have Websocket=true", route.Path)
			}
		}
	}
}
