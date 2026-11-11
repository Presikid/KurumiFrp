package net

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/net/websocket"

	"github.com/fatedier/frp/pkg/util/lifetime"
)

var ErrWebsocketListenerClosed = errors.New("websocket listener closed")

const (
	FrpWebsocketPath = "/~!frp"
)

type WebsocketListener struct {
	ln       net.Listener
	acceptCh chan net.Conn
	done     chan struct{}

	server *http.Server

	closeOnce sync.Once
}

// NewWebsocketListener to handle websocket connections
// ln: tcp listener for websocket connections
func NewWebsocketListener(ln net.Listener) (wl *WebsocketListener) {
	wl, _ = NewWebsocketListenerWithContext(context.Background(), ln)
	return wl
}

// NewWebsocketListenerWithContext associates its serving worker with one service.
func NewWebsocketListenerWithContext(ctx context.Context, ln net.Listener) (wl *WebsocketListener, err error) {
	return newWebsocketListenerWithContext(ctx, ln, false)
}

// NewWebsocketListenerWithPeer retains the addresses of the accepted transport
// connection after Upgrade. The addresses are captured by http.Server from its
// listener, never from Origin or another request header.
func NewWebsocketListenerWithPeer(ctx context.Context, ln net.Listener) (wl *WebsocketListener, err error) {
	return newWebsocketListenerWithContext(ctx, ln, true)
}

type websocketPeerKey struct{}

type websocketPeer struct {
	local  net.Addr
	remote net.Addr
	transport net.Conn
}

type websocketUpgradeCompleter interface {
	CompleteWebsocketUpgrade() bool
}

type websocketPeerConn struct {
	net.Conn
	peer websocketPeer
}

func (c *websocketPeerConn) LocalAddr() net.Addr  { return c.peer.local }
func (c *websocketPeerConn) RemoteAddr() net.Addr { return c.peer.remote }

func newWebsocketListenerWithContext(ctx context.Context, ln net.Listener, preservePeer bool) (wl *WebsocketListener, err error) {
	wl = &WebsocketListener{
		ln:       ln,
		acceptCh: make(chan net.Conn),
		done:     make(chan struct{}),
	}

	muxer := http.NewServeMux()
	muxer.Handle(FrpWebsocketPath, websocket.Handler(func(c *websocket.Conn) {
		done, ok := lifetime.Enter(ctx)
		if !ok {
			_ = c.Close()
			return
		}
		defer done()
		defer lifetime.Recover(ctx)
		defer c.Close()
		// The tunnel payload is a raw byte stream (yamux), not UTF-8 text.
		// Send it as binary frames; otherwise RFC 6455-compliant intermediaries
		// (e.g. API gateways/reverse proxies) UTF-8-validate the default text
		// frames and close the connection on invalid bytes.
		c.PayloadType = websocket.BinaryFrame
		notifyCh := make(chan struct{})
		var conn net.Conn = WrapCloseNotifyConn(c, func(_ error) {
			close(notifyCh)
		})
		if preservePeer {
			request := c.Request()
			if request == nil {
				_ = conn.SetDeadline(time.Now())
				_ = conn.Close()
				return
			}
			peer, ok := request.Context().Value(websocketPeerKey{}).(websocketPeer)
			completer, canComplete := peer.transport.(websocketUpgradeCompleter)
			if !ok || peer.local == nil || peer.remote == nil || !canComplete || !completer.CompleteWebsocketUpgrade() {
				if peer.transport != nil {
					_ = peer.transport.SetDeadline(time.Now())
					_ = peer.transport.Close()
				}
				_ = conn.SetDeadline(time.Now())
				_ = conn.Close()
				return
			}
			conn = &websocketPeerConn{Conn: conn, peer: peer}
		}
		select {
		case <-wl.done:
			conn.Close()
			return
		case wl.acceptCh <- conn:
			// A ready handoff may win when Close races with it. Once delivered,
			// the connection is owned by the caller of Accept.
		}
		<-notifyCh
	}))

	wl.server = &http.Server{
		Addr:              ln.Addr().String(),
		Handler:           muxer,
		ReadHeaderTimeout: 60 * time.Second,
	}
	if preservePeer {
		wl.server.ConnContext = func(ctx context.Context, conn net.Conn) context.Context {
			return context.WithValue(ctx, websocketPeerKey{}, websocketPeer{local: conn.LocalAddr(), remote: conn.RemoteAddr(), transport: conn})
		}
	}

	if !lifetime.Go(ctx, func() {
		defer wl.Close()
		_ = wl.server.Serve(ln)
	}) {
		_ = wl.Close()
		return nil, net.ErrClosed
	}
	return wl, nil
}

func (p *WebsocketListener) Accept() (net.Conn, error) {
	select {
	case <-p.done:
		return nil, ErrWebsocketListenerClosed
	default:
	}
	select {
	case <-p.done:
		return nil, ErrWebsocketListenerClosed
	case c := <-p.acceptCh:
		return c, nil
	}
}

func (p *WebsocketListener) Close() error {
	p.closeOnce.Do(func() { close(p.done) })
	err := p.server.Close()
	// Serve may not have registered ln when a concurrent shutdown begins.
	_ = p.ln.Close()
	return err
}

func (p *WebsocketListener) Addr() net.Addr {
	return p.ln.Addr()
}
