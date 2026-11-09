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

package server

import "github.com/fatedier/frp/server/traffic"

// The built-in counter is always private to a Service. An optional observer
// can independently forward deltas to its owner's accounting system.
type serviceTrafficRecorder struct {
	counter  *traffic.ServerCounter
	observer TrafficRecorder
}

func (r *serviceTrafficRecorder) RecordTraffic(name, proxyType string, received, sent int64) {
	r.counter.RecordTraffic(name, proxyType, received, sent)
	if r.observer != nil {
		r.observer.RecordTraffic(name, proxyType, received, sent)
	}
}
