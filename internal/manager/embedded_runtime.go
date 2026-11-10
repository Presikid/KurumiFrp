//go:build !kurumi_external_runtime

package manager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Presikid/KurumiFrp/internal/embedded"
	clienttraffic "github.com/fatedier/frp/client/traffic"
	v1 "github.com/fatedier/frp/pkg/config/v1"
	"github.com/fatedier/frp/pkg/util/xlog"
)

const embeddedTarget = "embedded-frp-v0.72.0/"

// A panic or failed cleanup can leave shared resources in an unknown state.
// Refuse to start another embedded service until the process is restarted.
var embeddedCleanupUnknown atomic.Bool

type embeddedRuntime struct{}

// NewEmbeddedRuntime creates the in-process backend. It supports
// only the restricted feature set documented in third_party/frp/KURUMI_ISOLATION.md.
func NewEmbeddedRuntime() RuntimeBackend { return &embeddedRuntime{} }

func (*embeddedRuntime) runtimeMode() string { return "embedded" }

func (*embeddedRuntime) Resolve(kind, _ string) (string, error) {
	if embeddedCleanupUnknown.Load() {
		return "", errors.New("内嵌核心清理状态未知，请重启工作台")
	}
	if kind != "frpc" && kind != "frps" {
		return "", errors.New("不支持的实例类型")
	}
	return embeddedTarget + kind, nil
}

type embeddedConfig struct {
	client *embedded.ClientConfig
	server *v1.ServerConfig
	warn   bool
}

func parseEmbeddedConfig(ctx context.Context, target string, cfg RuntimeConfig) (embeddedConfig, error) {
	if ctx == nil {
		return embeddedConfig{}, errors.New("缺少配置检查上下文")
	}
	if err := ctx.Err(); err != nil {
		return embeddedConfig{}, err
	}
	if cfg.InstanceID == "" || target != embeddedTarget+cfg.Kind {
		return embeddedConfig{}, errors.New("内嵌核心目标与实例不匹配")
	}
	var result embeddedConfig
	switch cfg.Kind {
	case "frpc":
		client, warning, err := embedded.ParseClientConfig([]byte(cfg.Rendered.Text), cfg.Rendered.Env)
		if err != nil || client == nil {
			return result, errors.New("客户端配置未通过检查")
		}
		if err := embedded.ValidateFRPClient(embedded.FRPClientOptions{
			Common: client.Common, Proxies: client.Proxies, Visitors: client.Visitors,
		}); err != nil {
			return result, errors.New("客户端配置包含当前内嵌核心不支持的功能")
		}
		result.client, result.warn = client, warning != nil
	case "frps":
		server, warning, err := embedded.ParseServerConfig([]byte(cfg.Rendered.Text), cfg.Rendered.Env)
		if err != nil || server == nil {
			return result, errors.New("服务端配置未通过检查")
		}
		if _, err := embedded.NewServer(server); err != nil {
			return result, errors.New("服务端配置包含当前内嵌核心不支持的功能")
		}
		result.server, result.warn = server, warning != nil
	default:
		return result, errors.New("不支持的实例类型")
	}
	if err := ctx.Err(); err != nil {
		return embeddedConfig{}, err
	}
	return result, nil
}

func (*embeddedRuntime) Verify(ctx context.Context, target string, cfg RuntimeConfig) (string, error) {
	config, err := parseEmbeddedConfig(ctx, target, cfg)
	if err != nil {
		return "", err
	}
	if config.warn {
		return "配置通过检查；核心报告了非致命警告。", nil
	}
	return "配置通过检查。", nil
}

func (b *embeddedRuntime) Start(target string, cfg RuntimeConfig, output io.Writer) (RuntimeInstance, error) {
	resolved, err := b.Resolve(cfg.Kind, "")
	if err != nil {
		return nil, err
	}
	if target != resolved || cfg.InstanceID == "" || output == nil {
		return nil, errors.New("内嵌核心启动参数无效")
	}
	sink, err := xlog.NewSink(256, xlog.Info)
	if err != nil {
		return nil, errors.New("无法建立实例日志")
	}
	// Start is called while the manager owns its state lock. Parsing, file
	// access and listener construction run asynchronously outside that lock.
	cfg.Rendered.Env = append([]string(nil), cfg.Rendered.Env...)
	ctx, cancel := context.WithCancel(context.Background())
	run := &embeddedRun{cancel: cancel, ready: make(chan struct{}), exiting: make(chan struct{}), done: make(chan struct{})}
	go run.serve(ctx, target, cfg, sink, output)
	return run, nil
}

type embeddedRun struct {
	cancel           context.CancelFunc
	ready            chan struct{}
	exiting          chan struct{}
	done             chan struct{}
	err              error // published by closing done
	exitOnce         sync.Once
	cleanupUncertain atomic.Bool

	mu           sync.RWMutex
	client       *embedded.Client
	server       *embedded.Server // retained through the end of this generation
	traffic      *clienttraffic.Run
	proxyTypes   map[string]string
	finalTraffic TrafficSnapshot
	engineExit   <-chan struct{}
}

func (r *embeddedRun) serve(ctx context.Context, target string, cfg RuntimeConfig, sink *xlog.Sink, output io.Writer) {
	stopLogs, logsDone := make(chan struct{}), make(chan struct{})
	go pumpEmbeddedLogs(sink, output, stopLogs, logsDone)
	defer func() {
		if recover() != nil {
			r.markCleanupUncertain()
			r.err = errors.New("内嵌核心异常退出，清理状态未知")
		}
		r.markEngineExited()
		r.cancel()
		r.mu.Lock()
		r.client, r.traffic, r.proxyTypes = nil, nil, nil
		r.mu.Unlock()
		close(stopLogs)
		<-logsDone
		// A failed worker may have unregistered a resource before closing it.
		// Keep Wait blocked when cleanup cannot be proved safe for replacement.
		if !r.cleanupUncertain.Load() {
			close(r.done)
		}
	}()

	config, err := parseEmbeddedConfig(ctx, target, cfg)
	if err != nil {
		// A concurrent cancellation must not hide a real configuration error.
		if !errors.Is(err, context.Canceled) {
			r.err = err
		}
		return
	}
	if cfg.Kind == "frps" {
		r.err = r.runServer(ctx, config.server, sink)
		return
	}
	r.err = r.runClient(ctx, cfg.InstanceID, config.client, sink)
}

func pumpEmbeddedLogs(sink *xlog.Sink, output io.Writer, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	write := func(record xlog.Record) { _, _ = fmt.Fprintln(output, record.Message) }
	for {
		select {
		case record := <-sink.Records():
			write(record)
		case <-stop:
			for {
				select {
				case record := <-sink.Records():
					write(record)
				default:
					if dropped := sink.Dropped(); dropped != 0 {
						_, _ = fmt.Fprintf(output, "日志过多，已省略 %d 条记录\n", dropped)
					}
					return
				}
			}
		}
	}
}

func (r *embeddedRun) markEngineExited() {
	r.exitOnce.Do(func() { close(r.exiting) })
}

// A panic can leave instance resources in an unknown state. Stop only this
// run; the shared guard also prevents new embedded runs until a restart.
func (r *embeddedRun) markCleanupUncertain() {
	r.cleanupUncertain.Store(true)
	embeddedCleanupUnknown.Store(true)
	r.markEngineExited()
	r.cancel()
}

func (r *embeddedRun) CleanupUncertain() bool { return r.cleanupUncertain.Load() }

// Track the run loop separately from the later completion of resource cleanup.
func (r *embeddedRun) observeEngine(exiting, done <-chan struct{}) {
	r.mu.Lock()
	r.engineExit = exiting
	r.mu.Unlock()
	go func() {
		select {
		case <-exiting:
		case <-done:
		}
		r.markEngineExited()
	}()
}

// Forward readiness only while this generation has not been cancelled or
// begun to exit. Completion may occur without a readiness notification.
func publishEmbeddedReady(ctx context.Context, ready, exiting, done <-chan struct{}, published chan<- struct{}) {
	select {
	case <-ready:
		if ctx.Err() != nil {
			return
		}
		select {
		case <-exiting:
			return
		case <-done:
			return
		default:
			close(published)
		}
	case <-ctx.Done():
	case <-exiting:
	case <-done:
	}
}

func (r *embeddedRun) runServer(ctx context.Context, config *v1.ServerConfig, sink *xlog.Sink) error {
	server, err := embedded.NewServerWithOptions(config, embedded.ServerOptions{LogSink: sink, OnPanic: r.markCleanupUncertain})
	if err != nil {
		return errors.New("无法创建内嵌服务端")
	}
	r.mu.Lock()
	r.server = server
	r.mu.Unlock()
	defer func() {
		if value := recover(); value != nil {
			r.markCleanupUncertain()
			_ = server.Stop(context.Background())
			panic(value)
		}
	}()
	if err = server.Start(ctx); err != nil {
		// A failed constructor may still own resources during rollback.
		r.markEngineExited()
		stopErr := server.Stop(context.Background())
		if errors.Is(stopErr, embedded.ErrServerPanicked) {
			r.markCleanupUncertain()
			return errors.New("内嵌服务端异常退出，清理状态未知，请重启工作台")
		}
		if ctx.Err() != nil && errors.Is(err, context.Canceled) {
			return nil
		}
		return errors.New("内嵌服务端启动失败")
	}
	r.observeEngine(server.Exiting(), server.Done())
	publishEmbeddedReady(ctx, server.Ready(), server.Exiting(), server.Done(), r.ready)
	<-server.Done()
	if err := server.Err(); errors.Is(err, embedded.ErrServerPanicked) {
		r.markCleanupUncertain()
		return errors.New("内嵌服务端异常退出，清理状态未知，请重启工作台")
	} else if err != nil {
		return errors.New("内嵌服务端意外退出")
	}
	return nil
}

func (r *embeddedRun) runClient(ctx context.Context, instanceID string, config *embedded.ClientConfig, sink *xlog.Sink) error {
	var book clienttraffic.Book
	run, err := book.Begin(instanceID)
	if err != nil {
		return errors.New("无法初始化客户端流量统计")
	}
	defer run.Close()
	proxies := make(map[string]string, len(config.Proxies))
	visitors := make(map[string]string, len(config.Visitors))
	for _, proxy := range config.Proxies {
		name := proxy.GetBaseConfig().Name
		proxies[name] = "proxy:" + name
	}
	for _, visitor := range config.Visitors {
		name := visitor.GetBaseConfig().Name
		visitors[name] = "visitor:" + name
	}
	bindings, err := clienttraffic.NewBindings(run, proxies, visitors)
	if err != nil {
		return errors.New("无法绑定客户端流量统计")
	}
	client, err := embedded.NewFRPClient(embedded.FRPClientOptions{
		Common: config.Common, Proxies: config.Proxies, Visitors: config.Visitors,
		LogSink: sink, Traffic: bindings, OnPanic: r.markCleanupUncertain,
	})
	if err != nil {
		return errors.New("无法创建内嵌客户端")
	}
	// Publish the handle before Run can exit, so a concurrent stop can inspect
	// the engine directly even if observeEngine has not yet run.
	r.mu.Lock()
	r.client = client
	r.mu.Unlock()
	defer func() {
		if value := recover(); value != nil {
			r.markCleanupUncertain()
			_ = client.Shutdown(context.Background())
			panic(value)
		}
	}()
	if err = client.Start(); err != nil {
		r.markEngineExited()
		if cleanupErr := client.Shutdown(context.Background()); cleanupErr != nil {
			r.markCleanupUncertain()
			return errors.New("内嵌客户端清理状态未知")
		}
		return errors.New("内嵌客户端启动失败")
	}
	types := make(map[string]string, len(config.Proxies))
	for _, proxy := range config.Proxies {
		base := proxy.GetBaseConfig()
		types[base.Name] = base.Type
	}
	r.mu.Lock()
	r.client, r.traffic, r.proxyTypes = client, run, types
	r.mu.Unlock()

	r.observeEngine(client.Exiting(), client.Done())
	publishEmbeddedReady(ctx, client.Ready(), client.Exiting(), client.Done(), r.ready)
	select {
	case <-ctx.Done():
		_ = client.Shutdown(context.Background())
	case <-client.Done():
	}
	status, err := client.Wait(context.Background())
	if errors.Is(err, embedded.ErrCleanupFailed) || status.Exit == embedded.ExitCleanupFailed {
		r.markCleanupUncertain()
		return errors.New("内嵌客户端清理状态未知")
	}
	if status.Panicked {
		r.markCleanupUncertain()
		return errors.New("内嵌客户端异常退出，清理状态未知，请重启工作台")
	}
	// The forwarding workers have stopped, so the last payload count is stable.
	// Preserve it for the manager's final sample before the run is discarded.
	if snapshot := run.Snapshot(); snapshot.Active {
		r.mu.Lock()
		r.finalTraffic = TrafficSnapshot{
			SampledAt: snapshot.SampledAt, Available: true,
			ReceivedBytes: snapshot.Total.Download, SentBytes: snapshot.Total.Upload,
		}
		r.mu.Unlock()
	}
	if err != nil || status.Exit == embedded.ExitFailed || status.Exit == embedded.ExitUnexpected {
		return errors.New("内嵌客户端意外退出")
	}
	return nil
}

func (r *embeddedRun) Wait() error {
	<-r.done
	return r.err
}

func (r *embeddedRun) Ready() <-chan struct{} { return r.ready }
func (r *embeddedRun) Done() <-chan struct{}  { return r.exiting }

// ExitedNow observes the upstream signal directly as well as the forwarded
// signal, so callers do not depend on the relay goroutine being scheduled.
func (r *embeddedRun) ExitedNow() bool {
	r.mu.RLock()
	exiting, client, server := r.engineExit, r.client, r.server
	r.mu.RUnlock()
	var completed <-chan struct{}
	if client != nil {
		if exiting == nil {
			exiting = client.Exiting()
		}
		completed = client.Done()
	} else if server != nil {
		if exiting == nil {
			exiting = server.Exiting()
		}
		completed = server.Done()
	}
	select {
	case <-exiting:
		return true
	case <-completed:
		return true
	case <-r.exiting:
		return true
	default:
		return false
	}
}

// Recheck the engine's own signal directly; the forwarding goroutine can be
// scheduled after Run has already exited. Wait still owns final cleanup.
func (r *embeddedRun) ReadyNow() bool {
	select {
	case <-r.ready:
	default:
		return false
	}
	r.mu.RLock()
	exiting := r.engineExit
	r.mu.RUnlock()
	if exiting == nil {
		return false
	}
	select {
	case <-exiting:
		return false
	case <-r.exiting:
		return false
	default:
		return true
	}
}

func (r *embeddedRun) Stop(ctx context.Context) error {
	if ctx == nil {
		return errors.New("缺少停止上下文")
	}
	r.cancel()
	select {
	case <-r.done:
		return r.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *embeddedRun) ReadProxyRegistration(ctx context.Context, name string) (ProxyRegistrationStatus, bool, error) {
	if err := ctx.Err(); err != nil {
		return ProxyRegistrationStatus{}, false, err
	}
	if !r.ReadyNow() {
		return ProxyRegistrationStatus{}, false, nil
	}
	r.mu.RLock()
	client, kind := r.client, r.proxyTypes[name]
	r.mu.RUnlock()
	if client == nil || kind == "" {
		return ProxyRegistrationStatus{}, false, nil
	}
	status := client.ProxyRegistration(name)
	if !status.Known || !r.ReadyNow() {
		return ProxyRegistrationStatus{}, false, nil
	}
	return ProxyRegistrationStatus{
		Name: name, Type: kind, Phase: status.Phase,
		Error: status.Error, RemoteAddr: status.RemoteAddr,
	}, true, nil
}

// ReadTraffic reports only the embedded client's payload counters for this run.
// Server forwarding counters use the separate ReadServerTraffic interface.
func (r *embeddedRun) ReadTraffic() TrafficSnapshot {
	observation := TrafficSnapshot{SampledAt: time.Now().UTC()}
	r.mu.RLock()
	client, run, final := r.client, r.traffic, r.finalTraffic
	r.mu.RUnlock()
	if final.Available {
		return final
	}
	if client == nil || run == nil || client.Status().State != embedded.ClientRunning || !r.ReadyNow() {
		return observation
	}
	snapshot := run.Snapshot()
	observation.SampledAt = snapshot.SampledAt
	if snapshot.Active && r.ReadyNow() {
		observation.Available = true
		observation.ReceivedBytes = snapshot.Total.Download
		observation.SentBytes = snapshot.Total.Upload
	}
	return observation
}

// ReadServerTraffic reads the per-service forwarding counter. The embedded
// Server remains referenced after shutdown: accepted handlers may still finish
// recording bytes, so a stopped run's snapshot is not necessarily final.
func (r *embeddedRun) ReadServerTraffic() ServerTrafficSnapshot {
	r.mu.RLock()
	server := r.server
	r.mu.RUnlock()
	if server == nil {
		return ServerTrafficSnapshot{}
	}
	raw, ok := server.TrafficSnapshot()
	if !ok {
		return ServerTrafficSnapshot{}
	}
	snapshot := ServerTrafficSnapshot{
		SampledAt: raw.SampledAt, Available: true,
		ReceivedBytes: raw.Received, SentBytes: raw.Sent,
		ResetEpoch: raw.ResetEpoch, EpochKnown: raw.ResetEpochValid,
		Proxies:          make([]ServerTrafficProxy, 0, len(raw.Proxies)),
		ProxiesTruncated: raw.ProxiesTruncated,
	}
	for _, proxy := range raw.Proxies {
		snapshot.Proxies = append(snapshot.Proxies, ServerTrafficProxy{
			Name: proxy.Name, Type: proxy.Type,
			ReceivedBytes: proxy.Received, SentBytes: proxy.Sent,
		})
	}
	return snapshot
}
