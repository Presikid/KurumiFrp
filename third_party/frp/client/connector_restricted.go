// Copyright 2026 KurumiFRP contributors
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

package client

import (
	"context"
	"crypto/tls"
	"net"
	"strconv"
	"time"

	"github.com/samber/lo"

	"github.com/fatedier/frp/client/internal/netx"
	netpkg "github.com/fatedier/frp/pkg/util/net"
)

// realConnectRestricted retains frp's hook order, while making the complete
// websocket handshake cancellable before the socket is published.
func (c *defaultConnectorImpl) realConnectRestricted(network *netx.Network, tlsConfig *tls.Config) (net.Conn, error) {
	ctx := c.ctx
	var cancel context.CancelFunc
	if timeout := time.Duration(c.cfg.Transport.DialServerTimeout) * time.Second; timeout != 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	target := net.JoinHostPort(c.cfg.ServerAddr, strconv.Itoa(c.cfg.ServerPort))
	conn, err := network.DialContext(ctx, "tcp", target, 0, time.Duration(c.cfg.Transport.DialServerKeepAlive)*time.Second, true)
	if err != nil {
		return nil, err
	}
	baseConn := conn
	stopClose := context.AfterFunc(ctx, func() { _ = baseConn.Close() })
	success := false
	defer func() {
		stopClose()
		if !success {
			_ = baseConn.Close()
		}
	}()

	protocol := c.cfg.Transport.Protocol
	customTLSHead := netpkg.DialHookCustomTLSHeadByte(tlsConfig != nil, lo.FromPtr(c.cfg.Transport.TLS.DisableCustomTLSFirstByte))
	switch protocol {
	case "websocket":
		_, conn, err = netpkg.DialHookWebsocket("tcp", "")(ctx, conn, target)
		if err == nil {
			_, conn, err = customTLSHead(ctx, conn, target)
		}
		if err == nil && tlsConfig != nil {
			conn = tls.Client(conn, tlsConfig)
		}
	case "wss":
		conn = tls.Client(conn, tlsConfig)
		_, conn, err = netpkg.DialHookWebsocket("tcp", tlsConfig.ServerName)(ctx, conn, target)
	default:
		_, conn, err = customTLSHead(ctx, conn, target)
		if err == nil && tlsConfig != nil {
			conn = tls.Client(conn, tlsConfig)
		}
	}
	if err != nil {
		return nil, err
	}
	if !stopClose() || ctx.Err() != nil {
		return nil, ctx.Err()
	}
	success = true
	return conn, nil
}
