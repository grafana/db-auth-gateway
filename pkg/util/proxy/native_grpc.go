// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"fmt"
	"net/http"
	"time"

	"github.com/go-kit/log"
	"github.com/grafana/dskit/grpcclient"
	"github.com/grafana/dskit/middleware"
	grpcproxy "github.com/mwitkow/grpc-proxy/proxy"
	otgrpc "github.com/opentracing-contrib/go-grpc"
	"github.com/opentracing/opentracing-go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	grpcstatus "google.golang.org/grpc/status"
)

// newNativeGRPCProxy creates a new proxy that will receive + send arbitrary GRPC messages.
//
// NOTE: this handler requires that the server has HTTP/2 enabled. e.g.
//
//	server.Config.Protocols = new(http.Protocols)
//	server.Config.Protocols.SetUnencryptedHTTP2(true)
//
// This is useful for load balancing when the end-user is sending GRPC, e.g. OTLP/grpc.
func newNativeGRPCProxy(name string, cfg Config, logger log.Logger, pm *Metrics) (http.Handler, error) {
	address := cfg.URL

	grpcClientConfig := grpcclient.Config{
		MaxRecvMsgSize:    cfg.GRPCMaxRecvMsgSize,
		MaxSendMsgSize:    cfg.GRPCMaxSendMsgSize,
		ClusterValidation: cfg.GRPCClientClusterValidation,
		TLSEnabled:        cfg.TLSEnabled,
		TLS:               cfg.TLS,
	}

	interceptors := []grpc.StreamClientInterceptor{
		pm.grpcClientMetrics.StreamClientInterceptor(),
		otgrpc.OpenTracingStreamClientInterceptor(opentracing.GlobalTracer()),
		middleware.StreamClientUserHeaderInterceptor,
	}
	invalidClusterValidationReporter := middleware.NoOpInvalidClusterValidationReporter
	if grpcClientConfig.ClusterValidation.Label != "" {
		client := fmt.Sprintf("proxy-%s", name)
		invalidClusterValidationReporter = newInvalidClusterValidationReporter(grpcClientConfig.ClusterValidation.Label, pm.invalidClusterValidation(client, "grpc"), logger)
	}
	gRPCDialOptions, err := grpcClientConfig.DialOption(nil, interceptors, invalidClusterValidationReporter)
	if err != nil {
		return nil, err
	}

	gRPCDialOptions = append(gRPCDialOptions,
		grpc.WithKeepaliveParams(
			keepalive.ClientParameters{
				Time:                time.Second * 10,
				Timeout:             time.Second * 5,
				PermitWithoutStream: true,
			},
		),
	)

	// Configure the load balancing policy.
	switch cfg.GRPCLoadBalancingConfig.Policy {
	case GRPCLoadBalancingPolicyRoundRobin:
		serviceConfig := fmt.Sprintf(grpcServiceConfigTemplate, GRPCLoadBalancingPolicyRoundRobin)
		gRPCDialOptions = append(gRPCDialOptions, grpc.WithDefaultServiceConfig(serviceConfig))
	case GRPCLoadBalancingPolicyBoundedLoad:
		// The bounded load policy requires gRPC connections and RPC statistics to be tracked.
		// To be able to correlate the stats tracked by this client with the right balancer instance,
		// we need to configure the client balancer using the unique ID provided.
		balancingPolicyID, _, statsHandler := newBoundedLoadBalancer(cfg.GRPCLoadBalancingConfig.OverloadedFactor, pm.grpcBoundedLoadMetrics)
		serviceConfig := fmt.Sprintf(grpcServiceConfigTemplate, balancingPolicyID)
		gRPCDialOptions = append(gRPCDialOptions, grpc.WithDefaultServiceConfig(serviceConfig), grpc.WithStatsHandler(statsHandler))
	default:
		return nil, fmt.Errorf("unsupported gRPC load balancing policy: %q", cfg.GRPCLoadBalancingConfig.Policy)
	}

	conn, err := grpc.NewClient(address, gRPCDialOptions...)
	if err != nil {
		return nil, fmt.Errorf("grpc write proxy dial failed: %w", err)
	}

	proxy := grpcproxy.NewProxy(conn, grpc.ChainStreamInterceptor(
		pm.grpcServerMetrics.StreamServerInterceptor(),
		otgrpc.OpenTracingStreamServerInterceptor(opentracing.GlobalTracer()),
		middleware.StreamServerUserHeaderInterceptor,
		metricsInterceptor(name, pm),
	))
	return proxy, nil
}

func metricsInterceptor(name string, pm *Metrics) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		rpcStart := time.Now()
		err := handler(srv, ss)
		if err != nil {
			reason := errorToReason(ss.Context(), err)
			pm.RequestsErrors.WithLabelValues(reason).Inc()
		}
		code := grpcstatus.Code(err)
		codeString := code.String()
		pm.DownstreamDuration.WithLabelValues(info.FullMethod, name, codeString).Observe(time.Since(rpcStart).Seconds())
		return err
	}
}
