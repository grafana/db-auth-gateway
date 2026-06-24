// SPDX-License-Identifier: AGPL-3.0-only

package router

import (
	"testing"

	"github.com/grafana/db-auth-gateway/pkg/auth"
)

func TestTempoQueryFrontendRoutes(t *testing.T) {
	tests := []struct {
		method        string
		path          string
		expectedScope string
		expectMatch   bool
	}{
		{"GET", "/tempo/api/echo", auth.ScopeTracesRead, true},
		{"GET", "/tempo/api/traces/abc123", auth.ScopeTracesRead, true},
		{"GET", "/tempo/api/search", auth.ScopeTracesRead, true},
		{"GET", "/tempo/api/search/tags", auth.ScopeTracesRead, true},
		{"GET", "/tempo/api/search/tag/service.name/values", auth.ScopeTracesRead, true},
		{"GET", "/tempo/api/mcp", auth.ScopeTracesRead, true},
		{"POST", "/tempo/api/mcp", auth.ScopeTracesRead, true},
		{"DELETE", "/tempo/api/mcp", auth.ScopeTracesRead, true},
		{"GET", "/tempo/api/metrics/summary", auth.ScopeTracesRead, true},
		{"GET", "/tempo/api/metrics/query_range", auth.ScopeTracesRead, true},
		{"GET", "/tempo/api/metrics/query", auth.ScopeTracesRead, true},
		{"GET", "/tempo/api/v2/traces/abc123", auth.ScopeTracesRead, true},
		{"GET", "/tempo/api/v2/search/tag/service.name/values", auth.ScopeTracesRead, true},
		{"GET", "/tempo/api/v2/search/tags", auth.ScopeTracesRead, true},
		{"GET", "/tempo/api/status/buildinfo", auth.ScopeTracesRead, true},

		// Negative: wrong method
		{"POST", "/tempo/api/echo", "", false},
		{"POST", "/tempo/api/search", "", false},
		{"DELETE", "/tempo/api/traces/abc123", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			r, ok := findRoute(TempoQueryFrontendRoutes, tt.method, tt.path)
			if tt.expectMatch && !ok {
				t.Errorf("Match(%s %s): expected match but got none", tt.method, tt.path)
				return
			}
			if !tt.expectMatch && ok {
				t.Errorf("Match(%s %s): expected no match but got one", tt.method, tt.path)
				return
			}
			if tt.expectMatch && r.Permission != tt.expectedScope {
				t.Errorf("Match(%s %s): got scope=%q, want %q", tt.method, tt.path, r.Permission, tt.expectedScope)
			}
		})
	}
}

func TestTempoUserConfigurableOverridesRoutes(t *testing.T) {
	tests := []struct {
		method        string
		path          string
		expectedScope string
		expectMatch   bool
	}{
		{"GET", "/tempo/api/overrides", auth.ScopeTracesRead, true},
		{"POST", "/tempo/api/overrides", auth.ScopeTracesWrite, true},
		{"PATCH", "/tempo/api/overrides", auth.ScopeTracesWrite, true},
		{"DELETE", "/tempo/api/overrides", auth.ScopeTracesWrite, true},

		// Negative: wrong method
		{"PUT", "/tempo/api/overrides", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			r, ok := findRoute(TempoUserConfigurableOverridesRoutes, tt.method, tt.path)
			if tt.expectMatch && !ok {
				t.Errorf("Match(%s %s): expected match but got none", tt.method, tt.path)
				return
			}
			if !tt.expectMatch && ok {
				t.Errorf("Match(%s %s): expected no match but got one", tt.method, tt.path)
				return
			}
			if tt.expectMatch && r.Permission != tt.expectedScope {
				t.Errorf("Match(%s %s): got scope=%q, want %q", tt.method, tt.path, r.Permission, tt.expectedScope)
			}
		})
	}
}

func TestTempoGRPCReadRoutes(t *testing.T) {
	tests := []struct {
		method        string
		path          string
		expectedScope string
		expectMatch   bool
	}{
		{"POST", "/tempopb.StreamingQuerier/Search", auth.ScopeTracesRead, true},
		{"POST", "/tempopb.StreamingQuerier/SearchTags", auth.ScopeTracesRead, true},
		{"POST", "/tempopb.StreamingQuerier/SearchTagsV2", auth.ScopeTracesRead, true},
		{"POST", "/tempopb.StreamingQuerier/SearchTagValues", auth.ScopeTracesRead, true},
		{"POST", "/tempopb.StreamingQuerier/SearchTagValuesV2", auth.ScopeTracesRead, true},
		{"POST", "/tempopb.StreamingQuerier/MetricsQueryRange", auth.ScopeTracesRead, true},
		{"POST", "/tempopb.StreamingQuerier/MetricsQueryInstant", auth.ScopeTracesRead, true},

		// Negative: wrong method
		{"GET", "/tempopb.StreamingQuerier/Search", "", false},
		{"DELETE", "/tempopb.StreamingQuerier/SearchTags", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			r, ok := findRoute(TempoGRPCReadRoutes, tt.method, tt.path)
			if tt.expectMatch && !ok {
				t.Errorf("Match(%s %s): expected match but got none", tt.method, tt.path)
				return
			}
			if !tt.expectMatch && ok {
				t.Errorf("Match(%s %s): expected no match but got one", tt.method, tt.path)
				return
			}
			if tt.expectMatch && r.Permission != tt.expectedScope {
				t.Errorf("Match(%s %s): got scope=%q, want %q", tt.method, tt.path, r.Permission, tt.expectedScope)
			}
		})
	}
}

func TestTempoGRPCWriteRoutes(t *testing.T) {
	tests := []struct {
		method        string
		path          string
		expectedScope string
		expectMatch   bool
	}{
		{"POST", "/opentelemetry.proto.collector.trace.v1.TraceService/Export", auth.ScopeTracesWrite, true},

		// Negative: wrong method
		{"GET", "/opentelemetry.proto.collector.trace.v1.TraceService/Export", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			r, ok := findRoute(TempoGRPCWriteRoutes, tt.method, tt.path)
			if tt.expectMatch && !ok {
				t.Errorf("Match(%s %s): expected match but got none", tt.method, tt.path)
				return
			}
			if !tt.expectMatch && ok {
				t.Errorf("Match(%s %s): expected no match but got one", tt.method, tt.path)
				return
			}
			if tt.expectMatch && r.Permission != tt.expectedScope {
				t.Errorf("Match(%s %s): got scope=%q, want %q", tt.method, tt.path, r.Permission, tt.expectedScope)
			}
		})
	}
}

func TestTempoHTTPWriteRoutes(t *testing.T) {
	tests := []struct {
		method        string
		path          string
		expectedScope string
		expectMatch   bool
	}{
		{"POST", "/tempo/api/v1/traces", auth.ScopeTracesWrite, true},
		{"POST", "/otlp/v1/traces", auth.ScopeTracesWrite, true},

		// Negative: wrong method
		{"GET", "/tempo/api/v1/traces", "", false},
		{"GET", "/otlp/v1/traces", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			r, ok := findRoute(TempoHTTPWriteRoutes, tt.method, tt.path)
			if tt.expectMatch && !ok {
				t.Errorf("Match(%s %s): expected match but got none", tt.method, tt.path)
				return
			}
			if !tt.expectMatch && ok {
				t.Errorf("Match(%s %s): expected no match but got one", tt.method, tt.path)
				return
			}
			if tt.expectMatch && r.Permission != tt.expectedScope {
				t.Errorf("Match(%s %s): got scope=%q, want %q", tt.method, tt.path, r.Permission, tt.expectedScope)
			}
		})
	}
}
