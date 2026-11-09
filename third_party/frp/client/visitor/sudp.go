// Copyright 2017 fatedier, fatedier@gmail.com
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

// Modified by KurumiFRP: track restricted SUDP visitor workers.

// Modified by KurumiFRP: use the owning client's resolver and sockets.

package visitor

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/fatedier/frp/client/internal/netx"
	"github.com/fatedier/frp/client/traffic"
	v1 "github.com/fatedier/frp/pkg/config/v1"
	"github.com/fatedier/frp/pkg/msg"
	"github.com/fatedier/frp/pkg/proto/udp"
	"github.com/fatedier/frp/pkg/util/lifetime"
	netpkg "github.com/fatedier/frp/pkg/util/net"
	"github.com/fatedier/frp/pkg/util/xlog"
)

type SUDPVisitor struct {
	*BaseVisitor

	checkCloseCh chan struct{}
	// udpConn is the listener of udp packet
	udpConn    *net.UDPConn
	closeUDP   func()
	readCh     chan *msg.UDPPacket
	sendCh     chan *msg.UDPPacket
	activeConn net.Conn // protected by BaseVisitor.mu

	cfg *v1.SUDPVisitorConfig
}

// SUDP Run start listen a udp port
func (sv *SUDPVisitor) Run() (err error) {
	xl := xlog.FromContextSafe(sv.ctx)

	// KurumiFRP: resolve and register the socket before shutdown can finish.
	udpConn, closeUDP, err := netx.ListenUDP(sv.ctx, net.JoinHostPort(sv.cfg.BindAddr, strconv.Itoa(sv.cfg.BindPort)))
	if err != nil {
		return fmt.Errorf("listen sudp port error: %v", err)
	}
	sv.mu.Lock()
	select {
	case <-sv.checkCloseCh:
		sv.mu.Unlock()
		if closeUDP != nil {
			closeUDP()
		} else {
			_ = udpConn.Close()
		}
		return context.Canceled
	default:
		sv.udpConn, sv.closeUDP = udpConn, closeUDP
	}
	sv.mu.Unlock()

	sv.sendCh = make(chan *msg.UDPPacket, 1024)
	sv.readCh = make(chan *msg.UDPPacket, 1024)

	xl.Infof("sudp start to work, listen on %s", sv.udpConn.LocalAddr())

	if !lifetime.Go(sv.ctx, sv.dispatcher) {
		sv.Close()
		return context.Canceled
	}
	if !lifetime.Go(sv.ctx, func() {
		udp.ForwardUserConnWithDone(sv.ctx, sv.udpConn, sv.readCh, sv.sendCh, int(sv.clientCfg.UDPPacketSize), sv.checkCloseCh)
	}) {
		sv.Close()
		return context.Canceled
	}

	return
}

func (sv *SUDPVisitor) dispatcher() {
	xl := xlog.FromContextSafe(sv.ctx)

	var (
		visitorConn net.Conn
		recycleFn   func()
		err         error

		firstPacket *msg.UDPPacket
	)

	for {
		select {
		case firstPacket = <-sv.sendCh:
			if firstPacket == nil {
				xl.Infof("frpc sudp visitor proxy is closed")
				return
			}
		case <-sv.checkCloseCh:
			xl.Infof("frpc sudp visitor proxy is closed")
			return
		}

		visitorConn, recycleFn, err = sv.getNewVisitorConn()
		if err != nil {
			xl.Warnf("newVisitorConn to frps error: %v, try to reconnect", err)
			continue
		}
		sv.mu.Lock()
		select {
		case <-sv.checkCloseCh:
			sv.mu.Unlock()
			_ = visitorConn.Close()
			recycleFn()
			return
		default:
			sv.activeConn = visitorConn
		}
		sv.mu.Unlock()

		// visitorConn always be closed when worker done.
		func() {
			defer recycleFn()
			sv.worker(visitorConn, firstPacket)
		}()
		sv.mu.Lock()
		if sv.activeConn == visitorConn {
			sv.activeConn = nil
		}
		sv.mu.Unlock()

		select {
		case <-sv.checkCloseCh:
			return
		default:
		}
	}
}

func (sv *SUDPVisitor) worker(workConn net.Conn, firstPacket *msg.UDPPacket) {
	xl := xlog.FromContextSafe(sv.ctx)
	xl.Debugf("starting sudp proxy worker")
	payloadRW, err := msg.NewUDPPacketReadWriter(workConn, sv.clientCfg.Transport.WireProtocol, udpPacketCodecFromHelper(sv.helper))
	if err != nil {
		xl.Errorf("create SUDP packet read writer: %v", err)
		_ = workConn.Close()
		return
	}
	if sv.trafficCounter != nil {
		payloadRW = traffic.WrapUDPPackets(payloadRW, sv.trafficCounter)
	}
	payloadConn := msg.NewConn(workConn, payloadRW)

	wg := &sync.WaitGroup{}
	wg.Add(2)
	closeCh := make(chan struct{})

	// udp service -> frpc -> frps -> frpc visitor -> user
	workConnReaderFn := func(payloadConn *msg.Conn) {
		defer func() {
			payloadConn.Close()
			close(closeCh)
			wg.Done()
		}()

		for {
			var (
				rawMsg msg.Message
				errRet error
			)

			// frpc will send heartbeat in workConn to frpc visitor for keeping alive
			_ = payloadConn.SetReadDeadline(time.Now().Add(60 * time.Second))
			if rawMsg, errRet = payloadConn.ReadMsg(); errRet != nil {
				xl.Warnf("read from workconn for user udp conn error: %v", errRet)
				return
			}

			_ = payloadConn.SetReadDeadline(time.Time{})
			switch m := rawMsg.(type) {
			case *msg.Ping:
				xl.Debugf("frpc visitor get ping message from frpc")
				continue
			case *msg.UDPPacket:
				select {
				case sv.readCh <- m:
					xl.Tracef("frpc visitor get udp packet from workConn, len: %d", len(m.Content))
				case <-sv.checkCloseCh:
					return
				case <-sv.ctx.Done():
					return
				}
			}
		}
	}

	// udp service <- frpc <- frps <- frpc visitor <- user
	workConnSenderFn := func(payloadConn *msg.Conn) {
		defer func() {
			payloadConn.Close()
			wg.Done()
		}()

		var errRet error
		if firstPacket != nil {
			if errRet = payloadConn.WriteMsg(firstPacket); errRet != nil {
				xl.Warnf("sender goroutine for udp work connection closed: %v", errRet)
				return
			}
			xl.Tracef("send udp package to workConn, len: %d", len(firstPacket.Content))
		}

		for {
			select {
			case udpMsg, ok := <-sv.sendCh:
				if !ok {
					xl.Infof("sender goroutine for udp work connection closed")
					return
				}

				if errRet = payloadConn.WriteMsg(udpMsg); errRet != nil {
					xl.Warnf("sender goroutine for udp work connection closed: %v", errRet)
					return
				}
				xl.Tracef("send udp package to workConn, len: %d", len(udpMsg.Content))
			case <-closeCh:
				return
			case <-sv.checkCloseCh:
				return
			case <-sv.ctx.Done():
				return
			}
		}
	}

	go workConnReaderFn(payloadConn)
	go workConnSenderFn(payloadConn)

	wg.Wait()
	xl.Infof("sudp worker is closed")
}

func (sv *SUDPVisitor) getNewVisitorConn() (net.Conn, func(), error) {
	rawConn, err := sv.dialRawVisitorConn(sv.cfg.GetBaseConfig())
	if err != nil {
		return nil, func() {}, err
	}
	rwc, recycleFn, err := wrapVisitorConn(rawConn, sv.cfg.GetBaseConfig())
	if err != nil {
		rawConn.Close()
		return nil, func() {}, err
	}
	return netpkg.WrapReadWriteCloserToConn(rwc, rawConn), recycleFn, nil
}

func (sv *SUDPVisitor) Close() {
	sv.mu.Lock()

	select {
	case <-sv.checkCloseCh:
		sv.mu.Unlock()
		return
	default:
		close(sv.checkCloseCh)
	}
	activeConn := sv.activeConn
	udpConn := sv.udpConn
	closeUDP := sv.closeUDP
	sv.mu.Unlock()
	sv.BaseVisitor.Close()
	if closeUDP != nil {
		closeUDP()
	} else if udpConn != nil {
		_ = udpConn.Close()
	}
	if activeConn != nil {
		_ = activeConn.Close()
	}
}
