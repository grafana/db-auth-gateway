// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"slices"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.uber.org/atomic"
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/stats"
)

// The bounded load balancer is a gRPC load balancer that attempts to route RPCs to backends
// using a round-robin algorithm, but temporarily excluding backends with a number of inflight
// requests significantly higher than the average.
//
// The main purpose of this algorithm is to:
// 1. Do not concentrate too many inflight requests on a single (or few) slow backends.
// 2. Do our best to keep round-robin requests to backends (particularly useful when the backend
//    expects it, e.g. due to how some global rate limits are implemented as local ones).
//
// A backend is temporarily excluded from receiving more requests each time its number of inflight
// requests is greater than the average number of inflight requests across all backends multiplied
// by a configurable "overloaded factor".
//
// For example, given the following scenario:
// - overloaded factor = 2
// - 1.1.1.1: 10 inflight requests
// - 2.2.2.2: 42 inflight requests
// - 3.3.3.3: 12 inflight requests
//
// We get:
// - The average number of inflight requests is ceil((10+40+12)/3) = 21
// - The overloaded threshold is 21 * 2 = 42
// - The backend "2.2.2.2" is not allowed to receive more inflight requests until some will complete

// Custom type to hide it from other packages.
type contextKey int

const (
	contextKeyRPCId          contextKey = 1
	contextKeyConnRemoteAddr contextKey = 2

	reasonUnknownRemoteAddr           = "unknown-remote-address"
	reasonUnknownRemoteAddrOnRPCStart = "unknown-remote-address-on-rpc-start"
	reasonUnknownRemoteAddrOnRPCEnd   = "unknown-remote-address-on-rpc-end"
	reasonUnknownRPCId                = "unknown-rpc-id"
)

var (
	nextBalancingPolicyID = atomic.NewUint32(0)
)

// newBoundedLoadBalancer creates and registers a new gRPC balancer. The returned balancing policy ID should be used
// to configure the gRPC client service policy, while the returned stats.Handler should be added to gRPC client
// dial options.
//
// This function automatically registers the balancer to gRPC library. This call is not thread-safe, so all bounded
// load balancers should be created upfront at application startup, before gRPC clients are getting used.
func newBoundedLoadBalancer(overloadedFactor float64, metrics *boundedLoadBalancerMetrics) (string, balancer.Builder, stats.Handler) {
	// Generate the balancing policy ID, used to reference it in the client service policy.
	balancingPolicyID := fmt.Sprintf("%s-%d", GRPCLoadBalancingPolicyBoundedLoad, nextBalancingPolicyID.Inc())

	var (
		stats         = newBoundedLoadBalancerStatsHandler(metrics)
		logger        = grpclog.Component(GRPCLoadBalancingPolicyBoundedLoad)
		customBuilder = &boundedLoadBalancerBuilder{overloadedFactor: overloadedFactor, logger: logger, stats: stats, metrics: metrics}
		builder       = base.NewBalancerBuilder(balancingPolicyID, customBuilder, base.Config{HealthCheck: true})
	)

	// The balancer will be registered with the balancerName.
	balancer.Register(builder)

	return balancingPolicyID, builder, stats
}

type boundedLoadBalancerBuilder struct {
	overloadedFactor float64
	logger           grpclog.DepthLoggerV2
	stats            *boundedLoadBalancerStatsHandler
	metrics          *boundedLoadBalancerMetrics
}

func (b *boundedLoadBalancerBuilder) Build(info base.PickerBuildInfo) balancer.Picker {
	b.logger.Infof("boundedLoadBalancerBuilder: Build called with info: %v", info)
	if len(info.ReadySCs) == 0 {
		return base.NewErrPicker(balancer.ErrNoSubConnAvailable)
	}

	return newBoundedLoadBalancerPicker(info, b.stats, b.metrics, b.overloadedFactor)
}

type boundedLoadBalancerPicker struct {
	// subConns is the snapshot of the balancer when this picker was created.
	// The slice is immutable.
	subConns     []balancer.SubConn
	subConnsAddr []string

	// nextRoundRobinID is an index incremented each time we need to pick the next
	// connection.
	nextRoundRobinID *atomic.Uint32

	stats            *boundedLoadBalancerStatsHandler
	metrics          *boundedLoadBalancerMetrics
	overloadedFactor float64
}

func newBoundedLoadBalancerPicker(info base.PickerBuildInfo, stats *boundedLoadBalancerStatsHandler, metrics *boundedLoadBalancerMetrics, overloadedFactor float64) *boundedLoadBalancerPicker {
	var (
		subConns       = make([]balancer.SubConn, 0, len(info.ReadySCs))
		subConnsAddr   = make([]string, 0, len(info.ReadySCs))
		subConnsByAddr = make(map[string]balancer.SubConn, len(info.ReadySCs))
	)

	// Build the list of remote addresses.
	for sc, scInfo := range info.ReadySCs {
		subConnsAddr = append(subConnsAddr, scInfo.Address.Addr)
		subConnsByAddr[scInfo.Address.Addr] = sc
	}

	// Sort connections by address to get stable test results.
	slices.Sort(subConnsAddr)

	// Populate the slice of connections keeping the same order of subConnsAddr.
	for _, addr := range subConnsAddr {
		subConns = append(subConns, subConnsByAddr[addr])
	}

	return &boundedLoadBalancerPicker{
		subConns:         subConns,
		subConnsAddr:     subConnsAddr,
		stats:            stats,
		metrics:          metrics,
		overloadedFactor: overloadedFactor,
		// Start at a random index, as the same balancer rebuilds a new
		// picker when SubConn states change, and we don't want to apply excess
		// load to the first server in the list.
		nextRoundRobinID: atomic.NewUint32(uint32(rand.Intn(len(subConns)))),
	}
}

func (p *boundedLoadBalancerPicker) Pick(_ balancer.PickInfo) (balancer.PickResult, error) {
	overloadThreshold := p.stats.getOverloadThreshold(p.overloadedFactor)
	if overloadThreshold <= 0 {
		conn, _ := p.nextRoundRobinConnection()
		return balancer.PickResult{SubConn: conn}, nil
	}

	// Try to find a non-overloaded connection. We try as many times as the current number of connections.
	//
	// IMPORTANT: this logic suffers a race condition. Concurrent requests to Pick() will increment the "next
	// connection ID" to try in round-robin, so we may end up with an overloaded connection checked 2+ times
	// during a single Pick() call. That's life, we live with that.
	var lastConn balancer.SubConn

	for try := 0; try < len(p.subConns); try++ {
		var addr string

		lastConn, addr = p.nextRoundRobinConnection()
		if !p.stats.isOverloaded(addr, overloadThreshold) {
			break
		}

		p.metrics.backendSkippedTotal.Inc()
	}

	// We haven't found a non overloaded connection. Just return the next one.
	return balancer.PickResult{SubConn: lastConn}, nil
}

func (p *boundedLoadBalancerPicker) nextRoundRobinConnection() (balancer.SubConn, string) {
	// The picker is guaranteed to always have at least a connection (the check is done in the builder).
	subConnsLen := uint32(len(p.subConns))
	nextIndex := p.nextRoundRobinID.Inc()
	pick := nextIndex % subConnsLen

	return p.subConns[pick], p.subConnsAddr[pick]
}

type boundedLoadBalancerStatsHandler struct {
	metrics *boundedLoadBalancerMetrics

	// nextRPCID keeps track of the next RPC ID, used to correlate RPCs with the connection remote address.
	nextRPCID *atomic.Uint64

	// The following data structures are used to keep track of gRPC connections and RPCs.
	mx                           sync.RWMutex
	remoteAddrByRPC              map[uint64]string
	inflightRequestsByRemoteAddr map[string]uint64
	inflightRequestsTotal        uint64
	connectionsByRemoteAddr      map[string]uint64
}

func newBoundedLoadBalancerStatsHandler(metrics *boundedLoadBalancerMetrics) *boundedLoadBalancerStatsHandler {
	return &boundedLoadBalancerStatsHandler{
		metrics:                      metrics,
		nextRPCID:                    atomic.NewUint64(0),
		remoteAddrByRPC:              make(map[uint64]string),
		inflightRequestsByRemoteAddr: make(map[string]uint64),
		connectionsByRemoteAddr:      make(map[string]uint64),
	}
}

func (s *boundedLoadBalancerStatsHandler) getOverloadThreshold(overloadedFactor float64) uint64 {
	// Overload factor is expected to be > 1.
	if overloadedFactor <= 1 {
		return 0
	}

	s.mx.RLock()
	defer s.mx.RUnlock()

	// No overloaded connection if there are no connections or no inflight requests at all.
	if s.inflightRequestsTotal == 0 || len(s.inflightRequestsByRemoteAddr) == 0 {
		return 0
	}

	// Compute the average number of inflight requests per connection.
	avg := float64(s.inflightRequestsTotal) / float64(len(s.inflightRequestsByRemoteAddr))

	// Compute the overload threshold.
	return uint64(math.Ceil(avg * overloadedFactor))
}

func (s *boundedLoadBalancerStatsHandler) isOverloaded(addr string, overloadThreshold uint64) bool {
	s.mx.RLock()
	inflightRequests, ok := s.inflightRequestsByRemoteAddr[addr]
	s.mx.RUnlock()

	return ok && inflightRequests >= overloadThreshold
}

// TagRPC implements stats.Handler.
func (s *boundedLoadBalancerStatsHandler) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context {
	s.metrics.rpcTotal.Inc()

	// Generate a unique ID for the RPC. This will be used to correlate statistics reported by HandleRPC().
	rpcID := s.nextRPCID.Inc()
	return context.WithValue(ctx, contextKeyRPCId, rpcID)
}

// HandleRPC implements stats.Handler.
func (s *boundedLoadBalancerStatsHandler) HandleRPC(ctx context.Context, info stats.RPCStats) {
	switch typedInfo := info.(type) {
	case *stats.OutHeader:
		if !typedInfo.Client {
			return
		}

		// Get the RPC ID.
		rpcID, ok := ctx.Value(contextKeyRPCId).(uint64)
		if !ok {
			// This is an unexpected condition. The error metric will be tracked by stats.End.
			return
		}

		remoteAddr := typedInfo.RemoteAddr.String()
		remoteAddrFound := false

		// Keep the lock as shortest as possible.
		{
			s.mx.Lock()

			// The OutHeader should be reported once for each RPC when the stream is created (gRPC creates
			// 1 stream for each unary call), before the RPC is issued. Even if we don't expect OutHeader
			// to be reported more than once per RCP call, we handle this edge case anyway to avoid future
			// regressions.
			firstHeader := s.remoteAddrByRPC[rpcID] == ""
			if !firstHeader {
				s.mx.Unlock()
				return
			}

			// Keep track of the remote address for this RPC, so that we'll have it once the stats.End is reported.
			s.remoteAddrByRPC[rpcID] = remoteAddr

			// Only track the inflight requests if the remote address is currently tracked. The remote address
			// should be always tracked, but we've seen some edge cases / race conditions where the stats.ConnEnd
			// is reported to HandleConn() before their RPC stats are notified.
			if _, ok := s.inflightRequestsByRemoteAddr[remoteAddr]; ok {
				s.inflightRequestsByRemoteAddr[remoteAddr]++
				s.inflightRequestsTotal++
				remoteAddrFound = true
			}

			s.mx.Unlock()
		}

		if !remoteAddrFound {
			// This is an unexpected condition. There's no much we can do except tracking the error.
			s.metrics.rpcErrorsTotal.WithLabelValues(reasonUnknownRemoteAddrOnRPCStart).Inc()
			return
		}

	case *stats.End:
		// Get the RPC ID.
		rpcID, ok := ctx.Value(contextKeyRPCId).(uint64)
		if !ok {
			// This is an unexpected condition. There's no much we can do except tracking the error.
			// To avoid tracking it multiple times, we only track it for the stats.End.
			s.metrics.rpcErrorsTotal.WithLabelValues(reasonUnknownRPCId).Inc()
			return
		}

		remoteAddrFound := false

		// Keep the lock as shortest as possible.
		{
			s.mx.Lock()

			// Find the remote address for this RPC and then remove the RPC, since it's over.
			var remoteAddr string
			remoteAddr, remoteAddrFound = s.remoteAddrByRPC[rpcID]
			delete(s.remoteAddrByRPC, rpcID)

			if remoteAddrFound {
				// Only track the inflight requests if the remote address is currently tracked. The remote address
				// should be always tracked, but we've seen some edge cases / race conditions where the stats.ConnEnd
				// is reported to HandleConn() before their RPC stats are notified.
				if _, ok := s.inflightRequestsByRemoteAddr[remoteAddr]; ok {
					s.inflightRequestsByRemoteAddr[remoteAddr]--
					s.inflightRequestsTotal--
				} else {
					// The remote address looks not tracked. Switch the flag to false so that we'll
					// track this error condition in a metric.
					remoteAddrFound = false
				}
			}

			s.mx.Unlock()
		}

		if !remoteAddrFound {
			// This is an unexpected condition. There's no much we can do except tracking the error.
			s.metrics.rpcErrorsTotal.WithLabelValues(reasonUnknownRemoteAddrOnRPCEnd).Inc()
			return
		}
	}
}

// TagConn implements stats.Handler.
//
// IMPORTANT: the connection info contains the remote address but the returned context is not the one used
// by RPC (and then passed to HandleRPC()). This means that even if we store the remote address
// in the context, it will just be available in HandleConn() and not in HandleRPC().
func (s *boundedLoadBalancerStatsHandler) TagConn(ctx context.Context, info *stats.ConnTagInfo) context.Context {
	s.metrics.connTotal.Inc()
	return context.WithValue(ctx, contextKeyConnRemoteAddr, info.RemoteAddr.String())
}

// HandleConn implements stats.Handler.
func (s *boundedLoadBalancerStatsHandler) HandleConn(ctx context.Context, info stats.ConnStats) {
	remoteAddr, ok := ctx.Value(contextKeyConnRemoteAddr).(string)
	if !ok {
		// This is an unexpected condition. There's no much we can do except tracking the error.
		// To avoid tracking it multiple times, we only track it for the stats.ConnBegin.
		if _, ok := info.(*stats.ConnBegin); ok {
			s.metrics.connErrorsTotal.WithLabelValues(reasonUnknownRemoteAddr).Inc()
		}

		return
	}

	s.mx.Lock()
	defer s.mx.Unlock()

	switch info.(type) {
	case *stats.ConnBegin:
		// Keep track of how many open connections we have against a remote backend.
		s.connectionsByRemoteAddr[remoteAddr]++

		// Add the remote backend to the map tracking the inflight requests (if not already there).
		// This is required because we want to easily count the number of unique backends connected to
		// just counting the number of entries in the map.
		if _, ok := s.inflightRequestsByRemoteAddr[remoteAddr]; !ok {
			s.inflightRequestsByRemoteAddr[remoteAddr] = 0
		}

	case *stats.ConnEnd:
		// Keep track of how many open connections we have against a remote backend.
		s.connectionsByRemoteAddr[remoteAddr]--

		// If there are no more connections against the remote backend, then remove it from the map
		// used to track the inflight requests.
		if s.connectionsByRemoteAddr[remoteAddr] == 0 {
			if count, ok := s.inflightRequestsByRemoteAddr[remoteAddr]; ok {
				s.inflightRequestsTotal -= count
			}

			delete(s.inflightRequestsByRemoteAddr, remoteAddr)
			delete(s.connectionsByRemoteAddr, remoteAddr)
		}
	}
}

type boundedLoadBalancerMetrics struct {
	connTotal           prometheus.Counter
	connErrorsTotal     *prometheus.CounterVec
	rpcTotal            prometheus.Counter
	rpcErrorsTotal      *prometheus.CounterVec
	backendSkippedTotal prometheus.Counter
}

func newBoundedLoadBalancerMetrics(namespace string, reg prometheus.Registerer) *boundedLoadBalancerMetrics {
	return &boundedLoadBalancerMetrics{
		connTotal: promauto.With(reg).NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "grpc_bounded_load_balancer_connections_total",
			Help:      "Total number of connections balanced (or attempted to load balanced) by the gRPC bounded load balancer.",
		}),
		connErrorsTotal: promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "grpc_bounded_load_balancer_connection_errors_total",
			Help:      "Total number of connections failed to be balanced by the gRPC bounded load balancer.",
		}, []string{"reason"}),
		rpcTotal: promauto.With(reg).NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "grpc_bounded_load_balancer_rpc_total",
			Help:      "Total number of RPC balanced (or attempted to load balance) by the gRPC bounded load balancer.",
		}),
		rpcErrorsTotal: promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "grpc_bounded_load_balancer_rpc_errors_total",
			Help:      "Total number of RPC failed to be balanced by the gRPC bounded load balancer.",
		}, []string{"reason"}),
		backendSkippedTotal: promauto.With(reg).NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "grpc_bounded_load_balancer_backend_skipped_total",
			Help:      "Total number of times a backend is not picked by the bounded load balancer because overloaded.",
		}),
	}
}
