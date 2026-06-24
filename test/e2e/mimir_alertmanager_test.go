// SPDX-License-Identifier: AGPL-3.0-only

//go:build requires_docker

package e2e

import (
	"bytes"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	e2epkg "github.com/grafana/e2e"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/grafana/db-auth-gateway/test/e2e/services"
)

// mimirAlertmanagerConfig is the shared Mimir config plus the settings needed to run the
// alertmanager component and serve its per-tenant config API: external_url is required for
// the alertmanager to start, enable_api turns on the config endpoints the gateway proxies,
// and the sharding ring is pinned to inmemory to match the other single-instance rings.
const mimirAlertmanagerConfig = mimirConfig + `
alertmanager:
  external_url: http://localhost/alertmanager
  enable_api: true
  sharding_ring:
    replication_factor: 1
    kvstore:
      store: inmemory
`

// TestGateway_Mimir_Alertmanager exercises the alertmanager routes (MimirAlertmanagerRoutes),
// which the shared Mimir suite does not cover: a tenant alertmanager config set through the
// gateway's /api/v1/alerts route is proxied to the alertmanager backend and read back under
// the same tenant. Alertmanager runs in its own Mimir here because it needs the alertmanager
// target and an external_url, which the shared read/write suite intentionally omits.
func TestGateway_Mimir_Alertmanager(t *testing.T) {
	s, err := e2epkg.NewScenario(networkName)
	require.NoError(t, err)
	defer s.Close()

	require.NoError(t, writeFileToSharedDir(s, "mimir.yaml", []byte(mimirAlertmanagerConfig)))

	mimir := services.NewMimir("mimir", map[string]string{
		"-config.file": filepath.Join(e2epkg.ContainerSharedDir, "mimir.yaml"),
		"-target":      "all,alertmanager",
	})
	require.NoError(t, s.StartAndWaitReady(mimir))

	mimirURL := "http://" + mimir.NetworkHTTPEndpoint()
	proxy := services.NewDBAuthGateway("proxy", map[string]string{
		"-mimir.alertmanager.endpoint": mimirURL,
	})
	require.NoError(t, s.StartAndWaitReady(proxy))

	base := "http://" + proxy.HTTPEndpoint()

	const tenant = "am-tenant"
	amConfig := `alertmanager_config: |
  route:
    receiver: default
  receivers:
    - name: default
`

	// Set the tenant alertmanager config through the gateway (POST /api/v1/alerts).
	req, err := http.NewRequest(http.MethodPost, base+"/api/v1/alerts", bytes.NewBufferString(amConfig))
	require.NoError(t, err)
	req.Header.Set("X-Scope-OrgID", tenant)
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = res.Body.Close()
	require.True(t, is2xx(res.StatusCode), "expected 2xx setting alertmanager config, got %d", res.StatusCode)

	// Read it back through the gateway under the same tenant.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, body, err := getWithOrg(base+"/api/v1/alerts", tenant)
		if !assert.NoError(c, err) {
			return
		}
		assert.Equal(c, http.StatusOK, resp.StatusCode)
		assert.True(c, bytes.Contains(body, []byte("receiver: default")), "alertmanager config not readable back yet")
	}, 15*time.Second, 250*time.Millisecond, "alertmanager config not readable back through the gateway")
}
