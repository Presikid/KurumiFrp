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

package proxy

import (
	"io"
	"net"
)

// recordTraffic is the only entry point for forwarding byte counts. Both the
// frp collectors and the optional recorder belong to this service; the old
// connection-close and UDP queue accounting must not add these bytes again.
func (pxy *BaseProxy) recordTraffic(received, sent int64) {
	if received <= 0 && sent <= 0 {
		return
	}
	name, proxyType := pxy.name, pxy.configurer.GetBaseConfig().Type
	if pxy.serverMetrics != nil {
		if received > 0 {
			pxy.serverMetrics.AddTrafficIn(name, proxyType, received)
		}
		if sent > 0 {
			pxy.serverMetrics.AddTrafficOut(name, proxyType, sent)
		}
	}
	if pxy.rc != nil && pxy.rc.TrafficRecorder != nil {
		pxy.rc.TrafficRecorder.RecordTraffic(name, proxyType, received, sent)
	}
}

// observeTraffic wraps the plaintext proxy side of a forwarding connection.
// Writes towards the proxy are recorded immediately. HTTP also counts reads
// from the proxy, since the reverse proxy owns the user-side connection.
func (pxy *BaseProxy) observeTraffic(conn io.ReadWriteCloser, countRead bool) io.ReadWriteCloser {
	wrapped := &trafficConn{ReadWriteCloser: conn, record: pxy.recordTraffic, countRead: countRead}
	reader, canRead := conn.(closeReader)
	writer, canWrite := conn.(closeWriter)
	switch {
	case canRead && canWrite:
		return struct {
			io.ReadWriteCloser
			closeReader
			closeWriter
		}{wrapped, reader, writer}
	case canRead:
		return struct {
			io.ReadWriteCloser
			closeReader
		}{wrapped, reader}
	case canWrite:
		return struct {
			io.ReadWriteCloser
			closeWriter
		}{wrapped, writer}
	default:
		return wrapped
	}
}

// For non-HTTP streams, record only bytes actually written towards the user.
func (pxy *BaseProxy) observeUserWrites(conn net.Conn) net.Conn {
	wrapped := &trafficUserConn{Conn: conn, record: pxy.recordTraffic}
	reader, canRead := conn.(closeReader)
	writer, canWrite := conn.(closeWriter)
	switch {
	case canRead && canWrite:
		return struct {
			net.Conn
			closeReader
			closeWriter
		}{wrapped, reader, writer}
	case canRead:
		return struct {
			net.Conn
			closeReader
		}{wrapped, reader}
	case canWrite:
		return struct {
			net.Conn
			closeWriter
		}{wrapped, writer}
	default:
		return wrapped
	}
}

// Keep the underlying half-close behavior while counting writes through the
// wrapper. Do not expose io.Copy fast paths which would bypass those writes.
type closeReader interface{ CloseRead() error }
type closeWriter interface{ CloseWrite() error }

type trafficConn struct {
	io.ReadWriteCloser
	record    func(received, sent int64)
	countRead bool
}

func (c *trafficConn) Read(p []byte) (int, error) {
	n, err := c.ReadWriteCloser.Read(p)
	if n > 0 && c.countRead {
		c.record(0, int64(n))
	}
	return n, err
}

func (c *trafficConn) Write(p []byte) (int, error) {
	n, err := c.ReadWriteCloser.Write(p)
	if n > 0 {
		c.record(int64(n), 0)
	}
	return n, err
}

type trafficUserConn struct {
	net.Conn
	record func(received, sent int64)
}

func (c *trafficUserConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 {
		c.record(0, int64(n))
	}
	return n, err
}
