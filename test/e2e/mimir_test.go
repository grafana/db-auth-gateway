// SPDX-License-Identifier: AGPL-3.0-only

//go:build requires_docker

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang/snappy"
	e2epkg "github.com/grafana/e2e"
	"github.com/prometheus/prometheus/prompb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	collectormetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"

	"github.com/grafana/db-auth-gateway/test/e2e/services"
)

const mimirConfig = `
multitenancy_enabled: true

usage_stats:
  enabled: false

ingester:
  ring:
    replication_factor: 1
    kvstore:
      store: inmemory

distributor:
  ring:
    kvstore:
      store: inmemory

store_gateway:
  sharding_ring:
    replication_factor: 1
    kvstore:
      store: inmemory

compactor:
  data_dir: /tmp/mimir/compactor

blocks_storage:
  backend: filesystem
  filesystem:
    dir: /tmp/mimir/blocks
  tsdb:
    dir: /tmp/mimir/tsdb

ruler_storage:
  backend: filesystem
  filesystem:
    dir: /tmp/mimir/ruler

alertmanager_storage:
  backend: filesystem
  filesystem:
    dir: /tmp/mimir/alertmanager
`

// TestGateway_Mimir exercises the Mimir-facing flows of db-auth-gateway against a real
// Mimir backend. A single Mimir + gateway pair is started once and shared across the
// subtests, which keeps the suite fast while still covering write, query, OTLP ingestion,
// the read APIs, path rewriting, tenant isolation, auth rejection, and ruler routing.
func TestGateway_Mimir(t *testing.T) {
	s, err := e2epkg.NewScenario(networkName)
	require.NoError(t, err)
	defer s.Close()

	require.NoError(t, writeFileToSharedDir(s, "mimir.yaml", []byte(mimirConfig)))

	mimir := services.NewMimir("mimir", map[string]string{
		"-config.file": filepath.Join(e2epkg.ContainerSharedDir, "mimir.yaml"),
	})
	require.NoError(t, s.StartAndWaitReady(mimir))

	mimirURL := "http://" + mimir.NetworkHTTPEndpoint()
	proxy := services.NewDBAuthGateway("proxy", map[string]string{
		"-gateway.distributor.endpoint": mimirURL,
		"-gateway.query.endpoint":       mimirURL,
		"-gateway.ruler.endpoint":       mimirURL,
		"-mimir.compactor.endpoint":     mimirURL,
	})
	require.NoError(t, s.StartAndWaitReady(proxy))

	base := "http://" + proxy.HTTPEndpoint()

	// WriteAndQuery covers the core path: the gateway injects X-Scope-OrgID on a remote
	// write to the distributor and on the instant query to the query-frontend, and the
	// sample written under a tenant is read back under the same tenant.
	t.Run("WriteAndQuery", func(t *testing.T) {
		const tenant, metric = "write-query", "e2e_write_query_metric"

		res, _, err := remoteWrite(base+"/api/v1/push", tenant, metric, time.Now())
		require.NoError(t, err)
		require.True(t, is2xx(res.StatusCode), "expected 2xx from Mimir write, got %d", res.StatusCode)

		requireQueryValue(t, base, tenant, metric, 1.0)
	})

	// APIPromPrefix verifies the legacy /api/prom prefix rewriting end to end: the write
	// goes through /api/prom/push and the read through /api/prom/api/v1/query, both of
	// which the gateway rewrites to the Cortex /api/v1 and /prometheus paths.
	t.Run("APIPromPrefix", func(t *testing.T) {
		const tenant, metric = "api-prom-prefix", "e2e_api_prom_metric"

		res, _, err := remoteWrite(base+"/api/prom/push", tenant, metric, time.Now())
		require.NoError(t, err)
		require.True(t, is2xx(res.StatusCode), "expected 2xx from /api/prom/push, got %d", res.StatusCode)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			v, ok, err := queryValue(base+"/api/prom/api/v1/query", tenant, metric)
			if !assert.NoError(c, err) {
				return
			}
			assert.True(c, ok, "no series returned yet")
			assert.Equal(c, 1.0, v)
		}, 15*time.Second, 250*time.Millisecond, "metric not queryable via /api/prom prefix")
	})

	// OTLPWrite verifies the unified OTLP metrics ingestion path (/otlp/v1/metrics): the
	// gateway proxies an OTLP protobuf payload to the distributor and the resulting series
	// is queryable through the Prometheus read path.
	t.Run("OTLPWrite", func(t *testing.T) {
		const tenant, metric = "otlp-write", "e2e_otlp_metric"

		res, body, err := otlpMetricsPush(base+"/otlp/v1/metrics", tenant, metric, 42.0, time.Now())
		require.NoError(t, err)
		require.True(t, is2xx(res.StatusCode), "expected 2xx from OTLP write, got %d: %s", res.StatusCode, body)

		requireQueryValue(t, base, tenant, metric, 42.0)
	})

	// ReadAPIs verifies the read-path routes beyond instant query (series, labels,
	// label values, range query) return the data written under the tenant.
	t.Run("ReadAPIs", func(t *testing.T) {
		const tenant, metric = "read-apis", "e2e_read_apis_metric"

		res, _, err := remoteWrite(base+"/api/v1/push", tenant, metric, time.Now())
		require.NoError(t, err)
		require.True(t, is2xx(res.StatusCode), "expected 2xx from write, got %d", res.StatusCode)
		requireQueryValue(t, base, tenant, metric, 1.0)

		t.Run("series", func(t *testing.T) {
			body := requireJSONOK(t, base+"/prometheus/api/v1/series?match[]="+url.QueryEscape(metric), tenant)
			require.True(t, hasSeries(requireSeriesData(t, body), metric), "series response should contain %q, got %s", metric, body)
		})

		t.Run("label_names", func(t *testing.T) {
			body := requireJSONOK(t, base+"/prometheus/api/v1/labels", tenant)
			require.Contains(t, requireDataStrings(t, body), "__name__")
		})

		t.Run("label_values", func(t *testing.T) {
			body := requireJSONOK(t, base+"/prometheus/api/v1/label/__name__/values", tenant)
			require.Contains(t, requireDataStrings(t, body), metric)
		})

		t.Run("query_range", func(t *testing.T) {
			now := time.Now()
			// End a minute past now so a step point lands after the just-written sample.
			q := fmt.Sprintf("%s?query=%s&start=%d&end=%d&step=15",
				base+"/prometheus/api/v1/query_range", url.QueryEscape(metric),
				now.Add(-time.Minute).Unix(), now.Add(time.Minute).Unix())
			body := requireJSONOK(t, q, tenant)
			require.True(t, rangeHasValue(t, body, 1.0), "range query should contain a 1.0 sample for %q, got %s", metric, body)
		})

		t.Run("not_visible_to_other_tenant", func(t *testing.T) {
			body := requireJSONOK(t, base+"/prometheus/api/v1/series?match[]="+url.QueryEscape(metric), "read-apis-other")
			require.Empty(t, requireSeriesData(t, body), "metric must not be visible to a different tenant")
		})
	})

	// TenantIsolation verifies the gateway forwards the per-request X-Scope-OrgID so that
	// a sample written under one tenant is not visible to another tenant.
	t.Run("TenantIsolation", func(t *testing.T) {
		const tenantA, tenantB, metric = "tenant-a", "tenant-b", "e2e_isolation_metric"

		res, _, err := remoteWrite(base+"/api/v1/push", tenantA, metric, time.Now())
		require.NoError(t, err)
		require.True(t, is2xx(res.StatusCode), "expected 2xx from write, got %d", res.StatusCode)
		requireQueryValue(t, base, tenantA, metric, 1.0)

		// Positive control: tenant-b's own write is visible to tenant-b, so the Never
		// assertion below cannot pass merely because tenant-b reads are broken.
		const metricB = "e2e_isolation_metric_b"
		resB, _, err := remoteWrite(base+"/api/v1/push", tenantB, metricB, time.Now())
		require.NoError(t, err)
		require.True(t, is2xx(resB.StatusCode), "expected 2xx from tenant-b write, got %d", resB.StatusCode)
		requireQueryValue(t, base, tenantB, metricB, 1.0)

		// tenantB must never observe tenantA's series; check it stays absent for a while.
		require.Never(t, func() bool {
			_, ok, _ := queryValue(base+"/prometheus/api/v1/query", tenantB, metric)
			return ok
		}, 3*time.Second, 500*time.Millisecond, "tenant-b observed tenant-a's series")
	})

	// MissingOrgID verifies the trust authenticator rejects a request without
	// X-Scope-OrgID with 401 before it reaches the backend.
	t.Run("MissingOrgID", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPost, base+"/api/v1/push", http.NoBody)
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/x-protobuf")

		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = res.Body.Close()
		require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	})

	// Ruler verifies the ruler routes: a rule group created through the gateway is
	// rewritten to the ruler config API and can be read back under the same tenant.
	t.Run("Ruler", func(t *testing.T) {
		const tenant, namespace = "ruler-tenant", "e2e_ns"
		ruleGroup := `name: e2e_group
rules:
  - record: e2e:up:sum
    expr: sum(up)
`
		req, err := http.NewRequest(http.MethodPost, base+"/prometheus/config/v1/rules/"+namespace, bytes.NewBufferString(ruleGroup))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/yaml")
		req.Header.Set("X-Scope-OrgID", tenant)
		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = res.Body.Close()
		require.True(t, is2xx(res.StatusCode), "expected 2xx creating rule group, got %d", res.StatusCode)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, body, err := getWithOrg(base+"/prometheus/config/v1/rules/"+namespace, tenant)
			if !assert.NoError(c, err) {
				return
			}
			assert.Equal(c, http.StatusOK, resp.StatusCode)
			assert.True(c, bytes.Contains(body, []byte("e2e:up:sum")), "rule group not readable back yet")
		}, 15*time.Second, 250*time.Millisecond, "rule group not readable back through the gateway")
	})

	// RulerPrefixRewrites verifies the path-rewrite ruler families beyond the canonical
	// /prometheus/config/v1 prefix tested by Ruler above. Each legacy prefix is rewritten by
	// the gateway to the same /prometheus/config/v1/rules/{namespace} backend route, so a rule
	// group written and read back through a given prefix proves that prefix's rewrite. These
	// rewrites are the most rewrite-bug-prone part of the route table (cf. gap.md), so each
	// family gets its own subtest under its own tenant and namespace to avoid cross-talk.
	t.Run("RulerPrefixRewrites", func(t *testing.T) {
		// rule returns a rule group whose single recording rule name is unique to the prefix,
		// so a read-back can assert it came from the write under the same prefix.
		rule := func(record string) string {
			return fmt.Sprintf("name: e2e_group\nrules:\n  - record: %s\n    expr: vector(1)\n", record)
		}

		cases := []struct{ name, prefix, record string }{
			// /api/prom/rules → /prometheus/config/v1/rules
			{"api_prom", "/api/prom/rules", "e2e:api_prom:rewrite"},
			// /api/v1/rules → (/api → /prometheus/config) → /prometheus/config/v1/rules
			{"api_v1", "/api/v1/rules", "e2e:api_v1:rewrite"},
			// /prometheus/rules → /prometheus/config/v1/rules
			{"prometheus", "/prometheus/rules", "e2e:prometheus:rewrite"},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				tenant := "ruler-rewrite-" + tc.name
				namespace := "e2e_ns_" + tc.name

				req, err := http.NewRequest(http.MethodPost, base+tc.prefix+"/"+namespace, bytes.NewBufferString(rule(tc.record)))
				require.NoError(t, err)
				req.Header.Set("Content-Type", "application/yaml")
				req.Header.Set("X-Scope-OrgID", tenant)
				res, err := http.DefaultClient.Do(req)
				require.NoError(t, err)
				_ = res.Body.Close()
				require.True(t, is2xx(res.StatusCode), "expected 2xx creating rule group via %s, got %d", tc.prefix, res.StatusCode)

				require.EventuallyWithT(t, func(c *assert.CollectT) {
					resp, body, err := getWithOrg(base+tc.prefix+"/"+namespace, tenant)
					if !assert.NoError(c, err) {
						return
					}
					assert.Equal(c, http.StatusOK, resp.StatusCode)
					assert.True(c, bytes.Contains(body, []byte(tc.record)), "rule group not readable back via %s yet", tc.prefix)
				}, 15*time.Second, 250*time.Millisecond, "rule group not readable back through the %s rewrite", tc.prefix)
			})
		}
	})

	// Compactor verifies the TSDB block-upload route reaches the compactor. Block upload is
	// disabled by default, so the compactor itself rejects a block-upload start with 400. The
	// point is that a 400 (a compactor-generated response) proves the gateway routed the
	// request to the compactor, as opposed to a 401 (auth) or 404 (no such gateway route).
	t.Run("Compactor", func(t *testing.T) {
		const tenant = "compactor-tenant"
		// A syntactically valid ULID for a block that does not exist.
		req, err := http.NewRequest(http.MethodPost, base+"/api/v1/upload/block/01JXXXXXXXXXXXXXXXXXXXXXXX/start", bytes.NewBufferString("{}"))
		require.NoError(t, err)
		req.Header.Set("X-Scope-OrgID", tenant)
		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = res.Body.Close()
		require.Equal(t, http.StatusBadRequest, res.StatusCode,
			"block-upload start should be rejected by the compactor with 400 (proves route wiring), got %d", res.StatusCode)
	})
}

// is2xx reports whether code is a 2xx success status.
func is2xx(code int) bool { return code >= 200 && code < 300 }

// remoteWrite sends a minimal Prometheus remote write request to url on behalf of orgID.
// It returns the response and the response body.
func remoteWrite(url, orgID, metricName string, ts time.Time) (*http.Response, []byte, error) {
	wr := &prompb.WriteRequest{
		Timeseries: []prompb.TimeSeries{
			{
				Labels: []prompb.Label{
					{Name: "__name__", Value: metricName},
				},
				Samples: []prompb.Sample{
					{Value: 1.0, Timestamp: ts.UnixMilli()},
				},
			},
		},
	}

	data, err := wr.Marshal()
	if err != nil {
		return nil, nil, err
	}
	compressed := snappy.Encode(nil, data)

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(compressed))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("Content-Encoding", "snappy")
	req.Header.Set("X-Prometheus-Remote-Write-Version", "0.1.0")
	req.Header.Set("X-Scope-OrgID", orgID)

	return doReadBody(req)
}

// otlpMetricsPush sends a minimal OTLP metrics payload (a single gauge) to url on behalf
// of orgID. It returns the response and the response body.
func otlpMetricsPush(url, orgID, metricName string, value float64, ts time.Time) (*http.Response, []byte, error) {
	exportReq := &collectormetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				Resource: &resourcepb.Resource{
					Attributes: []*commonpb.KeyValue{
						{Key: "service.name", Value: &commonpb.AnyValue{
							Value: &commonpb.AnyValue_StringValue{StringValue: "e2e-test"},
						}},
					},
				},
				ScopeMetrics: []*metricspb.ScopeMetrics{
					{
						Metrics: []*metricspb.Metric{
							{
								Name: metricName,
								Data: &metricspb.Metric_Gauge{
									Gauge: &metricspb.Gauge{
										DataPoints: []*metricspb.NumberDataPoint{
											{
												TimeUnixNano: uint64(ts.UnixNano()),
												Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: value},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	data, err := proto.Marshal(exportReq)
	if err != nil {
		return nil, nil, err
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("X-Scope-OrgID", orgID)

	return doReadBody(req)
}

// requireQueryValue polls an instant query until metric returns the expected value.
func requireQueryValue(t *testing.T, base, orgID, metric string, want float64) {
	t.Helper()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		v, ok, err := queryValue(base+"/prometheus/api/v1/query", orgID, metric)
		if !assert.NoError(c, err) {
			return
		}
		assert.True(c, ok, "no series returned yet")
		assert.Equal(c, want, v)
	}, 15*time.Second, 250*time.Millisecond)
}

// queryValue runs an instant query through the proxy and returns the first sample value.
// The bool is false when the query succeeds but returns no series. It returns an error
// (rather than failing the test) so callers can use it inside Eventually/Never closures,
// which testify runs on a separate goroutine where require.* must not be called.
func queryValue(rawURL, orgID, query string) (float64, bool, error) {
	resp, body, err := getWithOrg(rawURL+"?query="+url.QueryEscape(query), orgID)
	if err != nil {
		return 0, false, err
	}
	if resp.StatusCode != http.StatusOK {
		return 0, false, fmt.Errorf("instant query %s: status %d: %s", rawURL, resp.StatusCode, body)
	}

	var parsed struct {
		Data struct {
			Result []struct {
				Value [2]any `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, false, err
	}
	if len(parsed.Data.Result) == 0 {
		return 0, false, nil
	}
	s, ok := parsed.Data.Result[0].Value[1].(string)
	if !ok {
		return 0, false, nil
	}
	var v float64
	if _, err := fmt.Sscanf(s, "%g", &v); err != nil {
		return 0, false, err
	}
	return v, true, nil
}

// requireJSONOK issues a GET with the tenant header and asserts a 200, returning the body.
// It uses require, so call it directly from the test goroutine, not inside an Eventually
// closure (use getWithOrg there).
func requireJSONOK(t *testing.T, rawURL, orgID string) []byte {
	t.Helper()
	resp, body, err := getWithOrg(rawURL, orgID)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, "GET %s failed: %s", rawURL, body)
	return body
}

// requireDataStrings unmarshals a Prometheus {"data":[...]} string-array response, used by
// the labels and label-values endpoints.
func requireDataStrings(t *testing.T, body []byte) []string {
	t.Helper()
	var parsed struct {
		Data []string `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &parsed), "body: %s", body)
	return parsed.Data
}

// requireSeriesData unmarshals a Prometheus {"data":[{label:value}]} series response.
func requireSeriesData(t *testing.T, body []byte) []map[string]string {
	t.Helper()
	var parsed struct {
		Data []map[string]string `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &parsed), "body: %s", body)
	return parsed.Data
}

// hasSeries reports whether any series in the set has __name__ == metric.
func hasSeries(series []map[string]string, metric string) bool {
	for _, s := range series {
		if s["__name__"] == metric {
			return true
		}
	}
	return false
}

// rangeHasValue reports whether a range-query matrix response contains a sample equal to want.
func rangeHasValue(t *testing.T, body []byte, want float64) bool {
	t.Helper()
	var parsed struct {
		Data struct {
			Result []struct {
				Values [][2]any `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &parsed), "body: %s", body)
	for _, r := range parsed.Data.Result {
		for _, v := range r.Values {
			s, ok := v[1].(string)
			if !ok {
				continue
			}
			var f float64
			if _, err := fmt.Sscanf(s, "%g", &f); err == nil && f == want {
				return true
			}
		}
	}
	return false
}

// getWithOrg issues a GET, setting X-Scope-OrgID when orgID is non-empty (an empty orgID
// sends no header, e.g. when the gateway injects the tenant via forward_auth). It never
// fails the test, so it is safe inside Eventually/Never closures.
func getWithOrg(rawURL, orgID string) (*http.Response, []byte, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, nil, err
	}
	if orgID != "" {
		req.Header.Set("X-Scope-OrgID", orgID)
	}
	return doReadBody(req)
}

// doReadBody executes req, reads and closes the body, and returns the response and body.
func doReadBody(req *http.Request) (*http.Response, []byte, error) {
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}
	return resp, body, nil
}
