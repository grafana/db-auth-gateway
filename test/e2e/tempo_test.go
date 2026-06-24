// SPDX-License-Identifier: AGPL-3.0-only

//go:build requires_docker

package e2e

import (
	"bytes"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	e2epkg "github.com/grafana/e2e"
	"github.com/stretchr/testify/require"
	collectorpb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/grafana/db-auth-gateway/test/e2e/services"
)

const tempoConfig = `
distributor:
  receivers:
    otlp:
      protocols:
        http:
          endpoint: 0.0.0.0:4318

storage:
  trace:
    backend: local
    local:
      path: /tmp/tempo/traces
    wal:
      path: /tmp/tempo/wal
`

// TestGateway_Tempo_WriteAndQuery verifies that db-auth-gateway correctly proxies an OTLP
// trace write to Tempo and that a subsequent trace query returns HTTP 200.
func TestGateway_Tempo_WriteAndQuery(t *testing.T) {
	s, err := e2epkg.NewScenario(networkName)
	require.NoError(t, err)
	defer s.Close()

	require.NoError(t, writeFileToSharedDir(s, "tempo.yaml", []byte(tempoConfig)))

	tempo := services.NewTempo("tempo", map[string]string{
		"-config.file": filepath.Join(e2epkg.ContainerSharedDir, "tempo.yaml"),
	})
	require.NoError(t, s.StartAndWaitReady(tempo))

	proxy := services.NewDBAuthGateway("proxy", map[string]string{
		"-backend":                         "tempo",
		"-tempo.query.endpoint":            "http://" + tempo.NetworkHTTPEndpoint(),
		"-tempo.distributor.http-endpoint": "http://" + tempo.NetworkEndpoint(services.TempoOTLPHTTPPort),
	})
	require.NoError(t, s.StartAndWaitReady(proxy))

	proxyBase := "http://" + proxy.HTTPEndpoint()

	traceID, res, err := otlpPush(proxyBase+"/otlp/v1/traces", "test-tenant")
	require.NoError(t, err)
	require.True(t, res.StatusCode >= 200 && res.StatusCode < 300, "expected 2xx from Tempo write, got %d", res.StatusCode)

	res, err = traceQuery(proxyBase+"/tempo/api/traces/"+traceID, "test-tenant")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, res.StatusCode, "expected 200 from Tempo query")
}

// TestGateway_Tempo_MissingOrgID verifies that requests without X-Scope-OrgID are rejected
// with 401 by the trust authenticator before reaching the backend.
func TestGateway_Tempo_MissingOrgID(t *testing.T) {
	s, err := e2epkg.NewScenario(networkName)
	require.NoError(t, err)
	defer s.Close()

	require.NoError(t, writeFileToSharedDir(s, "tempo.yaml", []byte(tempoConfig)))

	tempo := services.NewTempo("tempo", map[string]string{
		"-config.file": filepath.Join(e2epkg.ContainerSharedDir, "tempo.yaml"),
	})
	require.NoError(t, s.StartAndWaitReady(tempo))

	proxy := services.NewDBAuthGateway("proxy", map[string]string{
		"-backend":                         "tempo",
		"-tempo.query.endpoint":            "http://" + tempo.NetworkHTTPEndpoint(),
		"-tempo.distributor.http-endpoint": "http://" + tempo.NetworkEndpoint(services.TempoOTLPHTTPPort),
	})
	require.NoError(t, s.StartAndWaitReady(proxy))

	req, err := http.NewRequest(http.MethodPost, "http://"+proxy.HTTPEndpoint()+"/otlp/v1/traces", http.NoBody)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-protobuf")

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = res.Body.Close()
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
}

// otlpPush sends a minimal OTLP trace to url on behalf of orgID.
// Returns the hex-encoded trace ID and the HTTP response.
func otlpPush(url, orgID string) (string, *http.Response, error) {
	traceID := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	spanID := [8]byte{1, 2, 3, 4, 5, 6, 7, 8}
	now := uint64(time.Now().UnixNano())

	exportReq := &collectorpb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{
			{
				Resource: &resourcepb.Resource{
					Attributes: []*commonpb.KeyValue{
						{Key: "service.name", Value: &commonpb.AnyValue{
							Value: &commonpb.AnyValue_StringValue{StringValue: "e2e-test"},
						}},
					},
				},
				ScopeSpans: []*tracepb.ScopeSpans{
					{
						Spans: []*tracepb.Span{
							{
								TraceId:           traceID[:],
								SpanId:            spanID[:],
								Name:              "test-span",
								Kind:              tracepb.Span_SPAN_KIND_INTERNAL,
								StartTimeUnixNano: now - 1_000_000,
								EndTimeUnixNano:   now,
							},
						},
					},
				},
			},
		},
	}

	data, err := proto.Marshal(exportReq)
	if err != nil {
		return "", nil, err
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("X-Scope-OrgID", orgID)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", nil, err
	}
	_ = resp.Body.Close()

	return fmt.Sprintf("%x", traceID[:]), resp, nil
}

// traceQuery fetches a trace by ID through the proxy.
func traceQuery(url, orgID string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Scope-OrgID", orgID)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	_ = resp.Body.Close()
	return resp, nil
}
