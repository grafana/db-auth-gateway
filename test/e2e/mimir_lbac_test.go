// SPDX-License-Identifier: AGPL-3.0-only

//go:build requires_docker

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
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

	"github.com/grafana/db-auth-gateway/test/e2e/services"
)

// TestGateway_Mimir_LBAC verifies that the gateway, in forward_auth mode, injects the
// X-Prom-Label-Policy header returned by the auth service so that Mimir's label-based
// access control filters reads to the policy's selector.
//
// LBAC (X-Prom-Label-Policy enforcement) is a new feature in Mimir OSS. This test
// is gated on MIMIR_LBAC_ENABLED and skips by default. When enabled, MIMIR_IMAGE must
// point at an LBAC-capable build that defines -auth.label-access-control-enabled;
// otherwise Mimir rejects the flag below and fails at startup.
func TestGateway_Mimir_LBAC(t *testing.T) {
	if !services.MimirLBACEnabled() {
		t.Skip("MIMIR_LBAC_ENABLED not set; skipping LBAC test (set MIMIR_LBAC_ENABLED=true with an LBAC-capable MIMIR_IMAGE to run it)")
	}

	s, err := e2epkg.NewScenario(networkName)
	require.NoError(t, err)
	defer s.Close()

	require.NoError(t, writeFileToSharedDir(s, "mimir.yaml", []byte(mimirConfig)))
	require.NoError(t, writeFileToSharedDir(s, "forward-auth.conf", []byte(services.ForwardAuthConfig("e2e-tenant", "e2e-tenant:%7Benv%3D%22prod%22%7D"))))

	mimir := services.NewMimirWithImage("mimir", services.DefaultMimirImage(), map[string]string{
		"-config.file":                       filepath.Join(e2epkg.ContainerSharedDir, "mimir.yaml"),
		"-auth.label-access-control-enabled": "true",
	})
	require.NoError(t, s.StartAndWaitReady(mimir))

	authMock := services.NewForwardAuthMock("forward-auth", filepath.Join(e2epkg.ContainerSharedDir, "forward-auth.conf"))
	require.NoError(t, s.StartAndWaitReady(authMock))

	mimirURL := "http://" + mimir.NetworkHTTPEndpoint()
	proxy := services.NewDBAuthGateway("proxy", map[string]string{
		"-auth.type":                    "forward_auth",
		"-forward-auth.url":             "http://" + authMock.NetworkHTTPEndpoint() + "/auth",
		"-gateway.distributor.endpoint": mimirURL,
		"-gateway.query.endpoint":       mimirURL,
	})
	require.NoError(t, s.StartAndWaitReady(proxy))

	base := "http://" + proxy.HTTPEndpoint()

	// Both series are written under the same tenant (the mock resolves every request to
	// e2e-tenant). LBAC is read-path only, so the policy header on the write is ignored
	// and both samples are accepted.
	const metric = "lbac_metric"
	now := time.Now()
	resProd, err := remoteWriteLabels(base+"/api/v1/push", map[string]string{"__name__": metric, "env": "prod"}, now)
	require.NoError(t, err)
	require.True(t, is2xx(resProd.StatusCode), "expected 2xx writing prod series, got %d", resProd.StatusCode)
	resDev, err := remoteWriteLabels(base+"/api/v1/push", map[string]string{"__name__": metric, "env": "dev"}, now)
	require.NoError(t, err)
	require.True(t, is2xx(resDev.StatusCode), "expected 2xx writing dev series, got %d", resDev.StatusCode)

	// Control: query Mimir directly (bypassing the gateway, so no policy is injected) and
	// confirm both series ingested. This makes the exclusion assertion below unfalsifiable:
	// a single-series result then can only mean LBAC filtering, not a missing write.
	mimirDirect := "http://" + mimir.HTTPEndpoint()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		series, err := querySeriesLabelsOrg(mimirDirect+"/prometheus/api/v1/query", metric, "e2e-tenant")
		if !assert.NoError(c, err) {
			return
		}
		envs := map[string]bool{}
		for _, m := range series {
			envs[m["env"]] = true
		}
		assert.True(c, envs["prod"] && envs["dev"], "control query should see both prod and dev series")
	}, 20*time.Second, 250*time.Millisecond, "control query (no policy) should see both prod and dev series, proving both ingested")

	// The policy restricts reads to {env="prod"}: querying the metric must return exactly
	// the prod series and never the dev series, proving the gateway injected the policy
	// and Mimir enforced it.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		series, err := querySeriesLabels(base+"/prometheus/api/v1/query", metric)
		if !assert.NoError(c, err) {
			return
		}
		assert.Len(c, series, 1)
		if len(series) == 1 {
			assert.Equal(c, "prod", series[0]["env"])
		}
	}, 20*time.Second, 250*time.Millisecond, "expected only the {env=\"prod\"} series to be visible under the LBAC policy")

	// The dev series must stay hidden even after it has had time to ingest.
	require.Never(t, func() bool {
		series, err := querySeriesLabels(base+"/prometheus/api/v1/query", metric)
		if err != nil {
			return false
		}
		for _, m := range series {
			if m["env"] == "dev" {
				return true
			}
		}
		return false
	}, 3*time.Second, 500*time.Millisecond, "dev series leaked past the LBAC policy")

	// A selector that does not match the policy must return an empty result, not an error:
	// LBAC narrows the query matcher ({env="prod"} AND {env="dev"} = nothing) rather than
	// rejecting the request.
	t.Run("NegativeRead", func(t *testing.T) {
		series, err := querySeriesLabels(base+"/prometheus/api/v1/query", metric+`{env="dev"}`)
		require.NoError(t, err)
		require.Empty(t, series, "a non-policy-matching selector should return empty under the LBAC policy")
	})

	// Cardinality endpoints are unsupported under a policy and must be rejected with 400.
	t.Run("CardinalityRejected", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, base+"/prometheus/api/v1/cardinality/label_names", nil)
		require.NoError(t, err)
		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = res.Body.Close()
		require.Equal(t, http.StatusBadRequest, res.StatusCode, "cardinality endpoint should be rejected when a policy is present")
	})
}

// remoteWriteLabels sends a remote write with an arbitrary label set (which must include
// __name__). The tenant is resolved by the forward-auth mock, so no X-Scope-OrgID is set.
func remoteWriteLabels(url string, labels map[string]string, ts time.Time) (*http.Response, error) {
	pbLabels := make([]prompb.Label, 0, len(labels))
	for name, value := range labels {
		pbLabels = append(pbLabels, prompb.Label{Name: name, Value: value})
	}
	wr := &prompb.WriteRequest{
		Timeseries: []prompb.TimeSeries{
			{
				Labels:  pbLabels,
				Samples: []prompb.Sample{{Value: 1.0, Timestamp: ts.UnixMilli()}},
			},
		},
	}

	data, err := wr.Marshal()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(snappy.Encode(nil, data)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("Content-Encoding", "snappy")
	req.Header.Set("X-Prometheus-Remote-Write-Version", "0.1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	_ = resp.Body.Close()
	return resp, nil
}

// querySeriesLabels runs an instant query through the gateway (which supplies the tenant
// via forward_auth) and returns the label set of each returned series.
func querySeriesLabels(rawURL, query string) ([]map[string]string, error) {
	return querySeriesLabelsOrg(rawURL, query, "")
}

// querySeriesLabelsOrg is querySeriesLabels with an explicit X-Scope-OrgID, for querying a
// backend directly (bypassing the gateway). An empty orgID sets no header. It returns an
// error rather than failing the test so callers can use it inside Eventually/Never closures,
// which testify runs on a separate goroutine where require.* must not be called.
func querySeriesLabelsOrg(rawURL, query, orgID string) ([]map[string]string, error) {
	resp, body, err := getWithOrg(rawURL+"?query="+url.QueryEscape(query), orgID)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("instant query %s: status %d: %s", rawURL, resp.StatusCode, body)
	}

	var parsed struct {
		Data struct {
			Result []struct {
				Metric map[string]string `json:"metric"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}

	series := make([]map[string]string, 0, len(parsed.Data.Result))
	for _, r := range parsed.Data.Result {
		series = append(series, r.Metric)
	}
	return series, nil
}
