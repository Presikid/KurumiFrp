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
	wl = &WebsocketListener{
		ln:       ln,
		acceptCh: make(chan net.Conn),
		done:     make(chan struct{}),
	}

	muxer := http.NewServeMux()
	muxer.Handle(FrpWebsocketPath, websocket.Handler(func(c *websocket.Conn) {
		// The tunnel payload is a raw byte stream (yamux), not UTF-8 text.
		// Send it as binary frames; otherwise RFC 6455-compliant intermediaries
		// (e.g. API gateways/reverse proxies) UTF-8-validate the default text
		// frames and close the connection on invalid bytes.
		c.PayloadType = websocket.BinaryFrame
		notifyCh := make(chan struct{})
		conn := WrapCloseNotifyConn(c, func(_ error) {
			close(notifyCh)
		})
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
