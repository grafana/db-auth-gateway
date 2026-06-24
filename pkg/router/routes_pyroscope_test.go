// SPDX-License-Identifier: AGPL-3.0-only

package router

import (
	"testing"

	"github.com/grafana/db-auth-gateway/pkg/auth"
)

func TestPyroscopeWriteRoutes(t *testing.T) {
	tests := []struct {
		method        string
		path          string
		expectedScope string
		expectMatch   bool
	}{
		{"POST", "/push.v1.PusherService/Push", auth.ScopeProfilesWrite, true},
		{"POST", "/pyroscope/ingest", auth.ScopeProfilesWrite, true},
		{"POST", "/ingest", auth.ScopeProfilesWrite, true},
		{"POST", "/opentelemetry.proto.collector.profiles.v1development.ProfilesService/Export", auth.ScopeProfilesWrite, true},
		{"POST", "/v1development/profiles", auth.ScopeProfilesWrite, true},
		{"POST", "/debuginfo.v1alpha1.DebuginfoService/ShouldInitiateUpload", auth.ScopeProfilesWrite, true},
		{"POST", "/debuginfo.v1alpha1.DebuginfoService/UploadFinished", auth.ScopeProfilesWrite, true},

		// Negative: old parca routes no longer exist
		{"POST", "/parca.debuginfo.v1alpha1.DebuginfoService/Upload", "", false},
		{"POST", "/parca.debuginfo.v1alpha1.DebuginfoService/ShouldInitiateUpload", "", false},
		{"POST", "/parca.debuginfo.v1alpha1.DebuginfoService/InitiateUpload", "", false},
		{"POST", "/parca.debuginfo.v1alpha1.DebuginfoService/MarkUploadFinished", "", false},

		// Negative: wrong method
		{"GET", "/push.v1.PusherService/Push", "", false},
		{"GET", "/pyroscope/ingest", "", false},
		{"GET", "/ingest", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			r, ok := findRoute(PyroscopeWriteRoutes, tt.method, tt.path)
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

func TestPyroscopeQueryRoutes(t *testing.T) {
	tests := []struct {
		method        string
		path          string
		expectedScope string
		expectMatch   bool
	}{
		// gRPC routes
		{"POST", "/querier.v1.QuerierService/ProfileTypes", auth.ScopeProfilesRead, true},
		{"POST", "/querier.v1.QuerierService/LabelValues", auth.ScopeProfilesRead, true},
		{"POST", "/querier.v1.QuerierService/LabelNames", auth.ScopeProfilesRead, true},
		{"POST", "/querier.v1.QuerierService/Series", auth.ScopeProfilesRead, true},
		{"POST", "/querier.v1.QuerierService/SelectMergeStacktraces", auth.ScopeProfilesRead, true},
		{"POST", "/querier.v1.QuerierService/SelectMergeProfile", auth.ScopeProfilesRead, true},
		{"POST", "/querier.v1.QuerierService/SelectMergeSpanProfile", auth.ScopeProfilesRead, true},
		{"POST", "/querier.v1.QuerierService/SelectSeries", auth.ScopeProfilesRead, true},
		{"POST", "/querier.v1.QuerierService/GetProfileStats", auth.ScopeProfilesRead, true},
		{"POST", "/querier.v1.QuerierService/AnalyzeQuery", auth.ScopeProfilesRead, true},
		{"POST", "/querier.v1.QuerierService/Diff", auth.ScopeProfilesRead, true},
		{"POST", "/querier.v1.QuerierService/SelectHeatmap", auth.ScopeProfilesRead, true},
		{"POST", "/version.v1.Version/Version", auth.ScopeProfilesRead, true},
		{"POST", "/capabilities.v1.FeatureFlagsService/GetFeatureFlags", auth.ScopeProfilesRead, true},
		{"POST", "/vcs.v1.VCSService/GithubApp", auth.ScopeProfilesRead, true},
		{"POST", "/vcs.v1.VCSService/GithubLogin", auth.ScopeProfilesRead, true},
		{"POST", "/vcs.v1.VCSService/GithubRefresh", auth.ScopeProfilesRead, true},
		{"POST", "/vcs.v1.VCSService/GetFile", auth.ScopeProfilesRead, true},
		{"POST", "/vcs.v1.VCSService/GetCommit", auth.ScopeProfilesRead, true},
		{"POST", "/vcs.v1.VCSService/GetCommits", auth.ScopeProfilesRead, true},

		// HTTP routes
		{"GET", "/pyroscope/render", auth.ScopeProfilesRead, true},
		{"GET", "/pyroscope/render-diff", auth.ScopeProfilesRead, true},
		{"GET", "/pyroscope/labels", auth.ScopeProfilesRead, true},
		{"GET", "/pyroscope/label-values", auth.ScopeProfilesRead, true},

		// Negative: wrong method
		{"POST", "/pyroscope/render", "", false},
		{"POST", "/pyroscope/labels", "", false},

		// Negative: unknown path
		{"GET", "/pyroscope/unknown", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			r, ok := findRoute(PyroscopeQueryRoutes, tt.method, tt.path)
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
