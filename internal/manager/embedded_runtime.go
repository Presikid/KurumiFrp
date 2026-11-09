//go:build kurumi_frp_patched

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

// NewEmbeddedRuntime creates the development in-process backend. It supports
// only the restricted feature set documented in third_party/frp/KURUMI_ISOLATION.md.
func NewEmbeddedRuntime() RuntimeBackend { return &embeddedRuntime{} }

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
	run := &embeddedRun{cancel: cancel, done: make(chan struct{})}
	go run.serve(ctx, target, cfg, sink, output)
	return run, nil
}

type embeddedRun struct {
	cancel context.CancelFunc
	done   chan struct{}
	err    error // published by closing done

	mu         sync.RWMutex
	client     *embedded.Client
	traffic    *clienttraffic.Run
	proxyTypes map[string]string
}

func (r *embeddedRun) serve(ctx context.Context, target string, cfg RuntimeConfig, sink *xlog.Sink, output io.Writer) {
	stopLogs, logsDone := make(chan struct{}), make(chan struct{})
	go pumpEmbeddedLogs(sink, output, stopLogs, logsDone)
	defer func() {
		if recover() != nil {
			embeddedCleanupUnknown.Store(true)
			r.err = errors.New("内嵌核心异常退出，清理状态未知")
		}
		r.cancel()
		r.mu.Lock()
		r.client, r.traffic, r.proxyTypes = nil, nil, nil
		r.mu.Unlock()
		close(stopLogs)
		<-logsDone
		close(r.done)
	}()

	config, err := parseEmbeddedConfig(ctx, target, cfg)
	if err != nil {
		if ctx.Err() == nil {
			r.err = err
		}
		return
	}
	if cfg.Kind == "frps" {
		r.err = runEmbeddedServer(ctx, config.server, sink)
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

func runEmbeddedServer(ctx context.Context, config *v1.ServerConfig, sink *xlog.Sink) error {
	server, err := embedded.NewServerWithOptions(config, embedded.ServerOptions{LogSink: sink})
	if err != nil {
		return errors.New("无法创建内嵌服务端")
	}
	defer func() {
		if value := recover(); value != nil {
			embeddedCleanupUnknown.Store(true)
			_ = server.Stop(context.Background())
			panic(value)
		}
	}()
	if err = server.Start(ctx); err != nil {
		// A failed constructor may still own resources during rollback.
		_ = server.Stop(context.Background())
		if ctx.Err() != nil && errors.Is(err, context.Canceled) {
			return nil
		}
		return errors.New("内嵌服务端启动失败")
	}
	<-server.Done()
	if server.Err() != nil {
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
		Common: config.Common, Proxies: config.Proxies, Visitors: config.Visitors, LogSink: sink, Traffic: bindings,
	})
	if err != nil {
		return errors.New("无法创建内嵌客户端")
	}
	defer func() {
		if value := recover(); value != nil {
			embeddedCleanupUnknown.Store(true)
			_ = client.Shutdown(context.Background())
			panic(value)
		}
	}()
	if err = client.Start(); err != nil {
		if cleanupErr := client.Shutdown(context.Background()); cleanupErr != nil {
			embeddedCleanupUnknown.Store(true)
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

	select {
	case <-ctx.Done():
		_ = client.Shutdown(context.Background())
	case <-client.Done():
	}
	status, err := client.Wait(context.Background())
	if errors.Is(err, embedded.ErrCleanupFailed) || status.Exit == embedded.ExitCleanupFailed {
		embeddedCleanupUnknown.Store(true)
		return errors.New("内嵌客户端清理状态未知")
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
	r.mu.RLock()
	client, kind := r.client, r.proxyTypes[name]
	r.mu.RUnlock()
	if client == nil || kind == "" {
		return ProxyRegistrationStatus{}, false, nil
	}
	status := client.ProxyRegistration(name)
	if !status.Known {
		return ProxyRegistrationStatus{}, false, nil
	}
	return ProxyRegistrationStatus{Name: name, Type: kind, Phase: status.Phase}, true, nil
}

// ReadTraffic reports the embedded client's payload counters for this run.
// The server source remains unavailable until its protocol accounting agrees
// with the manager's payload-only definition.
func (r *embeddedRun) ReadTraffic() TrafficSnapshot {
	observation := TrafficSnapshot{SampledAt: time.Now().UTC()}
	r.mu.RLock()
	client, run := r.client, r.traffic
	r.mu.RUnlock()
	if client == nil || run == nil || client.Status().State != embedded.ClientRunning {
		return observation
	}
	snapshot := run.Snapshot()
	if snapshot.Active {
		observation.Available = true
		observation.ReceivedBytes = snapshot.Total.Download
		observation.SentBytes = snapshot.Total.Upload
	}
	return observation
}
