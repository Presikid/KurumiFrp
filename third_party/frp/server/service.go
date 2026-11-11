// Modified by KurumiFRP contributors: route and protect instance logs.

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

// Modified by KurumiFRP: manage service lifecycles and isolate metrics.

// Modified by KurumiFRP: join admitted server work during shutdown.

package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	fmux "github.com/fatedier/yamux"
	quic "github.com/quic-go/quic-go"
	"github.com/samber/lo"

	"github.com/fatedier/frp/pkg/auth"
	v1 "github.com/fatedier/frp/pkg/config/v1"
	"github.com/fatedier/frp/pkg/config/v1/validation"
	modelmetrics "github.com/fatedier/frp/pkg/metrics"
	"github.com/fatedier/frp/pkg/metrics/mem"
	"github.com/fatedier/frp/pkg/msg"
	"github.com/fatedier/frp/pkg/nathole"
	plugin "github.com/fatedier/frp/pkg/plugin/server"
	"github.com/fatedier/frp/pkg/proto/wire"
	"github.com/fatedier/frp/pkg/ssh"
	"github.com/fatedier/frp/pkg/transport"
	mux "github.com/fatedier/frp/pkg/util/connmux"
	httppkg "github.com/fatedier/frp/pkg/util/http"
	"github.com/fatedier/frp/pkg/util/lifetime"
	netpkg "github.com/fatedier/frp/pkg/util/net"
	"github.com/fatedier/frp/pkg/util/tcpmux"
	"github.com/fatedier/frp/pkg/util/util"
	"github.com/fatedier/frp/pkg/util/version"
	"github.com/fatedier/frp/pkg/util/vhost"
	"github.com/fatedier/frp/pkg/util/xlog"
	"github.com/fatedier/frp/server/controller"
	"github.com/fatedier/frp/server/group"
	"github.com/fatedier/frp/server/ports"
	"github.com/fatedier/frp/server/proxy"
	"github.com/fatedier/frp/server/registry"
	"github.com/fatedier/frp/server/traffic"
	"github.com/fatedier/frp/server/visitor"
)

const (
	connReadTimeout        time.Duration = 10 * time.Second
	connWriteTimeout       time.Duration = 5 * time.Second
	vhostReadWriteTimeout  time.Duration = 30 * time.Second
	serviceShutdownTimeout               = 10 * time.Second
)

var (
	errControlReplaced = errors.New("control was replaced during login")
	// ErrServiceClosed is returned when Run is called after Close.
	ErrServiceClosed = errors.New("server service is closed")
	// ErrServiceRunning is returned when Run is called more than once concurrently.
	ErrServiceRunning = errors.New("server service is already running")
	// ErrWorkerPanicked omits the panic value, which could contain secrets.
	ErrWorkerPanicked = errors.New("server service worker panicked")
)

// ConstructionCleanupError retains a cleanup handle when constructor rollback
// cannot finish within the normal shutdown timeout.
type ConstructionCleanupError struct {
	err     error
	service *Service
}

func (e *ConstructionCleanupError) Error() string { return e.err.Error() }
func (e *ConstructionCleanupError) Unwrap() error { return e.err }

// WaitCleanup waits for the failed construction's background cleanup.
func (e *ConstructionCleanupError) WaitCleanup(ctx context.Context) error {
	err := e.service.CloseContext(ctx)
	if e.service.workerPanicked.Load() {
		err = errors.Join(err, ErrWorkerPanicked)
	}
	return err
}

type serveResult struct {
	name string
	err  error
}

// Server service
type Service struct {
	xl *xlog.Logger
	// Dispatch connections to different handlers listen on same port
	muxer *mux.Mux

	// Accept connections from client
	listener net.Listener

	// Accept connections using kcp
	kcpListener net.Listener

	// Accept connections using quic
	quicListener *quic.Listener

	// Accept connections using websocket
	websocketListener net.Listener

	// Accept WebSockets after the external TLS handshake.
	wssListener *wssBridge

	// Accept frp tls connections
	tlsListener net.Listener

	// Accept pipe connections from ssh tunnel gateway
	sshTunnelListener *netpkg.InternalListener

	// Manage all controllers
	ctlManager *ControlManager

	// Track logical clients keyed by user.clientID (runID fallback when raw clientID is empty).
	clientRegistry *registry.ClientRegistry

	// Manage all proxies
	pxyManager *proxy.Manager

	// Manage all plugins
	pluginManager *plugin.Manager

	// HTTP vhost router
	httpVhostRouter *vhost.Routers

	// All resource managers and controllers
	rc *controller.ResourceController
	// Each service has its own counter, including when metrics endpoints are off.
	trafficCounter *traffic.ServerCounter
	// Optional instance-owned admission policy for frp control connections.
	allowControlConnection func(net.Addr) bool
	managedLogin           ManagedLoginResolver
	accessLogMu            sync.Mutex
	accessLogAt            time.Time
	accessLogSuppressed    uint64

	// web server for dashboard UI and apis
	webServer     *httppkg.Server
	metricsServer *httppkg.Server
	metrics       *modelmetrics.Server
	// HTTP vhost resources are started and owned by NewService.
	httpVhostServer   *http.Server
	httpVhostListener net.Listener

	sshTunnelGateway *ssh.Gateway

	// Auth runtime and encryption materials
	auth *auth.ServerAuth

	tlsConfig *tls.Config

	cfg *v1.ServerConfig

	// service context
	ctx context.Context
	// call cancel to stop service
	cancel         context.CancelFunc
	lifecycleMu    sync.Mutex
	closed         bool
	running        bool
	closeOnce      sync.Once
	closeDone      chan struct{}
	ready          chan struct{}
	exiting        chan struct{}
	serveErrors    chan serveResult
	workScope      *lifetime.Scope
	workCtx        context.Context
	workCancel     context.CancelFunc
	workerPanicked atomic.Bool
}

// ServiceOptions controls additional features for an embedded server.
type ServiceOptions struct {
	// Context cancels authentication setup and verification for this service.
	// A nil value uses context.Background for standalone callers.
	Context context.Context
	// EnableMemoryMetrics allows in-process statistics queries without a dashboard.
	EnableMemoryMetrics bool
	// LogSink is dedicated to this service and consumed even if construction fails.
	LogSink *xlog.Sink
	// TrafficRecorder receives the same forwarding deltas as the service counter.
	// Supply a distinct recorder per service unless aggregate totals are intended.
	TrafficRecorder TrafficRecorder
	// AllowControlConnection is called for each connection to the frp control
	// listener, including work and visitor connections. It must be concurrent-
	// safe, prompt and non-panicking. It does not filter public proxy traffic.
	AllowControlConnection func(net.Addr) bool
	// ManagedLogin replaces shared-token authentication only when explicitly
	// supplied. It affects new control logins and does not revoke existing work.
	// Clients must enable auth.additionalScopes = ["NewWorkConns"]; otherwise
	// a control login can succeed while its work connections are rejected.
	ManagedLogin ManagedLoginResolver
	// OnWorkerPanic enables instance-scoped worker recovery and notification.
	// It must be concurrent-safe, return promptly, and not panic. It receives no
	// panic value and must cause the owning instance to stop.
	OnWorkerPanic func()
}

// TrafficRecorder receives forwarding deltas from a service. Calls may be
// concurrent; implementations must return promptly and must not panic.
type TrafficRecorder = controller.TrafficRecorder

func NewService(cfg *v1.ServerConfig) (*Service, error) {
	return NewServiceWithOptions(cfg, ServiceOptions{})
}

// NewServiceWithTrafficRecorder retains the recorder injection API.
func NewServiceWithTrafficRecorder(cfg *v1.ServerConfig, recorder TrafficRecorder) (*Service, error) {
	return NewServiceWithOptions(cfg, ServiceOptions{TrafficRecorder: recorder})
}

// NewServiceWithLogSink binds a dedicated sink to this service.
func NewServiceWithLogSink(cfg *v1.ServerConfig, sink *xlog.Sink) (*Service, error) {
	return NewServiceWithOptions(cfg, ServiceOptions{LogSink: sink})
}

// NewServiceWithOptions creates a server with instance-specific options.
func NewServiceWithOptions(cfg *v1.ServerConfig, options ServiceOptions) (result *Service, resultErr error) {
	if options.ManagedLogin != nil {
		if err := ValidateManagedLoginConfig(cfg); err != nil {
			return nil, err
		}
	}
	if options.LogSink != nil {
		if err := options.LogSink.Claim(); err != nil {
			return nil, err
		}
	}
	xl := xlog.NewWithSink(options.LogSink)
	tlsConfig, err := transport.NewServerTLSConfig(
		cfg.Transport.TLS.CertFile,
		cfg.Transport.TLS.KeyFile,
		cfg.Transport.TLS.TrustedCaFile)
	if err != nil {
		return nil, err
	}

	authRuntime, err := auth.BuildServerAuth(options.Context, &cfg.Auth)
	if err != nil {
		return nil, err
	}

	clientRegistry := registry.NewClientRegistry()
	trafficCounter := &traffic.ServerCounter{}
	var svr *Service
	workScope := lifetime.New()
	if options.OnWorkerPanic != nil {
		workScope = lifetime.NewWithPanicHandler(func() {
			svr.workerPanicked.Store(true)
			select {
			case svr.serveErrors <- serveResult{name: "worker", err: ErrWorkerPanicked}:
			default:
			}
			options.OnWorkerPanic()
		})
	}
	baseWorkCtx, workCancel := context.WithCancel(context.Background())
	workCtx := xlog.NewContext(lifetime.WithScope(baseWorkCtx, workScope), xl)
	svr = &Service{
		xl:                     xl,
		ctlManager:             NewControlManager(clientRegistry),
		clientRegistry:         clientRegistry,
		allowControlConnection: options.AllowControlConnection,
		managedLogin:           options.ManagedLogin,
		pxyManager:             proxy.NewManager(),
		pluginManager:          plugin.NewManagerWithLogger(xl),
		rc: &controller.ResourceController{
			VisitorManager:  visitor.NewManager(),
			TrafficRecorder: &serviceTrafficRecorder{counter: trafficCounter, observer: options.TrafficRecorder},
		},
		trafficCounter:    trafficCounter,
		sshTunnelListener: netpkg.NewInternalListener(),
		httpVhostRouter:   vhost.NewRouters(),
		auth:              authRuntime,
		tlsConfig:         tlsConfig,
		cfg:               cfg,
		ctx:               workCtx,
		workCtx:           workCtx,
		workScope:         workScope,
		workCancel:        workCancel,
		closeDone:         make(chan struct{}),
		ready:             make(chan struct{}),
		exiting:           make(chan struct{}),
		serveErrors:       make(chan serveResult, 1),
	}
	// The constructor owns each resource as soon as it is created. Roll back
	// all of them if any subsequent step fails.
	ready := false
	defer func() {
		if !ready {
			if err := svr.Close(); err != nil {
				resultErr = &ConstructionCleanupError{
					err:     errors.Join(resultErr, fmt.Errorf("constructor rollback: %w", err)),
					service: svr,
				}
			}
			if svr.workerPanicked.Load() {
				resultErr = errors.Join(resultErr, ErrWorkerPanicked)
			}
		}
	}()

	// Create tcpmux httpconnect multiplexer.
	if cfg.TCPMuxHTTPConnectPort > 0 {
		var l net.Listener
		address := net.JoinHostPort(cfg.ProxyBindAddr, strconv.Itoa(cfg.TCPMuxHTTPConnectPort))
		l, err = net.Listen("tcp", address)
		if err != nil {
			return nil, fmt.Errorf("create server listener error, %v", err)
		}

		svr.rc.TCPMuxHTTPConnectMuxer, err = tcpmux.NewHTTPConnectTCPMuxerWithContext(svr.workCtx, l, cfg.TCPMuxPassthrough, vhostReadWriteTimeout, cfg.Custom404Page)
		if err != nil {
			_ = l.Close()
			return nil, fmt.Errorf("create vhost tcpMuxer error, %v", err)
		}
		svr.startWorker("tcp http-connect muxer", svr.rc.TCPMuxHTTPConnectMuxer.Wait)
		xl.Infof("tcpmux httpconnect multiplexer listen on %s, passthrough: %v", address, cfg.TCPMuxPassthrough)
	}

	// Init all plugins
	for _, p := range cfg.HTTPPlugins {
		svr.pluginManager.Register(plugin.NewHTTPPluginOptions(p))
		xl.Infof("plugin [%s] has been registered", p.Name)
	}
	svr.rc.PluginManager = svr.pluginManager

	// Init HTTP group controller
	svr.rc.HTTPGroupCtl = group.NewHTTPGroupController(svr.httpVhostRouter)

	// Init TCP mux group controller
	svr.rc.TCPMuxGroupCtl = group.NewTCPMuxGroupCtl(svr.rc.TCPMuxHTTPConnectMuxer)

	var (
		httpMuxOn  bool
		httpsMuxOn bool
	)
	if cfg.BindAddr == cfg.ProxyBindAddr {
		if cfg.BindPort == cfg.VhostHTTPPort {
			httpMuxOn = true
		}
		if cfg.BindPort == cfg.VhostHTTPSPort {
			httpsMuxOn = true
		}
	}

	// Listen for accepting connections from client.
	address := net.JoinHostPort(cfg.BindAddr, strconv.Itoa(cfg.BindPort))
	ln, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("create server listener error, %v", err)
	}

	svr.muxer = mux.NewMuxWithContext(svr.workCtx, ln)
	svr.muxer.SetKeepAlive(time.Duration(cfg.Transport.TCPKeepAlive) * time.Second)
	svr.startWorker("tcp multiplexer", svr.muxer.Serve)
	ln = svr.muxer.DefaultListener()

	svr.listener = ln
	xl.Infof("frps tcp listen on %s", address)

	// Listen for accepting connections from client using kcp protocol.
	if cfg.KCPBindPort > 0 {
		address := net.JoinHostPort(cfg.BindAddr, strconv.Itoa(cfg.KCPBindPort))
		svr.kcpListener, err = netpkg.ListenKcpWithContext(svr.workCtx, address)
		if err != nil {
			return nil, fmt.Errorf("listen on kcp udp address %s error: %v", address, err)
		}
		xl.Infof("frps kcp listen on udp %s", address)
	}

	if cfg.QUICBindPort > 0 {
		address := net.JoinHostPort(cfg.BindAddr, strconv.Itoa(cfg.QUICBindPort))
		quicTLSCfg := tlsConfig.Clone()
		quicTLSCfg.NextProtos = []string{"frp"}
		svr.quicListener, err = quic.ListenAddr(address, quicTLSCfg, &quic.Config{
			MaxIdleTimeout:     time.Duration(cfg.Transport.QUIC.MaxIdleTimeout) * time.Second,
			MaxIncomingStreams: int64(cfg.Transport.QUIC.MaxIncomingStreams),
			KeepAlivePeriod:    time.Duration(cfg.Transport.QUIC.KeepalivePeriod) * time.Second,
		})
		if err != nil {
			return nil, fmt.Errorf("listen on quic udp address %s error: %v", address, err)
		}
		xl.Infof("frps quic listen on %s", address)
	}

	if cfg.SSHTunnelGateway.BindPort > 0 {
		sshGateway, err := ssh.NewGatewayWithLogger(cfg.SSHTunnelGateway, cfg.BindAddr, svr.sshTunnelListener, xl)
		if err != nil {
			return nil, fmt.Errorf("create ssh gateway error: %v", err)
		}
		svr.sshTunnelGateway = sshGateway
		xl.Infof("frps sshTunnelGateway listen on port %d", cfg.SSHTunnelGateway.BindPort)
	}

	// Account login initially supports only the direct TCP/TLS listener.
	if options.ManagedLogin == nil {
		websocketPrefix := []byte("GET " + netpkg.FrpWebsocketPath)
		websocketLn := svr.muxer.Listen(0, uint32(len(websocketPrefix)), func(data []byte) bool {
			return bytes.Equal(data, websocketPrefix)
		})
		svr.websocketListener, err = netpkg.NewWebsocketListenerWithContext(svr.workCtx, websocketLn)
		if err != nil {
			return nil, fmt.Errorf("create websocket listener: %w", err)
		}
		svr.wssListener, err = newWSSBridge(svr.workCtx)
		if err != nil {
			return nil, fmt.Errorf("create secure websocket listener: %w", err)
		}
	}

	// Create http vhost muxer.
	if cfg.VhostHTTPPort > 0 {
		rp := vhost.NewHTTPReverseProxy(vhost.HTTPReverseProxyOptions{
			ResponseHeaderTimeoutS: cfg.VhostHTTPTimeout,
			Custom404Page:          cfg.Custom404Page,
			Logger:                 xl,
		}, svr.httpVhostRouter)
		svr.rc.HTTPReverseProxy = rp

		address := net.JoinHostPort(cfg.ProxyBindAddr, strconv.Itoa(cfg.VhostHTTPPort))
		protocols := new(http.Protocols)
		protocols.SetHTTP1(true)
		protocols.SetUnencryptedHTTP2(true)
		server := &http.Server{
			Addr: address,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				done, ok := lifetime.Enter(r.Context())
				if !ok {
					http.Error(w, "server closing", http.StatusServiceUnavailable)
					return
				}
				defer done()
				rp.ServeHTTP(w, r)
			}),
			BaseContext:       func(net.Listener) context.Context { return svr.workCtx },
			ReadHeaderTimeout: 60 * time.Second,
			Protocols:         protocols,
		}
		var l net.Listener
		if httpMuxOn {
			l = svr.muxer.ListenHTTP(1)
		} else {
			l, err = net.Listen("tcp", address)
			if err != nil {
				return nil, fmt.Errorf("create vhost http listener error, %v", err)
			}
		}
		svr.httpVhostServer = server
		svr.httpVhostListener = l
		svr.startWorker("http vhost", func() error { return server.Serve(l) })
		xl.Infof("http service listen on %s", address)
	}

	// Create https vhost muxer.
	if cfg.VhostHTTPSPort > 0 {
		var l net.Listener
		if httpsMuxOn {
			l = svr.muxer.ListenHTTPS(1)
		} else {
			address := net.JoinHostPort(cfg.ProxyBindAddr, strconv.Itoa(cfg.VhostHTTPSPort))
			l, err = net.Listen("tcp", address)
			if err != nil {
				return nil, fmt.Errorf("create server listener error, %v", err)
			}
			xl.Infof("https service listen on %s", address)
		}

		svr.rc.VhostHTTPSMuxer, err = vhost.NewHTTPSMuxerWithContext(svr.workCtx, l, vhostReadWriteTimeout)
		if err != nil {
			_ = l.Close()
			return nil, fmt.Errorf("create vhost httpsMuxer error, %v", err)
		}
		svr.startWorker("https vhost muxer", svr.rc.VhostHTTPSMuxer.Wait)

		// Init HTTPS group controller after HTTPSMuxer is created
		svr.rc.HTTPSGroupCtl = group.NewHTTPSGroupController(svr.rc.VhostHTTPSMuxer)
	}

	// frp tls listener
	svr.tlsListener = svr.muxer.Listen(2, 1, func(data []byte) bool {
		// tls first byte can be 0x16 only when vhost https port is not same with bind port
		return int(data[0]) == netpkg.FRPTLSHeadByte || int(data[0]) == 0x16
	})

	// Create nat hole controller.
	nc, err := nathole.NewControllerWithLogger(time.Duration(cfg.NatHoleAnalysisDataReserveHours)*time.Hour, xl)
	if err != nil {
		return nil, fmt.Errorf("create nat hole controller error, %v", err)
	}
	svr.rc.NatHoleController = nc

	// The service owns its collectors; constructor rollback closes them too.
	enableMem, enablePrometheus := metricsCollectors(cfg)
	svr.metrics = modelmetrics.NewServerWithContext(svr.workCtx, enableMem || options.EnableMemoryMetrics, enablePrometheus)

	// Create HTTP servers after all other fallible steps.
	if cfg.WebServer.Port > 0 {
		ws, err := httppkg.NewServer(cfg.WebServer)
		if err != nil {
			return nil, err
		}
		svr.webServer = ws
	}
	if cfg.MetricsServer.Port > 0 {
		ms, err := httppkg.NewMetricsServer(cfg.MetricsServer, svr.metrics.Handler())
		if err != nil {
			return nil, err
		}
		svr.metricsServer = ms
	}

	// Start port-manager cleanup workers only after all fallible steps have succeeded.
	svr.rc.TCPPortManager = ports.NewManagerWithContext(svr.workCtx, "tcp", cfg.ProxyBindAddr, cfg.AllowPorts)
	svr.rc.UDPPortManager = ports.NewManagerWithContext(svr.workCtx, "udp", cfg.ProxyBindAddr, cfg.AllowPorts)
	svr.rc.TCPGroupCtl = group.NewTCPGroupCtlWithContext(svr.workCtx, svr.rc.TCPPortManager)

	if svr.webServer != nil {
		svr.webServer.RouteRegister(svr.registerRouteHandlers)
	}
	ready = true
	return svr, nil
}

// metricsCollectors reports which collectors should be started for cfg. The
// in-memory collector only backs the dashboard API (webServer), while the
// Prometheus collector needs a listener that serves /metrics: either the new
// metricsServer, or the legacy webServer endpoint gated by enablePrometheus.
func metricsCollectors(cfg *v1.ServerConfig) (mem bool, prometheus bool) {
	mem = cfg.WebServer.Port > 0
	prometheus = cfg.MetricsServer.Port > 0 || (cfg.EnablePrometheus && mem)
	return mem, prometheus
}

// StatsCollector returns this service's in-memory statistics, or nil if disabled.
func (svr *Service) StatsCollector() mem.Collector {
	if svr.metrics == nil {
		return nil
	}
	return svr.metrics.StatsCollector()
}

// TrafficSnapshot returns a consistent copy of this service's forwarding
// counters, regardless of whether the dashboard or Prometheus is enabled.
func (svr *Service) TrafficSnapshot() traffic.ServerSnapshot {
	return svr.trafficCounter.Snapshot()
}

// ResetTraffic starts a new interval for this service's own counter. External
// recorders and the frp dashboard/Prometheus collectors have their own lifetime.
func (svr *Service) ResetTraffic() {
	svr.trafficCounter.Reset()
}

// Run serves until the context is canceled, Close is called, or a listener
// exits unexpectedly. Cancellation and Close return nil after cleanup;
// unexpected exits and shutdown timeouts return an error.
// A Service may only be run once.
func (svr *Service) Run(ctx context.Context) (runErr error) {
	ctx, cancel := context.WithCancel(ctx)
	ctx = lifetime.WithScope(ctx, svr.workScope)
	svr.lifecycleMu.Lock()
	if svr.closed {
		svr.lifecycleMu.Unlock()
		cancel()
		return errors.Join(ErrServiceClosed, svr.Close())
	}
	if svr.running {
		svr.lifecycleMu.Unlock()
		cancel()
		return ErrServiceRunning
	}
	svr.running = true
	svr.ctx = xlog.NewContext(ctx, svr.xl)
	svr.cancel = cancel
	svr.lifecycleMu.Unlock()
	defer func() {
		if err := svr.Close(); err != nil {
			runErr = errors.Join(runErr, err)
		}
		if svr.workerPanicked.Load() {
			runErr = errors.Join(runErr, ErrWorkerPanicked)
		}
	}()
	// Publish that the run loop has ended before waiting for resource cleanup.
	defer close(svr.exiting)
	if ctx.Err() != nil {
		return nil
	}
	// run dashboard web server.
	if svr.webServer != nil {
		if svr.cfg.WebServer.PprofEnable {
			svr.xl.Warnf("webServer pprof endpoints are deprecated; use metricsServer.pprofEnable instead")
		}
		if svr.cfg.EnablePrometheus {
			svr.xl.Warnf("webServer /metrics is deprecated; configure metricsServer.port instead")
		}
		if svr.cfg.MetricsServer.Port == 0 {
			svr.xl.Infof("webServer /healthz is deprecated; health probes can migrate to metricsServer.port")
		}
		svr.xl.Infof("dashboard listen on %s", svr.webServer.Address())
		svr.startWorker("dashboard", svr.webServer.Run)
	}

	if svr.metricsServer != nil {
		svr.xl.Infof("metrics server listen on %s", svr.metricsServer.Address())
		svr.startWorker("metrics server", svr.metricsServer.Run)
	}

	if svr.managedLogin == nil {
		svr.startWorker("ssh tunnel listener", func() error { return svr.HandleListener(svr.sshTunnelListener, true) })
	}

	if svr.kcpListener != nil {
		svr.startWorker("kcp listener", func() error { return svr.HandleListener(svr.kcpListener, false) })
	}
	if svr.quicListener != nil {
		svr.startWorker("quic listener", func() error { return svr.HandleQUICListener(svr.quicListener) })
	}
	if svr.websocketListener != nil {
		svr.startWorker("websocket listener", func() error { return svr.HandleListener(svr.websocketListener, false) })
	}
	if svr.wssListener != nil {
		svr.startWorker("wss listener", func() error { return svr.handleListener(svr.wssListener, false, true, false) })
	}
	svr.startWorker("tls listener", func() error { return svr.handleListener(svr.tlsListener, false, false, svr.managedLogin == nil) })

	if svr.rc.NatHoleController != nil {
		lifetime.Go(svr.workCtx, func() { svr.rc.NatHoleController.CleanWorker(svr.ctx) })
	}

	if svr.sshTunnelGateway != nil {
		svr.startWorker("ssh tunnel gateway", func() error {
			svr.sshTunnelGateway.Run()
			return nil
		})
	}

	// This is the last admission to the same monotonic work scope; if it is
	// rejected, at least one listener worker may not have started.
	if !svr.startWorker("tcp listener", func() error {
		if svr.managedLogin != nil {
			return svr.handleListener(svr.listener, false, false, false)
		}
		return svr.HandleListener(svr.listener, false)
	}) {
		if ctx.Err() != nil {
			return nil
		}
		return errors.New("server work scope stopped before readiness")
	}

	// Signal only after the listener workers have been admitted. An already
	// reported listener failure or cancellation takes precedence over readiness.
	select {
	case <-ctx.Done():
		return nil
	case result := <-svr.serveErrors:
		if result.err == nil {
			return fmt.Errorf("%s exited unexpectedly", result.name)
		}
		return fmt.Errorf("%s exited: %w", result.name, result.err)
	default:
	}
	close(svr.ready)

	select {
	case <-ctx.Done():
		return nil
	case result := <-svr.serveErrors:
		if ctx.Err() != nil {
			return nil
		}
		if result.err == nil {
			return fmt.Errorf("%s exited unexpectedly", result.name)
		}
		return fmt.Errorf("%s exited: %w", result.name, result.err)
	}
}

// Ready closes after Run admits its listener workers. It remains open when
// startup ends first; callers must also observe their completion signal.
func (svr *Service) Ready() <-chan struct{} { return svr.ready }

// Exiting closes when Run leaves its loop, before cleanup has completed. It
// may stay open if Run was rejected before it began; observe completion too.
func (svr *Service) Exiting() <-chan struct{} { return svr.exiting }

// WorkerPanicked reports a recovered instance-scoped worker panic. It says
// nothing about whether that worker's resources have finished cleaning up.
func (svr *Service) WorkerPanicked() bool { return svr.workerPanicked.Load() }

func (svr *Service) serve(name string, run func() error) {
	result := serveResult{name: name, err: run()}
	select {
	case svr.serveErrors <- result:
	default:
	}
}

func (svr *Service) startWorker(name string, run func() error) bool {
	return lifetime.Go(svr.workCtx, func() { svr.serve(name, run) })
}

// Close requests shutdown and waits up to the service shutdown timeout.
func (svr *Service) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), serviceShutdownTimeout)
	defer cancel()
	return svr.CloseContext(ctx)
}

// CloseContext waits for listeners and admitted controls to finish closing.
// On cancellation, cleanup continues and a later call can wait for completion.
func (svr *Service) CloseContext(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("server shutdown context is nil")
	}
	svr.lifecycleMu.Lock()
	svr.closed = true
	cancel := svr.cancel
	svr.lifecycleMu.Unlock()
	if cancel != nil {
		cancel()
	}
	svr.closeOnce.Do(func() {
		go func() {
			defer lifetime.Recover(svr.workCtx)
			svr.closeResources()
			close(svr.closeDone)
		}()
	})
	select {
	case <-svr.closeDone:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("waiting for server shutdown; proxy ports may still be in use: %w", ctx.Err())
	}
}

func (svr *Service) closeResources() {
	svr.workCancel()
	// Interrupt WSS sockets before stopping the shared scope. Closing an active
	// WebSocket can otherwise block while it writes a close frame to a peer.
	if svr.wssListener != nil {
		_ = svr.wssListener.Close()
	}
	svr.workScope.Stop()
	if svr.kcpListener != nil {
		svr.kcpListener.Close()
	}
	if svr.quicListener != nil {
		svr.quicListener.Close()
	}
	if svr.websocketListener != nil {
		svr.websocketListener.Close()
	}
	if svr.tlsListener != nil {
		svr.tlsListener.Close()
	}
	if svr.sshTunnelListener != nil {
		svr.sshTunnelListener.Close()
	}
	if svr.listener != nil {
		svr.listener.Close()
	}
	if svr.webServer != nil {
		svr.webServer.Close()
	}
	if svr.metricsServer != nil {
		svr.metricsServer.Close()
	}
	if svr.httpVhostServer != nil {
		svr.httpVhostServer.Close()
		// Close even if Serve has not registered its listener yet.
		svr.httpVhostListener.Close()
	}
	if svr.sshTunnelGateway != nil {
		svr.sshTunnelGateway.Close()
	}
	svr.rc.Close()
	if svr.muxer != nil {
		svr.muxer.Close()
	}
	_ = svr.ctlManager.CloseContext(context.Background())
	svr.workScope.Wait()
	if svr.metrics != nil {
		svr.metrics.Close()
	}
}

// Report rejected control connections to the instance's existing log stream.
// Bound log volume independently for each service during repeated attempts.
func (svr *Service) logDeniedControl(ctx context.Context, remote net.Addr) {
	now := time.Now()
	svr.accessLogMu.Lock()
	if !svr.accessLogAt.IsZero() && now.Sub(svr.accessLogAt) < time.Second {
		if svr.accessLogSuppressed < ^uint64(0) {
			svr.accessLogSuppressed++
		}
		svr.accessLogMu.Unlock()
		return
	}
	suppressed := svr.accessLogSuppressed
	svr.accessLogSuppressed = 0
	svr.accessLogAt = now
	svr.accessLogMu.Unlock()
	var ip net.IP
	switch addr := remote.(type) {
	case *net.TCPAddr:
		if addr != nil {
			ip = addr.IP
		}
	case *net.UDPAddr:
		if addr != nil {
			ip = addr.IP
		}
	}
	source := "unavailable"
	if ip != nil {
		source = ip.String()
	}
	xlog.FromContextSafe(ctx).Warnf("control connection denied by source policy; peer=%s, additional suppressed=%d", source, suppressed)
}

func (svr *Service) handleConnection(ctx context.Context, conn net.Conn, internal bool, session io.Closer) {
	if svr.managedLogin != nil && internal {
		_ = conn.Close()
		return
	}
	if svr.allowControlConnection != nil && !svr.allowControlConnection(conn.RemoteAddr()) {
		svr.logDeniedControl(ctx, conn.RemoteAddr())
		_ = conn.Close()
		return
	}
	xl := xlog.FromContextSafe(ctx)

	acceptedConn, err := svr.acceptConnection(ctx, conn)
	if err != nil {
		xl.Tracef("failed to accept frp connection: %v", err)
		conn.Close()
		return
	}
	conn = acceptedConn.conn

	switch m := acceptedConn.firstMsg.(type) {
	case *msg.Login:
		// server plugin hook
		content := &plugin.LoginContent{
			Login:         *m,
			ClientAddress: conn.RemoteAddr().String(),
		}
		retContent, err := svr.pluginManager.Login(content)
		var ctl *Control
		if err == nil {
			m = &retContent.Login
			var managed *managedLogin
			if svr.managedLogin != nil {
				managed, err = resolveManagedLogin(ctx, svr.managedLogin, m, svr.cfg.Auth.AdditionalScopes)
			}
			controlConn := acceptedConn.conn
			if err == nil && !internal {
				var controlRW io.ReadWriter
				key := svr.auth.EncryptionKey()
				if managed != nil {
					key = managed.key
				}
				controlRW, err = acceptedConn.newControlReadWriter(conn, key)
				if err == nil {
					controlConn = acceptedConn.messageConnFor(controlRW)
				}
			}
			if err == nil {
				ctl, err = svr.registerControl(controlConn, m, internal, acceptedConn.wireProtocol, acceptedConn.udpPacketCodec, session, managed)
			}
		}

		if err != nil {
			xl.Warnf("register control error: %v", err)
			if ctl != nil {
				svr.ctlManager.Remove(ctl)
			}
			if writeErr := writeWithDeadline(conn, connWriteTimeout, func() error {
				return acceptedConn.conn.WriteMsg(&msg.LoginResp{
					Version: version.Full(),
					Error:   util.GenerateResponseErrorString("register control error", err, lo.FromPtr(svr.cfg.DetailedErrorsToClient)),
				})
			}); writeErr != nil {
				xl.Warnf("write login error response error: %v", writeErr)
			}
			if ctl != nil {
				_ = ctl.Close()
			} else {
				conn.Close()
			}
			return
		}
		if err = svr.completeControlLogin(ctl, func() error {
			return writeWithDeadline(conn, connWriteTimeout, func() error {
				return acceptedConn.conn.WriteMsg(&msg.LoginResp{
					Version: version.Full(),
					RunID:   ctl.runID,
					Error:   "",
				})
			})
		}); err != nil {
			xl.Warnf("complete control login error: %v", err)
			svr.ctlManager.Remove(ctl)
			_ = ctl.Close()
			return
		}
	case *msg.NewWorkConn:
		if err := svr.registerWorkConn(
			acceptedConn.conn,
			m,
			acceptedConn.wireProtocol,
			acceptedConn.clientHelloPresent,
			svr.managedLogin != nil,
		); err != nil {
			writeError := func() error {
				return acceptedConn.conn.WriteMsg(&msg.StartWorkConn{
					Error: util.GenerateResponseErrorString("invalid NewWorkConn", err, lo.FromPtr(svr.cfg.DetailedErrorsToClient)),
				})
			}
			if svr.managedLogin != nil {
				_ = writeWithDeadline(conn, connWriteTimeout, writeError)
			} else {
				_ = writeError()
			}
			conn.Close()
		}
	case *msg.NewVisitorConn:
		// The visitor must receive its protocol response before a target can
		// send payload bytes on the same connection.
		pending := &visitorResponseConn{Conn: conn, done: make(chan struct{})}
		defer pending.finish(false)
		if err = svr.RegisterVisitorConn(pending, m, acceptedConn.wireProtocol); err != nil {
			pending.finish(false)
			xl.Warnf("register visitor conn error: %v", err)
			_ = writeWithDeadline(conn, connWriteTimeout, func() error {
				return acceptedConn.conn.WriteMsg(&msg.NewVisitorConnResp{
					ProxyName: m.ProxyName,
					Error:     util.GenerateResponseErrorString("register visitor conn error", err, lo.FromPtr(svr.cfg.DetailedErrorsToClient)),
				})
			})
			_ = conn.Close()
			return
		}
		if err = writeWithDeadline(conn, connWriteTimeout, func() error {
			return acceptedConn.conn.WriteMsg(&msg.NewVisitorConnResp{
				ProxyName: m.ProxyName,
				Error:     "",
			})
		}); err != nil {
			pending.finish(false)
			_ = conn.Close()
			return
		}
		pending.finish(true)
	default:
		xl.Warnf("error message type for the new connection [%s]", conn.RemoteAddr().String())
		conn.Close()
	}
}

// visitorResponseConn carries the admission result through the visitor's
// compression/encryption wrappers. The response writer alone publishes it;
// a failed or panicked handler must never leave a worker waiting indefinitely.
type visitorResponseConn struct {
	net.Conn
	done    chan struct{}
	once    sync.Once
	success bool
}

func (c *visitorResponseConn) finish(success bool) {
	c.once.Do(func() {
		c.success = success
		close(c.done)
	})
}

func (c *visitorResponseConn) WaitVisitorResponse(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case <-c.done:
		return c.success && ctx.Err() == nil
	}
}

func (svr *Service) completeControlLogin(ctl *Control, writeSuccess func() error) error {
	committed, err := svr.ctlManager.completeLogin(ctl, writeSuccess)
	if err != nil {
		return err
	}
	if !committed {
		return errControlReplaced
	}
	return nil
}

type acceptedConnection struct {
	conn               *msg.Conn
	wireProtocol       string
	clientHelloPresent bool
	udpPacketCodec     string
	cryptoContext      *wire.CryptoContext
	firstMsg           msg.Message
}

func (svr *Service) acceptConnection(ctx context.Context, conn net.Conn) (*acceptedConnection, error) {
	_ = conn.SetReadDeadline(time.Now().Add(connReadTimeout))
	checkedConn, isV2, err := wire.CheckMagic(conn)
	if err != nil {
		return nil, fmt.Errorf("read wire protocol magic: %w", err)
	}

	wireProtocol := wire.ProtocolV1
	if isV2 {
		wireProtocol = wire.ProtocolV2
	}

	conn = netpkg.NewContextConn(ctx, checkedConn)
	acceptedConn := &acceptedConnection{wireProtocol: wireProtocol}
	if isV2 {
		wireConn := wire.NewConn(conn)
		rw := msg.NewV2ReadWriterWithConn(wireConn)
		acceptedConn.conn = msg.NewConn(conn, rw)
		acceptedConn.firstMsg, err = acceptedConn.readFirstV2Msg(conn, wireConn)
	} else {
		rw := msg.NewV1ReadWriter(conn)
		acceptedConn.conn = msg.NewConn(conn, rw)
		acceptedConn.firstMsg, err = acceptedConn.conn.ReadMsg()
	}
	if err != nil {
		return nil, err
	}
	_ = conn.SetReadDeadline(time.Time{})
	return acceptedConn, nil
}

func writeWithDeadline(conn net.Conn, timeout time.Duration, writeFn func() error) error {
	_ = conn.SetWriteDeadline(time.Now().Add(timeout))
	defer func() {
		_ = conn.SetWriteDeadline(time.Time{})
	}()
	return writeFn()
}

func (ac *acceptedConnection) messageConnFor(rw io.ReadWriter) *msg.Conn {
	return msg.NewConn(ac.conn, msg.NewReadWriter(rw, ac.wireProtocol))
}

func (ac *acceptedConnection) newControlReadWriter(rw io.ReadWriter, key []byte) (io.ReadWriter, error) {
	if ac.wireProtocol == wire.ProtocolV2 {
		if ac.cryptoContext == nil {
			return nil, fmt.Errorf("missing v2 crypto negotiation")
		}
		return netpkg.NewAEADCryptoReadWriter(
			rw,
			key,
			netpkg.AEADCryptoRoleServer,
			ac.cryptoContext.Algorithm,
			ac.cryptoContext.TranscriptHash,
		)
	}
	return netpkg.NewCryptoReadWriter(rw, key)
}

func (ac *acceptedConnection) readFirstV2Msg(conn net.Conn, wireConn *wire.Conn) (msg.Message, error) {
	frame, err := wireConn.ReadFrame()
	if err != nil {
		return nil, fmt.Errorf("read v2 frame: %w", err)
	}
	if frame.Type == wire.FrameTypeClientHello {
		ac.clientHelloPresent = true
		if err := ac.handleClientHello(conn, wireConn, frame); err != nil {
			return nil, err
		}
		frame, err = wireConn.ReadFrame()
		if err != nil {
			return nil, fmt.Errorf("read first v2 message frame: %w", err)
		}
	}

	m, err := msg.DecodeV2MessageFrame(frame)
	if err != nil {
		return nil, fmt.Errorf("decode v2 message: %w", err)
	}
	return m, nil
}

func (ac *acceptedConnection) handleClientHello(conn net.Conn, wireConn *wire.Conn, frame *wire.Frame) error {
	var hello wire.ClientHello
	if err := wireConn.UnmarshalFrame(frame, &hello); err != nil {
		return fmt.Errorf("decode ClientHello: %w", err)
	}

	serverHello, err := wire.NewServerHello(hello)
	if err != nil {
		serverHello = wire.DefaultServerHello()
		serverHello.Error = err.Error()
		if writeErr := writeWithDeadline(conn, connWriteTimeout, func() error {
			return wireConn.WriteJSONFrame(wire.FrameTypeServerHello, serverHello)
		}); writeErr != nil {
			return fmt.Errorf("%w; write ServerHello error: %v", err, writeErr)
		}
		return err
	}
	serverHelloFrame, err := wire.NewJSONFrame(wire.FrameTypeServerHello, serverHello)
	if err != nil {
		return fmt.Errorf("encode ServerHello: %w", err)
	}
	cryptoContext := wire.NewCryptoContext(
		serverHello.Selected.Crypto.Algorithm,
		frame.Payload,
		serverHelloFrame.Payload,
	)
	if err := writeWithDeadline(conn, connWriteTimeout, func() error {
		return wireConn.WriteFrame(serverHelloFrame)
	}); err != nil {
		return fmt.Errorf("write ServerHello: %w", err)
	}
	ac.cryptoContext = cryptoContext
	ac.udpPacketCodec = serverHello.Selected.Message.UDPPacketCodec
	return nil
}

// HandleListener accepts connections from client and call handleConnection to handle them.
// If internal is true, it means that this listener is used for internal communication like ssh tunnel gateway.
// TODO(fatedier): Pass some parameters of listener/connection through context to avoid passing too many parameters.
func (svr *Service) HandleListener(l net.Listener, internal bool) error {
	if svr.managedLogin != nil {
		return errors.New("account login requires a service-owned listener")
	}
	return svr.handleListener(l, internal, false, false)
}

// alreadyTLS is reserved for the WSS bridge after its completed outer TLS
// handshake and HTTP Upgrade. inspectWSS applies only to the TCP/TLS listener.
func (svr *Service) handleListener(l net.Listener, internal, alreadyTLS, inspectWSS bool) error {
	// Listen for incoming connections from client.
	for {
		c, err := l.Accept()
		if err != nil {
			return err
		}
		// Account mode checks the physical peer before TLS or TCP multiplexing.
		// handleConnection still rechecks the policy for each admitted stream.
		if svr.managedLogin != nil && svr.allowControlConnection != nil {
			peer := c.RemoteAddr()
			if !svr.allowControlConnection(peer) {
				svr.logDeniedControl(svr.ctx, peer)
				_ = c.Close()
				continue
			}
		}
		c, ok := lifetime.TrackConn(svr.workCtx, c)
		if !ok {
			return net.ErrClosed
		}
		// inject xlog object into net.Conn context
		xl := svr.xl.Spawn()
		ctx := xlog.NewContext(svr.ctx, xl)

		c = netpkg.NewContextConn(ctx, c)

		inspectTLS := false
		if !internal && !alreadyTLS {
			xl.Tracef("start check TLS connection...")
			originConn := c
			forceTLS := svr.cfg.Transport.TLS.Force
			var isTLS, custom bool
			c, isTLS, custom, err = netpkg.CheckAndEnableTLSServerConnWithTimeout(c, svr.tlsConfig, forceTLS, connReadTimeout)
			if err != nil {
				xl.Warnf("checkAndEnableTLSServerConnWithTimeout error: %v", err)
				originConn.Close()
				continue
			}
			xl.Tracef("check TLS connection success, isTLS: %v custom: %v internal: %v", isTLS, custom, internal)
			inspectTLS = inspectWSS && isTLS
		}

		// Start a new goroutine to handle connection.
		frpConn := c
		if !lifetime.Go(ctx, func() {
			conn := frpConn
			if inspectTLS {
				// Reject disallowed physical peers before TLS handshake and
				// protocol detection. handleConnection rechecks the policy.
				if svr.allowControlConnection != nil && !svr.allowControlConnection(conn.RemoteAddr()) {
					svr.logDeniedControl(ctx, conn.RemoteAddr())
					_ = conn.SetDeadline(time.Now())
					_ = conn.Close()
					return
				}
				inspected, isWSS, err := sniffWSS(conn, connReadTimeout)
				if err != nil {
					xl.Debugf("failed to inspect TLS transport")
					_ = conn.SetDeadline(time.Now())
					_ = conn.Close()
					return
				}
				conn = inspected
				if isWSS {
					_ = svr.wssListener.offer(inspected)
					return
				}
			}
			if lo.FromPtr(svr.cfg.Transport.TCPMux) && !internal {
				fmuxCfg := fmux.DefaultConfig()
				fmuxCfg.KeepAliveInterval = time.Duration(svr.cfg.Transport.TCPMuxKeepaliveInterval) * time.Second
				// Use trace level for yamux logs
				fmuxCfg.LogOutput = xlog.NewTraceWriter(xlog.FromContextSafe(ctx))
				fmuxCfg.MaxStreamWindowSize = 6 * 1024 * 1024
				session, err := fmux.Server(conn, fmuxCfg)
				if err != nil {
					xl.Warnf("failed to create mux connection: %v", err)
					conn.Close()
					return
				}

				for {
					stream, err := session.AcceptStream()
					if err != nil {
						xl.Debugf("accept new mux stream error: %v", err)
						session.Close()
						return
					}
					streamCtx := xlog.NewContext(ctx, xlog.FromContextSafe(ctx).Spawn())
					if !lifetime.Go(streamCtx, func() { svr.handleConnection(streamCtx, stream, internal, session) }) {
						_ = stream.Close()
						return
					}
				}
			} else {
				svr.handleConnection(ctx, conn, internal, nil)
			}
		}) {
			_ = frpConn.Close()
			return net.ErrClosed
		}
	}
}

type quicSessionCloser struct{ conn *quic.Conn }

func (c quicSessionCloser) Close() error { return c.conn.CloseWithError(0, "") }

func (svr *Service) HandleQUICListener(l *quic.Listener) error {
	if svr.managedLogin != nil {
		return errors.New("account login does not support QUIC")
	}
	// Listen for incoming connections from client.
	for {
		c, err := l.Accept(svr.ctx)
		if err != nil {
			return err
		}
		release, ok := lifetime.Track(svr.workCtx, quicSessionCloser{conn: c})
		if !ok {
			return net.ErrClosed
		}
		frpConn := c
		ctx := xlog.NewContext(svr.ctx, svr.xl.Spawn())
		if !lifetime.Go(ctx, func() {
			defer release()
			for {
				stream, err := frpConn.AcceptStream(ctx)
				if err != nil {
					xlog.FromContextSafe(ctx).Debugf("accept new quic mux stream error: %v", err)
					_ = frpConn.CloseWithError(0, "")
					return
				}
				streamCtx := xlog.NewContext(ctx, xlog.FromContextSafe(ctx).Spawn())
				streamConn := netpkg.QuicStreamToNetConn(stream, frpConn)
				if !lifetime.Go(streamCtx, func() { svr.handleConnection(streamCtx, streamConn, false, nil) }) {
					_ = streamConn.Close()
					return
				}
			}
		}) {
			release()
			_ = frpConn.CloseWithError(0, "")
			return net.ErrClosed
		}
	}
}

func (svr *Service) RegisterControl(
	ctlConn *msg.Conn,
	loginMsg *msg.Login,
	internal bool,
	wireProtocol string,
	udpPacketCodec string,
	session io.Closer,
) (*Control, error) {
	if svr.managedLogin != nil {
		return nil, errors.New("account login requires a service-owned connection")
	}
	return svr.registerControl(ctlConn, loginMsg, internal, wireProtocol, udpPacketCodec, session, nil)
}

func (svr *Service) registerControl(
	ctlConn *msg.Conn,
	loginMsg *msg.Login,
	internal bool,
	wireProtocol string,
	udpPacketCodec string,
	session io.Closer,
	managed *managedLogin,
) (*Control, error) {
	if svr.managedLogin != nil {
		if internal || managed == nil || managed.verifier == nil || len(managed.key) != 64 ||
			managed.accountID != loginMsg.User || !managedTimestampOK(loginMsg.Timestamp) {
			return nil, errors.New("verified account login is required")
		}
	} else if managed != nil {
		return nil, errors.New("account login is unavailable on this service")
	}
	switch wireProtocol {
	case wire.ProtocolV1:
		if udpPacketCodec != "" {
			return nil, fmt.Errorf("UDP packet codec %q requires wire protocol v2", udpPacketCodec)
		}
	case wire.ProtocolV2:
		if udpPacketCodec != "" && udpPacketCodec != wire.UDPPacketCodecBinary {
			return nil, fmt.Errorf("unsupported UDP packet codec selection: %s", udpPacketCodec)
		}
	default:
		return nil, fmt.Errorf("unsupported wire protocol: %s", wireProtocol)
	}
	// If client's RunID is empty, it's a new client, we just create a new controller.
	// Otherwise, we check if there is one controller has the same run id. If so, we release previous controller and start new one.
	var err error
	if loginMsg.RunID == "" {
		loginMsg.RunID, err = util.RandID()
		if err != nil {
			return nil, err
		}
	}
	if err := validation.ValidateRunID(loginMsg.RunID); err != nil {
		return nil, fmt.Errorf("invalid run id: %w", err)
	}

	ctx := netpkg.NewContextFromConn(ctlConn)
	xl := xlog.FromContextSafe(ctx)
	xl.AppendPrefix(loginMsg.RunID)
	ctx = xlog.NewContext(ctx, xl)
	xl.Infof("client login info: ip [%s] version [%s] hostname [%s] os [%s] arch [%s]",
		ctlConn.RemoteAddr().String(), loginMsg.Version, loginMsg.Hostname, loginMsg.Os, loginMsg.Arch)

	// Check auth.
	authVerifier := svr.auth.Verifier
	encryptionKey := svr.auth.EncryptionKey()
	if managed != nil {
		authVerifier = managed.verifier
		encryptionKey = managed.key
	} else if internal && loginMsg.ClientSpec.AlwaysAuthPass {
		authVerifier = auth.AlwaysPassVerifier
	}
	if err := authVerifier.VerifyLogin(loginMsg); err != nil {
		return nil, err
	}

	accountID := ""
	if managed != nil {
		accountID = managed.accountID
	}
	ctl, err := NewControl(ctx, &SessionContext{
		RC:             svr.rc,
		ServerMetrics:  svr.metrics,
		PxyManager:     svr.pxyManager,
		PluginManager:  svr.pluginManager,
		AuthVerifier:   authVerifier,
		EncryptionKey:  encryptionKey,
		Conn:           ctlConn,
		Session:        session,
		LoginMsg:       loginMsg,
		ServerCfg:      svr.cfg,
		WireProtocol:   wireProtocol,
		UDPPacketCodec: udpPacketCodec,
		managedAccountID: accountID,
	})
	if err != nil {
		xl.Warnf("create new controller error: %v", err)
		// don't return detailed errors to client
		return nil, fmt.Errorf("unexpected error when creating new controller")
	}

	if err := svr.ctlManager.Add(ctl); err != nil {
		return ctl, err
	}
	ctl.WaitForHandoff()

	active, err := svr.ctlManager.Activate(ctl)
	if err != nil {
		return ctl, err
	}
	if !active {
		return ctl, errControlReplaced
	}

	return ctl, nil
}

// RegisterWorkConn register a new work connection to control and proxies need it.
func (svr *Service) RegisterWorkConn(
	workConn *msg.Conn,
	newMsg *msg.NewWorkConn,
	workWireProtocol string,
	workClientHelloPresent bool,
) error {
	if svr.managedLogin != nil {
		return errors.New("account login requires a service-owned connection")
	}
	return svr.registerWorkConn(workConn, newMsg, workWireProtocol, workClientHelloPresent, false)
}

func (svr *Service) registerWorkConn(
	workConn *msg.Conn,
	newMsg *msg.NewWorkConn,
	workWireProtocol string,
	workClientHelloPresent bool,
	managedTransport bool,
) error {
	if (svr.managedLogin != nil) != managedTransport {
		return errors.New("account work connection requires service-owned TLS")
	}
	if workClientHelloPresent {
		return fmt.Errorf("ClientHello is not allowed on work connections")
	}
	xl := netpkg.NewLogFromConn(workConn)
	ctl, exist := svr.ctlManager.GetByID(newMsg.RunID)
	if !exist {
		xl.Warnf("no client control found for run id [%s]", newMsg.RunID)
		return fmt.Errorf("no client control found for run id [%s]", newMsg.RunID)
	}
	if workWireProtocol != ctl.sessionCtx.WireProtocol {
		return fmt.Errorf("work connection wire protocol mismatch: got %s want %s", workWireProtocol, ctl.sessionCtx.WireProtocol)
	}

	// server plugin hook
	content := &plugin.NewWorkConnContent{
		User: plugin.UserInfo{
			User:  ctl.sessionCtx.LoginMsg.User,
			Metas: ctl.sessionCtx.LoginMsg.Metas,
			RunID: ctl.sessionCtx.LoginMsg.RunID,
		},
		NewWorkConn: *newMsg,
	}
	retContent, err := svr.pluginManager.NewWorkConn(content)
	if err == nil {
		newMsg = &retContent.NewWorkConn
		// Check auth.
		if managedTransport && (ctl.sessionCtx.managedAccountID == "" || !managedTimestampOK(newMsg.Timestamp)) {
			err = errors.New("account credentials are invalid")
		} else {
			err = ctl.sessionCtx.AuthVerifier.VerifyNewWorkConn(newMsg)
		}
	}
	if err != nil {
		xl.Warnf("invalid NewWorkConn with run id [%s]", newMsg.RunID)
		if managedTransport {
			return errors.New("account credentials are invalid")
		}
		return err
	}
	return svr.ctlManager.RegisterWorkConn(ctl, proxy.NewWorkConn(workConn))
}

func (svr *Service) RegisterVisitorConn(visitorConn net.Conn, newMsg *msg.NewVisitorConn, wireProtocol string) error {
	if svr.managedLogin != nil {
		return errors.New("account login does not support visitor connections")
	}
	var rejected net.Conn
	defer func() {
		// The visitor/control admission locks have been released before this
		// close, but the caller still sees the original full-queue behavior.
		if rejected != nil {
			_ = rejected.Close()
		}
	}()
	admit := func(visitorUser, visitorWireProtocol, visitorUDPPacketCodec string) error {
		if visitorWireProtocol == "" {
			visitorWireProtocol = wireProtocol
		}
		var err error
		rejected, err = svr.rc.VisitorManager.NewConnDeferredClose(newMsg.ProxyName, visitorConn, newMsg.Timestamp, newMsg.SignKey,
			newMsg.UseEncryption, newMsg.UseCompression, visitorUser, visitorWireProtocol, visitorUDPPacketCodec)
		return err
	}
	// TODO(deprecation): Compatible with old versions, can be without runID, user is empty. In later versions, it will be mandatory to include runID.
	// If runID is required, it is not compatible with versions prior to v0.50.0.
	if newMsg.RunID != "" {
		admitted, err := svr.ctlManager.admitVisitorByRunID(newMsg.RunID, func(visitorUser, controlWireProtocol, controlUDPPacketCodec string) error {
			if wireProtocol != controlWireProtocol {
				return fmt.Errorf("visitor connection wire protocol mismatch: got %s want %s", wireProtocol, controlWireProtocol)
			}
			return admit(visitorUser, controlWireProtocol, controlUDPPacketCodec)
		})
		if err != nil {
			return err
		}
		if !admitted {
			return fmt.Errorf("no client control found for run id [%s]", newMsg.RunID)
		}
		return nil
	}
	return admit("", wireProtocol, "")
}
