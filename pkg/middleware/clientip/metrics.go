// SPDX-License-Identifier: AGPL-3.0-only

package clientip

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type metrics struct {
	failures *prometheus.CounterVec
}

func newMetrics(reg prometheus.Registerer) *metrics {
	return &metrics{
		failures: promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
			Namespace: "gateway",
			Subsystem: "clientipmiddleware",
			Name:      "failures",
			Help:      "The number of client IP extraction failures.",
		}, []string{"reason"}),
	}
}
