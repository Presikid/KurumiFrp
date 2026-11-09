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

// Package netx owns networking resources for a single restricted frpc service.
package netx

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

type contextKey struct{}

// Network owns the resolver and registered sockets of one Service.
type Network struct {
	resolver *net.Resolver
	localIP  *net.TCPAddr
	ctx      context.Context
	cancel   context.CancelFunc

	mu        sync.Mutex
	closed    bool
	resources map[*trackedResource]struct{}
	closeOnce sync.Once
	ops       sync.WaitGroup
}

func New(dnsServer, localIP string) (*Network, error) {
	n := &Network{resources: make(map[*trackedResource]struct{})}
	if localIP != "" {
		ip, err := netip.ParseAddr(localIP)
		if err != nil {
			return nil, errors.New("connectServerLocalIP must be an IP address")
		}
		n.localIP = net.TCPAddrFromAddrPort(netip.AddrPortFrom(ip, 0))
	}
	n.resolver = &net.Resolver{}
	if dnsServer != "" {
		address, err := dnsAddress(dnsServer)
		if err != nil {
			return nil, err
		}
		n.resolver = &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
				// address is required to be an IP literal, so dialing the DNS
				// server cannot recursively invoke the process resolver.
				return n.DialContext(ctx, network, address, 5*time.Second, 0, false)
			},
		}
	}
	n.ctx, n.cancel = context.WithCancel(context.Background())
	return n, nil
}

func dnsAddress(address string) (string, error) {
	host, port := address, "53"
	if strings.HasPrefix(address, "[") && strings.HasSuffix(address, "]") {
		host = address[1 : len(address)-1]
	} else if _, err := netip.ParseAddr(address); err != nil {
		var splitErr error
		host, port, splitErr = net.SplitHostPort(address)
		if splitErr != nil {
			return "", errors.New("dnsServer must be an IP address with an optional port")
		}
	}
	if _, err := netip.ParseAddr(host); err != nil || !validPort(port, false) {
		return "", errors.New("dnsServer must be an IP address with a valid port")
	}
	return net.JoinHostPort(host, port), nil
}

func validPort(port string, allowZero bool) bool {
	n, err := strconv.ParseUint(port, 10, 16)
	return err == nil && (allowZero || n != 0)
}

func WithNetwork(ctx context.Context, n *Network) context.Context {
	return context.WithValue(ctx, contextKey{}, n)
}

func FromContext(ctx context.Context) *Network {
	n, _ := ctx.Value(contextKey{}).(*Network)
	return n
}

// begin prevents operations from being admitted after shutdown and ensures
// Close waits for in-flight dials or listener creation to release their sockets.
func (n *Network) begin(ctx context.Context) (context.Context, func(), error) {
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		return nil, nil, net.ErrClosed
	}
	n.ops.Add(1)
	n.mu.Unlock()
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(n.ctx, cancel)
	if n.ctx.Err() != nil {
		cancel()
	}
	return ctx, func() {
		stop()
		cancel()
		n.ops.Done()
	}, nil
}

type trackedResource struct {
	owner  *Network
	closer io.Closer
	once   sync.Once
	err    error
}

func (r *trackedResource) Close() error {
	r.once.Do(func() {
		r.err = r.closer.Close()
		r.owner.mu.Lock()
		delete(r.owner.resources, r)
		r.owner.mu.Unlock()
	})
	return r.err
}

// track keeps the resource registered until Close has actually completed.
func (n *Network) track(c io.Closer) (*trackedResource, error) {
	r := &trackedResource{owner: n, closer: c}
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		_ = c.Close()
		return nil, net.ErrClosed
	}
	n.resources[r] = struct{}{}
	n.mu.Unlock()
	return r, nil
}

func (n *Network) Close() {
	n.closeOnce.Do(func() {
		n.mu.Lock()
		n.closed = true
		resources := make([]*trackedResource, 0, len(n.resources))
		for c := range n.resources {
			resources = append(resources, c)
		}
		n.resources = nil
		n.mu.Unlock()
		n.cancel()
		for _, c := range resources {
			_ = c.Close()
		}
		n.ops.Wait()
	})
}

type trackedConn struct {
	net.Conn
	resource *trackedResource
}

// DNS uses the PacketConn interface to choose UDP rather than TCP framing.
// Preserve it only when the underlying connection is a packet socket.
type trackedPacketConn struct {
	*trackedConn
	packet net.PacketConn
}

type trackedTCPConn struct {
	*trackedConn
	tcp *net.TCPConn
}

func (c *trackedTCPConn) CloseRead() error  { return c.tcp.CloseRead() }
func (c *trackedTCPConn) CloseWrite() error { return c.tcp.CloseWrite() }

func (c *trackedPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	return c.packet.ReadFrom(p)
}

func (c *trackedPacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	return c.packet.WriteTo(p, addr)
}

func (c *trackedConn) Close() error {
	return c.resource.Close()
}

func (n *Network) trackConn(conn net.Conn) (net.Conn, error) {
	resource, err := n.track(conn)
	if err != nil {
		return nil, err
	}
	c := &trackedConn{Conn: conn, resource: resource}
	if packet, ok := conn.(net.PacketConn); ok {
		return &trackedPacketConn{trackedConn: c, packet: packet}, nil
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		return &trackedTCPConn{trackedConn: c, tcp: tcp}, nil
	}
	return c, nil
}

func (n *Network) DialContext(ctx context.Context, network, address string, timeout, keepAlive time.Duration, local bool) (net.Conn, error) {
	ctx, end, err := n.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer end()
	d := net.Dialer{Resolver: n.resolver, Timeout: timeout, KeepAlive: keepAlive}
	if local && n.localIP != nil {
		d.LocalAddr = n.localIP
	}
	conn, err := d.DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	return n.trackConn(conn)
}

func (n *Network) resolve(ctx context.Context, address string) (net.IPAddr, int, error) {
	ctx, end, err := n.begin(ctx)
	if err != nil {
		return net.IPAddr{}, 0, err
	}
	defer end()
	host, port, err := net.SplitHostPort(address)
	if err != nil || !validPort(port, true) {
		return net.IPAddr{}, 0, errors.New("invalid network address")
	}
	portNumber, _ := strconv.Atoi(port)
	if host == "" {
		return net.IPAddr{}, portNumber, nil
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return net.IPAddr{IP: net.IP(ip.AsSlice()), Zone: ip.Zone()}, portNumber, nil
	}
	ips, err := n.resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return net.IPAddr{}, 0, err
	}
	if len(ips) == 0 {
		return net.IPAddr{}, 0, &net.DNSError{Err: "no addresses found", Name: host}
	}
	// Match net.ResolveTCPAddr/ResolveUDPAddr's IPv4 preference.
	for _, ip := range ips {
		if ip.IP.To4() != nil {
			return ip, portNumber, nil
		}
	}
	return ips[0], portNumber, nil
}

func ResolveTCPAddr(ctx context.Context, address string) (*net.TCPAddr, error) {
	n := FromContext(ctx)
	if n == nil {
		return net.ResolveTCPAddr("tcp", address)
	}
	ip, port, err := n.resolve(ctx, address)
	if err != nil {
		return nil, err
	}
	return &net.TCPAddr{IP: ip.IP, Zone: ip.Zone, Port: port}, nil
}

func ResolveUDPAddr(ctx context.Context, address string) (*net.UDPAddr, error) {
	n := FromContext(ctx)
	if n == nil {
		return net.ResolveUDPAddr("udp", address)
	}
	ip, port, err := n.resolve(ctx, address)
	if err != nil {
		return nil, err
	}
	return &net.UDPAddr{IP: ip.IP, Zone: ip.Zone, Port: port}, nil
}

func DialUDP(ctx context.Context, dst *net.UDPAddr) (net.Conn, error) {
	if n := FromContext(ctx); n != nil {
		return n.DialContext(ctx, "udp", dst.String(), 0, 0, false)
	}
	return net.DialUDP("udp", nil, dst)
}

type trackedListener struct {
	net.Listener
	n        *Network
	resource *trackedResource
}

func (l *trackedListener) Accept() (net.Conn, error) {
	_, end, err := l.n.begin(l.n.ctx)
	if err != nil {
		return nil, err
	}
	defer end()
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return l.n.trackConn(conn)
}

func (l *trackedListener) Close() error {
	return l.resource.Close()
}

func ListenTCP(ctx context.Context, address string) (net.Listener, error) {
	n := FromContext(ctx)
	if n == nil {
		return net.Listen("tcp", address)
	}
	ctx, end, err := n.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer end()
	addr, err := ResolveTCPAddr(ctx, address)
	if err != nil {
		return nil, err
	}
	lc := net.ListenConfig{}
	listener, err := lc.Listen(ctx, "tcp", addr.String())
	if err != nil {
		return nil, err
	}
	resource, err := n.track(listener)
	if err != nil {
		return nil, err
	}
	return &trackedListener{Listener: listener, n: n, resource: resource}, nil
}

func ListenUDP(ctx context.Context, address string) (*net.UDPConn, func(), error) {
	n := FromContext(ctx)
	if n == nil {
		addr, err := net.ResolveUDPAddr("udp", address)
		if err != nil {
			return nil, nil, err
		}
		conn, err := net.ListenUDP("udp", addr)
		return conn, nil, err
	}
	ctx, end, err := n.begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer end()
	addr, err := ResolveUDPAddr(ctx, address)
	if err != nil {
		return nil, nil, err
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, nil, err
	}
	resource, err := n.track(conn)
	if err != nil {
		return nil, nil, err
	}
	return conn, func() { _ = resource.Close() }, nil
}
