// SPDX-License-Identifier: AGPL-3.0-only

package gateway

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/go-kit/log"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestMimirOverridesMiddleware_GlobalSettings(t *testing.T) {
	rtc, err := initTenantOverrides(APIOverridesConfig{Path: "testdata/test_global_overrides.yaml", ReloadPeriod: time.Second * 15}, log.NewNopLogger(), prometheus.NewRegistry())
	require.NoError(t, err)

	mo := NewMimirOverridesMiddleware(rtc)
	mock := &mockHandler{}
	h := mo.Wrap(mock)

	for n, tc := range map[string]struct {
		path               string
		expectedCode       int
		expectedMessage    string
		expectedRetryAfter string
	}{
		"disabled read": {
			path:               "/api/prom/api/v1/read",
			expectedCode:       http.StatusInternalServerError,
			expectedMessage:    `{"status":"error","error":"Down for really good reasons."}`,
			expectedRetryAfter: "30",
		},
		"disabled write": {
			path:            "/api/v1/push",
			expectedCode:    http.StatusBadGateway,
			expectedMessage: `{"status":"error","error":"Down for silly reasons."}`,
		},
		"disabled ruler": {
			path:            "/api/prom/config/v1/rules/my-namespace",
			expectedCode:    http.StatusServiceUnavailable,
			expectedMessage: `{"status":"error","error":"Ruler API is temporarily disabled. Please retry later."}`,
		},
	} {
		t.Run(n, func(t *testing.T) {
			req := httptest.NewRequest("GET", tc.path, nil)
			req.Header.Add("X-Scope-OrgId", "1234")
			resp := httptest.NewRecorder()
			h.ServeHTTP(resp, req)

			assert.Equal(t, tc.expectedCode, resp.Code)
			assert.Equal(t, tc.expectedMessage, resp.Body.String())
			assert.Equal(t, tc.expectedRetryAfter, resp.Header().Get(httpRetryAfter))
		})
	}
}

func TestMimirOverridesMiddleware_PerTenantSettings(t *testing.T) {
	rtc, err := initTenantOverrides(APIOverridesConfig{Path: "testdata/test_tenant_overrides.yaml", ReloadPeriod: time.Second * 15}, log.NewNopLogger(), prometheus.NewRegistry())
	require.NoError(t, err)

	mo := NewMimirOverridesMiddleware(rtc)
	mock := &mockHandler{}
	h := mo.Wrap(mock)

	for n, tc := range map[string]struct {
		tenant             string
		path               string
		expectedCode       int
		expectedMessage    string
		expectedRetryAfter string
	}{
		"ok": {
			tenant:       "1111", // no overrides
			path:         "/api/prom/api/v1/read",
			expectedCode: http.StatusOK,
		},
		"enabled read": {
			tenant:       "5432",
			path:         "/api/prom/api/v1/read",
			expectedCode: http.StatusOK,
		},
		"disabled read": {
			tenant:          "9999",
			path:            "/api/prom/api/v1/read",
			expectedCode:    http.StatusUnauthorized,
			expectedMessage: `{"status":"error","error":"Please stop sending queries that crash our system."}`,
		},
		"disabled reads with custom response code": {
			tenant:          "6666",
			path:            "/api/prom/api/v1/read",
			expectedCode:    529,
			expectedMessage: `{"status":"error","error":"Please stop sending queries that crash our system."}`,
		},
		"disabled writes": {
			tenant:          "5432",
			path:            "/api/prom/push",
			expectedCode:    http.StatusUnauthorized,
			expectedMessage: `{"status":"error","error":"Go away!"}`,
		},
		"disabled writes with custom response code and retry after": {
			tenant:             "7777",
			path:               "/api/prom/push",
			expectedCode:       529,
			expectedMessage:    `{"status":"error","error":"Please stop sending queries that crash our system."}`,
			expectedRetryAfter: "120",
		},
		"disabled writes with custom response code and retry after is negative": {
			tenant:             "8888",
			path:               "/api/prom/push",
			expectedCode:       529,
			expectedMessage:    `{"status":"error","error":"Please stop sending queries that crash our system."}`,
			expectedRetryAfter: "", // retry-after header is removed if it is <= 0
		},
		"disabled writes with custom response code and retry after is +00123": {
			tenant:             "8889",
			path:               "/api/prom/push",
			expectedCode:       529,
			expectedMessage:    `{"status":"error","error":"Please stop sending queries that crash our system."}`,
			expectedRetryAfter: "123",
		},
		"disabled writes on otlp": {
			tenant:          "5432",
			path:            "/otlp/v1/metrics",
			expectedCode:    http.StatusUnauthorized,
			expectedMessage: `{"status":"error","error":"Go away!"}`,
		},
		"enabled ruler": {
			tenant:       "5432",
			path:         "/api/prom/config/v1/rules/my-namespace",
			expectedCode: 200,
		},
		"disabled ruler with default config": {
			tenant:          "5550",
			path:            "/api/prom/config/v1/rules/my-namespace",
			expectedCode:    http.StatusUnauthorized,
			expectedMessage: `{"status":"error","error":"Ruler API on this Hosted Metrics instance is disabled. Please contact support."}`,
		},
		"disabled ruler with custom config": {
			tenant:             "5555",
			path:               "/api/prom/config/v1/rules/my-namespace",
			expectedCode:       529,
			expectedMessage:    `{"status":"error","error":"Please stop sending ruler config requests that crash our system."}`,
			expectedRetryAfter: "120",
		},
		"disabled ruler with encoded slash in namespace": {
			tenant:             "5555",
			path:               "/api/prom/config/v1/rules/my%2Fnamespace",
			expectedCode:       529,
			expectedMessage:    `{"status":"error","error":"Please stop sending ruler config requests that crash our system."}`,
			expectedRetryAfter: "120",
		},
		"disabled ruler with encoded slash in namespace and rule group names": {
			tenant:             "5555",
			path:               "/api/prom/config/v1/rules/my%2Fnamespace/test%2F%2F%2Fgroup",
			expectedCode:       529,
			expectedMessage:    `{"status":"error","error":"Please stop sending ruler config requests that crash our system."}`,
			expectedRetryAfter: "120",
		},
		"disabled ruler with encoded slash in rule group name": {
			tenant:             "5555",
			path:               "/api/prom/config/v1/rules/namespace/test%2Ftest",
			expectedCode:       529,
			expectedMessage:    `{"status":"error","error":"Please stop sending ruler config requests that crash our system."}`,
			expectedRetryAfter: "120",
		},
	} {
		t.Run(n, func(t *testing.T) {
			req := httptest.NewRequest("GET", tc.path, nil)
			req.Header.Add("X-Scope-OrgId", tc.tenant)
			resp := httptest.NewRecorder()
			h.ServeHTTP(resp, req)

			assert.Equal(t, tc.expectedCode, resp.Code)
			assert.Equal(t, tc.expectedMessage, resp.Body.String())
			assert.Equal(t, tc.expectedRetryAfter, resp.Header().Get(httpRetryAfter))
		})
	}
}

func TestMimirOverridesMiddleware_MixedSettings(t *testing.T) {
	rtc, err := initTenantOverrides(APIOverridesConfig{Path: "testdata/test_mixed_overrides.yaml", ReloadPeriod: time.Second * 15}, log.NewNopLogger(), prometheus.NewRegistry())
	require.NoError(t, err)

	mo := NewMimirOverridesMiddleware(rtc)
	mock := &mockHandler{}
	h := mo.Wrap(mock)

	for n, tc := range map[string]struct {
		tenant             string
		path               string
		expectedCode       int
		expectedMessage    string
		expectedRetryAfter string
	}{
		"disabled read tenant with overrides": {
			tenant:          "1234",
			path:            "/prometheus/api/v1/query_range",
			expectedCode:    http.StatusServiceUnavailable,
			expectedMessage: `{"status":"error","error":"Reads are temporarily disabled. Please retry later."}`,
		},
		"disabled read tenant with no overrides": {
			tenant:          "5678",
			path:            "/prometheus/api/v1/query_range",
			expectedCode:    http.StatusServiceUnavailable,
			expectedMessage: `{"status":"error","error":"Reads are temporarily disabled. Please retry later."}`,
		},
		"disabled write": {
			tenant:          "1234",
			path:            "/api/v1/push",
			expectedCode:    http.StatusConflict,
			expectedMessage: `{"status":"error","error":"Writes to this Hosted Metrics instance are disabled. Please contact support."}`,
		},
		"allowed write": {
			tenant:       "5678",
			path:         "/api/v1/push",
			expectedCode: http.StatusOK,
		},
		"allowed ruler": {
			tenant:       "1234",
			path:         "/prometheus/config/v1/rules/my-namespace",
			expectedCode: http.StatusOK,
		},
	} {
		t.Run(n, func(t *testing.T) {
			req := httptest.NewRequest("GET", tc.path, nil)
			req.Header.Add("X-Scope-OrgId", tc.tenant)
			resp := httptest.NewRecorder()
			h.ServeHTTP(resp, req)

			assert.Equal(t, tc.expectedCode, resp.Code)
			assert.Equal(t, tc.expectedMessage, resp.Body.String())
			assert.Equal(t, tc.expectedRetryAfter, resp.Header().Get(httpRetryAfter))
		})
	}
}

func TestMimirOverridesMiddleware_ExplicitAllows(t *testing.T) {
	rtc, err := initTenantOverrides(APIOverridesConfig{Path: "testdata/test_tenant_explicit_allows.yaml", ReloadPeriod: time.Second * 15}, log.NewNopLogger(), prometheus.NewRegistry())
	require.NoError(t, err)

	mo := NewMimirOverridesMiddleware(rtc)
	mock := &mockHandler{}
	h := mo.Wrap(mock)

	tenants := []string{"no_allows", "with_allow_all", "with_allow_reads", "with_allow_writes", "with_allow_ruler"}
	var tenantsFromFile []string
	for k := range rtc.manager.GetConfig().(*Overrides).MimirTenantOverrides {
		tenantsFromFile = append(tenantsFromFile, k)
	}
	assert.ElementsMatch(t, tenants, tenantsFromFile) // ensure there's no drift from test and testdata

	for n, tc := range map[string]struct {
		path                   string
		expectedAllowedTenants []string
	}{
		"read": {
			path:                   "/prometheus/api/v1/query_range",
			expectedAllowedTenants: []string{"with_allow_all", "with_allow_reads"},
		},
		"write": {
			path:                   "/api/v1/push",
			expectedAllowedTenants: []string{"with_allow_all", "with_allow_writes"},
		},
		"ruler": {
			path:                   "/prometheus/config/v1/rules/my-namespace",
			expectedAllowedTenants: []string{"with_allow_all", "with_allow_ruler"},
		},
	} {
		t.Run(n, func(t *testing.T) {
			for _, tenant := range tenants {
				req := httptest.NewRequest("GET", tc.path, nil)
				req.Header.Add("X-Scope-OrgId", tenant)
				resp := httptest.NewRecorder()
				h.ServeHTTP(resp, req)

				if slices.Contains(tc.expectedAllowedTenants, tenant) {
					assert.Equal(t, http.StatusOK, resp.Code, "tenant %q should be allowed", tenant)
				} else {
					assert.Equal(t, http.StatusServiceUnavailable, resp.Code, "tenant %q should not be allowed", tenant)
				}
			}
		})
	}
}

func TestValidateMimirOverrides(t *testing.T) {
	t.Run("validate HTTP retry after", func(t *testing.T) {
		settings := []string{
			"block_reads_http_retry_after",
			"block_writes_http_retry_after",
			"block_ruler_http_retry_after",
			"block_aggregations_http_retry_after",
		}

		for name, tc := range map[string]struct {
			value     string
			expectErr error
		}{
			"valid retry after second": {
				value:     `300`,
				expectErr: nil,
			},
			"invalid retry after second": {
				value:     `300_XXX`,
				expectErr: errInvalidRetryAfter,
			},
			"valid retry after http-date": {
				value:     `"Wed, 21 Oct 2015 07:28:00 GMT"`,
				expectErr: nil,
			},
			"invalid retry after http-date": {
				value:     `"XXXXWed, 21 Oct 2015 07:28:00 GMT"`,
				expectErr: errInvalidRetryAfter,
			},
		} {
			t.Run(name, func(t *testing.T) {
				for _, setting := range settings {
					t.Run(setting, func(t *testing.T) {
						t.Run("global overrides", func(t *testing.T) {
							var global MimirGlobalOverrides
							err := yaml.Unmarshal([]byte(fmt.Sprintf("%s: %s", setting, tc.value)), &global)
							assert.Equal(t, tc.expectErr, err)
						})

						t.Run("per-tenant overrides", func(t *testing.T) {
							var tenant MimirTenantOverrides
							err := yaml.Unmarshal([]byte(fmt.Sprintf("%s: %s", setting, tc.value)), &tenant)
							assert.Equal(t, tc.expectErr, err)
						})
					})
				}
			})
		}
	})

	t.Run("validate HTTP response code", func(t *testing.T) {
		settings := []string{
			"block_reads_http_response_code",
			"block_writes_http_response_code",
			"block_ruler_http_response_code",
			"block_aggregations_http_response_code",
		}

		for name, tc := range map[string]struct {
			value     string
			expectErr error
		}{
			"block_reads response code must be only 4xx and 5xx": {
				value:     `200`,
				expectErr: errInvalidResponseCodeForRequestBlock,
			},
			"block_writes response code must be only 4xx and 5xx": {
				value:     `301`,
				expectErr: errInvalidResponseCodeForRequestBlock,
			},
			"block_reads response code is valid": {
				value:     `400`,
				expectErr: nil,
			},
			"block_writes response code is valid": {
				value:     `500`,
				expectErr: nil,
			},
		} {
			t.Run(name, func(t *testing.T) {
				for _, setting := range settings {
					t.Run(setting, func(t *testing.T) {
						t.Run("global overrides", func(t *testing.T) {
							var global MimirGlobalOverrides
							err := yaml.Unmarshal([]byte(fmt.Sprintf("%s: %s", setting, tc.value)), &global)
							assert.Equal(t, tc.expectErr, err)
						})

						t.Run("per-tenant overrides", func(t *testing.T) {
							var tenant MimirTenantOverrides
							err := yaml.Unmarshal([]byte(fmt.Sprintf("%s: %s", setting, tc.value)), &tenant)
							assert.Equal(t, tc.expectErr, err)
						})
					})
				}
			})
		}
	})
}

func TestLokiOverridesMiddleware(t *testing.T) {
	rtc, err := initTenantOverrides(APIOverridesConfig{Path: "testdata/loki_overrides.yaml", ReloadPeriod: time.Second * 15}, log.NewNopLogger(), prometheus.NewRegistry())
	require.NoError(t, err)

	lom := NewLokiOverridesMiddleware(rtc)
	mock := &mockHandler{}
	h := lom.Wrap(mock)

	for n, tc := range map[string]struct {
		tenant       string
		path         string
		headers      map[string]string
		expectedCode int
		expectedBody string
	}{
		"blocked queries for tenant": {
			tenant:       "1234",
			path:         "/loki/api/v1/query",
			expectedCode: http.StatusUnauthorized,
			expectedBody: `{"status":"error","error":"Loki queries are blocked for this tenant, please contact your system administrator"}`,
		},
		"allowed queries for unblocked tenant": {
			tenant:       "9999",
			path:         "/loki/api/v1/query",
			expectedCode: http.StatusOK,
		},
		"blocked writes for tenant": {
			tenant:       "9999",
			path:         "/loki/api/v1/push",
			expectedCode: http.StatusUnauthorized,
			expectedBody: `{"status":"error","error":"Loki writes are blocked for this tenant, please contact your system administrator"}`,
		},
		"allowed writes for unblocked tenant": {
			tenant:       "1234",
			path:         "/loki/api/v1/push",
			expectedCode: http.StatusOK,
		},
		"log volume histogram disabled for tenant": {
			tenant:       "1235",
			path:         "/loki/api/v1/query",
			headers:      map[string]string{queryTagsHTTPHeaderKey: logVolumeHistogramHTTPHeaderValue},
			expectedCode: http.StatusForbidden,
			expectedBody: `{"status":"error","error":"Log volume histogram is disabled for this tenant"}`,
		},
		"log volume histogram with custom timeout passes through": {
			tenant:       "1236", // has log_volume_histogram.timeout: 100ms, but enabled (default true)
			path:         "/loki/api/v1/query",
			headers:      map[string]string{queryTagsHTTPHeaderKey: logVolumeHistogramHTTPHeaderValue},
			expectedCode: http.StatusOK,
		},
		"hint queries blocked for tenant": {
			tenant:       "1111",
			path:         "/loki/api/v1/query",
			headers:      map[string]string{queryTagsHTTPHeaderKey: lokiHintQueriesHTTPHeaderValue},
			expectedCode: http.StatusUnauthorized,
			expectedBody: `{"status":"error","error":"Loki hint queries are blocked for this tenant, please contact your system administrator"}`,
		},
		"hint queries allowed for unblocked tenant": {
			tenant:       "9999",
			path:         "/loki/api/v1/query",
			headers:      map[string]string{queryTagsHTTPHeaderKey: lokiHintQueriesHTTPHeaderValue},
			expectedCode: http.StatusOK,
		},
		"non-loki path not affected by loki block": {
			tenant:       "1234",
			path:         "/prometheus/api/v1/query",
			expectedCode: http.StatusOK,
		},
	} {
		t.Run(n, func(t *testing.T) {
			req := httptest.NewRequest("GET", tc.path, nil)
			req.Header.Add("X-Scope-OrgId", tc.tenant)
			for k, v := range tc.headers {
				req.Header.Add(k, v)
			}
			resp := httptest.NewRecorder()
			h.ServeHTTP(resp, req)

			assert.Equal(t, tc.expectedCode, resp.Code)
			if tc.expectedBody != "" {
				assert.Equal(t, tc.expectedBody, resp.Body.String())
			}
		})
	}
}

type mockHandler struct{}

func (m *mockHandler) ServeHTTP(writer http.ResponseWriter, _ *http.Request) {
	writer.WriteHeader(http.StatusOK)
}
