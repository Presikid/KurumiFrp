package server

import (
	"crypto/tls"
	"errors"
	"sync"
)

// A managed direct TLS connection shares its first physical close result among
// the dispatcher, forwarding owner and revoker. It deliberately skips TLS
// close_notify so cleanup cannot wait for an unresponsive peer.
type managedTLSConn struct {
	*tls.Conn
	once sync.Once
	err  error
}

func (c *managedTLSConn) Close() error {
	c.once.Do(func() {
		defer func() {
			if recover() != nil {
				c.err = errors.New("managed TLS close panicked")
			}
		}()
		c.err = c.Conn.NetConn().Close()
	})
	return c.err
}
