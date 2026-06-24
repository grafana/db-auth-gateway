// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-kit/log"
	"github.com/grafana/dskit/clusterutil"
	dstls "github.com/grafana/dskit/crypto/tls"
	dskitmiddleware "github.com/grafana/dskit/middleware"
	"github.com/pkg/errors"
	"github.com/sercand/kuberesolver/v6"
)

const NoMaxSize = 0

var K8sProxyInitializer = sync.Once{}

// Config defines the config options required to configure a downstream proxy using either HTTP or httpgrpc.
type Config struct {
	URL           string             `yaml:"url" category:"advanced"`
	MirrorTargets MirrorTargets      `yaml:"mirror_targets" doc:"hidden"`
	KeepAlive     bool               `yaml:"enable_keepalive" category:"advanced"`
	TLSEnabled    bool               `yaml:"tls_enabled" category:"advanced"`
	DialTimeout   time.Duration      `yaml:"dial_timeout" category:"advanced"`
	TLS           dstls.ClientConfig `yaml:",inline"`

	GRPCLoadBalancingConfig GRPCLoadBalancingConfig `yaml:",inline"`
	GRPCMaxRecvMsgSize      int                     `yaml:"grpc_max_recv_msg_size" category:"advanced"`
	GRPCMaxSendMsgSize      int                     `yaml:"grpc_max_send_msg_size" category:"advanced"`
	// TODO: derelease GRPCClientClusterValidation, and use ClientClusterValidation instead.
	GRPCClientClusterValidation clusterutil.ClusterValidationConfig `yaml:"-"`
	ClientClusterValidation     clusterutil.ClusterValidationConfig `yaml:"-"`

	// Load balancing policy to use, based on DNS discovery.
	// Meant to be used with a kubernetes' headless service.
	// See proxy.HTTPLoadBalancingPolicies for supported values.
	HTTPLoadBalancingPolicy string `yaml:"-"`

	// Consistent with existing proxy package used by the cloud gateway
	BufferPoolSize  int   `yaml:"-"`
	BufferPoolWidth int   `yaml:"-"`
	MaxSize         int64 `yaml:"-"`

	MetricsNamespace string `yaml:"-"`

	// Optionally mutate the path the request is being forwarded to based on the existing path.
	ForwardPathMutator func(orgID, path string) string `yaml:"-"`

	// Optionally adjust request_downstream_duration_seconds metric to exclude the time spent reading upstream request
	IgnoreRequestReadTimeFromDownstreamDuration bool `yaml:"-"`

	// Use the request route instead of request method for the downstream duration metric
	DownstreamDurationRouteMethod bool `yaml:"-"`

	PerTenantConfig dskitmiddleware.PerTenantCallback `yaml:"-"`
}

// RegisterFlagsWithPrefix is used to register the flags using existing values from the enterprise gateway
func (cfg *Config) RegisterFlagsWithPrefix(prefix string, f *flag.FlagSet) {
	f.StringVar(&cfg.URL,
		prefix+".url",
		"",
		"URL for the backend. Use the scheme dns:// for HTTP over gRPC and the scheme h2c:// for HTTP2 proxying.")
	f.BoolVar(&cfg.KeepAlive, prefix+".enable-keepalive", true, "Enable keep alive for the backend.")
	f.BoolVar(&cfg.TLSEnabled,
		prefix+".tls-enabled",
		false,
		"Enable TLS in the GRPC client. This flag needs to be enabled when any other TLS flag is set. "+
			"If set to false, insecure connection to gRPC server will be used.")
	f.DurationVar(&cfg.DialTimeout,
		prefix+".dial-timeout",
		5*time.Second,
		"Timeout when dialing backend. For proxying over GRPC, this will be used only during the initial dial at startup. "+
			"For proxying over HTTP this is the connection timeout. Set to 0 to disable.")
	f.IntVar(&cfg.GRPCMaxRecvMsgSize,
		prefix+".grpc-max-recv-msg-size",
		DefaultGRPCMaxRecvMessageSize,
		"gRPC client max receive message size (bytes).")
	f.IntVar(&cfg.GRPCMaxSendMsgSize,
		prefix+".grpc-max-send-msg-size",
		DefaultGRPCMaxSendMessageSize,
		"gRPC client max send message size (bytes).")
	f.Var(&cfg.MirrorTargets, prefix+".mirror-targets", "Targets to mirror requests to (*HTTP only*). "+
		"Each target is a comma-separted URL optionally you can add a fraction of requests to forward as a query parameter (default is 1). "+
		"Example values: http://mirror1:8080?fraction=0.5,http://mirror2:8080")

	cfg.GRPCLoadBalancingConfig.RegisterFlagsWithPrefix(prefix, f)
	cfg.TLS.RegisterFlagsWithPrefix(prefix, f)
}

func (cfg Config) WithURL(url string) Config {
	// NOTE: value receiver to avoid mutating the original config.
	cfg.URL = url
	return cfg
}

func (cfg Config) WithMaxSize(maxSize int64) Config {
	// NOTE: value receiver to avoid mutating the original config.
	cfg.MaxSize = maxSize
	return cfg
}

func (cfg Config) WithHTTPLoadBalancingPolicy(policy string) Config {
	// NOTE: value receiver to avoid mutating the original config.
	cfg.HTTPLoadBalancingPolicy = policy
	return cfg
}

func wrapMaxBytesHandler(handler http.Handler, maxSize int64, errorHandler func(w http.ResponseWriter, r *http.Request, err error)) http.Handler {
	if maxSize == NoMaxSize {
		return handler
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Checking the Content-Length header directly makes it possible to completely skip
		// requests to the downstream service. Otherwise, given that `httputil.ReverseProxy`
		// streams outgoing requests, the default behavior is to abort the request once it
		// exceeds the configured body size. While this behavior might be acceptable by
		// most supported services, some report aborted requests as 500-level errors,
		// directly impacting their SLO.
		if r.ContentLength > maxSize {
			errorHandler(w, r, &http.MaxBytesError{Limit: maxSize})
			return
		}

		// Check actual body content in case the Content-Length header is inaccurate.
		h := http.MaxBytesHandler(handler, maxSize)
		h.ServeHTTP(w, r)
	})
}

type Scheme int

const (
	HTTP = iota
	GRPC
	NotFound
)

type SchemedHandler struct {
	http.Handler
	Scheme
}

func NewProxy(name string, cfg Config, logger log.Logger, pm *Metrics) (http.Handler, error) {
	K8sProxyInitializer.Do(func() {
		// Make sure we only register Kuberesolver a single time.
		// Used to resolve kubernetes:/// prefixed addresses.
		kuberesolver.RegisterInCluster()
	})

	wrapProxyError := func(err error) error {
		return errors.Wrapf(err, "failed to create proxy for %s", name)
	}

	if cfg.URL == "" {
		h, e := newNotFoundProxy()
		return SchemedHandler{h, NotFound}, wrapProxyError(e)
	}
	if strings.Contains(cfg.URL, "dns://") || strings.Contains(cfg.URL, "kubernetes://") {
		h, e := newHttpOverGrpcProxy(name, cfg, logger, pm)
		return SchemedHandler{h, GRPC}, wrapProxyError(e)
	}
	if strings.Contains(cfg.URL, "h2c://") {
		h, e := newHTTPProxy(name, cfg, true, logger, pm)
		return SchemedHandler{h, HTTP}, wrapProxyError(e)
	}
	if strings.HasPrefix(cfg.URL, "grpc:") { // static address, used for testing
		cfg.URL = cfg.URL[5:] // strip the prefix so gRPC gets a single address
		h, e := newHttpOverGrpcProxy(name, cfg, logger, pm)
		return SchemedHandler{h, GRPC}, wrapProxyError(e)
	}
	if strings.HasPrefix(cfg.URL, "grpc-proxy://") {
		cfg.URL = cfg.URL[len("grpc-proxy://"):] // strip the prefix so gRPC url parsing works
		h, e := newNativeGRPCProxy(name, cfg, logger, pm)
		return SchemedHandler{h, GRPC}, wrapProxyError(e)
	}
	// Blackhole handler can be used in integration tests.
	if cfg.URL == "http:blackhole" {
		h := http.HandlerFunc(blackhole)
		return SchemedHandler{h, HTTP}, nil
	}
	h, e := newHTTPProxy(name, cfg, false, logger, pm)
	return SchemedHandler{h, HTTP}, wrapProxyError(e)
}

// NotFoundProxy is used when no downstream URL is set
type NotFoundProxy struct{}

func (NotFoundProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	http.Error(w,
		fmt.Sprintf("No gateway downstream URL was configured for a request to URL: %s", r.URL),
		http.StatusNotFound)
}

func newNotFoundProxy() (*NotFoundProxy, error) {
	return &NotFoundProxy{}, nil
}

func blackhole(resp http.ResponseWriter, req *http.Request) {
	// Read full request body, and report back how many bytes were read, and how long it took.
	start := time.Now()
	size, err := io.Copy(io.Discard, req.Body)
	if err != nil {
		http.Error(resp, err.Error(), http.StatusInternalServerError)
		return
	}
	elapsed := time.Since(start)

	r := struct {
		BodySize int64         `json:"body_size"`
		ReadTime time.Duration `json:"read_time"`
	}{
		BodySize: size,
		ReadTime: elapsed,
	}

	body, err := json.Marshal(r)
	if err != nil {
		http.Error(resp, err.Error(), http.StatusInternalServerError)
		return
	}

	resp.WriteHeader(http.StatusOK)
	// ignore errors when writing body, we can't do much about them.
	_, _ = resp.Write(body)
}

// parseServerTimingArtificialDur extracts the artificial delay duration from Server-Timing headers.
// Accepts a string slice.
// Returns the duration and true if found, otherwise returns 0 and false.
func parseServerTimingArtificialDur(headerValues []string) (time.Duration, bool) {
	for _, headerValue := range headerValues {
		if !strings.HasPrefix(headerValue, "artificial_delay") {
			continue
		}
		parts := strings.Split(headerValue, ";")
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if !strings.HasPrefix(part, "dur=") {
				continue
			}
			if dur, err := strconv.ParseFloat(strings.TrimPrefix(part, "dur="), 64); err == nil {
				return time.Duration(dur * float64(time.Millisecond)), true
			}
		}
	}
	return 0, false
}
