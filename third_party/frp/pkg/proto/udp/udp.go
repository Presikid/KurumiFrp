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

// Modified by KurumiFRP: track restricted client UDP forwarding lifetimes.
// Modified by KurumiFRP: report successful UDP writes to an instance observer.
// Modified by KurumiFRP: allow service-owned UDP connections.

package udp

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/fatedier/golib/errors"
	"github.com/fatedier/golib/pool"

	"github.com/fatedier/frp/pkg/msg"
	"github.com/fatedier/frp/pkg/util/lifetime"
	netpkg "github.com/fatedier/frp/pkg/util/net"
)

func NewUDPPacket(buf []byte, laddr, raddr *net.UDPAddr) *msg.UDPPacket {
	content := make([]byte, len(buf))
	copy(content, buf)
	return &msg.UDPPacket{
		Content:    content,
		LocalAddr:  laddr,
		RemoteAddr: raddr,
	}
}

func GetContent(m *msg.UDPPacket) (buf []byte, err error) {
	return m.Content, nil
}

func ForwardUserConn(udpConn *net.UDPConn, readCh <-chan *msg.UDPPacket, sendCh chan<- *msg.UDPPacket, bufSize int) {
	ForwardUserConnWithContext(context.Background(), udpConn, readCh, sendCh, bufSize)
}

// ForwardUserConnWithWriteObserver reports bytes actually written to users.
// The observer must return promptly and must not panic.
func ForwardUserConnWithWriteObserver(
	udpConn *net.UDPConn, readCh <-chan *msg.UDPPacket, sendCh chan<- *msg.UDPPacket, bufSize int, onWrite func(int),
) {
	ForwardUserConnWithDoneAndWriteObserver(context.Background(), udpConn, readCh, sendCh, bufSize, nil, onWrite)
}

// ForwardUserConnWithContext tracks its background writer in the client scope.
func ForwardUserConnWithContext(ctx context.Context, udpConn *net.UDPConn, readCh <-chan *msg.UDPPacket, sendCh chan<- *msg.UDPPacket, bufSize int) {
	ForwardUserConnWithDone(ctx, udpConn, readCh, sendCh, bufSize, nil)
}

// ForwardUserConnWithDone also stops its writer when a visitor is replaced.
func ForwardUserConnWithDone(ctx context.Context, udpConn *net.UDPConn, readCh <-chan *msg.UDPPacket, sendCh chan<- *msg.UDPPacket, bufSize int, done <-chan struct{}) {
	ForwardUserConnWithDoneAndWriteObserver(ctx, udpConn, readCh, sendCh, bufSize, done, nil)
}

// ForwardUserConnWithDoneAndWriteObserver combines cancellation with the
// successful-write observation used by server traffic accounting.
func ForwardUserConnWithDoneAndWriteObserver(ctx context.Context, udpConn *net.UDPConn, readCh <-chan *msg.UDPPacket, sendCh chan<- *msg.UDPPacket, bufSize int, done <-chan struct{}, onWrite func(int)) {
	// read
	lifetime.Go(ctx, func() {
		for {
			var udpMsg *msg.UDPPacket
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case packet, ok := <-readCh:
				if !ok {
					return
				}
				udpMsg = packet
			}
			buf, err := GetContent(udpMsg)
			if err != nil {
				continue
			}
			n, _ := udpConn.WriteToUDP(buf, udpMsg.RemoteAddr)
			if n > 0 && onWrite != nil {
				onWrite(n)
			}
		}
	})

	// write
	buf := pool.GetBuf(bufSize)
	defer pool.PutBuf(buf)
	for {
		n, remoteAddr, err := udpConn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		// NewUDPPacket copies buf[:n], so the read buffer can be reused
		udpMsg := NewUDPPacket(buf[:n], nil, remoteAddr)

		if err = errors.PanicToError(func() {
			select {
			case <-done:
			case <-ctx.Done():
			case sendCh <- udpMsg:
			default:
			}
		}); err != nil {
			return
		}
	}
}

func Forwarder(dstAddr *net.UDPAddr, readCh <-chan *msg.UDPPacket, sendCh chan<- msg.Message, bufSize int, proxyProtocolVersion string) {
	ForwarderWithContext(context.Background(), dstAddr, readCh, sendCh, bufSize, proxyProtocolVersion)
}

// ForwarderWithContext joins the forwarding workers to the client scope.
func ForwarderWithContext(ctx context.Context, dstAddr *net.UDPAddr, readCh <-chan *msg.UDPPacket, sendCh chan<- msg.Message, bufSize int, proxyProtocolVersion string) {
	ForwarderWithDone(ctx, dstAddr, readCh, sendCh, bufSize, proxyProtocolVersion, nil, nil)
}

// ForwarderWithDone ends a work session without closing its data channels.
func ForwarderWithDone(ctx context.Context, dstAddr *net.UDPAddr, readCh <-chan *msg.UDPPacket, sendCh chan<- msg.Message, bufSize int, proxyProtocolVersion string, done <-chan struct{}, dial func(*net.UDPAddr) (net.Conn, error)) {
	var mu sync.RWMutex
	type udpConnection struct{ net.Conn }
	udpConnMap := make(map[string]*udpConnection)
	if dial == nil {
		dial = func(addr *net.UDPAddr) (net.Conn, error) { return net.DialUDP("udp", nil, addr) }
	}

	// read from dstAddr and write to sendCh
	writerFn := func(raddr *net.UDPAddr, udpConn *udpConnection) {
		addr := raddr.String()
		defer func() {
			mu.Lock()
			if udpConnMap[addr] == udpConn {
				delete(udpConnMap, addr)
			}
			mu.Unlock()
			udpConn.Close()
		}()

		buf := pool.GetBuf(bufSize)
		defer pool.PutBuf(buf)
		for {
			_ = udpConn.SetReadDeadline(time.Now().Add(30 * time.Second))
			n, err := udpConn.Read(buf)
			if err != nil {
				return
			}

			udpMsg := NewUDPPacket(buf[:n], nil, raddr)
			if err = errors.PanicToError(func() {
				select {
				case <-done:
				case <-ctx.Done():
				case sendCh <- udpMsg:
				default:
				}
			}); err != nil {
				return
			}
		}
	}

	// read from readCh
	lifetime.Go(ctx, func() {
		defer func() {
			mu.Lock()
			conns := make([]net.Conn, 0, len(udpConnMap))
			for _, conn := range udpConnMap {
				conns = append(conns, conn)
			}
			mu.Unlock()
			for _, conn := range conns {
				_ = conn.Close()
			}
		}()
		for {
			var udpMsg *msg.UDPPacket
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case packet, ok := <-readCh:
				if !ok {
					return
				}
				udpMsg = packet
			}
			if udpMsg == nil || udpMsg.RemoteAddr == nil {
				continue
			}
			buf, err := GetContent(udpMsg)
			if err != nil {
				continue
			}
			// Datagram receivers parse a separate PROXY header on every packet.
			// Do not send an unframed packet if its header cannot be built.
			if proxyProtocolVersion != "" {
				ppBuf, err := netpkg.BuildProxyProtocolHeader(udpMsg.RemoteAddr, dstAddr, proxyProtocolVersion)
				if err != nil {
					continue
				}
				finalBuf := make([]byte, len(ppBuf)+len(buf))
				copy(finalBuf, ppBuf)
				copy(finalBuf[len(ppBuf):], buf)
				buf = finalBuf
			}

			mu.Lock()
			udpConn, ok := udpConnMap[udpMsg.RemoteAddr.String()]
			var release func()
			if !ok {
				var conn net.Conn
				conn, err = dial(dstAddr)
				if err != nil {
					mu.Unlock()
					continue
				}
				udpConn = &udpConnection{Conn: conn}
				var tracked bool
				release, tracked = lifetime.Track(ctx, udpConn)
				if !tracked {
					mu.Unlock()
					return
				}
				udpConnMap[udpMsg.RemoteAddr.String()] = udpConn
			}
			mu.Unlock()

			_, err = udpConn.Write(buf)
			if err != nil {
				udpConn.Close()
			} else {
				_ = udpConn.SetReadDeadline(time.Now().Add(30 * time.Second))
			}

			if !ok {
				remoteAddr := udpMsg.RemoteAddr
				if !lifetime.Go(ctx, func() {
					defer release()
					writerFn(remoteAddr, udpConn)
				}) {
					release()
					mu.Lock()
					if udpConnMap[remoteAddr.String()] == udpConn {
						delete(udpConnMap, remoteAddr.String())
					}
					mu.Unlock()
					_ = udpConn.Close()
					return
				}
			}
		}
	})
}
