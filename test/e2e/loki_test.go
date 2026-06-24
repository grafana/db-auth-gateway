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
	"sort"
	"testing"
	"time"

	e2epkg "github.com/grafana/e2e"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/grafana/db-auth-gateway/test/e2e/services"
)

const lokiConfig = `
auth_enabled: true

server:
  http_listen_port: 3100

common:
  instance_addr: 127.0.0.1
  path_prefix: /tmp/loki
  storage:
    filesystem:
      chunks_directory: /tmp/loki/chunks
      rules_directory: /tmp/loki/rules
  replication_factor: 1
  ring:
    kvstore:
      store: inmemory

schema_config:
  configs:
    - from: 2020-10-24
      store: tsdb
      object_store: filesystem
      schema: v13
      index:
        prefix: index_
        period: 24h
`

// lokiTestStreams is the dev+prod fixture both Loki e2e tests push and query back.
func lokiTestStreams(base time.Time) []lokiStream {
	return []lokiStream{
		{
			Labels: map[string]string{
				"job": "e2e-test",
				"env": "dev",
			},
			Entries: []lokiEntry{
				{Timestamp: base, Line: "dev log entry 1"},
				{Timestamp: base.Add(time.Nanosecond), Line: "dev log entry 2"},
			},
		},
		{
			Labels: map[string]string{
				"job": "e2e-test",
				"env": "prod",
			},
			Entries: []lokiEntry{
				{Timestamp: base.Add(2 * time.Nanosecond), Line: "prod log entry 3"},
				{Timestamp: base.Add(3 * time.Nanosecond), Line: "prod log entry 4"},
			},
		},
	}
}

// TestGateway_Loki_WriteAndQuery verifies that db-auth-gateway proxies a Loki log push and
// query end to end and forwards the per-request X-Scope-OrgID: logs written under a tenant
// are read back under the same tenant. It runs against any Loki image and asserts no LBAC
// behaviour; the policy enforcement is covered by TestGateway_Loki_LBAC.
func TestGateway_Loki_WriteAndQuery(t *testing.T) {
	s, err := e2epkg.NewScenario(networkName)
	require.NoError(t, err)
	defer s.Close()

	require.NoError(t, writeFileToSharedDir(s, "loki.yaml", []byte(lokiConfig)))

	loki := services.NewLoki("loki", map[string]string{
		"-config.file": filepath.Join(e2epkg.ContainerSharedDir, "loki.yaml"),
	})
	require.NoError(t, s.StartAndWaitReady(loki))

	proxy := services.NewDBAuthGateway("proxy", map[string]string{
		"-backend":                      "loki",
		"-gateway.distributor.endpoint": "http://" + loki.NetworkHTTPEndpoint(),
		"-gateway.query.endpoint":       "http://" + loki.NetworkHTTPEndpoint(),
	})
	require.NoError(t, s.StartAndWaitReady(proxy))

	proxyBase := "http://" + proxy.HTTPEndpoint()

	const tenant = "grafana"
	base := time.Now()
	streams := lokiTestStreams(base)
	queryStart := base.Add(-time.Hour)
	queryEnd := base.Add(time.Hour)

	res, err := lokiPush(proxyBase+"/loki/api/v1/push", tenant, streams)
	require.NoError(t, err)
	require.True(t, res.StatusCode >= 200 && res.StatusCode < 300, "expected 2xx from Loki push, got %d", res.StatusCode)

	// Both streams written under the tenant are read back under the same tenant.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		res, body, err := lokiQuery(proxyBase+"/loki/api/v1/query_range", tenant, `{job="e2e-test"}`, queryStart, queryEnd)
		if !assert.NoError(c, err) {
			return
		}
		if !assert.Equal(c, http.StatusOK, res.StatusCode) {
			return
		}
		var result lokiQueryResult
		if !assert.NoError(c, json.Unmarshal(body, &result)) {
			return
		}
		assert.Equal(c, map[string][]string{
			"dev":  {"dev log entry 1", "dev log entry 2"},
			"prod": {"prod log entry 3", "prod log entry 4"},
		}, entriesByEnv(result))
	}, 20*time.Second, 250*time.Millisecond, "query should return both dev and prod streams written under the tenant")
}

// TestGateway_Loki_LBAC exercises the forward_auth path against Loki: the gateway injects
// the X-Scope-OrgID and X-Prom-Label-Policy returned by an inline nginx forward-auth mock
// (services.ForwardAuthConfig), so the fixture lives next to the test. It writes a dev and a
// prod stream and reads them back through the gateway. This mirrors mimir_lbac_test.go.
//
// The enforcement assertion is gated on LOKI_LBAC_ENABLED:
//   - LOKI_LBAC_ENABLED=true (LOKI_IMAGE must be an LBAC-capable build): the {env="dev"}
//     policy must hide the prod stream, so the read returns only the dev entries.
//   - unset/false (the default, public Loki): Loki ignores the policy header, so the read
//     returns all four entries. This still verifies the forward_auth write/query path
//     without needing an LBAC-capable image.
func TestGateway_Loki_LBAC(t *testing.T) {
	lbac := services.LokiLBACEnabled()

	s, err := e2epkg.NewScenario(networkName)
	require.NoError(t, err)
	defer s.Close()

	// The lbac config key only exists in LBAC-capable Loki builds; a public image rejects
	// it at startup, so only enable it when we are running the enforcement path.
	lokiCfg := lokiConfig
	if lbac {
		lokiCfg += "\nlbac:\n  enabled: true\n"
	}
	require.NoError(t, writeFileToSharedDir(s, "loki.yaml", []byte(lokiCfg)))
	require.NoError(t, writeFileToSharedDir(s, "forward-auth.conf",
		[]byte(services.ForwardAuthConfig("grafana", "grafana:%7Benv%3D%22dev%22%7D"))))

	loki := services.NewLoki("loki", map[string]string{
		"-config.file": filepath.Join(e2epkg.ContainerSharedDir, "loki.yaml"),
	})
	authMock := services.NewForwardAuthMock("forward-auth", filepath.Join(e2epkg.ContainerSharedDir, "forward-auth.conf"))
	require.NoError(t, s.StartAndWaitReady(loki, authMock))

	proxy := services.NewDBAuthGateway("proxy", map[string]string{
		"-backend":                      "loki",
		"-auth.type":                    "forward_auth",
		"-forward-auth.url":             "http://" + authMock.NetworkHTTPEndpoint() + "/auth",
		"-gateway.distributor.endpoint": "http://" + loki.NetworkHTTPEndpoint(),
		"-gateway.query.endpoint":       "http://" + loki.NetworkHTTPEndpoint(),
	})
	require.NoError(t, s.StartAndWaitReady(proxy))

	proxyBase := "http://" + proxy.HTTPEndpoint()

	base := time.Now()
	streams := lokiTestStreams(base)
	queryStart := base.Add(-time.Hour)
	queryEnd := base.Add(time.Hour)

	// Write both streams through the gateway. LBAC is read-path only, so the injected
	// {env="dev"} policy does not block the prod write.
	res, err := lokiPush(proxyBase+"/loki/api/v1/push", "", streams)
	require.NoError(t, err)
	require.True(t, res.StatusCode >= 200 && res.StatusCode < 300, "expected 2xx from Loki push, got %d", res.StatusCode)

	// Control: query Loki directly (bypassing the gateway, so no policy is injected) under
	// the same tenant and confirm both env streams ingested. This makes the LBAC exclusion
	// below unfalsifiable: a dev-only result then can only mean policy filtering, not a
	// missing write.
	lokiDirect := "http://" + loki.HTTPEndpoint()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		res, body, err := lokiQuery(lokiDirect+"/loki/api/v1/query_range", "grafana", `{job="e2e-test"}`, queryStart, queryEnd)
		if !assert.NoError(c, err) {
			return
		}
		if !assert.Equal(c, http.StatusOK, res.StatusCode) {
			return
		}
		var result lokiQueryResult
		if !assert.NoError(c, json.Unmarshal(body, &result)) {
			return
		}
		assert.Equal(c, map[string][]string{
			"dev":  {"dev log entry 1", "dev log entry 2"},
			"prod": {"prod log entry 3", "prod log entry 4"},
		}, entriesByEnv(result))
	}, 20*time.Second, 250*time.Millisecond, "control query (no policy) should see both dev and prod streams")

	// Through the gateway: with LBAC the {env="dev"} policy hides the prod stream; without it
	// the policy header is ignored and all four entries are returned.
	want := map[string][]string{
		"dev":  {"dev log entry 1", "dev log entry 2"},
		"prod": {"prod log entry 3", "prod log entry 4"},
	}
	msg := "without LBAC the policy is not enforced, so all streams should be returned"
	if lbac {
		want = map[string][]string{
			"dev": {"dev log entry 1", "dev log entry 2"},
		}
		msg = "expected only the {env=\"dev\"} logs to be visible under the LBAC policy"
	}
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		res, body, err := lokiQuery(proxyBase+"/loki/api/v1/query_range", "", `{job="e2e-test"}`, queryStart, queryEnd)
		if !assert.NoError(c, err) {
			return
		}
		if !assert.Equal(c, http.StatusOK, res.StatusCode) {
			return
		}
		var result lokiQueryResult
		if !assert.NoError(c, json.Unmarshal(body, &result)) {
			return
		}
		assert.Equal(c, want, entriesByEnv(result))
	}, 20*time.Second, 250*time.Millisecond, msg)
}

func entriesByEnv(result lokiQueryResult) map[string][]string {
	got := map[string][]string{}
	for _, s := range result.Data.Result {
		env := s.Stream["env"]
		for _, v := range s.Values {
			got[env] = append(got[env], v[1])
		}
	}
	for k := range got {
		sort.Strings(got[k])
	}
	return got
}

// lokiStream is a labelled set of log entries to push to Loki.
type lokiStream struct {
	Labels  map[string]string
	Entries []lokiEntry
}

// lokiEntry is a single log line with its timestamp.
type lokiEntry struct {
	Timestamp time.Time
	Line      string
}

// lokiPush sends a minimal Loki push request to url. It sets X-Scope-OrgID only when orgID
// is non-empty; through the gateway in forward_auth mode the tenant is injected upstream,
// so callers pass an empty orgID there.
func lokiPush(url, orgID string, streams []lokiStream) (*http.Response, error) {

	type wireStream struct {
		Stream map[string]string `json:"stream"`
		Values [][2]string       `json:"values"`
	}
	wire := struct {
		Streams []wireStream `json:"streams"`
	}{
		Streams: make([]wireStream, len(streams)),
	}
	for i, s := range streams {
		values := make([][2]string, len(s.Entries))
		for j, e := range s.Entries {
			values[j] = [2]string{fmt.Sprintf("%d", e.Timestamp.UnixNano()), e.Line}
		}
		wire.Streams[i] = wireStream{Stream: s.Labels, Values: values}
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if orgID != "" {
		req.Header.Set("X-Scope-OrgID", orgID)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	_ = resp.Body.Close()
	return resp, nil
}

// lokiQueryResult is the relevant subset of a Loki query_range response.
type lokiQueryResult struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Stream map[string]string `json:"stream"`
			Values [][2]string       `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

// lokiQuery sends a Loki query_range request over the [start, end] window. It sets
// X-Scope-OrgID only when orgID is non-empty (empty when querying through the gateway,
// which injects the tenant via forward_auth).
func lokiQuery(rawURL, orgID, query string, start, end time.Time) (*http.Response, []byte, error) {
	params := url.Values{
		"query": {query},
		"start": {fmt.Sprintf("%d", start.UnixNano())},
		"end":   {fmt.Sprintf("%d", end.UnixNano())},
	}
	req, err := http.NewRequest(http.MethodGet, rawURL+"?"+params.Encode(), nil)
	if err != nil {
		return nil, nil, err
	}
	if orgID != "" {
		req.Header.Set("X-Scope-OrgID", orgID)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return resp, nil, err
	}
	return resp, body, nil

}
