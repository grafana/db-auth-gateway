// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"time"

	"github.com/grafana/dskit/instrument"
	grpc_prometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	CanceledLabel         = "canceled"
	DeadLineExceededLabel = "deadline_exceeded"
	NetworkTimedOutLabel  = "timed_out"
	OtherLabel            = "other"
	ReasonLabel           = "reason"
	TooLargeLabel         = "too_large"
)

type Metrics struct {
	RequestsErrors              *prometheus.CounterVec
	DownstreamDuration          *prometheus.HistogramVec
	PerTenantDownstreamDuration *prometheus.HistogramVec
	grpcBoundedLoadMetrics      *boundedLoadBalancerMetrics
	invalidClusterValidation    func(string, string) *prometheus.CounterVec
	addedLatency                *prometheus.HistogramVec

	grpcClientMetrics *grpc_prometheus.ClientMetrics
	grpcServerMetrics *grpc_prometheus.ServerMetrics
}

func NewMetrics(reg prometheus.Registerer, namespace string) *Metrics {
	grpcClientMetrics := grpc_prometheus.NewClientMetrics()
	grpcServerMetrics := grpc_prometheus.NewServerMetrics()
	// Wrap the registry with a prefix so that multiple proxies can be run
	// without conflicting metrics.
	wrappedReg := prometheus.WrapRegistererWithPrefix(namespace+"_", reg)
	wrappedReg.MustRegister(grpcClientMetrics)
	wrappedReg.MustRegister(grpcServerMetrics)

	return &Metrics{
		RequestsErrors: promauto.With(reg).NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "requests_errors_total",
				Help:      "The total number of requests that were errors.",
			},
			[]string{ReasonLabel},
		),
		DownstreamDuration: promauto.With(reg).NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace:                       namespace,
				Name:                            "request_downstream_duration_seconds",
				Help:                            "Time (in seconds) spent handling the request.",
				Buckets:                         instrument.DefBuckets,
				NativeHistogramBucketFactor:     1.1,
				NativeHistogramMaxBucketNumber:  100,
				NativeHistogramMinResetDuration: time.Hour,
			},
			[]string{"method", "proxy", "status_code"},
		),
		PerTenantDownstreamDuration: promauto.With(reg).NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace:                       namespace,
				Name:                            "per_tenant_request_downstream_duration_seconds",
				Help:                            "Time (in seconds) spent handling the request for a particular tenant.",
				Buckets:                         instrument.DefBuckets,
				NativeHistogramBucketFactor:     1.1,
				NativeHistogramMaxBucketNumber:  100,
				NativeHistogramMinResetDuration: time.Hour,
			},
			[]string{"method", "proxy", "status_code", "tenant"},
		),
		grpcBoundedLoadMetrics: newBoundedLoadBalancerMetrics(namespace, reg),
		invalidClusterValidation: func(client string, protocol string) *prometheus.CounterVec {
			return newRequestInvalidClusterValidationLabelsTotalCounter(reg, namespace, client, protocol)
		},
		addedLatency: promauto.With(reg).NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace:                       namespace,
				Name:                            "request_added_latency_seconds",
				Help:                            "Time (in seconds) added to the request.",
				NativeHistogramBucketFactor:     1.1,
				NativeHistogramMaxBucketNumber:  100,
				NativeHistogramMinResetDuration: time.Hour,
			},
			[]string{"method", "proxy", "status_code"},
		),
		grpcClientMetrics: grpcClientMetrics,
		grpcServerMetrics: grpcServerMetrics,
	}
}

func newRequestInvalidClusterValidationLabelsTotalCounter(reg prometheus.Registerer, namespace string, client string, protocol string) *prometheus.CounterVec {
	return promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "client_invalid_cluster_validation_label_requests_total",
		Help:      "Number of requests with invalid cluster validation label.",
		ConstLabels: map[string]string{
			"client":   client,
			"protocol": protocol,
		},
	}, []string{"method"})
}
