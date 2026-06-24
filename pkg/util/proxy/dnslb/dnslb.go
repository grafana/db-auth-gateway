// SPDX-License-Identifier: AGPL-3.0-only

// Package dnslb provides dynamic load balancing mechanisms for HTTP requests,
// based on DNS discovery.
package dnslb

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-kit/log"

	"github.com/grafana/dskit/servicediscovery"
	"github.com/grafana/dskit/services"
	"github.com/pkg/errors"
)

const dnsDiscoveryLookupPeriod = 10 * time.Second

// DNSDiscoveredHostProvider provides the host to proxy the HTTP request to
// based on DNS discovery. It must be thread-safe.
type DNSDiscoveredHostProvider interface {
	servicediscovery.Notifications

	// StartHost returns the host to be used for the request, and
	// also starts tracking an inflight request for that host.
	StartHost() string

	// EndHost tells the hostProvider that the request is done processing. This
	// is used to track inflight requests and lets the hostProvier balance
	// traffic.
	EndHost(string)
}

// StartDNSLoop starts a background service that does DNS discovery loop for the
// given hostname. NOTE: this function is starting a background service without
// a way to stop it. The current gateway code structure does not make it easy to
// stop or listen for errors on background services to handle them properly. We
// might refactor the gateway to make this easier at some point.
func StartDNSLoop(hostname string, notifications servicediscovery.Notifications, logger log.Logger) error {
	// The DNS discovery service returns IP+port. Interestingly, it defaults
	// to port 443 if not specified, without knowing the URL scheme.
	// To avoid unexpected behavior, we require the port to be explicitly specified.
	if !strings.ContainsRune(hostname, ':') {
		return fmt.Errorf("host port must be explicit when load balancing is used, got %s", hostname)
	}

	serv, err := servicediscovery.NewDNS(logger, hostname, dnsDiscoveryLookupPeriod, notifications)
	if err != nil {
		return err
	}
	serv.AddListener(services.NewListener(nil, nil, nil, nil, func(_ services.State, err error) {
		// This is unexpected and non-recoverable, just panic.
		err = errors.Wrapf(err, "DNS discovery service for %s failed, can't proxy requests", hostname)
		panic(err)
	}))
	return serv.StartAsync(context.Background())
}
