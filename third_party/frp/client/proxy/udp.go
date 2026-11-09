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
// Modified by KurumiFRP to integrate client traffic accounting.

// Modified by KurumiFRP: use the owning client's resolver and sockets.

//go:build !frps

// Modified by KurumiFRP: stop and join restricted client UDP work.

package proxy

import (
	"net"
	"reflect"
	"strconv"
	"time"

	"github.com/fatedier/frp/client/internal/netx"
	"github.com/fatedier/frp/client/traffic"
	v1 "github.com/fatedier/frp/pkg/config/v1"
	"github.com/fatedier/frp/pkg/msg"
	"github.com/fatedier/frp/pkg/proto/udp"
	"github.com/fatedier/frp/pkg/util/lifetime"
	netpkg "github.com/fatedier/frp/pkg/util/net"
)

func init() {
	RegisterProxyFactory(reflect.TypeFor[*v1.UDPProxyConfig](), NewUDPProxy)
}

type UDPProxy struct {
	*BaseProxy

	cfg *v1.UDPProxyConfig

	localAddr *net.UDPAddr
	readCh    chan *msg.UDPPacket

	// include msg.UDPPacket and msg.Ping
	sendCh      chan msg.Message
	workConn    net.Conn
	sessionDone chan struct{}
	closed      bool
	stopped     bool
}

func NewUDPProxy(baseProxy *BaseProxy, cfg v1.ProxyConfigurer) Proxy {
	unwrapped, ok := cfg.(*v1.UDPProxyConfig)
	if !ok {
		return nil
	}
	return &UDPProxy{
		BaseProxy: baseProxy,
		cfg:       unwrapped,
	}
}

func (pxy *UDPProxy) Run() (err error) {
	// KurumiFRP: resolve local services through the owning client service.
	pxy.localAddr, err = netx.ResolveUDPAddr(pxy.ctx, net.JoinHostPort(pxy.cfg.LocalIP, strconv.Itoa(pxy.cfg.LocalPort)))
	if err != nil {
		return
	}
	return
}

func (pxy *UDPProxy) Close() {
	pxy.mu.Lock()
	defer pxy.mu.Unlock()
	pxy.stopped = true
	pxy.closeSessionLocked()
}

// Caller holds pxy.mu. Replacing a work connection must not reopen a stopped proxy.
func (pxy *UDPProxy) closeSessionLocked() {
	if pxy.closed {
		return
	}
	pxy.closed = true
	if pxy.sessionDone != nil {
		close(pxy.sessionDone)
	}
	if pxy.workConn != nil {
		_ = pxy.workConn.Close()
	}
}

func (pxy *UDPProxy) InWorkConn(conn net.Conn, _ *msg.StartWorkConn) {
	xl := pxy.xl
	xl.Infof("incoming a new work connection for udp proxy, %s", conn.RemoteAddr().String())
	// Close the old work session without marking the proxy itself stopped.
	pxy.mu.Lock()
	if pxy.stopped || pxy.ctx.Err() != nil {
		pxy.mu.Unlock()
		_ = conn.Close()
		return
	}
	pxy.closeSessionLocked()
	pxy.mu.Unlock()

	remote, _, err := pxy.wrapWorkConn(conn, pxy.encryptionKey)
	if err != nil {
		xl.Errorf("wrap work connection: %v", err)
		return
	}

	workConn := netpkg.WrapReadWriteCloserToConn(remote, conn)
	// Plain UDP payload follows the configured wire protocol for message framing.
	payloadRW, err := msg.NewUDPPacketReadWriter(workConn, pxy.clientCfg.Transport.WireProtocol, pxy.udpPacketCodec)
	if err != nil {
		xl.Errorf("create UDP packet read writer: %v", err)
		workConn.Close()
		return
	}

	if pxy.trafficCounter != nil {
		payloadRW = traffic.WrapUDPPackets(payloadRW, pxy.trafficCounter)
	}

	pxy.mu.Lock()
	if pxy.stopped || pxy.ctx.Err() != nil {
		pxy.mu.Unlock()
		_ = workConn.Close()
		return
	}
	pxy.closeSessionLocked()
	pxy.workConn = workConn
	readCh := make(chan *msg.UDPPacket, 1024)
	sendCh := make(chan msg.Message, 1024)
	sessionDone := make(chan struct{})
	pxy.readCh = readCh
	pxy.sendCh = sendCh
	pxy.sessionDone = sessionDone
	pxy.closed = false
	pxy.mu.Unlock()
	stopSession := func() {
		pxy.mu.Lock()
		defer pxy.mu.Unlock()
		if pxy.sessionDone == sessionDone {
			pxy.closeSessionLocked()
		}
	}

	workConnReaderFn := func(rw msg.ReadWriter, readCh chan *msg.UDPPacket) {
		defer stopSession()
		for {
			var udpMsg msg.UDPPacket
			if errRet := rw.ReadMsgInto(&udpMsg); errRet != nil {
				xl.Warnf("read from workConn for udp error: %v", errRet)
				return
			}
			xl.Tracef("get udp package from workConn, len: %d", len(udpMsg.Content))
			select {
			case readCh <- &udpMsg:
			case <-sessionDone:
				return
			case <-pxy.ctx.Done():
				return
			}
		}
	}
	workConnSenderFn := func(rw msg.ReadWriter, sendCh chan msg.Message) {
		defer stopSession()
		defer func() {
			xl.Infof("writer goroutine for udp work connection closed")
		}()
		var errRet error
		for {
			var rawMsg msg.Message
			select {
			case rawMsg = <-sendCh:
			case <-sessionDone:
				return
			case <-pxy.ctx.Done():
				return
			}
			switch m := rawMsg.(type) {
			case *msg.UDPPacket:
				xl.Tracef("send udp package to workConn, len: %d", len(m.Content))
			case *msg.Ping:
				xl.Tracef("send ping message to udp workConn")
			}
			if errRet = rw.WriteMsg(rawMsg); errRet != nil {
				xl.Errorf("udp work write error: %v", errRet)
				return
			}
		}
	}
	heartbeatFn := func(sendCh chan msg.Message) {
		defer stopSession()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-sessionDone:
				return
			case <-pxy.ctx.Done():
				return
			case <-ticker.C:
				select {
				case sendCh <- &msg.Ping{}:
				case <-sessionDone:
					return
				case <-pxy.ctx.Done():
					return
				}
			}
		}
	}

	if !lifetime.Go(pxy.ctx, func() { workConnSenderFn(payloadRW, sendCh) }) ||
		!lifetime.Go(pxy.ctx, func() { workConnReaderFn(payloadRW, readCh) }) ||
		!lifetime.Go(pxy.ctx, func() { heartbeatFn(sendCh) }) {
		stopSession()
		return
	}

	udp.ForwarderWithDone(
		pxy.ctx, pxy.localAddr, readCh, sendCh, int(pxy.clientCfg.UDPPacketSize),
		pxy.cfg.Transport.ProxyProtocolVersion, sessionDone,
		func(addr *net.UDPAddr) (net.Conn, error) { return netx.DialUDP(pxy.ctx, addr) },
	)
}
