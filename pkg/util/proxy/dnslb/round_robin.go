// SPDX-License-Identifier: AGPL-3.0-only

package dnslb

import (
	"sync"
	"sync/atomic"

	"github.com/grafana/dskit/servicediscovery"
)

// roundRobinHostProvider round robins across DNS-discovered hosts. Discovered
// hosts must be updated using the implemented servicediscovery.Notifications
// interface. If no hosts are discovered, the defaultHostname is used.
type roundRobinHostProvider struct {
	defaultHostname string
	mu              sync.RWMutex

	hosts    []string
	hostsSet map[string]struct{}
	count    atomic.Uint32
}

func NewRoundRobinHostProvider(defaultHostname string) DNSDiscoveredHostProvider {
	return newRoundRobinHostProvider(defaultHostname)
}

func newRoundRobinHostProvider(defaultHostname string) *roundRobinHostProvider {
	return &roundRobinHostProvider{
		defaultHostname: defaultHostname,
		hostsSet:        make(map[string]struct{}),
	}
}

func (rr *roundRobinHostProvider) StartHost() string {
	rr.mu.RLock()
	defer rr.mu.RUnlock()
	if len(rr.hosts) == 0 {
		return rr.defaultHostname
	}
	// count can overflow and that's ok, we always take the modulo
	i := int(rr.count.Add(1))
	return rr.hosts[i%len(rr.hosts)]
}

func (rr *roundRobinHostProvider) EndHost(string) {
}

func (rr *roundRobinHostProvider) InstanceChanged(servicediscovery.Instance) {
	// No-op since we're using DNS based discovery which doesn't use the InUse field
}

func (rr *roundRobinHostProvider) InstanceAdded(instance servicediscovery.Instance) {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	_, ok := rr.hostsSet[instance.Address]
	if !ok {
		rr.hosts = append(rr.hosts, instance.Address)
		rr.hostsSet[instance.Address] = struct{}{}
	}
}

func (rr *roundRobinHostProvider) InstanceRemoved(instance servicediscovery.Instance) {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	_, ok := rr.hostsSet[instance.Address]
	if ok {
		delete(rr.hostsSet, instance.Address)
		for i, h := range rr.hosts {
			if h == instance.Address {
				rr.hosts[i] = rr.hosts[len(rr.hosts)-1]
				rr.hosts = rr.hosts[:len(rr.hosts)-1]
				return
			}
		}
		panic("unreachable") // serious bug, just panic
	}
}
