// Modified by KurumiFRP contributors: route and protect instance logs.

// Copyright 2019 fatedier, fatedier@gmail.com
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

// Modified by KurumiFRP: isolate metrics per server instance.

package mem

import (
	"context"
	"sync"
	"time"

	"k8s.io/utils/clock"

	"github.com/fatedier/frp/pkg/util/lifetime"
	"github.com/fatedier/frp/pkg/util/log"
	"github.com/fatedier/frp/pkg/util/metric"
)

// ServerMetrics collects the statistics for one server instance.
type ServerMetrics struct {
	info     *ServerStatistics
	clock    clock.WithTicker
	mu       sync.Mutex
	stopCh   chan struct{}
	doneCh   chan struct{}
	stopOnce sync.Once
}

// NewServerMetrics starts a collector and its background pruning worker.
func NewServerMetrics() *ServerMetrics {
	return NewServerMetricsWithContext(context.Background())
}

// NewServerMetricsWithContext reports a pruning-worker panic to the owning
// service scope when one is present. Close still owns stopping and joining the
// worker; without an instance panic handler, panics retain their normal behavior.
func NewServerMetricsWithContext(ctx context.Context) *ServerMetrics {
	m := newServerMetricsWithClock(clock.RealClock{})
	go func() {
		defer close(m.doneCh)
		defer lifetime.Recover(ctx)
		m.runUntil(m.stopCh)
	}()
	return m
}

func newServerMetricsWithClock(clk clock.WithTicker) *ServerMetrics {
	if clk == nil {
		clk = clock.RealClock{}
	}
	return &ServerMetrics{
		clock:  clk,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
		info: &ServerStatistics{
			TotalTrafficIn:  metric.NewDateCounter(ReserveDays),
			TotalTrafficOut: metric.NewDateCounter(ReserveDays),
			CurConns:        metric.NewCounter(),

			ClientCounts:    metric.NewCounter(),
			ProxyTypeCounts: make(map[string]metric.Counter),

			ProxyStatistics: make(map[string]*ProxyStatistics),
		},
	}
}

// Close stops the collector's pruning worker. It is safe to call more than once.
func (m *ServerMetrics) Close() {
	m.stopOnce.Do(func() {
		close(m.stopCh)
		<-m.doneCh
	})
}

func (m *ServerMetrics) runUntil(stopCh <-chan struct{}) {
	ticker := m.clock.NewTicker(12 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C():
			start := m.clock.Now()
			count, total := m.clearUselessInfo(time.Duration(7*24) * time.Hour)
			log.Debugf("clear useless proxy statistics data count %d/%d, cost %v", count, total, m.clock.Since(start))
		case <-stopCh:
			return
		}
	}
}

func (m *ServerMetrics) clearUselessInfo(continuousOfflineDuration time.Duration) (int, int) {
	count := 0
	total := 0
	// To check if there are any proxies that have been closed for more than continuousOfflineDuration and remove them.
	m.mu.Lock()
	defer m.mu.Unlock()
	total = len(m.info.ProxyStatistics)
	for name, data := range m.info.ProxyStatistics {
		if m.shouldClearProxyStats(data, continuousOfflineDuration) {
			delete(m.info.ProxyStatistics, name)
			count++
			log.Tracef("clear inactive proxy statistics")
		}
	}
	return count, total
}

func (m *ServerMetrics) shouldClearProxyStats(data *ProxyStatistics, continuousOfflineDuration time.Duration) bool {
	return !data.LastCloseTime.IsZero() &&
		data.LastStartTime.Before(data.LastCloseTime) &&
		m.clock.Since(data.LastCloseTime) > continuousOfflineDuration
}

func (m *ServerMetrics) ClearOfflineProxies() (int, int) {
	return m.clearUselessInfo(0)
}

func (m *ServerMetrics) PruneOfflineProxies() (int, int) {
	return m.clearUselessInfo(0)
}

func (m *ServerMetrics) NewClient() {
	m.info.ClientCounts.Inc(1)
}

func (m *ServerMetrics) CloseClient() {
	m.info.ClientCounts.Dec(1)
}

func (m *ServerMetrics) NewProxy(name string, proxyType string, user string, clientID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	counter, ok := m.info.ProxyTypeCounts[proxyType]
	if !ok {
		counter = metric.NewCounter()
	}
	counter.Inc(1)
	m.info.ProxyTypeCounts[proxyType] = counter

	proxyStats, ok := m.info.ProxyStatistics[name]
	if !ok || proxyStats.ProxyType != proxyType {
		proxyStats = &ProxyStatistics{
			Name:       name,
			ProxyType:  proxyType,
			CurConns:   metric.NewCounter(),
			TrafficIn:  metric.NewDateCounter(ReserveDays),
			TrafficOut: metric.NewDateCounter(ReserveDays),
		}
		m.info.ProxyStatistics[name] = proxyStats
	}
	proxyStats.User = user
	proxyStats.ClientID = clientID
	proxyStats.LastStartTime = m.clock.Now()
}

func (m *ServerMetrics) CloseProxy(name string, proxyType string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if counter, ok := m.info.ProxyTypeCounts[proxyType]; ok {
		counter.Dec(1)
	}
	if proxyStats, ok := m.info.ProxyStatistics[name]; ok && proxyStats.ProxyType == proxyType {
		proxyStats.LastCloseTime = m.clock.Now()
	}
}

func (m *ServerMetrics) OpenConnection(name string, proxyType string) {
	m.info.CurConns.Inc(1)

	m.mu.Lock()
	defer m.mu.Unlock()
	proxyStats, ok := m.info.ProxyStatistics[name]
	if ok && proxyStats.ProxyType == proxyType {
		proxyStats.CurConns.Inc(1)
	}
}

func (m *ServerMetrics) CloseConnection(name string, proxyType string) {
	m.info.CurConns.Dec(1)

	m.mu.Lock()
	defer m.mu.Unlock()
	proxyStats, ok := m.info.ProxyStatistics[name]
	if ok && proxyStats.ProxyType == proxyType {
		proxyStats.CurConns.Dec(1)
	}
}

func (m *ServerMetrics) AddTrafficIn(name string, proxyType string, trafficBytes int64) {
	m.info.TotalTrafficIn.Inc(trafficBytes)

	m.mu.Lock()
	defer m.mu.Unlock()

	proxyStats, ok := m.info.ProxyStatistics[name]
	if ok && proxyStats.ProxyType == proxyType {
		proxyStats.TrafficIn.Inc(trafficBytes)
	}
}

func (m *ServerMetrics) AddTrafficOut(name string, proxyType string, trafficBytes int64) {
	m.info.TotalTrafficOut.Inc(trafficBytes)

	m.mu.Lock()
	defer m.mu.Unlock()

	proxyStats, ok := m.info.ProxyStatistics[name]
	if ok && proxyStats.ProxyType == proxyType {
		proxyStats.TrafficOut.Inc(trafficBytes)
	}
}

// Get stats data api.

func (m *ServerMetrics) GetServer() *ServerStats {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := &ServerStats{
		TotalTrafficIn:  m.info.TotalTrafficIn.TodayCount(),
		TotalTrafficOut: m.info.TotalTrafficOut.TodayCount(),
		CurConns:        int64(m.info.CurConns.Count()),
		ClientCounts:    int64(m.info.ClientCounts.Count()),
		ProxyTypeCounts: make(map[string]int64),
	}
	for k, v := range m.info.ProxyTypeCounts {
		s.ProxyTypeCounts[k] = int64(v.Count())
	}
	return s
}

func toProxyStats(name string, proxyStats *ProxyStatistics) *ProxyStats {
	ps := &ProxyStats{
		Name:            name,
		Type:            proxyStats.ProxyType,
		User:            proxyStats.User,
		ClientID:        proxyStats.ClientID,
		TodayTrafficIn:  proxyStats.TrafficIn.TodayCount(),
		TodayTrafficOut: proxyStats.TrafficOut.TodayCount(),
		CurConns:        int64(proxyStats.CurConns.Count()),
	}
	if !proxyStats.LastStartTime.IsZero() {
		ps.LastStartTime = proxyStats.LastStartTime.Format("01-02 15:04:05")
		ps.LastStartAt = proxyStats.LastStartTime.Unix()
	}
	if !proxyStats.LastCloseTime.IsZero() {
		ps.LastCloseTime = proxyStats.LastCloseTime.Format("01-02 15:04:05")
		ps.LastCloseAt = proxyStats.LastCloseTime.Unix()
	}
	return ps
}

func (m *ServerMetrics) GetProxiesByType(proxyType string) []*ProxyStats {
	res := make([]*ProxyStats, 0)
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, proxyStats := range m.info.ProxyStatistics {
		if proxyStats.ProxyType != proxyType {
			continue
		}
		res = append(res, toProxyStats(name, proxyStats))
	}
	return res
}

func (m *ServerMetrics) GetProxiesByTypeAndName(proxyType string, proxyName string) (res *ProxyStats) {
	m.mu.Lock()
	defer m.mu.Unlock()

	proxyStats, ok := m.info.ProxyStatistics[proxyName]
	if ok && proxyStats.ProxyType == proxyType {
		res = toProxyStats(proxyName, proxyStats)
	}
	return
}

func (m *ServerMetrics) GetProxyByName(proxyName string) (res *ProxyStats) {
	m.mu.Lock()
	defer m.mu.Unlock()

	proxyStats, ok := m.info.ProxyStatistics[proxyName]
	if ok {
		res = toProxyStats(proxyName, proxyStats)
	}
	return
}

func (m *ServerMetrics) GetProxyTraffic(name string) (res *ProxyTrafficInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()

	proxyStats, ok := m.info.ProxyStatistics[name]
	if ok {
		res = &ProxyTrafficInfo{
			Name: name,
		}
		res.TrafficIn = proxyStats.TrafficIn.GetLastDaysCount(ReserveDays)
		res.TrafficOut = proxyStats.TrafficOut.GetLastDaysCount(ReserveDays)
	}
	return
}
