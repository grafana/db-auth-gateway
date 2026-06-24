// SPDX-License-Identifier: AGPL-3.0-only

package gateway

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/gorilla/mux"
	"github.com/grafana/dskit/clusterutil"
	"github.com/grafana/dskit/flagext"
	"github.com/grafana/dskit/middleware"
	"github.com/grafana/dskit/tenant"
	"github.com/grafana/dskit/user"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/grafana/db-auth-gateway/pkg/auth"
	"github.com/grafana/db-auth-gateway/pkg/gateway/healthcheck"
	gatewaymiddleware "github.com/grafana/db-auth-gateway/pkg/gateway/middleware"
	"github.com/grafana/db-auth-gateway/pkg/inputs"
	"github.com/grafana/db-auth-gateway/pkg/middleware/clientip"
	"github.com/grafana/db-auth-gateway/pkg/router"
	"github.com/grafana/db-auth-gateway/pkg/util/proxy"
)

type Backend = string

const (
	Loki      Backend = "loki"
	Mimir     Backend = "mimir"
	Pyroscope Backend = "pyroscope"
	Tempo     Backend = "tempo"
)

const (
	// AuthTrust denotes the standard OSS X-Scope-OrgID authentication middleware will
	// be the only authentication middleware applied.
	AuthTrust = "trust"
	// AuthForward delegates authentication to an external HTTP service via the
	// ForwardAuthenticator.
	AuthForward = "forward_auth"
)

type HTTPLoadBalancing struct {
	Enabled bool
	Policy  string
}

func (h *HTTPLoadBalancing) policy() string {
	if !h.Enabled {
		return ""
	}
	return h.Policy
}

func (h *HTTPLoadBalancing) RegisterFlagsWithPrefixAndComponent(prefix string, component string, f *flag.FlagSet) {
	f.StringVar(&h.Policy,
		prefix+"load-balancing-policy",
		proxy.HTTPLoadBalancingPolicyLeastInflight,
		fmt.Sprintf("the load balancing policy to use for "+component+", only used if enabled. Supported values: %s.", strings.Join(proxy.HTTPLoadBalancingPolicies, ", ")))
	f.BoolVar(&h.Enabled,
		prefix+"load-balancing",
		false,
		"enable HTTP load balancing for "+component+" discovered via DNS")
}

// AuthConfig holds authentication configuration.
type AuthConfig struct {
	Type    string
	Forward auth.ForwardConfig
}

// RegisterFlags registers auth flags.
func (c *AuthConfig) RegisterFlags(f *flag.FlagSet) {
	f.StringVar(&c.Type, "auth.type", AuthTrust, "Authentication type. Supported values: trust, forward_auth. When set to forward_auth, forward-auth.url is required")
	c.Forward.RegisterFlags(f)
}

// NewAuthenticator builds the auth.Authenticator selected by cfg.Type.
func NewAuthenticator(cfg AuthConfig) (auth.Authenticator, error) {
	switch cfg.Type {
	case AuthTrust:
		return auth.NewTrust(), nil
	case AuthForward:
		if _, err := url.ParseRequestURI(cfg.Forward.URL); err != nil {
			return nil, fmt.Errorf("invalid forward-auth.url %q: %w", cfg.Forward.URL, err)
		}
		return auth.NewForwardAuthenticator(cfg.Forward), nil
	default:
		return nil, fmt.Errorf("invalid auth.type %q", cfg.Type)
	}
}

// Config configures the Gateway API proxy
type Config struct {
	Backend Backend

	AlertURL     string
	RuleURL      string
	QueryURL     string
	WriteURL     string
	CompactorURL string

	LokiTailURL   string
	LokiDeleteURL string

	TempoQueryURL                   string
	TempoGRPCWriteURL               string
	TempoGRPCReadURL                string
	TempoHTTPWriteURL               string
	TempoHTTPWriteHTTPLoadBalancing HTTPLoadBalancing
	TempoOverridesAPIEnabled        bool

	SensitiveHeaderRemovalEnabled bool
	ExtraHeadersToRemove          flagext.StringSliceCSV

	WritePoolSize         int
	WritePoolWidth        int
	WriteKeepAliveEnabled bool
	WriteMirrorTargets    proxy.MirrorTargets
	QueryKeepAliveEnabled bool

	WriteHTTPLoadBalancing HTTPLoadBalancing
	QueryHTTPLoadBalancing HTTPLoadBalancing

	WriteIgnoreRequestReadTimeFromDownstreamDuration bool
	WriteDownstreamDurationRouteMethod               bool

	ReadTimeout                     time.Duration
	WriteTimeout                    time.Duration
	RequestTimeout                  time.Duration
	ImportTimeout                   time.Duration
	ExportTimeout                   time.Duration
	PyroscopeDebugInfoUploadTimeout time.Duration
	DialTimeout                     time.Duration

	GRPCLoadBalancingConfig proxy.GRPCLoadBalancingConfig
	GRPCMaxRecvMsgSize      int
	GRPCMaxSendMsgSize      int
	// TODO: derelease GRPCClientClusterValidation, and use ClientClusterValidation instead.
	GRPCClientClusterValidation clusterutil.ClusterValidationConfig
	ClientClusterValidation     clusterutil.ClusterValidationConfig

	RequestLimit                    int
	ConnectionTTLMax                time.Duration
	ConnectionTTLMin                time.Duration
	ConnectionTTLIdleCheckFrequency time.Duration

	EnableThirdParty bool

	DropSkipLabelValidationRequests bool

	MaxBytesWriteLimit int64

	MetricsNamespace  string
	MatchEncodedPaths bool

	// Healthcheck configurations for proxied endpoints.
	AlertmanagerHealthcheck     healthcheck.EndpointConfig
	RulerHealthcheck            healthcheck.EndpointConfig
	QueryHealthcheck            healthcheck.EndpointConfig
	DistributorHealthcheck      healthcheck.EndpointConfig
	CompactorHealthcheck        healthcheck.EndpointConfig
	TempoQueryHealthcheck       healthcheck.EndpointConfig
	TempoDistributorHealthcheck healthcheck.EndpointConfig

	EnableHTTPWriteTimeoutRequestCancellation bool
	XForwardedForMiddlewareEnabled            bool

	ClientIPMiddlewareConfig clientip.Config

	Auth AuthConfig

	Overrides APIOverridesConfig
}

// RegisterFlags registers flags for Config.
func (cfg *Config) RegisterFlags(f *flag.FlagSet) {
	f.StringVar(&cfg.Backend,
		"backend",
		"mimir",
		"Determine if the backend is mimir, loki, pyroscope, or tempo")
	f.StringVar(&cfg.AlertURL, "mimir.alertmanager.endpoint", "", "Mimir alertmanager endpoint URL.")
	f.StringVar(&cfg.RuleURL, "gateway.ruler.endpoint", "", "Ruler endpoint URL.")
	f.StringVar(&cfg.QueryURL, "gateway.query.endpoint", "", "Query frontend endpoint URL.")
	f.StringVar(&cfg.WriteURL, "gateway.distributor.endpoint", "", "Distributor endpoint URL.")
	f.StringVar(&cfg.CompactorURL, "mimir.compactor.endpoint", "", "Mimir compactor endpoint URL.")

	f.StringVar(&cfg.LokiTailURL, "loki.tail.endpoint", "", "Loki tail endpoint to proxy WebSocket tail requests to.")
	f.StringVar(&cfg.LokiDeleteURL, "loki.delete.endpoint", "", "Loki delete endpoint to proxy delete requests to.")

	f.StringVar(&cfg.TempoQueryURL, "tempo.query.endpoint", "", "tempo query endpoint to proxy requests to")
	f.StringVar(&cfg.TempoGRPCWriteURL, "tempo.distributor.endpoint", "", "tempo distributor endpoint to proxy GRPC write requests to")
	f.StringVar(&cfg.TempoGRPCReadURL, "tempo.streaming-query.endpoint", "", "tempo streaming query endpoint to proxy GRPC read requests to")
	f.StringVar(&cfg.TempoHTTPWriteURL, "tempo.distributor.http-endpoint", "", "tempo distributor endpoint to proxy HTTP write requests to")
	cfg.TempoHTTPWriteHTTPLoadBalancing.RegisterFlagsWithPrefixAndComponent("tempo.distributor.", "tempo distributors", f)
	f.BoolVar(&cfg.TempoOverridesAPIEnabled, "tempo.overrides.api-enabled", true,
		"Enable the Tempo user-configurable overrides API. When disabled, the /tempo/api/overrides routes are not registered.")

	f.IntVar(&cfg.WritePoolSize,
		"gateway.distributor.bufferpool-size",
		100,
		"max number of byte buffers in the write buffer pool")
	f.IntVar(&cfg.WritePoolWidth,
		"gateway.distributor.bufferpool-width",
		1024,
		"capacity of byte array provided by the write buffer pool")
	f.BoolVar(&cfg.WriteKeepAliveEnabled,
		"gateway.distributor.http-keepalive-enabled",
		false,
		"HTTP keep-alive enabled on the downstream write path")
	f.Var(&cfg.WriteMirrorTargets, "gateway.distributor.mirror-targets",
		"Targets to mirror distributor requests to (applies to Mimir, Loki, and Pyroscope HTTP write paths). "+
			"Each target is a comma-separated URL optionally with a fraction query parameter (default is 1). "+
			"Example values: http://mirror1:8080?fraction=0.5,http://mirror2:8080")
	f.BoolVar(&cfg.QueryKeepAliveEnabled,
		"gateway.query.http-keepalive-enabled",
		false,
		"HTTP keep-alive enabled on the downstream query path (applies to Mimir, Loki, and Pyroscope)")
	f.BoolVar(&cfg.WriteIgnoreRequestReadTimeFromDownstreamDuration,
		"gateway.distributor.ignore-request-read-time-from-downstream-duration-enabled",
		false,
		"if enabled, the request_downstream_duration_seconds metric is adjusted to exclude the time spent reading upstream request (applies to Mimir, Loki, and Pyroscope)")
	f.BoolVar(&cfg.WriteDownstreamDurationRouteMethod,
		"gateway.distributor.downstream-duration-route-method-enabled",
		false,
		"if enabled, the request_downstream_duration_seconds metric uses request route instead of request method as the method metrics label (applies to Mimir, Loki, and Pyroscope).")
	cfg.QueryHTTPLoadBalancing.RegisterFlagsWithPrefixAndComponent("gateway.query.", "queriers", f)
	cfg.WriteHTTPLoadBalancing.RegisterFlagsWithPrefixAndComponent("gateway.distributor.", "distributors", f)

	f.BoolVar(&cfg.EnableThirdParty, "mimir.inputs.enable-thirdparty", false, "whether to enable 3rd party write endpoints (Influx, OpenTSDB)")

	f.BoolVar(&cfg.SensitiveHeaderRemovalEnabled, "sensitive-header-removal-middleware.enabled", true, "Enable removing sensitive HTTP headers from proxied requests.")
	f.Var(&cfg.ExtraHeadersToRemove, "sensitive-header-removal-middleware.extra-headers", "Additional headers to remove from proxied requests.")

	f.DurationVar(&cfg.ReadTimeout,
		"gateway.read.timeout",
		time.Second*120,
		"Timeout for read requests to the backend, set to <=0 to disable")
	f.DurationVar(&cfg.WriteTimeout,
		"gateway.write.timeout",
		time.Second*15,
		"Timeout for write requests to the backend, set to <=0 to disable")
	f.DurationVar(&cfg.ImportTimeout,
		"gateway.import.timeout",
		30*time.Minute,
		"Timeout for import requests to the backend, set to <=0 to disable")
	f.DurationVar(&cfg.ExportTimeout,
		"gateway.export.timeout",
		10*time.Minute,
		"Timeout for export requests to the backend, set to <=0 to disable")
	f.DurationVar(&cfg.PyroscopeDebugInfoUploadTimeout,
		"pyroscope.debug-info-upload.timeout",
		2*time.Minute,
		"Timeout for Pyroscope debug info upload requests, set to <=0 to disable")
	f.DurationVar(&cfg.RequestTimeout,
		"gateway.request.timeout",
		0,
		"A global timeout (inclusive of upstream requests, authentication, etc) for all incoming requests, set to <=0 to disable")
	f.DurationVar(&cfg.DialTimeout,
		"gateway.dial.timeout",
		5*time.Second,
		"Timeout when dialing backend. This is the connection timeout. Set to 0 to disable.")

	f.IntVar(&cfg.RequestLimit,
		"gateway.request.limit",
		0,
		"Maximum number of gateway in-flight requests limit, set to <=0 to disable")

	f.DurationVar(
		&cfg.ConnectionTTLMax,
		"gateway.connection-ttl.max",
		0,
		"Maximum TTL of a TCP connection. When this limit is set, it will represent the maximum lifetime of TCP connections before the gateway sends a 'Connection: Close' response header to signal the client to close the connection. Set to <=0 to disable, or to at least -gateway.connection-ttl.min to enable.",
	)
	f.DurationVar(
		&cfg.ConnectionTTLMin,
		"gateway.connection-ttl.min",
		0,
		"Minimum TTL of a TCP connection. When this limit is set, it will represent the minimum lifetime of TCP connections before the gateway sends a 'Connection: Close' response header to signal the client to close the connection. Set to <=0 to disable, or to at most -gateway.connection-ttl.max to enable.",
	)
	f.DurationVar(
		&cfg.ConnectionTTLIdleCheckFrequency,
		"gateway.connection-ttl.idle-check-frequency",
		1*time.Minute,
		"The frequency of idle connections check. If during a check a connection is idle for at least -gateway.connection-ttl.max amount of time, its state is not kept anymore. Must be positive, and is taken into account only when -gateway.connection-ttl.max > 0.",
	)

	f.BoolVar(
		&cfg.DropSkipLabelValidationRequests,
		"mimir.write.drop-requests-with-skip-label-validation-header.enabled",
		true,
		"whether drop incoming write requests with the X-Mimir-SkipLabelNameValidation header, requests will be dropped by default",
	)

	f.Int64Var(&cfg.MaxBytesWriteLimit,
		"gateway.max-bytes-write-limit",
		0,
		"maximum number of bytes that the incoming write request can have in the request body")

	f.IntVar(&cfg.GRPCMaxRecvMsgSize,
		"gateway.grpc-max-recv-msg-size",
		proxy.DefaultGRPCMaxRecvMessageSize,
		"gRPC max receive message size in bytes.")
	f.IntVar(&cfg.GRPCMaxSendMsgSize,
		"gateway.grpc-max-send-msg-size",
		proxy.DefaultGRPCMaxSendMessageSize,
		"gRPC max send message size in bytes.")

	cfg.GRPCClientClusterValidation.RegisterFlagsWithPrefix("gateway.grpc-client-cluster-validation.", f)
	cfg.ClientClusterValidation.RegisterFlagsWithPrefix("gateway.client-cluster-validation.", f)

	f.BoolVar(&cfg.MatchEncodedPaths, "gateway.router.match-encoded-paths",
		true,
		"Enables or disables matching encoded paths in the http router. Matching encoded paths can have a noticeable CPU impact at high RPS")

	f.BoolVar(&cfg.EnableHTTPWriteTimeoutRequestCancellation, "gateway.request-write-timeout-enabled",
		false,
		"This flag enables the use of HTTP write timeout middleware, than when the configured timeout is reached, the request context is cancelled. NOTE: This will break streaming responses, so if you need that, avoid enabling this flag.")

	f.BoolVar(&cfg.XForwardedForMiddlewareEnabled, "x-forwarded-for-middleware.enabled",
		false,
		"Ensures if an existing X-Forwarded-For header is not present the value from that middleware is used.")

	cfg.ClientIPMiddlewareConfig.RegisterFlags(f)

	// Healthcheck flags.
	cfg.AlertmanagerHealthcheck.RegisterFlagsWithPrefix("mimir.alertmanager", f)
	cfg.RulerHealthcheck.RegisterFlagsWithPrefix("gateway.ruler", f)
	cfg.QueryHealthcheck.RegisterFlagsWithPrefix("gateway.query", f)
	cfg.DistributorHealthcheck.RegisterFlagsWithPrefix("gateway.distributor", f)
	cfg.CompactorHealthcheck.RegisterFlagsWithPrefix("mimir.compactor", f)
	cfg.TempoQueryHealthcheck.RegisterFlagsWithPrefix("tempo.query", f)
	cfg.TempoDistributorHealthcheck.RegisterFlagsWithPrefix("tempo.distributor", f)

	cfg.GRPCLoadBalancingConfig.RegisterFlagsWithPrefix("gateway", f)

	cfg.Auth.RegisterFlags(f)
	f.StringVar(&cfg.MetricsNamespace, "gateway.metrics-namespace", "",
		"Metrics namespace prefix. Defaults to the backend name if empty.")
}

// API is the core HTTP proxy that routes and authenticates backend requests.
type API struct {
	alertProxy     http.Handler
	ruleProxy      http.Handler
	queryProxy     http.Handler
	writeProxy     http.Handler
	compactorProxy http.Handler

	lokiTailProxy   http.Handler
	lokiDeleteProxy http.Handler

	tempoQueryProxy     http.Handler
	tempoGRPCReadProxy  http.Handler
	tempoGRPCWriteProxy http.Handler
	tempoHTTPWriteProxy http.Handler

	// influxProxy and openTSDBHandler are only non-nil when EnableThirdParty=true.
	influxProxy     http.Handler
	openTSDBHandler http.Handler

	healthChecker *healthcheck.Checker

	readTimeoutMiddleware                     middleware.Interface
	writeTimeoutMiddleware                    middleware.Interface
	requestTimeoutMiddleware                  middleware.Interface
	importTimeoutMiddleware                   middleware.Interface
	exportTimeoutMiddleware                   middleware.Interface
	pyroscopeDebugInfoUploadTimeoutMiddleware middleware.Interface
	requestLimiterMiddleware                  middleware.Interface
	connectionWithTTLMiddleware               middleware.Interface
	xForwardedForMiddleware                   middleware.Interface
	clientIPMiddleware                        *clientip.ClientIPMiddleware
	propagateTraceIDMiddleware                middleware.Interface
	sensitiveHeaderRemovalMiddleware          middleware.Interface

	// WriteMiddleware allows to wrap writeProxy handlers. This is used e.g. to inject the business intelligence middleware.
	WriteMiddleware middleware.Interface

	cfg                 Config
	logger              log.Logger
	auth                auth.Authenticator
	overridesMiddleware middleware.Interface
	reg                 prometheus.Registerer
}

// NewAPI creates a new API from the given config, authenticator, and logger.
func NewAPI(cfg Config, a auth.Authenticator, reg prometheus.Registerer, logger log.Logger) (*API, error) {
	defaultProxyConfig := proxy.Config{
		DialTimeout:                 cfg.DialTimeout,
		GRPCLoadBalancingConfig:     cfg.GRPCLoadBalancingConfig,
		GRPCMaxRecvMsgSize:          cfg.GRPCMaxRecvMsgSize,
		GRPCMaxSendMsgSize:          cfg.GRPCMaxSendMsgSize,
		GRPCClientClusterValidation: cfg.GRPCClientClusterValidation,
		ClientClusterValidation:     cfg.ClientClusterValidation,
		MetricsNamespace:            cfg.MetricsNamespace,
	}

	// Derive metrics namespace: use cfg.MetricsNamespace if set, else fall back to the backend name.
	metricsNamespace := cfg.MetricsNamespace
	if metricsNamespace == "" {
		metricsNamespace = strings.ReplaceAll(cfg.Backend, "-", "_")
	}

	// Build a shared proxy.Metrics instance.
	pm := proxy.NewMetrics(reg, metricsNamespace)

	// Build write proxy using proxy.NewProxy.
	writeCfg := proxy.Config{
		URL:                     cfg.WriteURL,
		DialTimeout:             cfg.DialTimeout,
		KeepAlive:               cfg.WriteKeepAliveEnabled,
		MirrorTargets:           cfg.WriteMirrorTargets,
		BufferPoolSize:          cfg.WritePoolSize,
		BufferPoolWidth:         cfg.WritePoolWidth,
		HTTPLoadBalancingPolicy: cfg.WriteHTTPLoadBalancing.policy(),
		IgnoreRequestReadTimeFromDownstreamDuration: cfg.WriteIgnoreRequestReadTimeFromDownstreamDuration,
		DownstreamDurationRouteMethod:               cfg.WriteDownstreamDurationRouteMethod,
		GRPCClientClusterValidation:                 cfg.GRPCClientClusterValidation,
		ClientClusterValidation:                     cfg.ClientClusterValidation,
		GRPCLoadBalancingConfig:                     cfg.GRPCLoadBalancingConfig,
		GRPCMaxRecvMsgSize:                          cfg.GRPCMaxRecvMsgSize,
		GRPCMaxSendMsgSize:                          cfg.GRPCMaxSendMsgSize,
	}
	writeProxy, err := proxy.NewProxy("write", writeCfg, logger, pm)
	if err != nil {
		return nil, fmt.Errorf("failed to build write proxy: %w", err)
	}

	// Wrap write proxy with write-specific middlewares.
	writeHandler := writeProxy
	if cfg.MaxBytesWriteLimit > 0 {
		writeHandler = newMaxBytesMiddleware(cfg.MaxBytesWriteLimit).Wrap(writeHandler)
	}
	if cfg.DropSkipLabelValidationRequests {
		writeHandler = newDropSkipLabelValidationMiddleware().Wrap(writeHandler)
	}

	// Build query proxy using proxy.NewProxy.
	queryCfg := proxy.Config{
		URL:                         cfg.QueryURL,
		DialTimeout:                 cfg.DialTimeout,
		KeepAlive:                   cfg.QueryKeepAliveEnabled,
		HTTPLoadBalancingPolicy:     cfg.QueryHTTPLoadBalancing.policy(),
		GRPCClientClusterValidation: cfg.GRPCClientClusterValidation,
		ClientClusterValidation:     cfg.ClientClusterValidation,
		GRPCLoadBalancingConfig:     cfg.GRPCLoadBalancingConfig,
		GRPCMaxRecvMsgSize:          cfg.GRPCMaxRecvMsgSize,
		GRPCMaxSendMsgSize:          cfg.GRPCMaxSendMsgSize,
	}
	queryProxy, err := proxy.NewProxy("query", queryCfg, logger, pm)
	if err != nil {
		return nil, fmt.Errorf("failed to build query proxy: %w", err)
	}

	ruleProxy, err := newReverseProxy("rule", cfg.RuleURL, cfg.DialTimeout, logger)
	if err != nil {
		return nil, err
	}
	alertProxy, err := newReverseProxy("alert", cfg.AlertURL, cfg.DialTimeout, logger)
	if err != nil {
		return nil, err
	}
	compactorProxy, err := newReverseProxy("compactor", cfg.CompactorURL, cfg.DialTimeout, logger)
	if err != nil {
		return nil, err
	}

	lokiTailProxy, err := proxy.NewProxy("loki_tail_proxy", defaultProxyConfig.WithURL(cfg.LokiTailURL), logger, pm)
	if err != nil {
		return nil, fmt.Errorf("failed to build loki tail proxy: %w", err)
	}

	lokiDeleteProxy, err := proxy.NewProxy("loki_delete_proxy", defaultProxyConfig.WithURL(cfg.LokiDeleteURL), logger, pm)
	if err != nil {
		return nil, fmt.Errorf("failed to build loki delete proxy: %w", err)
	}

	tempoQueryProxy, err := proxy.NewProxy("tempo_query_proxy", defaultProxyConfig.WithURL(cfg.TempoQueryURL), logger, pm)
	if err != nil {
		return nil, fmt.Errorf("failed to build tempo query proxy: %w", err)
	}

	tempoGRPCReadProxy, err := proxy.NewProxy("tempo_read_grpc_proxy", defaultProxyConfig.WithURL(cfg.TempoGRPCReadURL), logger, pm)
	if err != nil {
		return nil, fmt.Errorf("failed to build tempo gRPC read proxy: %w", err)
	}

	tempoGRPCWriteProxy, err := proxy.NewProxy("tempo_write_grpc_proxy", proxy.Config{
		URL:                         cfg.TempoGRPCWriteURL,
		DialTimeout:                 cfg.DialTimeout,
		BufferPoolSize:              cfg.WritePoolSize,
		BufferPoolWidth:             cfg.WritePoolWidth,
		KeepAlive:                   cfg.WriteKeepAliveEnabled,
		GRPCClientClusterValidation: cfg.GRPCClientClusterValidation,
		ClientClusterValidation:     cfg.ClientClusterValidation,
		GRPCLoadBalancingConfig:     cfg.GRPCLoadBalancingConfig,
		GRPCMaxRecvMsgSize:          cfg.GRPCMaxRecvMsgSize,
		GRPCMaxSendMsgSize:          cfg.GRPCMaxSendMsgSize,
	}, logger, pm)
	if err != nil {
		return nil, fmt.Errorf("failed to build tempo gRPC write proxy: %w", err)
	}

	tempoHTTPWriteProxyConfig := proxy.Config{
		URL:                         cfg.TempoHTTPWriteURL,
		DialTimeout:                 cfg.DialTimeout,
		BufferPoolSize:              cfg.WritePoolSize,
		BufferPoolWidth:             cfg.WritePoolWidth,
		KeepAlive:                   cfg.WriteKeepAliveEnabled,
		GRPCClientClusterValidation: cfg.GRPCClientClusterValidation,
		ClientClusterValidation:     cfg.ClientClusterValidation,
		GRPCLoadBalancingConfig:     cfg.GRPCLoadBalancingConfig,
		GRPCMaxRecvMsgSize:          cfg.GRPCMaxRecvMsgSize,
		GRPCMaxSendMsgSize:          cfg.GRPCMaxSendMsgSize,
	}
	tempoHTTPWriteProxyConfig = tempoHTTPWriteProxyConfig.WithHTTPLoadBalancingPolicy(cfg.TempoHTTPWriteHTTPLoadBalancing.policy())
	tempoHTTPWriteProxy, err := proxy.NewProxy("tempo_write_http_proxy", tempoHTTPWriteProxyConfig, logger, pm)
	if err != nil {
		return nil, fmt.Errorf("failed to build tempo HTTP write proxy: %w", err)
	}

	var influxProxy, openTSDBHandler http.Handler
	if cfg.EnableThirdParty {
		influxProxy = writeProxy // Influx routes forward to the same write proxy
		openTSDBHandler = &inputs.OpenTSDBHandler{
			WriteProxy: writeProxy,
			Logger:     logger,
		}
	}

	// Build healthcheck service.
	healthBuilder := healthcheck.NewEndpointBuilder().
		Add("alertmanager", cfg.AlertURL, cfg.AlertmanagerHealthcheck).
		Add("ruler", cfg.RuleURL, cfg.RulerHealthcheck).
		Add("query", cfg.QueryURL, cfg.QueryHealthcheck).
		Add("distributor", cfg.WriteURL, cfg.DistributorHealthcheck).
		Add("compactor", cfg.CompactorURL, cfg.CompactorHealthcheck).
		Add("tempo-query", cfg.TempoQueryURL, cfg.TempoQueryHealthcheck).
		Add("tempo-distributor", cfg.TempoHTTPWriteURL, cfg.TempoDistributorHealthcheck)

	healthChecker, err := healthcheck.NewChecker(healthBuilder.Build(), reg, metricsNamespace, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to build healthcheck: %w", err)
	}

	var sensitiveHeaderRemovalMiddleware middleware.Interface
	if cfg.SensitiveHeaderRemovalEnabled {
		sensitiveHeaderRemovalMiddleware = gatewaymiddleware.NewSensitiveHeaderRemovalMiddleware(cfg.ExtraHeadersToRemove)
	}

	var clientIPMw *clientip.ClientIPMiddleware
	if cfg.ClientIPMiddlewareConfig.Enabled {
		if len(cfg.ClientIPMiddlewareConfig.Type) == 0 {
			level.Warn(logger).Log("msg", "client-ip-middleware.enabled is set but no extractor types are configured, skipping client IP middleware")
		} else {
			clientIPMw, err = clientip.New(cfg.ClientIPMiddlewareConfig, reg, logger)
			if err != nil {
				return nil, fmt.Errorf("failed to initialize client IP middleware: %w", err)
			}
		}
	}

	var xForwardedForMiddleware middleware.Interface
	if cfg.XForwardedForMiddlewareEnabled {
		if !cfg.ClientIPMiddlewareConfig.Enabled {
			level.Warn(logger).Log("msg", "x-forwarded-for-middleware.enabled is true but client-ip-middleware.enabled is false, X-Forwarded-For header will not be set")
		} else {
			xForwardedForMiddleware = gatewaymiddleware.NewXForwardedForMiddleware(logger)
		}
	}

	propagateTraceIDMiddleware := gatewaymiddleware.NewPropagateTraceIDMiddleware()

	connectionWithTTLMiddleware, err := newConnectionWithTTLMiddleware(cfg.ConnectionTTLMin, cfg.ConnectionTTLMax, cfg.ConnectionTTLIdleCheckFrequency, reg)
	if err != nil {
		return nil, err
	}

	api := &API{
		alertProxy:     alertProxy,
		ruleProxy:      ruleProxy,
		queryProxy:     queryProxy,
		writeProxy:     writeHandler,
		compactorProxy: compactorProxy,

		lokiTailProxy:   lokiTailProxy,
		lokiDeleteProxy: lokiDeleteProxy,

		tempoQueryProxy:     tempoQueryProxy,
		tempoGRPCWriteProxy: tempoGRPCWriteProxy,
		tempoGRPCReadProxy:  tempoGRPCReadProxy,
		tempoHTTPWriteProxy: tempoHTTPWriteProxy,

		influxProxy:     influxProxy,
		openTSDBHandler: openTSDBHandler,

		healthChecker: healthChecker,

		readTimeoutMiddleware:    withTimeout(cfg.ReadTimeout),
		writeTimeoutMiddleware:   withTimeout(cfg.WriteTimeout),
		requestTimeoutMiddleware: withTimeout(cfg.RequestTimeout),
		importTimeoutMiddleware:  withTimeout(cfg.ImportTimeout),
		exportTimeoutMiddleware:  withTimeout(cfg.ExportTimeout),
		// Debug info uploads can take longer than the default write timeout.
		pyroscopeDebugInfoUploadTimeoutMiddleware: withTimeout(cfg.PyroscopeDebugInfoUploadTimeout),
		requestLimiterMiddleware:                  newRequestLimiterMiddleware(cfg.RequestLimit, logger),
		connectionWithTTLMiddleware:               connectionWithTTLMiddleware,
		xForwardedForMiddleware:                   xForwardedForMiddleware,
		clientIPMiddleware:                        clientIPMw,
		propagateTraceIDMiddleware:                propagateTraceIDMiddleware,
		sensitiveHeaderRemovalMiddleware:          sensitiveHeaderRemovalMiddleware,

		cfg:    cfg,
		logger: logger,
		auth:   a,
		reg:    reg,
	}

	rtc, err := initTenantOverrides(cfg.Overrides, logger, reg)
	if err != nil {
		return nil, err
	}
	api.overridesMiddleware = NewOverridesMiddleware(rtc)

	return api, nil
}

// HealthChecker returns the healthcheck.Checker, which may be nil if no healthchecks are configured.
func (a *API) HealthChecker() *healthcheck.Checker {
	return a.healthChecker
}

// validScope returns true if the permission string is a known auth scope constant or empty (unauthenticated).
func validScope(permission string) bool {
	switch permission {
	case auth.ScopeMetricsRead, auth.ScopeMetricsWrite, auth.ScopeMetricsExport,
		auth.ScopeLogsRead, auth.ScopeLogsWrite, auth.ScopeLogsDelete,
		auth.ScopeRulesRead, auth.ScopeRulesWrite,
		auth.ScopeAlertsRead, auth.ScopeAlertsWrite,
		auth.ScopeProfilesRead, auth.ScopeProfilesWrite,
		auth.ScopeTracesRead, auth.ScopeTracesWrite,
		"":
		return true
	}
	return false
}

// registerRoutes registers the provided slice of routes with the specified router and handler.
func (a *API) registerRoutes(r *mux.Router, routeHandler http.Handler, routes []router.Route) error {
	for _, route := range routes {
		if !validScope(route.Permission) {
			return fmt.Errorf("unknown permission scope %q for route %s", route.Permission, route.Path)
		}

		h := routeHandler

		// Only register a timeout if the provided route is not a websocket.
		if !route.Websocket {
			var tm middleware.Interface
			if route.TimeoutOverride != nil {
				tm = route.TimeoutOverride
			} else {
				tm = a.timeoutMiddleware(route)
			}
			if tm != nil {
				h = tm.Wrap(h)
			}
		}

		// if the route has an embedded middleware, ensure that it wraps the provided handler
		if route.Middleware != nil {
			h = route.Middleware.Wrap(h)
		}

		// Wrap the handler in the request limiter middleware
		if a.requestLimiterMiddleware != nil {
			h = a.requestLimiterMiddleware.Wrap(h)
		}

		// Wrap the handler in the request per connection limiter middleware
		if a.connectionWithTTLMiddleware != nil {
			h = a.connectionWithTTLMiddleware.Wrap(h)
		}

		// Wrap the handler in the overrides middleware. This must be placed here so that it
		// runs after authMiddleware has resolved and injected X-Scope-OrgID, allowing
		// per-tenant config lookup.
		if a.overridesMiddleware != nil {
			h = a.overridesMiddleware.Wrap(h)
		}

		// This should immediately precede the auth middleware below to ensure sensitive headers are removed as soon as possible.
		if a.sensitiveHeaderRemovalMiddleware != nil {
			h = a.sensitiveHeaderRemovalMiddleware.Wrap(h)
		}

		// Wrap the handler in the authentication middleware
		h = a.authMiddleware(route.Permission, h)

		// Wrap the handler in the global request timeout middleware,
		// unless it's a websocket route, since websocket sessions are supposed to live for a long time.
		if !route.Websocket && a.requestTimeoutMiddleware != nil {
			h = a.requestTimeoutMiddleware.Wrap(h)
		}

		// Wrap the handler in the X-Forwarded-For middleware
		if a.xForwardedForMiddleware != nil {
			h = a.xForwardedForMiddleware.Wrap(h)
		}

		// Extract client IP from the request and store in context for use by auth and downstream handlers.
		if a.clientIPMiddleware != nil {
			h = a.clientIPMiddleware.Wrap(h)
		}

		// Propagate the trace ID from request context into the response headers.
		if a.propagateTraceIDMiddleware != nil {
			h = a.propagateTraceIDMiddleware.Wrap(h)
		}

		mr := r.NewRoute()
		if route.PathPrefix {
			mr = mr.PathPrefix(route.Path)
		} else {
			mr = mr.Path(route.Path)
		}
		mr.Methods(route.Methods...).Handler(h)
	}
	return nil
}

// RegisterRoutes registers the provided routes with the specified cluster
func (a *API) RegisterRoutes(r *mux.Router) error {
	// Conditionally enable routing the encoded URL it is required for the ruler API but not for all backends
	if a.cfg.MatchEncodedPaths {
		r.UseEncodedPath()
	}

	if err := a.registerWriteRoutes(r, a.wrapHandlerWithWriteMiddleware(a.writeProxy)); err != nil {
		return fmt.Errorf("unable to register remote write routes: %w", err)
	}

	if err := a.registerRoutes(r, a.compactorProxy, router.MimirCompactorRoutes); err != nil {
		return fmt.Errorf("unable to register compactor routes: %w", err)
	}

	if err := a.registerQueryRoutes(r, a.queryProxy); err != nil {
		return fmt.Errorf("unable to register query routes: %w", err)
	}

	if err := a.registerRulerRoutes(r, a.ruleProxy); err != nil {
		return fmt.Errorf("unable to register ruler routes: %w", err)
	}

	if err := a.registerTailRoutes(r, a.lokiTailProxy); err != nil {
		return fmt.Errorf("unable to register tail routes: %w", err)
	}

	if err := a.registerDeleteRoutes(r, a.lokiDeleteProxy); err != nil {
		return fmt.Errorf("unable to register delete routes: %w", err)
	}

	if err := a.registerRoutes(r, a.alertProxy, router.MimirAlertmanagerRoutes); err != nil {
		return fmt.Errorf("unable to register alertmanager routes: %w", err)
	}

	if err := a.registerRoutes(r, a.tempoGRPCWriteProxy, router.TempoGRPCWriteRoutes); err != nil {
		return fmt.Errorf("unable to register tempo gRPC write routes: %w", err)
	}
	if err := a.registerRoutes(r, a.tempoGRPCReadProxy, router.TempoGRPCReadRoutes); err != nil {
		return fmt.Errorf("unable to register tempo gRPC read routes: %w", err)
	}
	if err := a.registerRoutes(r, a.tempoHTTPWriteProxy, router.TempoHTTPWriteRoutes); err != nil {
		return fmt.Errorf("unable to register tempo HTTP write routes: %w", err)
	}
	if err := a.registerRoutes(r, a.tempoQueryProxy, router.TempoQueryFrontendRoutes); err != nil {
		return fmt.Errorf("unable to register tempo query routes: %w", err)
	}

	if a.cfg.TempoOverridesAPIEnabled {
		level.Info(a.logger).Log("msg", "registering tempo user-configurable overrides API")
		if err := a.registerRoutes(r, a.tempoQueryProxy, router.TempoUserConfigurableOverridesRoutes); err != nil {
			return fmt.Errorf("unable to register tempo user-configurable overrides routes: %w", err)
		}
	}

	// Register third-party routes when enabled.
	if a.cfg.EnableThirdParty {
		if err := a.registerRoutes(r, a.influxProxy, router.MimirInfluxWriteRoutes); err != nil {
			return fmt.Errorf("unable to register influx write routes: %w", err)
		}
		if err := a.registerRoutes(r, a.openTSDBHandler, router.MimirOpenTSDBWriteRoutes); err != nil {
			return fmt.Errorf("unable to register opentsdb write routes: %w", err)
		}
	}

	return nil
}

func (a *API) registerRulerRoutes(r *mux.Router, ruleProxy http.Handler) error {
	var routes []router.Route
	switch a.cfg.Backend {
	case Mimir:
		routes = router.MimirRulerRoutes
	case Loki:
		routes = router.LokiRulerRoutes
	default:
		return nil
	}
	return a.registerRoutes(r, ruleProxy, routes)
}

func (a *API) registerWriteRoutes(r *mux.Router, writeProxy http.Handler) error {
	var routes []router.Route
	routes = append(routes, router.MimirWriteRoutes...)
	routes = append(routes, router.LokiWriteRoutes...)
	routes = append(routes, router.PyroscopeWriteRoutes...)
	debugInfoUpload := router.PyroscopeDebugInfoUploadRoute
	debugInfoUpload.TimeoutOverride = a.pyroscopeDebugInfoUploadTimeoutMiddleware
	routes = append(routes, debugInfoUpload)
	return a.registerRoutes(r, writeProxy, routes)
}

func (a *API) registerQueryRoutes(r *mux.Router, readProxy http.Handler) error {
	var routes []router.Route
	routes = append(routes, router.MimirQueryRoutes...)
	routes = append(routes, router.LokiQueryRoutes...)
	routes = append(routes, router.PyroscopeQueryRoutes...)
	return a.registerRoutes(r, readProxy, routes)
}

func (a *API) registerTailRoutes(r *mux.Router, tailProxy http.Handler) error {
	var routes []router.Route
	switch a.cfg.Backend {
	case Loki:
		routes = router.LokiTailRoutes
	default:
		return nil
	}
	return a.registerRoutes(r, tailProxy, routes)
}

func (a *API) registerDeleteRoutes(r *mux.Router, deleteProxy http.Handler) error {
	var routes []router.Route
	switch a.cfg.Backend {
	case Loki:
		routes = router.LokiDeleteRoutes
	default:
		return nil
	}
	return a.registerRoutes(r, deleteProxy, routes)
}

// wrapHandlerWithWriteMiddleware wraps the input handler with the configured write middleware (if any).
func (a *API) wrapHandlerWithWriteMiddleware(handler http.Handler) http.Handler {
	if a.WriteMiddleware == nil {
		return handler
	}
	return a.WriteMiddleware.Wrap(handler)
}

// timeoutMiddleware returns the appropriate timeout middleware for a route based on its permission.
func (a *API) timeoutMiddleware(r router.Route) middleware.Interface {
	switch r.Permission {
	case auth.ScopeMetricsRead, auth.ScopeAlertsRead, auth.ScopeRulesRead,
		auth.ScopeTracesRead, auth.ScopeLogsRead, auth.ScopeProfilesRead:
		return a.readTimeoutMiddleware
	case auth.ScopeMetricsWrite, auth.ScopeAlertsWrite, auth.ScopeRulesWrite,
		auth.ScopeTracesWrite, auth.ScopeLogsWrite, auth.ScopeLogsDelete, auth.ScopeProfilesWrite:
		return a.writeTimeoutMiddleware
	case auth.ScopeMetricsExport:
		return a.exportTimeoutMiddleware

	default:
		return a.requestTimeoutMiddleware
	}
}

// authMiddleware returns an http.Handler that authenticates the request, strips
// incoming auth headers, and injects X-Scope-OrgID before calling next.
func (a *API) authMiddleware(permission string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		isGRPC := strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc")

		result, err := a.auth.Authenticate(r.Context(), r, permission)
		if err != nil {
			if auth.IsUnauthorized(err) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if auth.IsForbidden(err) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			level.Error(a.logger).Log("msg", "auth error", "err", err)
			http.Error(w, "authentication error", http.StatusBadGateway)
			return
		}

		// Clone request before modifying headers.
		r = r.Clone(r.Context())

		// Strip client-supplied auth-related headers; AuthResultHeaders is authoritative.
		r.Header.Del("Authorization")
		r.Header.Del(user.OrgIDHeaderName)
		r.Header.Del(auth.LabelPolicyHeader)

		for k, vs := range auth.AuthResultHeaders(result) {
			r.Header[k] = vs
		}

		if isGRPC {
			// HTTP/2 requires lowercase header keys.
			canonicalOrgID := http.CanonicalHeaderKey(user.OrgIDHeaderName)
			if v, ok := r.Header[canonicalOrgID]; ok {
				delete(r.Header, canonicalOrgID)
				r.Header["x-scope-orgid"] = v //nolint:staticcheck // SA1008: intentional non-canonical key — gRPC/HTTP2 requires lowercase headers
			}
		} else {
			// Propagate orgID via dskit context for any downstream Go code.
			r = r.WithContext(user.InjectOrgID(r.Context(), tenant.JoinTenantIDs(result.TenantIDs())))
		}

		next.ServeHTTP(w, r)
	})
}

// newReverseProxy builds an http.Handler that reverse-proxies to targetURL.
// If targetURL is empty, the handler returns 502 with a "not configured" message.
func newReverseProxy(name, targetURL string, dialTimeout time.Duration, logger log.Logger) (http.Handler, error) {
	if targetURL == "" {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, fmt.Sprintf("backend %q not configured", name), http.StatusBadGateway)
		}), nil
	}
	target, err := url.Parse(targetURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL for backend %q: %w", name, err)
	}
	p := httputil.NewSingleHostReverseProxy(target)
	if dialTimeout > 0 {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.DialContext = (&net.Dialer{Timeout: dialTimeout}).DialContext
		p.Transport = transport
	}
	p.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		level.Error(logger).Log("msg", "proxy error", "backend", name, "err", err)
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}
	return p, nil
}

// withTimeout returns a middleware that applies the given timeout to the request.
// If timeout is <= 0, it returns a pass-through middleware.
func withTimeout(d time.Duration) middleware.Interface {
	if d <= 0 {
		return middleware.Func(func(next http.Handler) http.Handler { return next })
	}
	return middleware.Func(func(next http.Handler) http.Handler {
		return http.TimeoutHandler(next, d, "request timed out")
	})
}

// newDropSkipLabelValidationMiddleware returns a middleware that drops write requests
// containing the X-Mimir-SkipLabelNameValidation header.
func newDropSkipLabelValidationMiddleware() middleware.Interface {
	return middleware.Func(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Mimir-SkipLabelNameValidation") != "" {
				http.Error(w, "X-Mimir-SkipLabelNameValidation header is not allowed", http.StatusBadRequest)
				return
			}
			next.ServeHTTP(w, r)
		})
	})
}

// newMaxBytesMiddleware returns a middleware that limits the request body size.
func newMaxBytesMiddleware(limit int64) middleware.Interface {
	return middleware.Func(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
			next.ServeHTTP(w, r)
		})
	})
}
