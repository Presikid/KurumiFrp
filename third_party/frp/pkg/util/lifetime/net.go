// SPDX-License-Identifier: Apache-2.0
// Modified by KurumiFRP: Connection lifetime tracking for the KurumiFRP embedded engine.

package lifetime

import (
	"context"
	"net"
	"sync"
)

// TrackConn closes the connection when its scope stops. Normal Close removes
// the registration, including when ownership passes between handlers.
func TrackConn(ctx context.Context, conn net.Conn) (net.Conn, bool) {
	if fromContext(ctx) == nil {
		return conn, true
	}
	release, ok := Track(ctx, conn)
	if !ok {
		return nil, false
	}
	tracked := &trackedConn{Conn: conn, release: release}
	reader, canRead := conn.(closeReader)
	writer, canWrite := conn.(closeWriter)
	switch {
	case canRead && canWrite:
		return struct {
			*trackedConn
			closeReader
			closeWriter
		}{tracked, reader, writer}, true
	case canRead:
		return struct {
			*trackedConn
			closeReader
		}{tracked, reader}, true
	case canWrite:
		return struct {
			*trackedConn
			closeWriter
		}{tracked, writer}, true
	default:
		return tracked, true
	}
}

type closeReader interface{ CloseRead() error }
type closeWriter interface{ CloseWrite() error }

type trackedConn struct {
	net.Conn
	once    sync.Once
	release func()
	err     error
}

func (c *trackedConn) Close() error {
	c.once.Do(func() {
		c.err = c.Conn.Close()
		c.release()
	})
	return c.err
}

// Preserve visitor framing metadata through a tracked wrapper.
func (c *trackedConn) WireProtocol() string {
	if inner, ok := c.Conn.(interface{ WireProtocol() string }); ok {
		return inner.WireProtocol()
	}
	return ""
}

func (c *trackedConn) UDPPacketCodec() string {
	if inner, ok := c.Conn.(interface{ UDPPacketCodec() string }); ok {
		return inner.UDPPacketCodec()
	}
	return ""
}
