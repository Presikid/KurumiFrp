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

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	netpkg "github.com/fatedier/frp/pkg/util/net"
)

const (
	wssUpgradePrefix = "GET " + netpkg.FrpWebsocketPath
	wssUpgradeTimeout = 10 * time.Second
	// Includes connections waiting for Upgrade and established WSS transports.
	maxWSSConnections = 512
)

// wssTLSConn replays the bytes consumed to distinguish WSS from ordinary frp
// after TLS. Embedding tls.Conn retains its normal TLS methods. It must only be
// used by the non-managed transport path; WebSocket does not preserve the peer
// certificate identity required by the managed-identity path.
type wssTLSConn struct {
	*tls.Conn
	mu     sync.Mutex
	prefix []byte
}

func (c *wssTLSConn) Read(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.prefix) != 0 {
		n := copy(p, c.prefix)
		c.prefix = c.prefix[n:]
		return n, nil
	}
	return c.Conn.Read(p)
}

// sniffWSS completes the TLS handshake and examines only enough plaintext to
// distinguish an Upgrade request from frp. It returns every consumed byte to
// the caller via the returned connection. On error the caller retains conn and
// must close it. Call this in a scoped connection worker, never the accept loop.
// The fresh connection must not have an outstanding caller-owned deadline.
func sniffWSS(conn net.Conn, timeout time.Duration) (*wssTLSConn, bool, error) {
	secure, ok := conn.(*tls.Conn)
	if !ok || secure == nil {
		return nil, false, fmt.Errorf("WSS detection requires a TLS connection")
	}
	if timeout <= 0 {
		return nil, false, fmt.Errorf("WSS detection requires a positive timeout")
	}
	// TLS handshake may need to write as well as read. One deadline bounds both
	// it and prefix detection; shutdown also closes the tracked underlying socket.
	if err := secure.SetDeadline(time.Now().Add(timeout)); err != nil {
		return nil, false, err
	}
	var prefix [len(wssUpgradePrefix)]byte
	for n := range prefix {
		if _, err := io.ReadFull(secure, prefix[n:n+1]); err != nil {
			return nil, false, err
		}
		if prefix[n] != wssUpgradePrefix[n] {
			if err := secure.SetDeadline(time.Time{}); err != nil {
				return nil, false, err
			}
			return &wssTLSConn{Conn: secure, prefix: append([]byte(nil), prefix[:n+1]...)}, false, nil
		}
	}
	if err := secure.SetDeadline(time.Time{}); err != nil {
		return nil, false, err
	}
	return &wssTLSConn{Conn: secure, prefix: append([]byte(nil), prefix[:]...)}, true, nil
}

// wssBridge feeds already-secured Upgrade requests to the same HTTP/WebSocket
// implementation as plain WebSocket. The internal queue holds at most 128
// connections; the bridge also bounds connections after the HTTP server accepts
// them. Its owner must close it during constructor rollback and service shutdown.
type wssBridge struct {
	input     *netpkg.InternalListener
	listener  *netpkg.WebsocketListener
	mu        sync.Mutex
	closed    bool
	conns     map[*wssBridgeConn]struct{}
	closeOnce sync.Once
	closeErr  error
}

func newWSSBridge(ctx context.Context) (*wssBridge, error) {
	if ctx == nil {
		return nil, fmt.Errorf("WSS bridge requires a service context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	input := netpkg.NewInternalListener()
	listener, err := netpkg.NewWebsocketListenerWithPeer(ctx, input)
	if err != nil {
		_ = input.Close()
		return nil, err
	}
	return &wssBridge{input: input, listener: listener, conns: make(map[*wssBridgeConn]struct{})}, nil
}

// offer always consumes conn: it either transfers it to the Upgrade server or
// closes it on saturation/shutdown. It must be called outside service locks.
func (b *wssBridge) offer(conn *wssTLSConn) bool {
	if conn == nil {
		return false
	}
	owned := &wssBridgeConn{wssTLSConn: conn, owner: b}
	b.mu.Lock()
	if b.closed || len(b.conns) >= maxWSSConnections {
		b.mu.Unlock()
		_ = owned.Close()
		return false
	}
	b.conns[owned] = struct{}{}
	owned.startUpgradeTimer()
	b.mu.Unlock()
	accepted, err := b.input.TryPutConn(owned)
	if err != nil || !accepted {
		_ = owned.Close()
		return false
	}
	return true
}

// Accept returns an upgraded WebSocket whose outer TLS was already verified.
// It must enter normal external frp authentication, without a second TLS check
// and without treating the connection as an internal SSH gateway connection.
func (b *wssBridge) Accept() (net.Conn, error) { return b.listener.Accept() }

func (b *wssBridge) Addr() net.Addr { return b.listener.Addr() }

func (b *wssBridge) Close() error {
	b.closeOnce.Do(func() {
		b.mu.Lock()
		b.closed = true
		conns := make([]*wssBridgeConn, 0, len(b.conns))
		for conn := range b.conns {
			conns = append(conns, conn)
		}
		b.mu.Unlock()
		b.closeErr = b.listener.Close()
		for _, conn := range conns {
			_ = conn.Close()
		}
	})
	return b.closeErr
}

type wssBridgeConn struct {
	*wssTLSConn
	owner *wssBridge
	once  sync.Once
	err   error

	upgradeMu       sync.Mutex
	upgradeTimer    *time.Timer
	upgradeDeadline time.Time
	upgraded        bool
	timedOut        bool
	closed          bool
}

// The timer covers queueing, HTTP headers and the 101 write, including after
// Hijack clears socket deadlines. It is started before the bridge publishes the
// connection. Expiry closes outside the state lock and interrupts blocked I/O.
func (c *wssBridgeConn) startUpgradeTimer() {
	c.upgradeMu.Lock()
	defer c.upgradeMu.Unlock()
	c.upgradeDeadline = time.Now().Add(wssUpgradeTimeout)
	c.upgradeTimer = time.AfterFunc(time.Until(c.upgradeDeadline), c.expireUpgrade)
}

func (c *wssBridgeConn) expireUpgrade() {
	c.upgradeMu.Lock()
	if c.upgraded || c.closed {
		c.upgradeMu.Unlock()
		return
	}
	c.timedOut = true
	c.upgradeMu.Unlock()
	_ = c.Close()
}

// CompleteWebsocketUpgrade is called by the Upgrade handler only after its 101
// response has been written. A fired timer or expired deadline rejects handoff,
// even if the timer callback has not yet acquired upgradeMu.
func (c *wssBridgeConn) CompleteWebsocketUpgrade() bool {
	c.upgradeMu.Lock()
	defer c.upgradeMu.Unlock()
	if c.closed || c.timedOut || c.upgraded || c.upgradeTimer == nil {
		return false
	}
	if !time.Now().Before(c.upgradeDeadline) {
		c.timedOut = true
		c.upgradeTimer.Stop()
		return false
	}
	if !c.upgradeTimer.Stop() {
		c.timedOut = true
		return false
	}
	c.upgraded = true
	return true
}

func (c *wssBridgeConn) Close() error {
	c.once.Do(func() {
		c.upgradeMu.Lock()
		c.closed = true
		if c.upgradeTimer != nil {
			c.upgradeTimer.Stop()
		}
		c.upgradeMu.Unlock()
		// Close the tracked transport directly: tls.Conn.Close may reset its
		// write deadline while sending close_notify and delay shutdown. A normal
		// WebSocket close still attempts its own close frame before reaching here.
		c.err = c.wssTLSConn.Conn.NetConn().Close()
		c.owner.mu.Lock()
		delete(c.owner.conns, c)
		c.owner.mu.Unlock()
	})
	return c.err
}
