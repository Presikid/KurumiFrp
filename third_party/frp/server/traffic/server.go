// Copyright 2026 The KurumiFRP Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package traffic provides counters owned by individual runtime instances.
package traffic

import (
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	maxServerProxyEntries   = 1024
	maxServerProxyNameBytes = 256
	maxServerProxyTypeBytes = 32
)

// ServerProxy contains bytes recorded at the forwarding boundary for one proxy
// name and type. The forwarding implementation defines the exact byte scope.
type ServerProxy struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Received uint64 `json:"received"`
	Sent     uint64 `json:"sent"`
}

// ServerSnapshot is a point-in-time view of a server instance's counters.
type ServerSnapshot struct {
	Received         uint64        `json:"received"`
	Sent             uint64        `json:"sent"`
	Proxies          []ServerProxy `json:"proxies"`
	ProxiesTruncated bool          `json:"proxiesTruncated"`
	// ResetEpoch and its validity are copied under the same lock as totals.
	ResetEpoch      uint64 `json:"-"`
	ResetEpochValid bool   `json:"-"`
	SampledAt       time.Time `json:"-"`
}

type serverProxyKey struct {
	name, proxyType string
}

// ServerCounter is an in-memory counter for one server instance. Its zero
// value is ready to use; create a separate counter for each instance. Counts
// persist until the owner replaces the counter, including across proxy reconnects.
// Proxy details are bounded; totals continue counting when details are omitted.
type ServerCounter struct {
	mu               sync.RWMutex
	received         uint64
	sent             uint64
	proxies          map[serverProxyKey]ServerProxy
	proxiesTruncated bool
	resetEpoch       uint64
	resetExhausted   bool
}

// RecordTraffic records positive deltas. It can be called concurrently by the
// forwarding goroutines. Received is towards the proxy, sent towards its user.
func (c *ServerCounter) RecordTraffic(name, proxyType string, received, sent int64) {
	if received <= 0 && sent <= 0 {
		return
	}
	var in, out uint64
	if received > 0 {
		in = uint64(received)
	}
	if sent > 0 {
		out = uint64(sent)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.received = addSaturating(c.received, in)
	c.sent = addSaturating(c.sent, out)
	if len(name) > maxServerProxyNameBytes || len(proxyType) > maxServerProxyTypeBytes {
		c.proxiesTruncated = true
		return
	}
	if c.proxies == nil {
		c.proxies = make(map[serverProxyKey]ServerProxy)
	}
	key := serverProxyKey{name: name, proxyType: proxyType}
	proxy, exists := c.proxies[key]
	if !exists {
		if len(c.proxies) >= maxServerProxyEntries {
			c.proxiesTruncated = true
			return
		}
		// Keep only these bounded strings, not a potentially larger backing buffer.
		key.name, key.proxyType = strings.Clone(name), strings.Clone(proxyType)
		proxy.Name, proxy.Type = key.name, key.proxyType
	} else {
		key.name, key.proxyType = proxy.Name, proxy.Type
	}
	proxy.Received = addSaturating(proxy.Received, in)
	proxy.Sent = addSaturating(proxy.Sent, out)
	c.proxies[key] = proxy
}

func addSaturating(a, b uint64) uint64 {
	if math.MaxUint64-a < b {
		return math.MaxUint64
	}
	return a + b
}

// Snapshot copies counters atomically with respect to RecordTraffic. The
// returned slice belongs to the caller and is ordered by proxy name and type.
func (c *ServerCounter) Snapshot() ServerSnapshot {
	c.mu.RLock()
	snapshot := ServerSnapshot{
		Received:         c.received,
		Sent:             c.sent,
		Proxies:          make([]ServerProxy, 0, len(c.proxies)),
		ProxiesTruncated: c.proxiesTruncated,
		ResetEpoch:       c.resetEpoch,
		ResetEpochValid:  !c.resetExhausted,
		SampledAt:        time.Now().UTC(),
	}
	for _, proxy := range c.proxies {
		snapshot.Proxies = append(snapshot.Proxies, proxy)
	}
	c.mu.RUnlock()
	sort.Slice(snapshot.Proxies, func(i, j int) bool {
		if snapshot.Proxies[i].Name == snapshot.Proxies[j].Name {
			return snapshot.Proxies[i].Type < snapshot.Proxies[j].Type
		}
		return snapshot.Proxies[i].Name < snapshot.Proxies[j].Name
	})
	return snapshot
}

// Reset starts a new collection interval for the instance. Traffic arriving
// concurrently after the reset is included in the new interval.
func (c *ServerCounter) Reset() {
	c.mu.Lock()
	if c.resetEpoch == math.MaxUint64 {
		c.resetExhausted = true
	} else {
		c.resetEpoch++
	}
	c.received, c.sent = 0, 0
	c.proxies = nil
	c.proxiesTruncated = false
	c.mu.Unlock()
}
