// Copyright 2026 KurumiFRP contributors
// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"context"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	fmux "github.com/fatedier/yamux"

	"github.com/fatedier/frp/client/internal/netx"
	"github.com/fatedier/frp/pkg/config/types"
	v1 "github.com/fatedier/frp/pkg/config/v1"
	plugin "github.com/fatedier/frp/pkg/plugin/client"
	"github.com/fatedier/frp/pkg/util/lifetime"
)

const (
	isolatedSocks5HandshakeTimeout = 20 * time.Second
	isolatedSocks5DialTimeout      = 10 * time.Second
)

// This plugin is deliberately not registered with the process-wide plugin
// registry. A restricted client must construct it with its own network.
type isolatedSocks5Plugin struct {
	network  *netx.Network
	cleanup  *pluginCleanupState
	username []byte
	password []byte

	mu            sync.Mutex
	closing       bool
	cleanupFailed bool
	sessions      map[*isolatedSocks5Session]struct{}
	work          sync.WaitGroup
	closeOnce     sync.Once
	closeErr      error
}

var _ plugin.Plugin = (*isolatedSocks5Plugin)(nil)

func newIsolatedSocks5Plugin(network *netx.Network, cleanup *pluginCleanupState, opts *v1.Socks5PluginOptions) (plugin.Plugin, error) {
	if network == nil || cleanup == nil || !validIsolatedSocks5Options(opts) {
		return nil, errors.New("invalid isolated SOCKS5 configuration")
	}
	return &isolatedSocks5Plugin{
		network: network, cleanup: cleanup, username: []byte(opts.Username), password: []byte(opts.Password),
		sessions: make(map[*isolatedSocks5Session]struct{}),
	}, nil
}

// ValidIsolatedSocks5Config applies the same restrictions before completion,
// on reload, and at construction. The empty bandwidth mode defaults to client.
func ValidIsolatedSocks5Config(base *v1.ProxyBaseConfig) bool {
	if base == nil || base.Plugin.Type != v1.PluginSocks5 {
		return false
	}
	opts, ok := base.Plugin.ClientPluginOptions.(*v1.Socks5PluginOptions)
	return ok && validIsolatedSocks5Options(opts) && base.HealthCheck.Type == "" &&
		base.Transport.ProxyProtocolVersion == "" &&
		(base.Transport.BandwidthLimit.Bytes() <= 0 || base.Transport.BandwidthLimitMode == types.BandwidthLimitModeServer)
}

func validIsolatedSocks5Options(opts *v1.Socks5PluginOptions) bool {
	return opts != nil && (opts.Type == "" || opts.Type == v1.PluginSocks5) &&
		isolatedSocks5CredentialOK(opts.Username) && isolatedSocks5CredentialOK(opts.Password)
}

func isolatedSocks5CredentialOK(value string) bool {
	if !utf8.ValidString(value) || len(value) < 1 || len(value) > 255 {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func (p *isolatedSocks5Plugin) Name() string { return v1.PluginSocks5 }

type isolatedSocks5Session struct {
	owner      *isolatedSocks5Plugin
	ctx        context.Context
	cancel     context.CancelFunc
	client     io.ReadWriteCloser
	underlying net.Conn

	mu     sync.Mutex
	closed bool
	target net.Conn
}

// These errors establish that the stream or its multiplexed session is already
// closed or reset. Other Close errors leave cleanup uncertain.
func isolatedSocks5CloseFailed(err error) bool {
	return err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, io.ErrClosedPipe) &&
		!errors.Is(err, fmux.ErrSessionShutdown) && !errors.Is(err, fmux.ErrStreamClosed) &&
		!errors.Is(err, fmux.ErrConnectionReset)
}

func (p *isolatedSocks5Plugin) noteCloseError(err error) {
	if isolatedSocks5CloseFailed(err) {
		p.noteCleanupFailure()
	}
}

func (p *isolatedSocks5Plugin) noteCleanupFailure() {
	p.mu.Lock()
	first := !p.cleanupFailed
	p.cleanupFailed = true
	p.mu.Unlock()
	if first {
		p.cleanup.fail()
	}
}

// setTarget also covers a successful dial racing with cancellation. The late
// connection remains owned by this session until it has actually been closed.
func (s *isolatedSocks5Session) setTarget(conn net.Conn) bool {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		s.owner.noteCloseError(conn.Close())
		return false
	}
	s.target = conn
	s.mu.Unlock()
	return true
}

// Deadline changes during the handshake must be ordered with shutdown.
// Multiplexed work streams can implement Close as a write half-close, so a
// late SetDeadline(time.Time{}) could otherwise re-enable a blocked read after
// the shutdown watcher has already set a deadline and exited.
func (s *isolatedSocks5Session) setHandshakeDeadline(deadline time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.ctx.Err() != nil {
		return false
	}
	return s.underlying.SetDeadline(deadline) == nil
}

// Called only by the session's watcher. Closing the wrapped client rather than
// bypassing it preserves frp's stream wrappers. Deadlines help unblock I/O
// while it is closing; the underlying connection must honor Close/Deadline.
func (s *isolatedSocks5Session) shutdown() {
	s.mu.Lock()
	s.closed = true
	target := s.target
	s.mu.Unlock()
	now := time.Now()
	if target != nil {
		_ = target.SetDeadline(now)
	}
	_ = s.underlying.SetDeadline(now)
	if target != nil {
		s.owner.noteCloseError(target.Close())
	}
	s.owner.noteCloseError(s.client.Close())
}

// watch is joined by Handle, including when a timeout or parent cancellation
// races with a completed handshake. No timer callback outlives the session.
func (s *isolatedSocks5Session) watch(handshakeDone <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	defer func() {
		if recover() != nil {
			s.owner.noteCleanupFailure()
			s.cancel()
			if !lifetime.ReportPanic(s.ctx) {
				panic("isolated SOCKS5 worker panicked")
			}
		}
	}()
	timer := time.NewTimer(isolatedSocks5HandshakeTimeout)
	defer timer.Stop()
	timerC := timer.C
	for {
		select {
		case <-s.ctx.Done():
			s.shutdown()
			return
		case <-timerC:
			s.cancel()
			s.shutdown()
			return
		case <-handshakeDone:
			timer.Stop()
			timerC = nil
			handshakeDone = nil
		}
	}
}

func (p *isolatedSocks5Plugin) Handle(ctx context.Context, info *plugin.ConnectionInfo) {
	if info == nil || info.Conn == nil {
		return
	}
	validContext := ctx != nil
	if !validContext {
		ctx = context.Background()
	}
	sessionCtx, cancel := context.WithCancel(ctx)
	s := &isolatedSocks5Session{owner: p, ctx: sessionCtx, cancel: cancel, client: info.Conn, underlying: info.UnderlyingConn}
	p.mu.Lock()
	if p.closing {
		p.mu.Unlock()
		cancel()
		// This handoff was not admitted; the caller still owns the worker.
		_ = info.Conn.Close()
		return
	}
	p.sessions[s] = struct{}{}
	p.work.Add(1)
	p.mu.Unlock()
	defer func() {
		cancel()
		p.mu.Lock()
		delete(p.sessions, s)
		p.mu.Unlock()
		p.work.Done()
	}()
	if !validContext || netx.FromContext(ctx) != p.network || info.UnderlyingConn == nil || info.ProxyProtocolHeader != nil {
		p.noteCloseError(info.Conn.Close())
		return
	}

	handshakeDone := make(chan struct{})
	watchDone := make(chan struct{})
	go s.watch(handshakeDone, watchDone)
	defer func() {
		cancel()
		<-watchDone
	}()
	if sessionCtx.Err() != nil {
		return
	}

	// Conn may be a compression, encryption, or traffic wrapper without
	// deadlines of its own. UnderlyingConn is the individual frp work stream.
	if !s.setHandshakeDeadline(time.Now().Add(isolatedSocks5HandshakeTimeout)) {
		return
	}
	target, ok := p.handshake(s)
	if !ok {
		return
	}
	if !s.setHandshakeDeadline(time.Time{}) {
		return
	}
	close(handshakeDone)
	p.relay(s, target)
}

// Close rejects new sessions before canceling admitted ones. A handle remains
// in work until its watcher, dial, and both relay directions have exited.
// An uncooperative transport whose Close/Deadline cannot interrupt I/O can make
// this wait unbounded; returning early would leave an unmanaged goroutine.
// The restricted profile rejects client-side bandwidth limiting, whose current
// waits cannot be interrupted by cancellation or by closing the work stream.
func (p *isolatedSocks5Plugin) Close() error {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closing = true
		for session := range p.sessions {
			session.cancel()
		}
		p.mu.Unlock()
		p.work.Wait()
		p.mu.Lock()
		clear(p.username)
		clear(p.password)
		if p.cleanupFailed {
			p.closeErr = errors.New("isolated SOCKS5 connection cleanup failed")
		}
		p.mu.Unlock()
	})
	return p.closeErr
}

func isolatedSocks5Write(w io.Writer, data []byte) bool {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil || n <= 0 || n > len(data) {
			return false
		}
		data = data[n:]
	}
	return true
}

func isolatedSocks5Failure(w io.Writer, code byte) {
	_ = isolatedSocks5Write(w, []byte{5, code, 0, 1, 0, 0, 0, 0, 0, 0})
}

func (p *isolatedSocks5Plugin) handshake(s *isolatedSocks5Session) (net.Conn, bool) {
	var head [4]byte
	if _, err := io.ReadFull(s.client, head[:2]); err != nil || head[0] != 5 || head[1] == 0 {
		return nil, false
	}
	methods := make([]byte, int(head[1]))
	if _, err := io.ReadFull(s.client, methods); err != nil {
		return nil, false
	}
	found := false
	for _, method := range methods {
		found = found || method == 2
	}
	if !found {
		_ = isolatedSocks5Write(s.client, []byte{5, 0xff})
		return nil, false
	}
	if !isolatedSocks5Write(s.client, []byte{5, 2}) {
		return nil, false
	}
	if _, err := io.ReadFull(s.client, head[:2]); err != nil || head[0] != 1 || head[1] == 0 {
		return nil, false
	}
	username := make([]byte, int(head[1]))
	if _, err := io.ReadFull(s.client, username); err != nil {
		return nil, false
	}
	if _, err := io.ReadFull(s.client, head[:1]); err != nil || head[0] == 0 {
		return nil, false
	}
	password := make([]byte, int(head[0]))
	defer func() { clear(password) }()
	if _, err := io.ReadFull(s.client, password); err != nil {
		return nil, false
	}
	match := subtle.ConstantTimeCompare(username, p.username) & subtle.ConstantTimeCompare(password, p.password)
	clear(password)
	if match != 1 {
		_ = isolatedSocks5Write(s.client, []byte{1, 1})
		return nil, false
	}
	if !isolatedSocks5Write(s.client, []byte{1, 0}) {
		return nil, false
	}
	if _, err := io.ReadFull(s.client, head[:]); err != nil {
		return nil, false
	}
	if head[0] != 5 || head[2] != 0 {
		isolatedSocks5Failure(s.client, 1)
		return nil, false
	}
	if head[1] != 1 {
		isolatedSocks5Failure(s.client, 7)
		return nil, false
	}
	var host string
	switch head[3] {
	case 1:
		var ip [4]byte
		if _, err := io.ReadFull(s.client, ip[:]); err != nil {
			return nil, false
		}
		host = net.IP(ip[:]).String()
	case 4:
		var ip [16]byte
		if _, err := io.ReadFull(s.client, ip[:]); err != nil {
			return nil, false
		}
		host = net.IP(ip[:]).String()
	case 3:
		if _, err := io.ReadFull(s.client, head[:1]); err != nil || head[0] == 0 {
			return nil, false
		}
		name := make([]byte, int(head[0]))
		if _, err := io.ReadFull(s.client, name); err != nil {
			return nil, false
		}
		host = string(name)
	default:
		isolatedSocks5Failure(s.client, 8)
		return nil, false
	}
	if _, err := io.ReadFull(s.client, head[:2]); err != nil {
		return nil, false
	}
	port := binary.BigEndian.Uint16(head[:2])
	if port == 0 {
		isolatedSocks5Failure(s.client, 1)
		return nil, false
	}
	target, err := p.network.DialContext(s.ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(int(port))), isolatedSocks5DialTimeout, 0, false)
	if err != nil {
		isolatedSocks5Failure(s.client, 1)
		return nil, false
	}
	if !s.setTarget(target) {
		return nil, false
	}
	addr, ok := target.LocalAddr().(*net.TCPAddr)
	if !ok || addr.Port < 1 || addr.Port > 65535 {
		isolatedSocks5Failure(s.client, 1)
		return nil, false
	}
	var reply []byte
	if ip := addr.IP.To4(); ip != nil {
		reply = append([]byte{5, 0, 0, 1}, ip...)
	} else if ip := addr.IP.To16(); ip != nil {
		reply = append([]byte{5, 0, 0, 4}, ip...)
	} else {
		isolatedSocks5Failure(s.client, 1)
		return nil, false
	}
	reply = binary.BigEndian.AppendUint16(reply, uint16(addr.Port))
	if !isolatedSocks5Write(s.client, reply) {
		return nil, false
	}
	return target, true
}

func (p *isolatedSocks5Plugin) relay(s *isolatedSocks5Session, target net.Conn) {
	var work sync.WaitGroup
	work.Add(2)
	copyOne := func(dst io.Writer, src io.Reader) {
		defer work.Done()
		defer func() {
			if recover() != nil {
				s.owner.noteCleanupFailure()
				s.cancel()
				if !lifetime.ReportPanic(s.ctx) {
					panic("isolated SOCKS5 worker panicked")
				}
			}
		}()
		_, err := io.Copy(dst, src)
		if err == nil {
			if half, ok := dst.(interface{ CloseWrite() error }); ok {
				err = half.CloseWrite()
			} else {
				// Do not half-close UnderlyingConn behind encryption/compression.
				err = net.ErrClosed
			}
		}
		if err != nil {
			s.cancel()
		}
	}
	go copyOne(target, s.client)
	go copyOne(s.client, target)
	work.Wait()
}
