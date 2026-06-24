// SPDX-License-Identifier: AGPL-3.0-only

package gateway

import (
	"strings"
	"testing"
	"time"

	"github.com/go-kit/log"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLimitsLoadingFromYaml(t *testing.T) {
	inp1 := `loki: { '1234': { block_queries: true, block_writes: true} }`
	inp2 := `loki: { '1234': { block_queries: true, block_queries_message: "test test test", block_writes: true, block_writes_message: "hello", log_volume_histogram: {timeout: "20s"}, block_hint_queries: {timeout: "10s"}} }`

	// Init fake overrides as default so that in the future when
	// new fields are added to the overrides then the later asserts
	// will catch if the yaml unmarshalling needs to be fixed.
	var fakeOverrides Overrides
	fakeOverrides.LokiTenantOverrides = make(map[string]*LokiOverrides)
	fakeOverrides.LokiTenantOverrides["1234"] = &LokiOverrides{
		BlockQueries:        true,
		BlockQueriesMessage: defaultLokiOverrides.BlockQueriesMessage,
		BlockWrites:         true,
		BlockWritesMessage:  defaultLokiOverrides.BlockWritesMessage,
		LogVolumeHistogram: LogVolmeHistogramOverrides{
			Enabled:         true,
			Timeout:         10 * time.Second,
			HTTPHeaderKey:   queryTagsHTTPHeaderKey,
			HTTPHeaderValue: logVolumeHistogramHTTPHeaderValue,
		},
		BlockHintQueries: BlockHintQueriesOverrides{
			Enabled:         false,
			Timeout:         3 * time.Second,
			HTTPHeaderKey:   queryTagsHTTPHeaderKey,
			HTTPHeaderValue: lokiHintQueriesHTTPHeaderValue,
			Message:         defaultLokiOverrides.BlockHintQueries.Message,
		},
	}

	v, err := loadRuntimeConfig(strings.NewReader(inp1))
	require.NoError(t, err)
	l := *v.(*Overrides)
	assert.Equal(t, fakeOverrides, l, "from yaml")

	fakeOverrides.LokiTenantOverrides["1234"] = &LokiOverrides{
		BlockQueries:        true,
		BlockQueriesMessage: "test test test",
		BlockWrites:         true,
		BlockWritesMessage:  "hello",
		LogVolumeHistogram: LogVolmeHistogramOverrides{
			Enabled:         true,
			Timeout:         20 * time.Second,
			HTTPHeaderKey:   queryTagsHTTPHeaderKey,
			HTTPHeaderValue: logVolumeHistogramHTTPHeaderValue},
		BlockHintQueries: BlockHintQueriesOverrides{
			Enabled:         false,
			Timeout:         10 * time.Second,
			HTTPHeaderKey:   queryTagsHTTPHeaderKey,
			HTTPHeaderValue: lokiHintQueriesHTTPHeaderValue,
			Message:         defaultLokiOverrides.BlockHintQueries.Message,
		},
	}
	v, err = loadRuntimeConfig(strings.NewReader(inp2))
	require.NoError(t, err)
	l = *v.(*Overrides)
	assert.Equal(t, fakeOverrides, l, "from yaml")
}

func TestTenantOverrides(t *testing.T) {
	rtc, err := initTenantOverrides(APIOverridesConfig{Path: "testdata/test_tenant_overrides.yaml", ReloadPeriod: time.Second * 15}, log.NewNopLogger(), prometheus.NewRegistry())
	require.NoError(t, err)

	t.Run("loki", func(t *testing.T) {
		assert.True(t, rtc.GetLokiConfig("1234").BlockQueries)
		assert.False(t, rtc.GetLokiConfig("2345").BlockQueries)
	})

	t.Run("mimir writes", func(t *testing.T) {
		_, user1 := rtc.GetMimirConfig("5432")
		assert.Equal(t, defaultMimirTenantOverrides.BlockReads, user1.BlockReads)
		assert.Equal(t, defaultMimirTenantOverrides.BlockReadsMessage, user1.BlockReadsMessage)
		assert.True(t, *user1.BlockWrites)
		assert.Equal(t, "Go away!", user1.BlockWritesMessage)
	})

	t.Run("mimir reads", func(t *testing.T) {
		_, user1 := rtc.GetMimirConfig("9999")
		assert.True(t, *user1.BlockReads)
		assert.Equal(t, "Please stop sending queries that crash our system.", user1.BlockReadsMessage)
		assert.Equal(t, defaultMimirTenantOverrides.BlockWrites, user1.BlockWrites)
		assert.Equal(t, defaultMimirTenantOverrides.BlockWritesMessage, user1.BlockWritesMessage)
	})

	t.Run("mimir ruler", func(t *testing.T) {
		_, user1 := rtc.GetMimirConfig("5550")
		assert.True(t, *user1.BlockRuler)
		assert.Equal(t, "Ruler API on this Hosted Metrics instance is disabled. Please contact support.", user1.BlockRulerMessage)
		assert.Equal(t, defaultMimirTenantOverrides.BlockRulerHTTPResponseCode, user1.BlockRulerHTTPResponseCode)
		assert.Equal(t, defaultMimirTenantOverrides.BlockRulerHTTPRetryAfter, user1.BlockRulerHTTPRetryAfter)

		_, user2 := rtc.GetMimirConfig("5555")
		assert.True(t, *user2.BlockRuler)
		assert.Equal(t, "Please stop sending ruler config requests that crash our system.", user2.BlockRulerMessage)
		assert.Equal(t, 529, user2.BlockRulerHTTPResponseCode)
		assert.Equal(t, "120", user2.BlockRulerHTTPRetryAfter)
	})
}

func TestTenantOverrides_MultipleFiles(t *testing.T) {
	rtc, err := initTenantOverrides(APIOverridesConfig{Path: "testdata/test_global_overrides.yaml,testdata/test_mixed_overrides.yaml", ReloadPeriod: time.Second * 15}, log.NewNopLogger(), prometheus.NewRegistry())
	require.NoError(t, err)

	mimirGlobalConfig, mimirTenantOverrides := rtc.GetMimirConfig("1234")
	assert.True(t, *mimirGlobalConfig.BlockRuler)                          // From test_global_overrides.yaml
	assert.Nil(t, mimirTenantOverrides.BlockRuler)                         // No override for this config in test_mixed_overrides.yaml
	assert.Equal(t, 502, mimirGlobalConfig.BlockWritesHTTPResponseCode)    // From test_global_overrides.yaml
	assert.Equal(t, 409, mimirTenantOverrides.BlockWritesHTTPResponseCode) // From test_mixed_overrides.yaml
}

func TestNilOverrides(t *testing.T) {
	rtc, err := initTenantOverrides(APIOverridesConfig{Path: "", ReloadPeriod: time.Second * 15}, log.NewNopLogger(), prometheus.NewRegistry())
	require.NoError(t, err)

	t.Run("loki", func(t *testing.T) {
		assert.False(t, rtc.GetLokiConfig("1234").BlockQueries)
		assert.False(t, rtc.GetLokiConfig("2345").BlockQueries)
	})

	t.Run("mimir", func(t *testing.T) {
		_, user1 := rtc.GetMimirConfig("5432")
		assert.Nil(t, user1.BlockReads)
		assert.Equal(t, defaultMimirTenantOverrides.BlockReadsMessage, user1.BlockReadsMessage)
		assert.Nil(t, user1.BlockWrites)
		assert.Equal(t, defaultMimirTenantOverrides.BlockWritesMessage, user1.BlockWritesMessage)
		assert.Nil(t, user1.BlockRuler)
		assert.Equal(t, defaultMimirTenantOverrides.BlockRulerMessage, user1.BlockRulerMessage)
	})
}
