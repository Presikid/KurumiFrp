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
// Modified by KurumiFRP to integrate client traffic accounting.

// Modified by KurumiFRP: support the restricted embedded client profile.

// Modified by KurumiFRP: scope each restricted control generation.

// Modified by KurumiFRP: isolate the embedded client's networking and lifecycle.

package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/samber/lo"

	"github.com/fatedier/frp/client/internal/netx"
	"github.com/fatedier/frp/client/proxy"
	"github.com/fatedier/frp/client/traffic"
	"github.com/fatedier/frp/pkg/auth"
	"github.com/fatedier/frp/pkg/config"
	"github.com/fatedier/frp/pkg/config/source"
	v1 "github.com/fatedier/frp/pkg/config/v1"
	"github.com/fatedier/frp/pkg/config/v1/validation"
	"github.com/fatedier/frp/pkg/metrics/text"
	"github.com/fatedier/frp/pkg/msg"
	"github.com/fatedier/frp/pkg/policy/security"
	httppkg "github.com/fatedier/frp/pkg/util/http"
	"github.com/fatedier/frp/pkg/util/wait"
	"github.com/fatedier/frp/pkg/util/xlog"
	"github.com/fatedier/frp/pkg/vnet"
)

type cancelErr struct {
	Err error
}

// KurumiRestrictedClientProfileVersion identifies the limited Isolated API
// profile, not full frp feature isolation or an end-to-end interoperability
// guarantee. See KURUMI_ISOLATION.md for restrictions and outstanding work.
const KurumiRestrictedClientProfileVersion = 1

// ValidateRestrictedProxyConfigurer admits only plugins whose connections are
// owned by the isolated control generation. Other plugin types stay disabled.
func ValidateRestrictedProxyConfigurer(cfg v1.ProxyConfigurer) error {
	if cfg == nil {
		return errors.New("invalid isolated proxy configuration")
	}
	value := reflect.ValueOf(cfg)
	if (value.Kind() == reflect.Ptr && value.IsNil()) || cfg.GetBaseConfig() == nil {
		return errors.New("invalid isolated proxy configuration")
	}
	base := cfg.GetBaseConfig()
	if base.Plugin.Type == "" {
		return nil
	}
	if _, ok := cfg.(*v1.TCPProxyConfig); !ok {
		return errors.New("proxy plugin is not supported by the isolated client")
	}
	switch base.Plugin.Type {
	case v1.PluginUnixDomainSocket:
		opts, ok := base.Plugin.ClientPluginOptions.(*v1.UnixDomainSocketPluginOptions)
		if !ok || opts == nil || !strings.HasPrefix(opts.UnixPath, "/") || len(opts.UnixPath) > 1024 || strings.ContainsAny(opts.UnixPath, "{}") || strings.IndexFunc(opts.UnixPath, unicode.IsControl) >= 0 || base.HealthCheck.Type != "" {
			return errors.New("invalid isolated unix socket configuration")
		}
	case v1.PluginSocks5:
		// Empty mode defaults to client when the proxy config is completed.
		// Only an explicitly server-side limit avoids the uncancellable client
		// bandwidth wrapper used by frp's work streams.
		if !proxy.ValidIsolatedSocks5Config(base) {
			return errors.New("invalid isolated SOCKS5 configuration")
		}
	default:
		return errors.New("proxy plugin is not supported by the isolated client")
	}
	return nil
}

var (
	// ErrServiceClosed reports that the client was closed before Run started.
	ErrServiceClosed = errors.New("client service is closed")
	// ErrServiceRunning reports a second Run attempt on the same client.
	ErrServiceRunning = errors.New("client service has already been started")
	// ErrWorkerPanicked omits the panic value, which could contain secrets.
	ErrWorkerPanicked = errors.New("client service worker panicked")
	// ErrPluginCleanupFailed reports an isolated plugin's uncertain cleanup.
	ErrPluginCleanupFailed = proxy.ErrPluginCleanupFailed
	// ErrRestrictedConfigReload requires a new isolated service generation.
	ErrRestrictedConfigReload = errors.New("isolated client configuration is fixed at construction; create a new client")
)

func (e cancelErr) Error() string {
	return e.Err.Error()
}

// ServiceOptions contains options for creating a new client service.
type ServiceOptions struct {
	Common *v1.ClientCommonConfig

	// Isolated enables the restricted embedding profile: no inherited proxy
	// environment or inherited logs, and only supported, isolated features.
	// The caller must exclusively own Common and ConfigSourceAggregator for
	// the service lifetime; NewFRPClient makes private copies for that reason.
	Isolated bool

	// LogSink is dedicated to this service. Nil discards logs in the isolated profile;
	// otherwise it preserves the process logger.
	// Once claimed, a non-nil sink is consumed even if later construction fails.
	LogSink *xlog.Sink

	// ParentLogger is only for a child service within the same security
	// principal, such as an frps SSH virtual client. Never use it to share
	// logs between independently owned services. It is mutually exclusive
	// with LogSink.
	ParentLogger *xlog.Logger

	// ConfigSourceAggregator manages internal config and optional store sources.
	// It is required for creating a Service.
	ConfigSourceAggregator *source.Aggregator

	UnsafeFeatures *security.UnsafeFeatures

	// ConfigFilePath is the path to the configuration file used to initialize.
	// If it is empty, it means that the configuration file is not used for initialization.
	// It may be initialized using command line parameters or called directly.
	ConfigFilePath string

	// ClientSpec is the client specification that control the client behavior.
	ClientSpec *msg.ClientSpec

	// ConnectorCreator is a function that creates a new connector to make connections to the server.
	// The Connector shields the underlying connection details, whether it is through TCP or QUIC connection,
	// and regardless of whether multiplexing is used.
	//
	// If it is not set, the default frpc connector will be used.
	// By using a custom Connector, it can be used to implement a VirtualClient, which connects to frps
	// through a pipe instead of a real physical connection.
	ConnectorCreator func(context.Context, *v1.ClientCommonConfig) Connector

	// HandleWorkConnCb is a callback function that is called when a new work connection is created.
	//
	// If it is not set, the default frpc implementation will be used.
	HandleWorkConnCb func(*v1.ProxyBaseConfig, net.Conn, *msg.StartWorkConn) bool

	// Traffic is owned by this service's instance generation. Nil disables
	// client traffic accounting. A configured binding must cover every rule.
	Traffic *traffic.Bindings

	// OnWorkerPanic is an isolated-profile lifecycle notification. It must be
	// concurrent-safe, return promptly, and not panic. It receives no panic value.
	OnWorkerPanic func()
	// OnPluginCleanupFailure reports uncertain isolated plugin cleanup as soon
	// as it is detected. It must be concurrent-safe, return promptly, and not
	// panic or wait for this service to stop.
	OnPluginCleanupFailure func()
}

// setServiceOptionsDefault sets the default values for ServiceOptions.
func setServiceOptionsDefault(options *ServiceOptions) error {
	if options.Common == nil {
		return errors.New("client common configuration is required")
	}
	if options.Isolated {
		if err := options.Common.CompleteWithoutProxyEnvironment(); err != nil {
			return err
		}
	} else if err := options.Common.Complete(); err != nil {
		return err
	}
	if options.ConnectorCreator == nil {
		options.ConnectorCreator = NewConnector
	}
	return nil
}

// Service is the client service that connects to frps and provides proxy services.
type Service struct {
	logBound bool
	network  *netx.Network
	ctlMu    sync.RWMutex
	// Stores gracefulShutdownDuration independently from ctlMu, because the
	// graceful shutdown wait may hold ctlMu for an arbitrary duration.
	gracefulShutdownDuration atomic.Int64
	// manager control connection with server
	ctl *Control
	// Uniq id got from frps, it will be attached to loginMsg.
	runID string

	// Auth runtime and encryption materials
	auth *auth.ClientAuth

	// web server for admin UI and apis
	webServer     *httppkg.Server
	metricsServer *httppkg.Server

	vnetController *vnet.Controller

	cfgMu sync.RWMutex
	// reloadMu serializes config updates with control startup and publication,
	// keeping reloadCommon and applied config in sync across reloads and reconnects.
	reloadMu sync.Mutex
	common   *v1.ClientCommonConfig
	// reloadCommon is used for filtering/defaulting during config-source reloads.
	// It can be updated by /api/reload without mutating startup-only common behavior.
	reloadCommon    *v1.ClientCommonConfig
	proxyCfgs       []v1.ProxyConfigurer
	visitorCfgs     []v1.VisitorConfigurer
	clientSpec      *msg.ClientSpec
	trafficBindings *traffic.Bindings
	trafficSnapshot *traffic.BindingSnapshot

	// aggregator manages multiple configuration sources.
	// When set, the service watches for config changes and reloads automatically.
	aggregator   *source.Aggregator
	configSource *source.ConfigSource
	storeSource  *source.StoreSource

	unsafeFeatures *security.UnsafeFeatures

	// The configuration file used to initialize this client, or an empty
	// string if no configuration file was used.
	configFilePath string

	// service context
	ctx context.Context
	// call cancel to stop service
	cancel context.CancelCauseFunc
	// Protect cancel publication and shutdown before the first Run.
	lifecycleMu sync.Mutex
	closed      bool
	started     bool
	done        chan struct{}
	ready       chan struct{}
	exiting     chan struct{}
	isolateLogs bool
	logger      *xlog.Logger

	connectorCreator func(context.Context, *v1.ClientCommonConfig) Connector
	handleWorkConnCb func(*v1.ProxyBaseConfig, net.Conn, *msg.StartWorkConn) bool
	onWorkerPanic    func()
	onPluginCleanupFailure func()
	workerPanic      func() // published before control workers are started
	workerPanicked   atomic.Bool
}

// ValidateRestrictedNetworkConfig checks the isolated network profile without
// opening sockets. Callers should avoid exposing configuration values in errors.
func ValidateRestrictedNetworkConfig(common *v1.ClientCommonConfig) error {
	if common == nil {
		return errors.New("client common configuration is required")
	}
	network, err := netx.New(common.DNSServer, common.Transport.ConnectServerLocalIP)
	if err == nil {
		network.Close()
	}
	return err
}

func NewService(options ServiceOptions) (*Service, error) {
	if options.Isolated && (options.ConnectorCreator != nil || options.HandleWorkConnCb != nil ||
		options.ParentLogger != nil || options.ClientSpec != nil || options.UnsafeFeatures != nil || options.ConfigFilePath != "") {
		return nil, errors.New("custom client hooks and file-based initialization are not supported by the isolated client")
	}
	if options.LogSink != nil && options.ParentLogger != nil {
		return nil, fmt.Errorf("log sink and parent logger are mutually exclusive")
	}
	if options.LogSink != nil {
		if err := options.LogSink.Claim(); err != nil {
			return nil, err
		}
	}
	logger := xlog.NewWithSink(options.LogSink)
	if options.ParentLogger != nil {
		logger = options.ParentLogger.Spawn()
	}
	if options.Isolated && options.LogSink == nil {
		logger = xlog.NewDiscard()
	}
	if err := setServiceOptionsDefault(&options); err != nil {
		return nil, err
	}
	if !options.Isolated && options.Common.DNSServer != "" {
		return nil, errors.New("custom DNS is not supported without changing the process resolver")
	}
	if options.Isolated && (options.Common.VirtualNet.Address != "" ||
		options.Common.WebServer.Port != 0 || options.Common.MetricsServer.Port != 0 ||
		options.Common.Store.IsEnabled() || len(options.Common.IncludeConfigFiles) != 0 ||
		options.Common.Auth.Method != v1.AuthMethodToken || options.Common.Auth.TokenSource != nil ||
		options.Common.Transport.ProxyURL != "" ||
		(options.Common.Transport.Protocol != "tcp" &&
			options.Common.Transport.Protocol != "kcp" &&
			options.Common.Transport.Protocol != "quic" &&
			options.Common.Transport.Protocol != "websocket" &&
			options.Common.Transport.Protocol != "wss")) {
		return nil, errors.New("configuration is not supported by the isolated client")
	}
	var network *netx.Network
	if options.Isolated {
		var err error
		network, err = netx.New(options.Common.DNSServer, options.Common.Transport.ConnectServerLocalIP)
		if err != nil {
			return nil, err
		}
	}
	constructed := false
	defer func() {
		if !constructed && network != nil {
			network.Close()
		}
	}()

	authRuntime, err := auth.BuildClientAuth(&options.Common.Auth)
	if err != nil {
		return nil, err
	}

	if options.ConfigSourceAggregator == nil {
		return nil, fmt.Errorf("config source aggregator is required")
	}

	configSource := options.ConfigSourceAggregator.ConfigSource()
	storeSource := options.ConfigSourceAggregator.StoreSource()
	if options.Isolated && storeSource != nil {
		return nil, errors.New("store sources are not supported by the isolated client")
	}

	proxyCfgs, visitorCfgs, loadErr := options.ConfigSourceAggregator.Load()
	if loadErr != nil {
		return nil, fmt.Errorf("failed to load config from aggregator: %w", loadErr)
	}
	proxyCfgs, visitorCfgs = config.FilterClientConfigurers(options.Common, proxyCfgs, visitorCfgs)
	proxyCfgs = config.CompleteProxyConfigurers(proxyCfgs)
	visitorCfgs = config.CompleteVisitorConfigurers(visitorCfgs)
	if options.Isolated {
		for _, cfg := range proxyCfgs {
			if err := ValidateRestrictedProxyConfigurer(cfg); err != nil {
				return nil, err
			}
		}
		for _, cfg := range visitorCfgs {
			if cfg.GetBaseConfig().Plugin.Type != "" {
				return nil, errors.New("visitor plugins are not supported by the isolated client")
			}
		}
	}
	trafficSnapshot, err := snapshotTraffic(options.Traffic, proxyCfgs, visitorCfgs)
	if err != nil {
		return nil, err
	}

	// Create HTTP servers after all other fallible steps, rolling back the web
	// listener if the metrics server cannot be created.
	var webServer *httppkg.Server
	if options.Common.WebServer.Port > 0 {
		ws, err := httppkg.NewServer(options.Common.WebServer)
		if err != nil {
			return nil, err
		}
		webServer = ws
	}
	// frpc exposes Go runtime metrics only. They are written by pkg/metrics/text
	// so that client_golang and protobuf stay out of the frpc binary.
	var metricsHandler http.Handler
	if options.Common.MetricsServer.Port > 0 {
		registry := text.NewRegistry()
		registry.RegisterRuntime()
		metricsHandler = registry.Handler()
	}
	metricsServer, err := httppkg.NewMetricsServer(options.Common.MetricsServer, metricsHandler)
	if err != nil {
		if webServer != nil {
			_ = webServer.Close()
		}
		return nil, err
	}
	if options.Traffic != nil {
		if err := options.Traffic.Claim(); err != nil {
			if webServer != nil {
				_ = webServer.Close()
			}
			if metricsServer != nil {
				_ = metricsServer.Close()
			}
			return nil, err
		}
	}

	s := &Service{
		logBound:         options.Isolated || options.LogSink != nil || options.ParentLogger != nil,
		network:          network,
		ctx:              context.Background(),
		done:             make(chan struct{}),
		ready:            make(chan struct{}),
		exiting:          make(chan struct{}),
		isolateLogs:      options.Isolated,
		logger:           logger,
		auth:             authRuntime,
		webServer:        webServer,
		metricsServer:    metricsServer,
		common:           options.Common,
		reloadCommon:     options.Common,
		configFilePath:   options.ConfigFilePath,
		unsafeFeatures:   options.UnsafeFeatures,
		proxyCfgs:        proxyCfgs,
		visitorCfgs:      visitorCfgs,
		trafficBindings:  options.Traffic,
		trafficSnapshot:  trafficSnapshot,
		clientSpec:       options.ClientSpec,
		aggregator:       options.ConfigSourceAggregator,
		configSource:     configSource,
		storeSource:      storeSource,
		connectorCreator: options.ConnectorCreator,
		handleWorkConnCb: options.HandleWorkConnCb,
		onWorkerPanic:    options.OnWorkerPanic,
		onPluginCleanupFailure: options.OnPluginCleanupFailure,
	}

	if webServer != nil {
		webServer.RouteRegister(s.registerRouteHandlers)
	}
	if options.Common.VirtualNet.Address != "" {
		s.vnetController = vnet.NewControllerWithLogger(options.Common.VirtualNet, logger)
	}
	constructed = true
	return s, nil
}

func (svr *Service) Run(ctx context.Context) (runErr error) {
	ctx, cancel := context.WithCancelCause(ctx)
	if svr.isolateLogs {
		ctx = proxy.WithPluginCleanupFailureHandler(ctx, func() {
			// The owner can block new runs before this service starts draining.
			// Keep cancellation even if a caller violates the no-panic contract.
			defer cancel(ErrPluginCleanupFailed)
			if svr.onPluginCleanupFailure != nil {
				svr.onPluginCleanupFailure()
			}
		})
		// This defer runs after the service's cleanup defer has joined scoped
		// workers, so a panic during shutdown is not reported as a clean exit.
		defer func() {
			if svr.workerPanicked.Load() {
				runErr = errors.Join(runErr, ErrWorkerPanicked)
			}
		}()
	}
	svr.lifecycleMu.Lock()
	if svr.closed {
		svr.lifecycleMu.Unlock()
		cancel(nil)
		return ErrServiceClosed
	}
	if svr.started {
		svr.lifecycleMu.Unlock()
		cancel(nil)
		return ErrServiceRunning
	}
	svr.started = true
	if svr.network != nil {
		ctx = netx.WithNetwork(ctx, svr.network)
	}
	xl := xlog.FromContextSafe(ctx)
	if svr.logBound {
		xl = svr.logger
	}
	svr.ctx = xlog.NewContext(ctx, xl)
	svr.cancel = cancel
	if svr.isolateLogs {
		svr.workerPanic = func() {
			svr.workerPanicked.Store(true)
			cancel(ErrWorkerPanicked)
			if svr.onWorkerPanic != nil {
				svr.onWorkerPanic()
			}
		}
	}
	svr.lifecycleMu.Unlock()
	var stopNetworkClose func() bool
	var networkCloseDone chan struct{}
	if svr.network != nil {
		networkCloseDone = make(chan struct{})
		stopNetworkClose = context.AfterFunc(ctx, func() {
			defer close(networkCloseDone)
			if svr.workerPanic != nil {
				defer func() {
					if recover() != nil {
						svr.workerPanic()
					}
				}()
			}
			svr.network.Close()
		})
	}
	var reconnectDone <-chan struct{}
	defer func() {
		// Prevent the callback if it has not started. Otherwise wait for it to
		// report any panic before publishing completion to Close and the owner.
		stoppedNetworkClose := true
		if stopNetworkClose != nil {
			stoppedNetworkClose = stopNetworkClose()
		}
		cancel(nil)
		if svr.network != nil {
			if stoppedNetworkClose {
				svr.network.Close()
			} else {
				<-networkCloseDone
			}
		}
		if reconnectDone != nil {
			<-reconnectDone
		}
		svr.stop()
		if proxy.PluginCleanupFailed(ctx) {
			runErr = errors.Join(runErr, ErrPluginCleanupFailed)
		}
		svr.lifecycleMu.Lock()
		svr.closed = true
		svr.lifecycleMu.Unlock()
		close(svr.done)
	}()
	// Publish that the run loop has ended before waiting for resource cleanup.
	defer close(svr.exiting)
	if err := ctx.Err(); err != nil {
		return err
	}

	if svr.vnetController != nil {
		vnetController := svr.vnetController
		if err := svr.vnetController.Init(); err != nil {
			xl.Errorf("init virtual network controller error: %v", err)
			return err
		}
		go func() {
			xl.Infof("virtual network controller start...")
			if err := vnetController.Run(); err != nil && !errors.Is(err, net.ErrClosed) {
				xl.Warnf("virtual network controller exit with error: %v", err)
			}
		}()
	}

	if svr.webServer != nil {
		if svr.common.WebServer.PprofEnable {
			xl.Warnf("webServer pprof endpoints are deprecated; use metricsServer.pprofEnable instead")
		}
		if svr.common.MetricsServer.Port == 0 {
			xl.Infof("webServer /healthz is deprecated; health probes can migrate to metricsServer.port")
		}
		webServer := svr.webServer
		go func() {
			xl.Infof("admin server listen on %s", webServer.Address())
			if err := webServer.Run(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				xl.Warnf("admin server exit with error: %v", err)
			}
		}()
	}

	if svr.metricsServer != nil {
		metricsServer := svr.metricsServer
		go func() {
			xl.Infof("metrics server listen on %s", metricsServer.Address())
			if err := metricsServer.Run(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				xl.Warnf("metrics server exit with error: %v", err)
			}
		}()
	}

	// Run has passed initialization and is about to enter its login/retry loop.
	// This signals engine readiness only, not a successful login or registration.
	if err := ctx.Err(); err != nil {
		return err
	}
	close(svr.ready)

	// first login to frps
	svr.loopLoginUntilSuccess(10*time.Second, lo.FromPtr(svr.common.LoginFailExit))
	svr.ctlMu.RLock()
	loggedIn := svr.ctl != nil
	svr.ctlMu.RUnlock()
	if !loggedIn {
		cancelCause := cancelErr{}
		if errors.As(context.Cause(svr.ctx), &cancelCause) {
			return fmt.Errorf("login to the server failed: %v. With loginFailExit enabled, no additional retries will be attempted", cancelCause.Err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return errors.New("client login ended without a control connection")
	}

	done := make(chan struct{})
	reconnectDone = done
	go func() {
		defer close(done)
		if svr.workerPanic != nil {
			defer func() {
				if recover() != nil {
					svr.workerPanic()
				}
			}()
		}
		svr.keepControllerWorking()
	}()

	<-svr.ctx.Done()
	return nil
}

// Ready closes once Run is about to enter its login/retry loop. It remains open
// if startup fails first; callers must also observe their completion signal.
func (svr *Service) Ready() <-chan struct{} { return svr.ready }

// Exiting closes when Run leaves its loop, before cleanup has completed. It
// may stay open if Run was rejected before it began; observe completion too.
func (svr *Service) Exiting() <-chan struct{} { return svr.exiting }

func (svr *Service) keepControllerWorking() {
	// ctl is swapped under ctlMu in loopLoginUntilSuccess / stop; take a
	// snapshot before waiting so stop's nil write does not race this read.
	svr.ctlMu.RLock()
	ctl := svr.ctl
	svr.ctlMu.RUnlock()
	if ctl == nil {
		return
	}
	select {
	case <-ctl.Done():
	case <-svr.ctx.Done():
		return
	}
	if proxy.PluginCleanupFailed(svr.ctx) {
		return
	}

	// There is a situation where the login is successful but due to certain reasons,
	// the control immediately exits. It is necessary to limit the frequency of reconnection in this case.
	// The interval for the first three retries in 1 minute will be very short, and then it will increase exponentially.
	// The maximum interval is 20 seconds.
	wait.BackoffUntil(func() (bool, error) {
		if proxy.PluginCleanupFailed(svr.ctx) {
			return true, nil
		}
		// loopLoginUntilSuccess is another layer of loop that will continuously attempt to
		// login to the server until successful.
		svr.loopLoginUntilSuccess(20*time.Second, false)
		svr.ctlMu.RLock()
		ctl := svr.ctl
		svr.ctlMu.RUnlock()
		if ctl != nil {
			select {
			case <-ctl.Done():
			case <-svr.ctx.Done():
				return true, nil
			}
			if proxy.PluginCleanupFailed(svr.ctx) {
				return true, nil
			}
			return false, errors.New("control is closed and try another loop")
		}
		// If the control is nil, it means that the login failed and the service is also closed.
		return false, nil
	}, wait.NewFastBackoffManager(
		wait.FastBackoffOptions{
			Duration:        time.Second,
			Factor:          2,
			Jitter:          0.1,
			MaxDuration:     20 * time.Second,
			FastRetryCount:  3,
			FastRetryDelay:  200 * time.Millisecond,
			FastRetryWindow: time.Minute,
			FastRetryJitter: 0.5,
		},
	), true, svr.ctx.Done())
}

func (svr *Service) loopLoginUntilSuccess(maxInterval time.Duration, firstLoginExit bool) {
	xl := xlog.FromContextSafe(svr.ctx)

	loginFunc := func() (bool, error) {
		if proxy.PluginCleanupFailed(svr.ctx) {
			return true, nil
		}
		xl.Infof("try to connect to server...")
		dialer := &controlSessionDialer{
			ctx:              svr.ctx,
			common:           svr.common,
			auth:             svr.auth,
			clientSpec:       svr.clientSpec,
			vnetController:   svr.vnetController,
			connectorCreator: svr.connectorCreator,
		}
		sessionCtx, err := dialer.Dial(svr.runID)
		if err != nil {
			xl.Warnf("connect to server error: %v", err)
			if firstLoginExit {
				svr.cancel(cancelErr{Err: err})
			}
			return false, err
		}

		svr.runID = sessionCtx.RunID
		xl.AddPrefix(xlog.LogPrefix{Name: "runID", Value: svr.runID})
		xl.Infof("login to server success, get run id [%s]", svr.runID)

		sessionCtx.Isolated = svr.isolateLogs
		sessionCtx.workerPanic = svr.workerPanic
		ctl, err := NewControl(svr.ctx, sessionCtx)
		if err != nil {
			sessionCtx.Conn.Close()
			sessionCtx.Connector.Close()
			xl.Errorf("new control error: %v", err)
			return false, err
		}
		ctl.SetInWorkConnCallback(svr.handleWorkConnCb)

		// An update must either precede this snapshot or apply to the published
		// control. Keep dialing and login outside the reload transaction.
		svr.reloadMu.Lock()
		defer svr.reloadMu.Unlock()
		svr.lifecycleMu.Lock()
		defer svr.lifecycleMu.Unlock()
		if svr.closed || svr.ctx.Err() != nil || proxy.PluginCleanupFailed(svr.ctx) {
			_ = sessionCtx.Conn.Close()
			_ = sessionCtx.Connector.Close()
			return true, nil
		}
		svr.cfgMu.RLock()
		proxyCfgs := svr.proxyCfgs
		visitorCfgs := svr.visitorCfgs
		trafficSnapshot := svr.trafficSnapshot
		svr.cfgMu.RUnlock()

		ctl.run(proxyCfgs, visitorCfgs, trafficSnapshot)
		// close and replace previous control
		svr.ctlMu.Lock()
		if svr.ctl != nil {
			svr.ctl.Close()
		}
		svr.ctl = ctl
		svr.ctlMu.Unlock()
		return true, nil
	}

	// try to reconnect to server until success
	wait.BackoffUntil(loginFunc, wait.NewFastBackoffManager(
		wait.FastBackoffOptions{
			Duration:    time.Second,
			Factor:      2,
			Jitter:      0.1,
			MaxDuration: maxInterval,
		}), true, svr.ctx.Done())
}

func (svr *Service) UpdateAllConfigurer(proxyCfgs []v1.ProxyConfigurer, visitorCfgs []v1.VisitorConfigurer) error {
	svr.reloadMu.Lock()
	defer svr.reloadMu.Unlock()
	return svr.updateAllConfigurerLocked(proxyCfgs, visitorCfgs)
}

// Caller must hold reloadMu.
func (svr *Service) updateAllConfigurerLocked(proxyCfgs []v1.ProxyConfigurer, visitorCfgs []v1.VisitorConfigurer) error {
	if svr.isolateLogs {
		return ErrRestrictedConfigReload
	}
	trafficSnapshot, err := snapshotTraffic(svr.trafficBindings, proxyCfgs, visitorCfgs)
	if err != nil {
		return err
	}
	svr.cfgMu.Lock()
	svr.proxyCfgs = proxyCfgs
	svr.visitorCfgs = visitorCfgs
	svr.trafficSnapshot = trafficSnapshot
	svr.cfgMu.Unlock()

	svr.ctlMu.RLock()
	ctl := svr.ctl
	svr.ctlMu.RUnlock()

	if ctl != nil {
		return ctl.updateAllConfigurer(proxyCfgs, visitorCfgs, trafficSnapshot)
	}
	return nil
}

func snapshotTraffic(bindings *traffic.Bindings, proxies []v1.ProxyConfigurer, visitors []v1.VisitorConfigurer) (*traffic.BindingSnapshot, error) {
	if bindings == nil {
		return nil, nil
	}
	proxyNames := make([]string, 0, len(proxies))
	for _, cfg := range proxies {
		proxyNames = append(proxyNames, cfg.GetBaseConfig().Name)
	}
	visitorNames := make([]string, 0, len(visitors))
	for _, cfg := range visitors {
		visitorNames = append(visitorNames, cfg.GetBaseConfig().Name)
	}
	return bindings.Snapshot(proxyNames, visitorNames)
}

func (svr *Service) UpdateConfigSource(
	common *v1.ClientCommonConfig,
	proxyCfgs []v1.ProxyConfigurer,
	visitorCfgs []v1.VisitorConfigurer,
) error {
	svr.reloadMu.Lock()
	defer svr.reloadMu.Unlock()

	return svr.updateConfigSourceLocked(common, proxyCfgs, visitorCfgs)
}

// Caller must hold reloadMu.
func (svr *Service) updateConfigSourceLocked(
	common *v1.ClientCommonConfig,
	proxyCfgs []v1.ProxyConfigurer,
	visitorCfgs []v1.VisitorConfigurer,
) error {
	// This path is also used by the configuration manager. Reject before
	// ReplaceAll so an isolated source cannot be partially changed.
	if svr.isolateLogs {
		return ErrRestrictedConfigReload
	}
	cfgSource := svr.configSource
	if cfgSource == nil {
		return fmt.Errorf("config source is not available")
	}

	if err := cfgSource.ReplaceAll(proxyCfgs, visitorCfgs); err != nil {
		return err
	}

	// Non-atomic update semantics: source has been updated at this point.
	// Even if reload fails below, keep this common config for subsequent reloads.
	svr.cfgMu.Lock()
	svr.reloadCommon = common
	svr.cfgMu.Unlock()

	if err := svr.reloadConfigFromSourcesLocked(); err != nil {
		return err
	}
	return nil
}

func (svr *Service) Close() {
	svr.GracefulClose(time.Duration(0))
}

func (svr *Service) GracefulClose(d time.Duration) {
	svr.lifecycleMu.Lock()
	if svr.closed {
		svr.lifecycleMu.Unlock()
		<-svr.done
		return
	}
	svr.closed = true
	svr.gracefulShutdownDuration.Store(int64(d))
	cancel := svr.cancel
	started := svr.started
	svr.lifecycleMu.Unlock()
	if started {
		cancel(nil)
		<-svr.done
	} else {
		svr.stop()
		close(svr.done)
	}
}

func (svr *Service) stop() {
	if svr.network != nil {
		svr.network.Close()
	}
	// Coordinate shutdown with reload/update paths that read source pointers.
	svr.reloadMu.Lock()
	if svr.aggregator != nil {
		svr.aggregator = nil
	}
	svr.configSource = nil
	svr.storeSource = nil
	svr.reloadMu.Unlock()

	svr.ctlMu.Lock()
	defer svr.ctlMu.Unlock()
	if svr.ctl != nil {
		d := time.Duration(svr.gracefulShutdownDuration.Load())
		svr.ctl.GracefulClose(d)
		<-svr.ctl.Done()
		svr.ctl = nil
	}
	if svr.webServer != nil {
		svr.webServer.Close()
		svr.webServer = nil
	}
	if svr.metricsServer != nil {
		svr.metricsServer.Close()
		svr.metricsServer = nil
	}
	if svr.vnetController != nil {
		_ = svr.vnetController.Stop()
		svr.vnetController = nil
	}
}

func (svr *Service) getProxyStatus(name string) (*proxy.WorkingStatus, bool) {
	svr.ctlMu.RLock()
	ctl := svr.ctl
	svr.ctlMu.RUnlock()

	if ctl == nil {
		return nil, false
	}
	return ctl.pm.GetProxyStatus(name)
}

func (svr *Service) getVisitorCfg(name string) (v1.VisitorConfigurer, bool) {
	svr.ctlMu.RLock()
	ctl := svr.ctl
	svr.ctlMu.RUnlock()

	if ctl == nil {
		return nil, false
	}
	return ctl.vm.GetVisitorCfg(name)
}

func (svr *Service) StatusExporter() StatusExporter {
	return &statusExporterImpl{
		getProxyStatusFunc: svr.getProxyStatus,
	}
}

type StatusExporter interface {
	GetProxyStatus(name string) (*proxy.WorkingStatus, bool)
}

type statusExporterImpl struct {
	getProxyStatusFunc func(name string) (*proxy.WorkingStatus, bool)
}

func (s *statusExporterImpl) GetProxyStatus(name string) (*proxy.WorkingStatus, bool) {
	return s.getProxyStatusFunc(name)
}

func (svr *Service) reloadConfigFromSources() error {
	svr.reloadMu.Lock()
	defer svr.reloadMu.Unlock()
	return svr.reloadConfigFromSourcesLocked()
}

func (svr *Service) reloadConfigFromSourcesLocked() error {
	if svr.isolateLogs {
		return ErrRestrictedConfigReload
	}
	aggregator := svr.aggregator
	if aggregator == nil {
		return errors.New("config aggregator is not initialized")
	}

	svr.cfgMu.RLock()
	reloadCommon := svr.reloadCommon
	svr.cfgMu.RUnlock()

	proxies, visitors, err := aggregator.Load()
	if err != nil {
		return fmt.Errorf("reload config from sources failed: %w", err)
	}

	proxies, visitors = config.FilterClientConfigurers(reloadCommon, proxies, visitors)
	proxies = config.CompleteProxyConfigurers(proxies)
	visitors = config.CompleteVisitorConfigurers(visitors)
	requirements := validation.GetClientConfigRequirements(reloadCommon, proxies, visitors)
	if svr.vnetController == nil && requirements.VirtualNet {
		return errors.New(
			"VirtualNet-dependent configuration requires a VirtualNet runtime enabled at startup; " +
				"restart frpc after configuring featureGates.VirtualNet and virtualNet.address",
		)
	}

	// Atomically replace the entire configuration
	if err := svr.updateAllConfigurerLocked(proxies, visitors); err != nil {
		return err
	}
	return nil
}
