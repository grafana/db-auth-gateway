// SPDX-License-Identifier: AGPL-3.0-only

package router

import "github.com/grafana/db-auth-gateway/pkg/auth"

var PyroscopeWriteRoutes = []Route{
	NewRoute("/push.v1.PusherService/Push", []string{"POST"}, auth.ScopeProfilesWrite),
	NewRoute("/pyroscope/ingest", []string{"POST"}, auth.ScopeProfilesWrite),
	NewRoute("/ingest", []string{"POST"}, auth.ScopeProfilesWrite),
	NewRoute("/opentelemetry.proto.collector.profiles.v1development.ProfilesService/Export", []string{"POST"}, auth.ScopeProfilesWrite),
	NewRoute("/v1development/profiles", []string{"POST"}, auth.ScopeProfilesWrite),
	NewRoute("/debuginfo.v1alpha1.DebuginfoService/ShouldInitiateUpload", []string{"POST"}, auth.ScopeProfilesWrite),
	NewRoute("/debuginfo.v1alpha1.DebuginfoService/UploadFinished", []string{"POST"}, auth.ScopeProfilesWrite),
}

var PyroscopeDebugInfoUploadRoute = NewRoute("/debuginfo.v1alpha1.DebuginfoService/Upload/{gnu_build_id}", []string{"POST"}, auth.ScopeProfilesWrite)

var PyroscopeQueryRoutes = []Route{
	NewRoute("/querier.v1.QuerierService/ProfileTypes", []string{"POST"}, auth.ScopeProfilesRead),
	NewRoute("/querier.v1.QuerierService/LabelValues", []string{"POST"}, auth.ScopeProfilesRead),
	NewRoute("/querier.v1.QuerierService/LabelNames", []string{"POST"}, auth.ScopeProfilesRead),
	NewRoute("/querier.v1.QuerierService/Series", []string{"POST"}, auth.ScopeProfilesRead),
	NewRoute("/querier.v1.QuerierService/SelectMergeStacktraces", []string{"POST"}, auth.ScopeProfilesRead),
	NewRoute("/querier.v1.QuerierService/SelectMergeProfile", []string{"POST"}, auth.ScopeProfilesRead),
	NewRoute("/querier.v1.QuerierService/SelectMergeSpanProfile", []string{"POST"}, auth.ScopeProfilesRead),
	NewRoute("/querier.v1.QuerierService/SelectSeries", []string{"POST"}, auth.ScopeProfilesRead),
	NewRoute("/querier.v1.QuerierService/GetProfileStats", []string{"POST"}, auth.ScopeProfilesRead),
	NewRoute("/querier.v1.QuerierService/AnalyzeQuery", []string{"POST"}, auth.ScopeProfilesRead),
	NewRoute("/querier.v1.QuerierService/Diff", []string{"POST"}, auth.ScopeProfilesRead),
	NewRoute("/querier.v1.QuerierService/SelectHeatmap", []string{"POST"}, auth.ScopeProfilesRead),
	NewRoute("/version.v1.Version/Version", []string{"POST"}, auth.ScopeProfilesRead),
	NewRoute("/capabilities.v1.FeatureFlagsService/GetFeatureFlags", []string{"POST"}, auth.ScopeProfilesRead),
	NewRoute("/vcs.v1.VCSService/GithubApp", []string{"POST"}, auth.ScopeProfilesRead),
	NewRoute("/vcs.v1.VCSService/GithubLogin", []string{"POST"}, auth.ScopeProfilesRead),
	NewRoute("/vcs.v1.VCSService/GithubRefresh", []string{"POST"}, auth.ScopeProfilesRead),
	NewRoute("/vcs.v1.VCSService/GetFile", []string{"POST"}, auth.ScopeProfilesRead),
	NewRoute("/vcs.v1.VCSService/GetCommit", []string{"POST"}, auth.ScopeProfilesRead),
	NewRoute("/vcs.v1.VCSService/GetCommits", []string{"POST"}, auth.ScopeProfilesRead),
	NewRoute("/pyroscope/render", []string{"GET"}, auth.ScopeProfilesRead),
	NewRoute("/pyroscope/render-diff", []string{"GET"}, auth.ScopeProfilesRead),
	NewRoute("/pyroscope/labels", []string{"GET"}, auth.ScopeProfilesRead),
	NewRoute("/pyroscope/label-values", []string{"GET"}, auth.ScopeProfilesRead),
}
