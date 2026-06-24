// SPDX-License-Identifier: AGPL-3.0-only

package dnslb

import (
	"container/heap"
	"sync"

	"github.com/grafana/dskit/servicediscovery"
)

// leastInflightHostProvider balances traffic by picking the host with the least
// inflight requests, among the DNS-discovered hosts. Discovered hosts must be
// updated using the implemented servicediscovery.Notifications interface. If no
// hosts are discovered, the defaultHostname is used. The EndHost method must be
// called after a selected host is done processing a request.
type leastInflightHostProvider struct {
	defaultHostname string
	mu              sync.Mutex

	h namedMinHeap
}

func NewLeastInflightHostProvider(defaultHostname string) DNSDiscoveredHostProvider {
	return newLeastInflightHostProvider(defaultHostname)
}

func newLeastInflightHostProvider(defaultHostname string) *leastInflightHostProvider {
	return &leastInflightHostProvider{
		defaultHostname: defaultHostname,
		h:               namedMinHeap{nameIndex: make(map[string]int)},
	}
}

func (li *leastInflightHostProvider) StartHost() string {
	li.mu.Lock()
	defer li.mu.Unlock()
	if li.h.Len() == 0 {
		return li.defaultHostname
	}
	host := li.h.Min()
	li.h.Inc(host)
	return host
}

func (li *leastInflightHostProvider) EndHost(host string) {
	li.mu.Lock()
	defer li.mu.Unlock()
	li.h.Dec(host)
}

func (li *leastInflightHostProvider) InstanceChanged(servicediscovery.Instance) {
	// No-op since we're using DNS based discovery which doesn't use the InUse field
}

func (li *leastInflightHostProvider) InstanceAdded(instance servicediscovery.Instance) {
	li.mu.Lock()
	defer li.mu.Unlock()
	li.h.Add(instance.Address)
}

func (li *leastInflightHostProvider) InstanceRemoved(instance servicediscovery.Instance) {
	li.mu.Lock()
	defer li.mu.Unlock()
	li.h.Remove(instance.Address)
}

// namedMinHeap is a heap that lets you increment and decrement values by name,
// and get the name of the minimum value.
type namedMinHeap struct {
	q []struct {
		name string
		val  int
	}
	nameIndex map[string]int
}

func (h *namedMinHeap) Inc(name string) {
	i, ok := h.nameIndex[name]
	if !ok {
		panic("name not found")
	}
	h.q[i].val++
	heap.Fix(h, i)
}

func (h *namedMinHeap) Dec(name string) {
	i, ok := h.nameIndex[name]
	if !ok {
		return
	}
	h.q[i].val--
	heap.Fix(h, i)
}

func (h *namedMinHeap) Min() string {
	if len(h.q) == 0 {
		panic("empty heap")
	}
	return h.q[0].name
}

func (h *namedMinHeap) Add(name string) {
	if _, ok := h.nameIndex[name]; ok {
		return
	}
	heap.Push(h, struct {
		name string
		val  int
	}{name: name, val: 0})
}

func (h *namedMinHeap) Remove(name string) {
	i, ok := h.nameIndex[name]
	if !ok {
		return
	}
	heap.Remove(h, i)
}

// The methods below implement the heap.Interface interface.

func (h *namedMinHeap) Len() int {
	return len(h.q)
}

func (h *namedMinHeap) Less(i, j int) bool {
	return h.q[i].val < h.q[j].val
}

func (h *namedMinHeap) Swap(i, j int) {
	h.q[i], h.q[j] = h.q[j], h.q[i]
	h.nameIndex[h.q[i].name] = i
	h.nameIndex[h.q[j].name] = j
}

func (h *namedMinHeap) Push(x interface{}) {
	s := x.(struct {
		name string
		val  int
	})
	h.nameIndex[s.name] = len(h.q)
	h.q = append(h.q, s)
}

func (h *namedMinHeap) Pop() interface{} {
	old := h.q
	n := len(old)
	x := old[n-1]
	h.q = old[:n-1]
	delete(h.nameIndex, x.name)
	return x
}
