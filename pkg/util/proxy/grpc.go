// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/felixge/httpsnoop"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/grafana/dskit/grpcclient"
	"github.com/grafana/dskit/grpcutil"
	"github.com/grafana/dskit/httpgrpc"
	"github.com/grafana/dskit/httpgrpc/server"
	"github.com/grafana/dskit/instrument"
	"github.com/grafana/dskit/middleware"
	"github.com/grafana/dskit/tenant"
	grpc_prometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/net/http/httptrace/otelhttptrace"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

var tracer = otel.Tracer("pkg/util/proxy")

const (
	GRPCLoadBalancingPolicyRoundRobin  = "round_robin"
	GRPCLoadBalancingPolicyBoundedLoad = "bounded_load"

	DefaultGRPCLoadBalancingOverloadedFactor = 2.0

	// Our default max receive size increases the default from grpc-go from 4 to 100 MiB.
	// Our default max send size matches the default from grpc-go.
	DefaultGRPCMaxRecvMessageSize = 100 << 20
	DefaultGRPCMaxSendMessageSize = math.MaxInt32

	grpcServiceConfigTemplate = `{"loadBalancingPolicy":"%s"}`
)

var (
	GRPCLoadBalancingPolicies = []string{GRPCLoadBalancingPolicyRoundRobin, GRPCLoadBalancingPolicyBoundedLoad}
)

type GRPCLoadBalancingConfig struct {
	Policy           string  `yaml:"grpc_load_balancing_policy" category:"advanced"`
	OverloadedFactor float64 `yaml:"grpc_load_balancing_overloaded_factor" category:"advanced"`
}

func (cfg *GRPCLoadBalancingConfig) RegisterFlagsWithPrefix(prefix string, f *flag.FlagSet) {
	f.StringVar(&cfg.Policy,
		prefix+".grpc-load-balancing-policy",
		GRPCLoadBalancingPolicyRoundRobin,
		fmt.Sprintf("gRPC load balancing policy. Supported values: %s.", strings.Join(GRPCLoadBalancingPolicies, ", ")))
	f.Float64Var(&cfg.OverloadedFactor,
		prefix+".grpc-load-balancing-overloaded-factor",
		DefaultGRPCLoadBalancingOverloadedFactor,
		fmt.Sprintf("When the gRPC load balancing policy is set to %q, the balancer will attempt to not send to each backend a number of inflight requests higher than the average inflight requests across all backends multiplied by the overloaded factor.", GRPCLoadBalancingPolicyBoundedLoad))
}

func (cfg *GRPCLoadBalancingConfig) Validate() error {
	if !slices.Contains(GRPCLoadBalancingPolicies, cfg.Policy) {
		return fmt.Errorf("invalid gRPC load balancing policy: %s", cfg.Policy)
	}
	return nil
}

type httpOverGrpcProxy struct {
	name   string
	client httpgrpc.HTTPClient
	conn   *grpc.ClientConn
	logger log.Logger

	proxyMetrics *Metrics
}

func newHttpOverGrpcProxy(name string, cfg Config, logger log.Logger, pm *Metrics) (http.Handler, error) {
	address := cfg.URL

	if strings.Contains(address, "kubernetes://") {
		// we only use server.ParseURL for Kubernetes because it doesn't support direct ip parsing (ex: grpc://192.168.0.1:3100).
		parsed, err := server.ParseURL(cfg.URL)
		if err != nil {
			return nil, fmt.Errorf("grpc write proxy URL parse: %w", err)
		}
		address = parsed
	}

	grpcClientConfig := grpcclient.Config{
		MaxRecvMsgSize:    cfg.GRPCMaxRecvMsgSize,
		MaxSendMsgSize:    cfg.GRPCMaxSendMsgSize,
		ClusterValidation: cfg.GRPCClientClusterValidation,
		TLSEnabled:        cfg.TLSEnabled,
		TLS:               cfg.TLS,
	}

	unary := []grpc.UnaryClientInterceptor{
		grpc_prometheus.UnaryClientInterceptor,
		middleware.ClientUserHeaderInterceptor,
	}
	invalidClusterValidationReporter := middleware.NoOpInvalidClusterValidationReporter
	if grpcClientConfig.ClusterValidation.Label != "" {
		client := fmt.Sprintf("proxy-%s", name)
		invalidClusterValidationReporter = newInvalidClusterValidationReporter(grpcClientConfig.ClusterValidation.Label, pm.invalidClusterValidation(client, "grpc"), logger)
	}
	gRPCDialOptions, err := grpcClientConfig.DialOption(unary, nil, invalidClusterValidationReporter)
	if err != nil {
		return nil, err
	}

	// nolint:staticcheck // grpc.WithBlock() has been deprecated; we'll address it before upgrading to gRPC 2
	gRPCDialOptions = append(gRPCDialOptions,
		grpc.WithBlock(),
		grpc.WithKeepaliveParams(
			keepalive.ClientParameters{
				Time:                time.Second * 10,
				Timeout:             time.Second * 5,
				PermitWithoutStream: true,
			},
		),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)

	// Configure the load balancing policy.
	loadBalancingDialOptions, err := LoadBalancingDialOptions(cfg.GRPCLoadBalancingConfig.Policy, cfg.GRPCLoadBalancingConfig.OverloadedFactor, pm)
	if err != nil {
		return nil, err
	}
	gRPCDialOptions = append(gRPCDialOptions, loadBalancingDialOptions...)

	ctx := context.Background()
	if cfg.DialTimeout > 0 {
		var cancel func()
		ctx, cancel = context.WithTimeout(context.Background(), cfg.DialTimeout)
		defer cancel()
	}

	// nolint:staticcheck // grpc.Dial() has been deprecated; we'll address it before upgrading to gRPC 2.
	conn, err := grpc.DialContext(ctx, address, gRPCDialOptions...)
	if err != nil {
		return nil, fmt.Errorf("grpc write proxy dial failed: %w", err)
	}

	return wrapMaxBytesHandler(&httpOverGrpcProxy{
		name:         name,
		client:       httpgrpc.NewHTTPClient(conn),
		conn:         conn,
		logger:       logger,
		proxyMetrics: pm,
	}, cfg.MaxSize, NewErrorHandler(logger, pm)), nil
}

func LoadBalancingDialOptions(policyName string, overloadedFactor float64, pm *Metrics) ([]grpc.DialOption, error) {
	// Configure the load balancing policy.
	switch policyName {
	case GRPCLoadBalancingPolicyRoundRobin:
		serviceConfig := fmt.Sprintf(grpcServiceConfigTemplate, GRPCLoadBalancingPolicyRoundRobin)
		return []grpc.DialOption{grpc.WithDefaultServiceConfig(serviceConfig)}, nil
	case GRPCLoadBalancingPolicyBoundedLoad:
		// The bounded load policy requires gRPC connections and RPC statistics to be tracked.
		// To be able to correlate the stats tracked by this client with the right balancer instance,
		// we need to configure the client balancer using the unique ID provided.
		balancingPolicyID, _, statsHandler := newBoundedLoadBalancer(overloadedFactor, pm.grpcBoundedLoadMetrics)
		serviceConfig := fmt.Sprintf(grpcServiceConfigTemplate, balancingPolicyID)
		return []grpc.DialOption{grpc.WithDefaultServiceConfig(serviceConfig), grpc.WithStatsHandler(statsHandler)}, nil
	default:
		return nil, fmt.Errorf("unsupported gRPC load balancing policy: %q", policyName)
	}
}

// ServeHTTP implements http.Handler
func (g *httpOverGrpcProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Ignore error since failure to get tenant ID shouldn't be a showstopper
	tenantID, _ := tenant.TenantID(r.Context())

	ctx, span := tracer.Start(r.Context(), "gateway/ProxyGRPC",
		trace.WithAttributes(
			attribute.String("user", tenantID),
		),
	)
	defer span.End()
	otelhttptrace.Inject(ctx, r)

	req, err := g.readRequest(ctx, w, r)
	if err != nil {
		return
	}

	metrics := httpsnoop.CaptureMetricsFn(w, func(w http.ResponseWriter) {
		g.handleDownstream(ctx, span, w, r, req)
	})
	instrument.ObserveWithExemplar(ctx, g.proxyMetrics.DownstreamDuration.WithLabelValues(r.Method, g.name, strconv.Itoa(metrics.Code)), metrics.Duration.Seconds())
}

func (g *httpOverGrpcProxy) readRequest(ctx context.Context, w http.ResponseWriter, r *http.Request) (*httpgrpc.HTTPRequest, error) {
	ctx, span := tracer.Start(ctx, "gateway/ProxyGRPC/ReadRequest")
	defer span.End()

	// Read the HTTP request, including the full body, and wrap it into an HTTPgRPC request.
	// If a timeout happens here, then it means we've hit the timeout while reading the HTTP
	// request body.
	req, err := HTTPRequest(ctx, r)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			span.AddEvent("timed out reading request body")
			err = errHTTPRequestReadTimeout
		}

		errorHandler(w, r, err, g.logger, g.proxyMetrics)
		return nil, err
	}

	span.AddEvent("finished reading request body",
		trace.WithAttributes(
			attribute.Int("sizeBytes", len(req.Body)),
		),
	)

	return req, nil
}

func (g *httpOverGrpcProxy) handleDownstream(ctx context.Context, span trace.Span, w http.ResponseWriter, r *http.Request, req *httpgrpc.HTTPRequest) {
	ctx = httpgrpc.AppendRequestMetadataToContext(ctx, req)
	ctx = grpcutil.AppendMessageSizeToOutgoingContext(ctx, req)

	resp, err := g.client.Handle(ctx, req)
	if err != nil {
		// Some errors will actually contain a valid resp, just need to unpack it
		var ok bool
		resp, ok = httpgrpc.HTTPResponseFromError(err)

		if !ok {
			errorHandler(w, r, err, g.logger, g.proxyMetrics)
			return
		}
	}

	span.AddEvent("finished reading response body",
		trace.WithAttributes(
			attribute.Int("sizeBytes", len(resp.Body)),
		),
	)
	defer span.AddEvent("finished sending response body")

	// Check for ServerTiming header and extract artificial_dur if present
	for _, header := range resp.Headers {
		if header.Key == "Server-Timing" {
			if dur, ok := parseServerTimingArtificialDur(header.Values); ok {
				g.proxyMetrics.addedLatency.WithLabelValues(r.Method, g.name, strconv.Itoa(int(resp.Code))).Observe(dur.Seconds())
			}
			break
		}
	}

	if err := httpgrpc.WriteResponse(w, resp); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

// HTTPRequest wraps an ordinary HTTPRequest with a gRPC one
// Adapted from grafana/dskit so we can allocate ContentLength if we know it.
func HTTPRequest(ctx context.Context, r *http.Request) (*httpgrpc.HTTPRequest, error) {
	var body []byte
	var err error
	if r.ContentLength == -1 { // unknown
		body, err = io.ReadAll(r.Body)
	} else {
		body, err = ReadSize(ctx, r.Body, r.ContentLength)
	}
	if err != nil {
		return nil, errors.Wrap(err, "could not read request body")
	}
	return &httpgrpc.HTTPRequest{
		Method:  r.Method,
		Url:     r.RequestURI,
		Body:    body,
		Headers: fromHeader(r.Header),
	}, ctx.Err()
}

// ReadSize is exactly like io.ReadAll, except we are given a max number of bytes to read.
func ReadSize(ctx context.Context, r io.Reader, size int64) ([]byte, error) {
	b := make([]byte, 0, size)
	for int64(len(b)) < size {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		n, err := r.Read(b[len(b):cap(b)])
		b = b[:len(b)+n]
		if err != nil {
			if err == io.EOF {
				if int64(len(b)) == size {
					return b, nil
				}
				return b, fmt.Errorf("expected %d bytes but only %d could be read", size, len(b))
			}
			return b, err
		}
	}
	return b, nil // leave the remainder of the stream unread
}

func fromHeader(hs http.Header) []*httpgrpc.Header {
	result := make([]*httpgrpc.Header, 0, len(hs))
	for k, vs := range hs {
		result = append(result, &httpgrpc.Header{
			Key:    k,
			Values: vs,
		})
	}
	return result
}

// errHTTPRequestReadTimeout is the error used to signal the proxy hit a timeout while reading the HTTP request body.
// This error message will be returned back to the client, so keep it as clear as possible.
var errHTTPRequestReadTimeout = fmt.Errorf("timeout reached while reading the HTTP request body (looks like the client is too slow sending the request)")

func newInvalidClusterValidationReporter(cluster string, invalidClusterValidations *prometheus.CounterVec, logger log.Logger) middleware.InvalidClusterValidationReporter {
	return func(msg string, method string) {
		level.Warn(logger).Log("msg", msg, "method", method, "cluster_validation_label", cluster)
		invalidClusterValidations.WithLabelValues(method).Inc()
	}
}
