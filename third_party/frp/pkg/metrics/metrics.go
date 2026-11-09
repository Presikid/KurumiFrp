// Copyright 2023 The frp Authors
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

package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/fatedier/frp/pkg/metrics/aggregate"
	"github.com/fatedier/frp/pkg/metrics/mem"
	prommetrics "github.com/fatedier/frp/pkg/metrics/prometheus"
	servermetrics "github.com/fatedier/frp/server/metrics"
)

// Server owns all metrics state for one server instance.
type Server struct {
	servermetrics.ServerMetrics
	stats   *mem.ServerMetrics
	handler http.Handler
}

// NewServer creates only the collectors required by the server's configuration.
func NewServer(enableMem, enablePrometheus bool) *Server {
	s := &Server{}
	var backends []servermetrics.ServerMetrics
	if enableMem {
		s.stats = mem.NewServerMetrics()
		backends = append(backends, s.stats)
	}
	if enablePrometheus {
		registry := prometheus.NewRegistry()
		// Runtime and process metrics describe the shared host process, but the
		// collectors and the frp metrics registry are owned by this server.
		registry.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
		backends = append(backends, prommetrics.NewServerMetrics(registry))
		s.handler = promhttp.InstrumentMetricHandler(registry, promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	}
	s.ServerMetrics = aggregate.NewServerMetrics(backends...)
	return s
}

// StatsCollector returns nil when in-memory metrics are disabled.
func (s *Server) StatsCollector() mem.Collector {
	if s.stats == nil {
		return nil
	}
	return s.stats
}

// Handler serves only this server's registry.
func (s *Server) Handler() http.Handler {
	if s.handler == nil {
		return http.NotFoundHandler()
	}
	return s.handler
}

// Close releases the background work owned by this server's collectors.
func (s *Server) Close() {
	if s.stats != nil {
		s.stats.Close()
	}
}
