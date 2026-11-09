// Copyright 2019 fatedier, fatedier@gmail.com
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

// Modified by KurumiFRP: isolate metrics per server instance.
// Modified by KurumiFRP: stop and join UDP forwarding work.

package proxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"reflect"
	"strconv"
	"sync"
	"time"

	libio "github.com/fatedier/golib/io"

	v1 "github.com/fatedier/frp/pkg/config/v1"
	"github.com/fatedier/frp/pkg/msg"
	"github.com/fatedier/frp/pkg/proto/udp"
	"github.com/fatedier/frp/pkg/util/lifetime"
	"github.com/fatedier/frp/pkg/util/limit"
	netpkg "github.com/fatedier/frp/pkg/util/net"
)

func init() {
	RegisterProxyFactory(reflect.TypeFor[*v1.UDPProxyConfig](), NewUDPProxy)
}

type UDPProxy struct {
	*BaseProxy
	cfg *v1.UDPProxyConfig

	realBindPort int

	// udpConn is the listener of udp packages
	udpConn *net.UDPConn

	// there are always only one workConn at the same time
	// get another one if it closed
	workConn net.Conn

	// sendCh is used for sending packages to workConn
	sendCh chan *msg.UDPPacket

	// readCh is used for reading packages from workConn
	readCh chan *msg.UDPPacket

	// checkCloseCh is used for watching if workConn is closed
	checkCloseCh chan int
	closeCh      chan struct{}

	isClosed        bool
	portReleaseOnce sync.Once
}

func NewUDPProxy(baseProxy *BaseProxy) Proxy {
	unwrapped, ok := baseProxy.GetConfigurer().(*v1.UDPProxyConfig)
	if !ok {
		return nil
	}
	baseProxy.usedPortsNum = 1
	return &UDPProxy{
		BaseProxy: baseProxy,
		cfg:       unwrapped,
	}
}

func (pxy *UDPProxy) Run() (remoteAddr string, err error) {
	xl := pxy.xl
	pxy.realBindPort, err = pxy.rc.UDPPortManager.Acquire(pxy.name, pxy.cfg.RemotePort)
	if err != nil {
		return "", fmt.Errorf("acquire port %d error: %v", pxy.cfg.RemotePort, err)
	}
	defer func() {
		if err != nil {
			pxy.releasePort()
		}
	}()

	remoteAddr = fmt.Sprintf(":%d", pxy.realBindPort)
	pxy.cfg.RemotePort = pxy.realBindPort
	addr, errRet := net.ResolveUDPAddr("udp", net.JoinHostPort(pxy.serverCfg.ProxyBindAddr, strconv.Itoa(pxy.realBindPort)))
	if errRet != nil {
		err = errRet
		return
	}
	udpConn, errRet := net.ListenUDP("udp", addr)
	if errRet != nil {
		err = errRet
		xl.Warnf("listen udp port error: %v", err)
		return
	}
	xl.Infof("udp proxy listen port [%d]", pxy.cfg.RemotePort)

	pxy.udpConn = udpConn
	pxy.sendCh = make(chan *msg.UDPPacket, 1024)
	pxy.readCh = make(chan *msg.UDPPacket, 1024)
	pxy.checkCloseCh = make(chan int)
	pxy.closeCh = make(chan struct{})

	// read message from workConn, if it returns any error, notify proxy to start a new workConn
	workConnReaderFn := func(payloadConn *msg.Conn, sessionCtx context.Context) {
		for {
			var (
				rawMsg msg.Message
				errRet error
			)
			xl.Tracef("loop waiting message from udp workConn")
			// client will send heartbeat in workConn for keeping alive
			_ = payloadConn.SetReadDeadline(time.Now().Add(time.Duration(60) * time.Second))
			if rawMsg, errRet = payloadConn.ReadMsg(); errRet != nil {
				xl.Warnf("read from workConn for udp error: %v", errRet)
				_ = payloadConn.Close()
				// notify proxy to start a new work connection
				// ignore error here, it means the proxy is closed
				select {
				case pxy.checkCloseCh <- 1:
				case <-pxy.closeCh:
				case <-sessionCtx.Done():
				}
				return
			}
			if err := payloadConn.SetReadDeadline(time.Time{}); err != nil {
				xl.Warnf("set read deadline error: %v", err)
			}
			switch m := rawMsg.(type) {
			case *msg.Ping:
				xl.Tracef("udp work conn get ping message")
				continue
			case *msg.UDPPacket:
				select {
				case pxy.readCh <- m:
					xl.Tracef("get udp message from workConn, len: %d", len(m.Content))
				case <-pxy.closeCh:
					return
				case <-sessionCtx.Done():
					return
				}
			}
		}
	}

	// send message to workConn
	workConnSenderFn := func(payloadConn *msg.Conn, ctx context.Context) {
		var errRet error
		for {
			select {
			case udpMsg, ok := <-pxy.sendCh:
				if !ok {
					xl.Infof("sender goroutine for udp work connection closed")
					return
				}
				if errRet = payloadConn.WriteMsg(udpMsg); errRet != nil {
					xl.Infof("sender goroutine for udp work connection closed: %v", errRet)
					_ = payloadConn.Close()
					return
				}
				xl.Tracef("send message to udp workConn, len: %d", len(udpMsg.Content))
				pxy.recordTraffic(int64(len(udpMsg.Content)), 0)
				continue
			case <-ctx.Done():
				xl.Infof("sender goroutine for udp work connection closed")
				return
			}
		}
	}

	if !lifetime.Go(pxy.ctx, func() {
		// Sleep a while for waiting control send the NewProxyResp to client.
		select {
		case <-time.After(500 * time.Millisecond):
		case <-pxy.closeCh:
			return
		case <-pxy.ctx.Done():
			return
		}
		for {
			workConn, err := pxy.GetWorkConnFromPool(nil, nil)
			if err != nil {
				select {
				case <-time.After(time.Second):
				case <-pxy.closeCh:
					return
				case <-pxy.ctx.Done():
					return
				}
				continue
			}

			var rwc io.ReadWriteCloser = workConn
			if pxy.cfg.Transport.UseEncryption {
				rwc, err = libio.WithEncryption(rwc, pxy.encryptionKey)
				if err != nil {
					xl.Errorf("create encryption stream error: %v", err)
					workConn.Close()
					continue
				}
			}
			if pxy.cfg.Transport.UseCompression {
				rwc = libio.WithCompression(rwc)
			}

			if pxy.GetLimiter() != nil {
				rwc = libio.WrapReadWriteCloser(limit.NewReader(rwc, pxy.GetLimiter()), limit.NewWriter(rwc, pxy.GetLimiter()), rwc.Close)
			}

			wrappedConn := netpkg.WrapReadWriteCloserToConn(rwc, workConn)
			pxy.mu.Lock()
			if pxy.isClosed {
				pxy.mu.Unlock()
				_ = wrappedConn.Close()
				return
			}
			oldConn := pxy.workConn
			pxy.workConn = wrappedConn
			pxy.mu.Unlock()
			if oldConn != nil {
				_ = oldConn.Close()
			}
			// Plain UDP payload follows the negotiated wire protocol for message framing.
			payloadRW, err := msg.NewUDPPacketReadWriter(wrappedConn, pxy.wireProtocol, pxy.udpPacketCodec)
			if err != nil {
				xl.Errorf("create UDP packet read writer: %v", err)
				_ = wrappedConn.Close()
				continue
			}
			payloadConn := msg.NewConn(wrappedConn, payloadRW)
			ctx, cancel := context.WithCancel(pxy.ctx)
			if !lifetime.Go(ctx, func() { workConnReaderFn(payloadConn, ctx) }) ||
				!lifetime.Go(ctx, func() { workConnSenderFn(payloadConn, ctx) }) {
				cancel()
				_ = payloadConn.Close()
				return
			}
			select {
			case <-pxy.checkCloseCh:
			case <-pxy.closeCh:
			case <-ctx.Done():
			}
			cancel()
			_ = payloadConn.Close()
			select {
			case <-pxy.closeCh:
				return
			case <-pxy.ctx.Done():
				return
			default:
			}
		}
	}) {
		pxy.Close()
		return "", context.Canceled
	}

	// Read from user connections and send wrapped udp message to sendCh (forwarded by workConn).
	// Client will transfor udp message to local udp service and waiting for response for a while.
	// Response will be wrapped to be forwarded by work connection to server.
	// Shutdown signals closeCh; senders never race a closed data channel.
	if !lifetime.Go(pxy.ctx, func() {
		udp.ForwardUserConnWithDoneAndWriteObserver(pxy.ctx, udpConn, pxy.readCh, pxy.sendCh, int(pxy.serverCfg.UDPPacketSize), pxy.closeCh, func(n int) {
			pxy.recordTraffic(0, int64(n))
		})
		pxy.Close()
	}) {
		pxy.Close()
		return "", context.Canceled
	}
	return remoteAddr, nil
}

func (pxy *UDPProxy) Close() {
	pxy.mu.Lock()
	defer pxy.mu.Unlock()
	if !pxy.isClosed {
		pxy.isClosed = true
		close(pxy.closeCh)

		pxy.BaseProxy.Close()
		if pxy.workConn != nil {
			pxy.workConn.Close()
		}
		pxy.udpConn.Close()
	}
	pxy.releasePort()
}

func (pxy *UDPProxy) releasePort() {
	pxy.portReleaseOnce.Do(func() {
		if pxy.realBindPort > 0 {
			pxy.rc.UDPPortManager.Release(pxy.realBindPort)
		}
	})
}
