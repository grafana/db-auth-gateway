// SPDX-License-Identifier: AGPL-3.0-only

package healthcheck

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Result constants for healthcheck_requests_total and related metrics.
// ResultFailure is used when the health endpoint returns a non-2xx HTTP status.
// ResultError is used for other failures (e.g. connection refused, DNS, or building the request).
const (
	ResultSuccess = "success"
	ResultFailure = "failure" // Health endpoint returned non-2xx.
	ResultTimeout = "timeout"
	ResultError   = "error" // Connection, DNS, or request build failure.
)

// Metrics holds the Prometheus metrics for endpoint healthchecks.
type Metrics struct {
	// Health indicates the current health status of each backend.
	// Value is 1 if healthy, 0 if unhealthy.
	Health *prometheus.GaugeVec

	// ChecksTotal counts the total number of healthcheck requests.
	ChecksTotal *prometheus.CounterVec

	// CheckDuration tracks the duration of healthcheck requests.
	CheckDuration *prometheus.HistogramVec
}

// NewMetrics creates and registers the healthcheck metrics.
func NewMetrics(reg prometheus.Registerer, namespace string) *Metrics {
	return &Metrics{
		Health: promauto.With(reg).NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Subsystem: "gateway",
				Name:      "backend_health",
				Help:      "Health status of proxied backends. 1 = healthy, 0 = unhealthy.",
			},
			[]string{"backend"},
		),
		ChecksTotal: promauto.With(reg).NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "gateway",
				Name:      "healthcheck_requests_total",
				Help:      "Total number of healthcheck requests by result.",
			},
			[]string{"backend", "result"},
		),
		CheckDuration: promauto.With(reg).NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Subsystem: "gateway",
				Name:      "healthcheck_duration_seconds",
				Help:      "Duration of healthcheck requests.",
				Buckets:   []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
			},
			[]string{"backend"},
		),
	}
}
