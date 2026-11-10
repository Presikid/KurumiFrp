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

package visitor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	libio "github.com/fatedier/golib/io"
	fmux "github.com/fatedier/yamux"
	quic "github.com/quic-go/quic-go"
	"golang.org/x/time/rate"

	"github.com/fatedier/frp/client/internal/netx"
	"github.com/fatedier/frp/client/traffic"
	v1 "github.com/fatedier/frp/pkg/config/v1"
	"github.com/fatedier/frp/pkg/msg"
	"github.com/fatedier/frp/pkg/naming"
	"github.com/fatedier/frp/pkg/nathole"
	"github.com/fatedier/frp/pkg/transport"
	"github.com/fatedier/frp/pkg/util/lifetime"
	netpkg "github.com/fatedier/frp/pkg/util/net"
	"github.com/fatedier/frp/pkg/util/util"
	"github.com/fatedier/frp/pkg/util/xlog"
)

var ErrNoTunnelSession = errors.New("no tunnel session")

type XTCPVisitor struct {
	*BaseVisitor
	session       TunnelSession
	startTunnelCh chan struct{}
	retryLimiter  *rate.Limiter
	cancel        context.CancelFunc

	cfg *v1.XTCPVisitorConfig
}

func (sv *XTCPVisitor) Run() (err error) {
	sv.ctx, sv.cancel = context.WithCancel(sv.ctx)

	if sv.cfg.Protocol == "kcp" {
		sv.session = NewKCPTunnelSession()
	} else {
		sv.session = NewQUICTunnelSession(sv.clientCfg)
	}

	if sv.cfg.BindPort > 0 {
		sv.l, err = netx.ListenTCP(sv.ctx, net.JoinHostPort(sv.cfg.BindAddr, strconv.Itoa(sv.cfg.BindPort)))
		if err != nil {
			sv.cancel()
			return
		}
		if !lifetime.Go(sv.ctx, func() { sv.acceptLoop(sv.l, "xtcp local", sv.handleConn) }) {
			sv.Close()
			return context.Canceled
		}
	}

	if !lifetime.Go(sv.ctx, func() { sv.acceptLoop(sv.internalLn, "xtcp internal", sv.handleConn) }) ||
		!lifetime.Go(sv.ctx, sv.processTunnelStartEvents) {
		sv.Close()
		return context.Canceled
	}
	if sv.cfg.KeepTunnelOpen {
		sv.retryLimiter = rate.NewLimiter(rate.Every(time.Hour/time.Duration(sv.cfg.MaxRetriesAnHour)), sv.cfg.MaxRetriesAnHour)
		if !lifetime.Go(sv.ctx, sv.keepTunnelOpenWorker) {
			sv.Close()
			return context.Canceled
		}
	}

	if sv.plugin != nil {
		sv.plugin.Start()
	}
	return
}

func (sv *XTCPVisitor) Close() {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	sv.BaseVisitor.Close()
	if sv.cancel != nil {
		sv.cancel()
	}
	if sv.session != nil {
		sv.session.Close()
	}
}

func (sv *XTCPVisitor) processTunnelStartEvents() {
	for {
		select {
		case <-sv.ctx.Done():
			return
		case <-sv.startTunnelCh:
			// A delayed failure from an older connection can arrive after a
			// replacement has already been established.
			if sv.session.Ready() {
				continue
			}
			start := time.Now()
			sv.makeNatHole()
			duration := time.Since(start)
			// avoid too frequently
			if duration < 10*time.Second {
				timer := time.NewTimer(10*time.Second - duration)
				select {
				case <-sv.ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
		}
	}
}

func (sv *XTCPVisitor) keepTunnelOpenWorker() {
	xl := xlog.FromContextSafe(sv.ctx)
	ticker := time.NewTicker(time.Duration(sv.cfg.MinRetryInterval) * time.Second)
	defer ticker.Stop()

	select {
	case sv.startTunnelCh <- struct{}{}:
	case <-sv.ctx.Done():
		return
	}
	for {
		select {
		case <-sv.ctx.Done():
			return
		case <-ticker.C:
			xl.Debugf("keepTunnelOpenWorker try to check tunnel...")
			conn, err := sv.getTunnelConn(sv.ctx)
			if err != nil {
				xl.Warnf("keepTunnelOpenWorker get tunnel connection error: %v", err)
				_ = sv.retryLimiter.Wait(sv.ctx)
				continue
			}
			xl.Debugf("keepTunnelOpenWorker check success")
			if conn != nil {
				conn.Close()
			}
		}
	}
}

func (sv *XTCPVisitor) handleConn(userConn net.Conn) {
	xl := xlog.FromContextSafe(sv.ctx)
	isConnTransferred := false
	var tunnelErr error
	defer func() {
		if !isConnTransferred {
			// If there was an error and connection supports CloseWithError, use it
			if tunnelErr != nil {
				if eConn, ok := userConn.(interface{ CloseWithError(error) error }); ok {
					_ = eConn.CloseWithError(tunnelErr)
					return
				}
			}
			userConn.Close()
		}
	}()

	xl.Debugf("get a new xtcp user connection")

	// Open a tunnel connection to the server. If there is already a successful hole-punching connection,
	// it will be reused. Otherwise, it will block and wait for a successful hole-punching connection until timeout.
	ctx := sv.ctx
	if sv.cfg.FallbackTo != "" {
		timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(sv.cfg.FallbackTimeoutMs)*time.Millisecond)
		defer cancel()
		ctx = timeoutCtx
	}
	tunnelConn, err := sv.openTunnel(ctx)
	if err != nil {
		xl.Errorf("open tunnel error: %v", err)
		tunnelErr = err

		// no fallback, just return
		if sv.cfg.FallbackTo == "" {
			return
		}

		xl.Debugf("try to transfer connection to visitor: %s", sv.cfg.FallbackTo)
		if err := sv.helper.TransferConn(sv.cfg.FallbackTo, attributeConn(userConn, sv.trafficCounter)); err != nil {
			xl.Errorf("transfer connection to visitor %s error: %v", sv.cfg.FallbackTo, err)
			return
		}
		isConnTransferred = true
		return
	}

	muxConnRWCloser, recycleFn, err := wrapVisitorConn(tunnelConn, sv.cfg.GetBaseConfig())
	if err != nil {
		xl.Errorf("%v", err)
		tunnelConn.Close()
		tunnelErr = err
		return
	}
	defer recycleFn()
	if counter := counterForConn(userConn, sv.trafficCounter); counter != nil {
		muxConnRWCloser = traffic.WrapStream(muxConnRWCloser, counter)
	}

	_, _, errs := libio.Join(userConn, muxConnRWCloser)
	xl.Debugf("join connections closed")
	if len(errs) > 0 {
		xl.Tracef("join connections errors: %v", errs)
	}
}

// openTunnel will open a tunnel connection to the target server.
func (sv *XTCPVisitor) openTunnel(ctx context.Context) (conn net.Conn, err error) {
	xl := xlog.FromContextSafe(sv.ctx)
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-sv.ctx.Done():
			return nil, sv.ctx.Err()
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, fmt.Errorf("open tunnel timeout")
			}
			return nil, ctx.Err()
		case <-timer.C:
			conn, err = sv.getTunnelConn(ctx)
			if err != nil {
				if !errors.Is(err, ErrNoTunnelSession) {
					xl.Warnf("get tunnel connection error: %v", err)
				}
				timer.Reset(500 * time.Millisecond)
				continue
			}
			return conn, nil
		}
	}
}

func (sv *XTCPVisitor) getTunnelConn(ctx context.Context) (net.Conn, error) {
	conn, err := sv.session.OpenConn(ctx)
	if err == nil {
		return conn, nil
	}
	if ctx.Err() != nil {
		return nil, err
	}
	select {
	case sv.startTunnelCh <- struct{}{}:
	default:
	}
	return nil, err
}

// 0. PreCheck
// 1. Prepare
// 2. ExchangeInfo
// 3. MakeNATHole
// 4. Create a tunnel session using an underlying UDP connection.
func (sv *XTCPVisitor) makeNatHole() {
	xl := xlog.FromContextSafe(sv.ctx)
	targetProxyName := naming.BuildTargetServerProxyName(sv.clientCfg.User, sv.cfg.ServerUser, sv.cfg.ServerName)
	xl.Tracef("makeNatHole start")
	if err := nathole.PreCheck(sv.ctx, sv.helper.MsgTransporter(), targetProxyName, 5*time.Second); err != nil {
		xl.Warnf("nathole precheck error: %v", err)
		return
	}

	xl.Tracef("nathole prepare start")

	// Prepare NAT traversal options
	var opts nathole.PrepareOptions
	if sv.cfg.NatTraversal != nil && sv.cfg.NatTraversal.DisableAssistedAddrs {
		opts.DisableAssistedAddrs = true
	}
	if netx.FromContext(sv.ctx) != nil {
		opts.ResolveUDPAddr = netx.ResolveUDPAddr
	}

	prepareResult, err := nathole.PrepareContext(sv.ctx, []string{sv.clientCfg.NatHoleSTUNServer}, opts)
	if err != nil {
		xl.Warnf("nathole prepare error: %v", err)
		return
	}

	xl.Infof("nathole prepare success, nat type: %s, behavior: %s, addresses: %v, assistedAddresses: %v",
		prepareResult.NatType, prepareResult.Behavior, prepareResult.Addrs, prepareResult.AssistedAddrs)

	listenConn := prepareResult.ListenConn
	stopInitial := context.AfterFunc(sv.ctx, func() { _ = prepareResult.ListenConn.Close() })
	defer stopInitial()

	// send NatHoleVisitor to server
	now := time.Now().Unix()
	transactionID := nathole.NewTransactionID()
	natHoleVisitorMsg := &msg.NatHoleVisitor{
		TransactionID: transactionID,
		ProxyName:     targetProxyName,
		Protocol:      sv.cfg.Protocol,
		SignKey:       util.GetAuthKey(sv.cfg.SecretKey, now),
		Timestamp:     now,
		MappedAddrs:   prepareResult.Addrs,
		AssistedAddrs: prepareResult.AssistedAddrs,
	}

	xl.Tracef("nathole exchange info start")
	natHoleRespMsg, err := nathole.ExchangeInfo(sv.ctx, sv.helper.MsgTransporter(), transactionID, natHoleVisitorMsg, 5*time.Second)
	if err != nil {
		listenConn.Close()
		xl.Warnf("nathole exchange info error: %v", err)
		return
	}

	xl.Infof("get natHoleRespMsg, sid [%s], protocol [%s], candidate address %v, assisted address %v, detectBehavior: %+v",
		natHoleRespMsg.Sid, natHoleRespMsg.Protocol, natHoleRespMsg.CandidateAddrs,
		natHoleRespMsg.AssistedAddrs, natHoleRespMsg.DetectBehavior)

	newListenConn, raddr, err := nathole.MakeHole(sv.ctx, listenConn, natHoleRespMsg, []byte(sv.cfg.SecretKey))
	if err != nil {
		listenConn.Close()
		xl.Warnf("make hole error: %v", err)
		return
	}
	listenConn = newListenConn
	stopSelected := context.AfterFunc(sv.ctx, func() { _ = newListenConn.Close() })
	defer stopSelected()
	xl.Infof("establishing nat hole connection successful, sid [%s], remoteAddr [%s]", natHoleRespMsg.Sid, raddr)

	if err := sv.session.Init(sv.ctx, listenConn, raddr); err != nil {
		listenConn.Close()
		xl.Warnf("init tunnel session error: %v", err)
		return
	}
}

type TunnelSession interface {
	Init(context.Context, *net.UDPConn, *net.UDPAddr) error
	OpenConn(context.Context) (net.Conn, error)
	Ready() bool
	Close()
}

type KCPTunnelSession struct {
	session   *fmux.Session
	lConn     *net.UDPConn
	openSlots chan struct{}
	mu        sync.RWMutex
}

func NewKCPTunnelSession() TunnelSession {
	return &KCPTunnelSession{openSlots: make(chan struct{}, 32)}
}

func (ks *KCPTunnelSession) Init(ctx context.Context, listenConn *net.UDPConn, raddr *net.UDPAddr) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	listenConn.Close()
	laddr, _ := net.ResolveUDPAddr("udp", listenConn.LocalAddr().String())
	lConn, err := net.DialUDP("udp", laddr, raddr)
	if err != nil {
		return fmt.Errorf("dial udp error: %v", err)
	}
	remote, err := netpkg.NewKCPConnFromUDP(lConn, true, raddr.String())
	if err != nil {
		lConn.Close()
		return fmt.Errorf("create kcp connection from udp connection error: %v", err)
	}

	fmuxCfg := fmux.DefaultConfig()
	fmuxCfg.KeepAliveInterval = 10 * time.Second
	fmuxCfg.MaxStreamWindowSize = 6 * 1024 * 1024
	fmuxCfg.LogOutput = io.Discard
	session, err := fmux.Client(remote, fmuxCfg)
	if err != nil {
		remote.Close()
		lConn.Close()
		return fmt.Errorf("initial client session error: %v", err)
	}
	ks.mu.Lock()
	if err := ctx.Err(); err != nil {
		ks.mu.Unlock()
		_ = session.Close()
		_ = lConn.Close()
		return err
	}
	previous, previousConn := ks.session, ks.lConn
	ks.session, ks.lConn = session, lConn
	ks.mu.Unlock()
	if previous != nil {
		_ = previous.Close()
	}
	if previousConn != nil {
		_ = previousConn.Close()
	}
	return nil
}

func (ks *KCPTunnelSession) OpenConn(ctx context.Context) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ks.mu.RLock()
	session := ks.session
	ks.mu.RUnlock()
	if session == nil {
		return nil, ErrNoTunnelSession
	}
	// yamux.Open does not accept a context and may wait for an available SYN
	// slot. Bound the number of outstanding opens while allowing the caller's
	// fallback deadline to expire without closing other active streams.
	select {
	case ks.openSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-session.CloseChan():
		return nil, ErrNoTunnelSession
	}
	type openResult struct {
		conn net.Conn
		err  error
	}
	result := make(chan openResult)
	go func() {
		defer func() { <-ks.openSlots }()
		conn, err := session.Open()
		select {
		case result <- openResult{conn, err}:
		case <-ctx.Done():
			if conn != nil {
				_ = conn.Close()
			}
		}
	}()
	var conn net.Conn
	var err error
	select {
	case opened := <-result:
		conn, err = opened.conn, opened.err
		if ctx.Err() != nil {
			if conn != nil {
				_ = conn.Close()
			}
			return nil, ctx.Err()
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err != nil {
		// Another goroutine may have installed a replacement while Open was
		// failing. Only retire the session that actually failed.
		ks.mu.Lock()
		if ks.session == session {
			ks.session = nil
			lConn := ks.lConn
			ks.lConn = nil
			ks.mu.Unlock()
			_ = session.Close()
			if lConn != nil {
				_ = lConn.Close()
			}
		} else {
			ks.mu.Unlock()
		}
	}
	return conn, err
}

func (ks *KCPTunnelSession) Ready() bool {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	return ks.session != nil && !ks.session.IsClosed()
}

func (ks *KCPTunnelSession) Close() {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	if ks.session != nil {
		_ = ks.session.Close()
		ks.session = nil
	}
	if ks.lConn != nil {
		_ = ks.lConn.Close()
		ks.lConn = nil
	}
}

type QUICTunnelSession struct {
	session    *quic.Conn
	listenConn *net.UDPConn
	mu         sync.RWMutex

	clientCfg *v1.ClientCommonConfig
}

func NewQUICTunnelSession(clientCfg *v1.ClientCommonConfig) TunnelSession {
	return &QUICTunnelSession{
		clientCfg: clientCfg,
	}
}

func (qs *QUICTunnelSession) Init(ctx context.Context, listenConn *net.UDPConn, raddr *net.UDPAddr) error {
	tlsConfig, err := transport.NewClientTLSConfig("", "", "", raddr.String())
	if err != nil {
		return fmt.Errorf("create tls config error: %v", err)
	}
	tlsConfig.NextProtos = []string{"frp"}
	quicConn, err := quic.Dial(ctx, listenConn, raddr, tlsConfig,
		&quic.Config{
			MaxIdleTimeout:     time.Duration(qs.clientCfg.Transport.QUIC.MaxIdleTimeout) * time.Second,
			MaxIncomingStreams: int64(qs.clientCfg.Transport.QUIC.MaxIncomingStreams),
			KeepAlivePeriod:    time.Duration(qs.clientCfg.Transport.QUIC.KeepalivePeriod) * time.Second,
		})
	if err != nil {
		return fmt.Errorf("dial quic error: %v", err)
	}
	qs.mu.Lock()
	if err := ctx.Err(); err != nil {
		qs.mu.Unlock()
		_ = quicConn.CloseWithError(0, "")
		return err
	}
	previous, previousConn := qs.session, qs.listenConn
	qs.session, qs.listenConn = quicConn, listenConn
	qs.mu.Unlock()
	if previous != nil {
		_ = previous.CloseWithError(0, "")
	}
	if previousConn != nil && previousConn != listenConn {
		_ = previousConn.Close()
	}
	return nil
}

func (qs *QUICTunnelSession) OpenConn(ctx context.Context) (net.Conn, error) {
	qs.mu.RLock()
	session := qs.session
	qs.mu.RUnlock()
	if session == nil {
		return nil, ErrNoTunnelSession
	}
	stream, err := session.OpenStreamSync(ctx)
	if err != nil {
		// A caller's timeout does not make an otherwise healthy shared
		// tunnel unusable for other connections.
		if ctx.Err() == nil || session.Context().Err() != nil {
			qs.mu.Lock()
			if qs.session == session {
				qs.session = nil
				listenConn := qs.listenConn
				qs.listenConn = nil
				qs.mu.Unlock()
				_ = session.CloseWithError(0, "")
				if listenConn != nil {
					_ = listenConn.Close()
				}
			} else {
				qs.mu.Unlock()
			}
		}
		return nil, err
	}
	return netpkg.QuicStreamToNetConn(stream, session), nil
}

func (qs *QUICTunnelSession) Ready() bool {
	qs.mu.RLock()
	defer qs.mu.RUnlock()
	return qs.session != nil && qs.session.Context().Err() == nil
}

func (qs *QUICTunnelSession) Close() {
	qs.mu.Lock()
	defer qs.mu.Unlock()
	if qs.session != nil {
		_ = qs.session.CloseWithError(0, "")
		qs.session = nil
	}
	if qs.listenConn != nil {
		_ = qs.listenConn.Close()
		qs.listenConn = nil
	}
}
