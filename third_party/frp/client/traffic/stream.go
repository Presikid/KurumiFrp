// SPDX-License-Identifier: Apache-2.0

package traffic

import (
	"io"
	"net"
)

// WrapStream measures bytes read from and written to the remote payload side
// of a TCP-like forwarding stream. Read is download and Write is upload, for
// both a proxy and a visitor. It counts the actual n returned, even when an I/O
// operation also returns an error. A nil counter leaves the stream untouched.
//
// Install this only after the frp handshake and after its encryption and
// compression wrappers. Install it once per business stream, before passing
// that stream to the proxy, plugin, or visitor forwarding code. In particular,
// do not wrap the control connection or the transport carrying UDP messages.
// The wrapper preserves net.Conn and CloseRead/CloseWrite when provided by the
// stream; it does not expose other optional interfaces of the underlying value.
func WrapStream(remote io.ReadWriteCloser, counter Counter) io.ReadWriteCloser {
	if counter == nil {
		return remote
	}
	stream := &meteredStream{ReadWriteCloser: remote, counter: counter}
	readCloser, canCloseRead := remote.(closeReader)
	writeCloser, canCloseWrite := remote.(closeWriter)
	if conn, ok := remote.(net.Conn); ok {
		wrapped := &meteredConn{Conn: conn, stream: stream}
		switch {
		case canCloseRead && canCloseWrite:
			return struct {
				net.Conn
				closeReader
				closeWriter
			}{wrapped, readCloser, writeCloser}
		case canCloseRead:
			return struct {
				net.Conn
				closeReader
			}{wrapped, readCloser}
		case canCloseWrite:
			return struct {
				net.Conn
				closeWriter
			}{wrapped, writeCloser}
		default:
			return wrapped
		}
	}
	switch {
	case canCloseRead && canCloseWrite:
		return struct {
			io.ReadWriteCloser
			closeReader
			closeWriter
		}{stream, readCloser, writeCloser}
	case canCloseRead:
		return struct {
			io.ReadWriteCloser
			closeReader
		}{stream, readCloser}
	case canCloseWrite:
		return struct {
			io.ReadWriteCloser
			closeWriter
		}{stream, writeCloser}
	default:
		return stream
	}
}

type closeReader interface{ CloseRead() error }
type closeWriter interface{ CloseWrite() error }

type meteredConn struct {
	net.Conn
	stream *meteredStream
}

func (c *meteredConn) Read(p []byte) (int, error)  { return c.stream.Read(p) }
func (c *meteredConn) Write(p []byte) (int, error) { return c.stream.Write(p) }

type meteredStream struct {
	io.ReadWriteCloser
	counter Counter
}

func (s *meteredStream) Read(p []byte) (int, error) {
	n, err := s.ReadWriteCloser.Read(p)
	if n > 0 {
		s.counter.AddDownload(uint64(n))
	}
	return n, err
}

func (s *meteredStream) Write(p []byte) (int, error) {
	n, err := s.ReadWriteCloser.Write(p)
	if n > 0 {
		s.counter.AddUpload(uint64(n))
	}
	return n, err
}
