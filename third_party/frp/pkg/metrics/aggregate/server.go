// Copyright 2020 fatedier, fatedier@gmail.com
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

package aggregate

import (
	"github.com/fatedier/frp/server/metrics"
)

// NewServerMetrics creates a fixed set of collectors owned by one server.
func NewServerMetrics(collectors ...metrics.ServerMetrics) metrics.ServerMetrics {
	return &serverMetrics{ms: append([]metrics.ServerMetrics(nil), collectors...)}
}

type serverMetrics struct {
	ms []metrics.ServerMetrics
}

func (m *serverMetrics) NewClient() {
	for _, v := range m.ms {
		v.NewClient()
	}
}

func (m *serverMetrics) CloseClient() {
	for _, v := range m.ms {
		v.CloseClient()
	}
}

func (m *serverMetrics) NewProxy(name string, proxyType string, user string, clientID string) {
	for _, v := range m.ms {
		v.NewProxy(name, proxyType, user, clientID)
	}
}

func (m *serverMetrics) CloseProxy(name string, proxyType string) {
	for _, v := range m.ms {
		v.CloseProxy(name, proxyType)
	}
}

func (m *serverMetrics) OpenConnection(name string, proxyType string) {
	for _, v := range m.ms {
		v.OpenConnection(name, proxyType)
	}
}

func (m *serverMetrics) CloseConnection(name string, proxyType string) {
	for _, v := range m.ms {
		v.CloseConnection(name, proxyType)
	}
}

func (m *serverMetrics) AddTrafficIn(name string, proxyType string, trafficBytes int64) {
	for _, v := range m.ms {
		v.AddTrafficIn(name, proxyType, trafficBytes)
	}
}

func (m *serverMetrics) AddTrafficOut(name string, proxyType string, trafficBytes int64) {
	for _, v := range m.ms {
		v.AddTrafficOut(name, proxyType, trafficBytes)
	}
}
