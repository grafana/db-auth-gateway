// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/felixge/httpsnoop"
	"github.com/go-kit/log"
	"github.com/grafana/dskit/instrument"
	dskitmiddleware "github.com/grafana/dskit/middleware"
	"github.com/grafana/dskit/user"
	"github.com/mwitkow/go-conntrack"
	"github.com/oxtoacart/bpool"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/contrib/instrumentation/net/http/httptrace/otelhttptrace"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/net/http2"

	"github.com/grafana/db-auth-gateway/pkg/util/proxy/dnslb"
)

type h2cTransportWrapper struct {
	*http2.Transport
}

func (t *h2cTransportWrapper) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	return t.Transport.RoundTrip(req)
}

type transportWithClusterValidation struct {
	*http.Transport
}

func (t *transportWithClusterValidation) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.Transport.RoundTrip(req)
}

func newTransportWithClusterValidation(transport *http.Transport, _ string, _ Config, _ *Metrics, _ log.Logger) *transportWithClusterValidation {
	return &transportWithClusterValidation{Transport: transport}
}

const (
	HTTPLoadBalancingPolicyRoundRobin    = "round_robin"
	HTTPLoadBalancingPolicyLeastInflight = "least_inflight"
)

var (
	HTTPLoadBalancingPolicies = []string{HTTPLoadBalancingPolicyRoundRobin, HTTPLoadBalancingPolicyLeastInflight}
)

func newHTTPProxy(name string, cfg Config, enableHTTP2 bool, logger log.Logger, pm *Metrics) (http.Handler, error) {
	cortexURL, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("unable to parse url '%s': %v", cfg.URL, err)
	}

	var pool httputil.BufferPool
	if cfg.BufferPoolSize > 0 {
		pool = bpool.NewBytePool(cfg.BufferPoolSize, cfg.BufferPoolWidth)
	}

	t := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: conntrack.NewDialContextFunc(
			conntrack.DialWithTracing(),
			conntrack.DialWithName(name),
			conntrack.DialWithDialer(&net.Dialer{
				Timeout:   cfg.DialTimeout,
				KeepAlive: 30 * time.Second,
			}),
		),
		MaxIdleConns:          10000,
		MaxIdleConnsPerHost:   1000, // see https://github.com/golang/go/issues/13801
		IdleConnTimeout:       90 * time.Second,
		DisableKeepAlives:     !cfg.KeepAlive,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	transport := newTransportWithClusterValidation(t, name, cfg, pm, logger)

	tlsConfig, err := cfg.TLS.GetTLSConfig()
	if err != nil {
		return nil, fmt.Errorf("unable to parse tls configs '%s': %v", cfg.URL, err)
	}

	if tlsConfig != nil {
		transport.TLSClientConfig = tlsConfig
	}

	// Check if we are testing, in which case we can disable certificate checking.
	if strings.HasPrefix(cfg.URL, "https://127.0.0.1:") {
		if transport.TLSClientConfig == nil {
			transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS13}
		}
		transport.TLSClientConfig.InsecureSkipVerify = true
	}

	if enableHTTP2 {
		transport.RegisterProtocol("h2c", &h2cTransportWrapper{
			Transport: &http2.Transport{
				DialTLS: func(netw, addr string, _ *tls.Config) (net.Conn, error) {
					return net.Dial(netw, addr)
				},
				AllowHTTP: true,
			},
		})

		err := http2.ConfigureTransport(transport.Transport)
		if err != nil {
			return nil, fmt.Errorf("failed to configure http2 transport: %w", err)
		}
	}

	errorHandler := NewErrorHandler(logger, pm)

	var hp dnslb.DNSDiscoveredHostProvider
	if cfg.HTTPLoadBalancingPolicy != "" {
		switch cfg.HTTPLoadBalancingPolicy {
		case HTTPLoadBalancingPolicyRoundRobin:
			hp = dnslb.NewRoundRobinHostProvider(cortexURL.Host)
		case HTTPLoadBalancingPolicyLeastInflight:
			hp = dnslb.NewLeastInflightHostProvider(cortexURL.Host)
		default:
			return nil, fmt.Errorf("unknown HTTP load balancing policy '%s'", cfg.HTTPLoadBalancingPolicy)
		}
		err := dnslb.StartDNSLoop(cortexURL.Host, hp, logger)
		if err != nil {
			return nil, fmt.Errorf("failed to start DNS loop for proxy '%s': %w", name, err)
		}
		wrappedErrorHandler := errorHandler
		errorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			// Not only do we need to end the host in ModifyResponse, but also in the error handler
			hp.EndHost(r.URL.Host)
			wrappedErrorHandler(w, r, err)
		}
	}

	// handle request mirroring
	var roundTripper http.RoundTripper = otelhttp.NewTransport(transport, otelhttp.WithClientTrace(func(ctx context.Context) *httptrace.ClientTrace {
		return otelhttptrace.NewClientTrace(ctx, otelhttptrace.WithoutSubSpans())
	}))
	if len(cfg.MirrorTargets) > 0 {
		roundTripper = newMirrorRoundTripper(
			logger,
			cfg.MirrorTargets,
			roundTripper,
			pool,
		)
	}

	// Separate Proxy for Writes with added buffer pool
	proxyHandler := wrapMaxBytesHandler(&httputil.ReverseProxy{
		ErrorHandler: errorHandler,
		Director: func(req *http.Request) {
			ctx := req.Context()
			orgID, _ := user.ExtractOrgID(ctx)

			req.URL.Scheme = cortexURL.Scheme
			if hp != nil {
				req.URL.Host = hp.StartHost()
			} else {
				req.URL.Host = cortexURL.Host
			}
			if cfg.ForwardPathMutator != nil {
				req.URL.Path = cfg.ForwardPathMutator(orgID, req.URL.Path)
			}
			if trace.SpanFromContext(ctx).SpanContext().IsValid() {
				otelhttptrace.Inject(ctx, req)
			}
		},
		ModifyResponse: func(r *http.Response) error {
			if hp != nil {
				hp.EndHost(r.Request.URL.Host)
			}

			// Check for ServerTiming header and extract artificial_dur if present
			if dur, ok := parseServerTimingArtificialDur(strings.Split(r.Header.Get("Server-Timing"), ", ")); ok {
				pm.addedLatency.WithLabelValues(r.Request.Method, name, strconv.Itoa(r.StatusCode)).Observe(dur.Seconds())
			}

			return nil
		},
		BufferPool: pool,
		Transport:  roundTripper,
	}, cfg.MaxSize, errorHandler)

	handler := http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		ctx, span := tracer.Start(req.Context(), "Proxy/Forward")
		defer span.End()

		proxyHandler.ServeHTTP(writer, req.Clone(ctx))
	})

	h := &downstreamMetricsHandler{
		name:                       name,
		next:                       handler,
		metrics:                    pm,
		excludeRequestReadDuration: cfg.IgnoreRequestReadTimeFromDownstreamDuration,
		routeMethod:                cfg.DownstreamDurationRouteMethod,
		perTenantConfig:            cfg.PerTenantConfig,

		httpCodes: newHTTPCodesTable(),
	}
	return h, nil
}

type downstreamMetricsHandler struct {
	name                       string
	next                       http.Handler
	metrics                    *Metrics
	excludeRequestReadDuration bool
	routeMethod                bool
	perTenantConfig            dskitmiddleware.PerTenantCallback

	httpCodes httpCodesTable
}

func (h *downstreamMetricsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var metrics httpsnoop.Metrics
	if h.excludeRequestReadDuration {
		origBody := r.Body
		defer func() {
			// No need to leak our Body wrapper beyond the scope of this handler.
			r.Body = origBody
		}()
		upstreamRequestBody := &reqBody{b: origBody}
		r.Body = upstreamRequestBody

		metrics = httpsnoop.CaptureMetrics(h.next, w, r)
		metrics.Duration -= upstreamRequestBody.readDuration
	} else {
		metrics = httpsnoop.CaptureMetrics(h.next, w, r)
	}

	h.observe(r, metrics)
}

func (h *downstreamMetricsHandler) observe(r *http.Request, metrics httpsnoop.Metrics) {
	ctx := r.Context()
	code := h.httpCodes.String(metrics.Code)
	method := r.Method
	if h.routeMethod {
		method = dskitmiddleware.ExtractRouteName(ctx)
	}
	exemplarLabels := instrument.ExtractExemplarLabels(ctx)
	seconds := metrics.Duration.Seconds()
	h.metrics.DownstreamDuration.WithLabelValues(
		method,
		h.name,
		code,
	).(prometheus.ExemplarObserver).ObserveWithExemplar(
		seconds,
		exemplarLabels,
	)

	var cfg *dskitmiddleware.PerTenantConfig
	if h.perTenantConfig != nil {
		cfg = h.perTenantConfig(ctx)
	}
	if cfg != nil &&
		cfg.TenantID != "" &&
		cfg.DurationHistogram {

		h.metrics.PerTenantDownstreamDuration.WithLabelValues(
			method,
			h.name,
			code,
			cfg.TenantID,
		).(prometheus.ExemplarObserver).ObserveWithExemplar(
			seconds,
			exemplarLabels,
		)
	}
}

type httpCodesTable [600]string

func (t *httpCodesTable) String(code int) string {
	if code < 0 || code >= len(t) {
		return strconv.Itoa(code)
	}
	return t[code]
}
func newHTTPCodesTable() httpCodesTable {
	var httpCodes = httpCodesTable{}
	for i := range httpCodes {
		httpCodes[i] = strconv.Itoa(i)
	}
	return httpCodes
}

type reqBody struct {
	b            io.ReadCloser
	readDuration time.Duration
}

func (w *reqBody) Read(p []byte) (int, error) {
	t1 := time.Now()
	n, err := w.b.Read(p)
	w.readDuration += time.Since(t1)
	return n, err
}

func (w *reqBody) Close() error {
	return w.b.Close()
}
