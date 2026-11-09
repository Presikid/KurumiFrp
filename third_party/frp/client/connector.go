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

// Modified by KurumiFRP: support the restricted embedded client profile.

// Modified by KurumiFRP: join cancellation callbacks before releasing connector setup.

package client

import (
	"context"
	"crypto/tls"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	libnet "github.com/fatedier/golib/net"
	fmux "github.com/fatedier/yamux"
	quic "github.com/quic-go/quic-go"
	"github.com/samber/lo"

	v1 "github.com/fatedier/frp/pkg/config/v1"
	"github.com/fatedier/frp/pkg/msg"
	"github.com/fatedier/frp/pkg/proto/wire"
	"github.com/fatedier/frp/pkg/transport"
	netpkg "github.com/fatedier/frp/pkg/util/net"
	"github.com/fatedier/frp/pkg/util/xlog"
)

// Connector is an interface for establishing connections to the server.
type Connector interface {
	Open() error
	Connect() (net.Conn, error)
	Close() error
}

type MessageConnector interface {
	Connect() (*msg.Conn, error)
	Close() error
}

type messageConnector struct {
	connector    Connector
	wireProtocol string
}

func newMessageConnector(connector Connector, wireProtocol string) *messageConnector {
	return &messageConnector{
		connector:    connector,
		wireProtocol: wireProtocol,
	}
}

func (c *messageConnector) Connect() (*msg.Conn, error) {
	conn, err := c.connector.Connect()
	if err != nil {
		return nil, err
	}
	if err = wire.WriteMagicIfV2(conn, c.wireProtocol); err != nil {
		conn.Close()
		return nil, err
	}
	return msg.NewConn(conn, msg.NewReadWriter(conn, c.wireProtocol)), nil
}

func (c *messageConnector) Close() error {
	return c.connector.Close()
}

// defaultConnectorImpl is the default implementation of Connector for normal frpc.
type defaultConnectorImpl struct {
	ctx    context.Context
	cfg    *v1.ClientCommonConfig
	cancel context.CancelFunc
	mu     sync.Mutex
	closed bool
	conns  map[*trackedConnectorConn]struct{}

	muxSession *fmux.Session
	quicConn   *quic.Conn
	closeOnce  sync.Once
}

func NewConnector(ctx context.Context, cfg *v1.ClientCommonConfig) Connector {
	ctx, cancel := context.WithCancel(ctx)
	return &defaultConnectorImpl{
		ctx:    ctx,
		cfg:    cfg,
		cancel: cancel,
		conns:  make(map[*trackedConnectorConn]struct{}),
	}
}

type trackedConnectorConn struct {
	net.Conn
	owner *defaultConnectorImpl
	once  sync.Once
	err   error
}

func (conn *trackedConnectorConn) Close() error {
	conn.once.Do(func() {
		conn.owner.mu.Lock()
		delete(conn.owner.conns, conn)
		conn.owner.mu.Unlock()
		conn.err = conn.Conn.Close()
	})
	return conn.err
}

func (c *defaultConnectorImpl) track(conn net.Conn) (net.Conn, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		_ = conn.Close()
		return nil, net.ErrClosed
	}
	tracked := &trackedConnectorConn{Conn: conn, owner: c}
	c.conns[tracked] = struct{}{}
	c.mu.Unlock()
	return tracked, nil
}

// Open opens an underlying connection to the server.
// The underlying connection is either a TCP connection or a QUIC connection.
// After the underlying connection is established, you can call Connect() to get a stream.
// If TCPMux isn't enabled, the underlying connection is nil, you will get a new real TCP connection every time you call Connect().
func (c *defaultConnectorImpl) Open() error {
	xl := xlog.FromContextSafe(c.ctx)

	// special for quic
	if strings.EqualFold(c.cfg.Transport.Protocol, "quic") {
		var tlsConfig *tls.Config
		var err error
		sn := c.cfg.Transport.TLS.ServerName
		if sn == "" {
			sn = c.cfg.ServerAddr
		}
		if lo.FromPtr(c.cfg.Transport.TLS.Enable) {
			tlsConfig, err = transport.NewClientTLSConfigWithSystemCA(
				c.cfg.Transport.TLS.CertFile,
				c.cfg.Transport.TLS.KeyFile,
				c.cfg.Transport.TLS.TrustedCaFile,
				sn,
				c.cfg.Transport.TLS.UseSystemCa)
		} else {
			tlsConfig, err = transport.NewClientTLSConfig("", "", "", sn)
		}
		if err != nil {
			xl.Warnf("fail to build tls configuration, err: %v", err)
			return err
		}
		tlsConfig.NextProtos = []string{"frp"}

		conn, err := quic.DialAddr(
			c.ctx,
			net.JoinHostPort(c.cfg.ServerAddr, strconv.Itoa(c.cfg.ServerPort)),
			tlsConfig, &quic.Config{
				MaxIdleTimeout:     time.Duration(c.cfg.Transport.QUIC.MaxIdleTimeout) * time.Second,
				MaxIncomingStreams: int64(c.cfg.Transport.QUIC.MaxIncomingStreams),
				KeepAlivePeriod:    time.Duration(c.cfg.Transport.QUIC.KeepalivePeriod) * time.Second,
			})
		if err != nil {
			return err
		}
		c.quicConn = conn
		return nil
	}

	if !lo.FromPtr(c.cfg.Transport.TCPMux) {
		return nil
	}

	conn, err := c.realConnect()
	if err != nil {
		return err
	}

	fmuxCfg := fmux.DefaultConfig()
	fmuxCfg.KeepAliveInterval = time.Duration(c.cfg.Transport.TCPMuxKeepaliveInterval) * time.Second
	// Use trace level for yamux logs
	fmuxCfg.LogOutput = xlog.NewTraceWriter(xl)
	fmuxCfg.MaxStreamWindowSize = 6 * 1024 * 1024
	session, err := fmux.Client(conn, fmuxCfg)
	if err != nil {
		conn.Close()
		return err
	}
	c.muxSession = session
	return nil
}

// Connect returns a stream from the underlying connection, or a new TCP connection if TCPMux isn't enabled.
func (c *defaultConnectorImpl) Connect() (net.Conn, error) {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return nil, net.ErrClosed
	}
	if c.quicConn != nil {
		stream, err := c.quicConn.OpenStreamSync(c.ctx)
		if err != nil {
			return nil, err
		}
		return c.track(netpkg.QuicStreamToNetConn(stream, c.quicConn))
	} else if c.muxSession != nil {
		stream, err := c.muxSession.OpenStream()
		if err != nil {
			return nil, err
		}
		return c.track(stream)
	}

	conn, err := c.realConnect()
	if err != nil {
		return nil, err
	}
	return c.track(conn)
}

func (c *defaultConnectorImpl) realConnect() (net.Conn, error) {
	xl := xlog.FromContextSafe(c.ctx)
	var tlsConfig *tls.Config
	var err error
	tlsEnable := lo.FromPtr(c.cfg.Transport.TLS.Enable)
	if c.cfg.Transport.Protocol == "wss" {
		tlsEnable = true
	}
	if tlsEnable {
		sn := c.cfg.Transport.TLS.ServerName
		if sn == "" {
			sn = c.cfg.ServerAddr
		}

		tlsConfig, err = transport.NewClientTLSConfigWithSystemCA(
			c.cfg.Transport.TLS.CertFile,
			c.cfg.Transport.TLS.KeyFile,
			c.cfg.Transport.TLS.TrustedCaFile,
			sn,
			c.cfg.Transport.TLS.UseSystemCa)
		if err != nil {
			xl.Warnf("fail to build tls configuration, err: %v", err)
			return nil, err
		}
	}

	proxyType, addr, auth, err := libnet.ParseProxyURL(c.cfg.Transport.ProxyURL)
	if err != nil {
		xl.Errorf("fail to parse proxy url")
		return nil, err
	}
	dialOptions := []libnet.DialOption{}
	// DialContext's TCP dial respects cancellation, but several subsequent
	// websocket/TLS hooks perform blocking I/O. Close the established socket if
	// the service is cancelled while those hooks are still running. A proxy
	// hook runs before public hooks in golib, so the isolated profile rejects
	// proxy URLs until that earlier handshake is independently cancellable.
	var stopHandshakeClose func() bool
	var handshakeCloseDone chan struct{}
	dialOptions = append(dialOptions, libnet.WithAfterHook(libnet.AfterHook{
		Priority: 1,
		Hook: func(ctx context.Context, conn net.Conn, _ string) (context.Context, net.Conn, error) {
			done := make(chan struct{})
			handshakeCloseDone = done
			stopHandshakeClose = context.AfterFunc(c.ctx, func() {
				defer close(done)
				_ = conn.Close()
			})
			return ctx, conn, nil
		},
	}))
	defer func() {
		if stopHandshakeClose != nil && !stopHandshakeClose() {
			<-handshakeCloseDone
		}
	}()
	protocol := c.cfg.Transport.Protocol
	switch protocol {
	case "websocket":
		protocol = "tcp"
		dialOptions = append(dialOptions, libnet.WithAfterHook(libnet.AfterHook{Hook: netpkg.DialHookWebsocket(protocol, "")}))
		dialOptions = append(dialOptions, libnet.WithAfterHook(libnet.AfterHook{
			Hook: netpkg.DialHookCustomTLSHeadByte(tlsConfig != nil, lo.FromPtr(c.cfg.Transport.TLS.DisableCustomTLSFirstByte)),
		}))
		dialOptions = append(dialOptions, libnet.WithTLSConfig(tlsConfig))
	case "wss":
		protocol = "tcp"
		dialOptions = append(dialOptions, libnet.WithTLSConfigAndPriority(100, tlsConfig))
		// Make sure that if it is wss, the websocket hook is executed after the tls hook.
		dialOptions = append(dialOptions, libnet.WithAfterHook(libnet.AfterHook{Hook: netpkg.DialHookWebsocket(protocol, tlsConfig.ServerName), Priority: 110}))
	default:
		dialOptions = append(dialOptions, libnet.WithAfterHook(libnet.AfterHook{
			Hook: netpkg.DialHookCustomTLSHeadByte(tlsConfig != nil, lo.FromPtr(c.cfg.Transport.TLS.DisableCustomTLSFirstByte)),
		}))
		dialOptions = append(dialOptions, libnet.WithTLSConfig(tlsConfig))
	}

	if c.cfg.Transport.ConnectServerLocalIP != "" {
		dialOptions = append(dialOptions, libnet.WithLocalAddr(c.cfg.Transport.ConnectServerLocalIP))
	}
	dialOptions = append(dialOptions,
		libnet.WithProtocol(protocol),
		libnet.WithTimeout(time.Duration(c.cfg.Transport.DialServerTimeout)*time.Second),
		libnet.WithKeepAlive(time.Duration(c.cfg.Transport.DialServerKeepAlive)*time.Second),
		libnet.WithProxy(proxyType, addr),
		libnet.WithProxyAuth(auth),
	)
	conn, err := libnet.DialContext(
		c.ctx,
		net.JoinHostPort(c.cfg.ServerAddr, strconv.Itoa(c.cfg.ServerPort)),
		dialOptions...,
	)
	return conn, err
}

func (c *defaultConnectorImpl) Close() error {
	c.closeOnce.Do(func() {
		c.cancel()
		c.mu.Lock()
		c.closed = true
		conns := make([]*trackedConnectorConn, 0, len(c.conns))
		for conn := range c.conns {
			conns = append(conns, conn)
		}
		c.mu.Unlock()
		if c.quicConn != nil {
			_ = c.quicConn.CloseWithError(0, "")
		}
		if c.muxSession != nil {
			_ = c.muxSession.Close()
		}
		for _, conn := range conns {
			_ = conn.Close()
		}
	})
	return nil
}
