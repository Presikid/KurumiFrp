//go:build !kurumi_external_runtime

package embedded

import (
	"context"
	"encoding/json"
	"errors"

	frpclient "github.com/fatedier/frp/client"
	clienttraffic "github.com/fatedier/frp/client/traffic"
	"github.com/fatedier/frp/pkg/config"
	"github.com/fatedier/frp/pkg/config/source"
	v1 "github.com/fatedier/frp/pkg/config/v1"
	"github.com/fatedier/frp/pkg/config/v1/validation"
	"github.com/fatedier/frp/pkg/util/xlog"
)

// This marker covers only the restricted profile documented by the fork; the
// upstream-wide KurumiEmbeddedIsolationVersion remains deliberately undefined.
// The options and completion call below are required to activate this profile.
var _ [1]struct{} = [frpclient.KurumiRestrictedClientProfileVersion]struct{}{}

var (
	ErrInvalidFRPConfig = errors.New("invalid embedded frp client configuration")
	ErrFRPUnsupported   = errors.New("unsupported embedded frp client configuration")
	ErrFRPCreate        = errors.New("could not create embedded frp client service")
)

// FRPClientOptions supplies one instance's complete in-memory configuration.
// Do not mutate any input while NewFRPClient is copying it. The constructor
// keeps its own copies. Proxies and visitors use their unprefixed config names.
// Includes, store sources, frp admin/metrics servers, virtual net, OIDC and
// token sources, upstream proxies and plugins
// are unsupported. Scoped DNS accepts an IP literal. Input completed with
// upstream Complete may already
// contain an inherited http_proxy; use ParseClientConfig or
// CompleteWithoutProxyEnvironment before passing a completed configuration.
type FRPClientOptions struct {
	Common   *v1.ClientCommonConfig
	Proxies  []v1.ProxyConfigurer
	Visitors []v1.VisitorConfigurer
	// Traffic belongs to this service generation. Nil leaves accounting disabled.
	Traffic *clienttraffic.Bindings
	// LogSink is dedicated to this generation. Nil retains the restricted
	// profile's discard logger. Records may contain sensitive upstream details.
	LogSink *xlog.Sink
}

type preparedFRPClient struct {
	common v1.ClientCommonConfig
	source *source.ConfigSource
}

// ValidateFRPClient checks the restricted configuration without opening a
// listener or claiming the optional traffic bindings or log sink. Construction
// can still fail later if those generation-specific resources are invalid.
func ValidateFRPClient(options FRPClientOptions) error {
	_, err := prepareFRPClient(options)
	return err
}

func prepareFRPClient(options FRPClientOptions) (*preparedFRPClient, error) {
	if options.Common == nil {
		return nil, ErrInvalidFRPConfig
	}
	data, err := json.Marshal(options.Common)
	if err != nil {
		return nil, ErrInvalidFRPConfig
	}
	var common v1.ClientCommonConfig
	if err := json.Unmarshal(data, &common); err != nil {
		return nil, ErrInvalidFRPConfig
	}
	if common.VirtualNet.Address != "" ||
		common.WebServer.Port != 0 || common.MetricsServer.Port != 0 ||
		len(common.IncludeConfigFiles) != 0 || common.Store.IsEnabled() ||
		(common.Auth.Method != "" && common.Auth.Method != v1.AuthMethodToken) ||
		common.Auth.TokenSource != nil || common.Transport.ProxyURL != "" {
		return nil, ErrFRPUnsupported
	}
	if err := frpclient.ValidateRestrictedNetworkConfig(&common); err != nil {
		return nil, ErrFRPUnsupported
	}

	proxies := make([]v1.ProxyConfigurer, 0, len(options.Proxies))
	proxyNames := make(map[string]struct{}, len(options.Proxies))
	for _, original := range options.Proxies {
		if nilInterface(original) {
			return nil, ErrInvalidFRPConfig
		}
		proxy := original.Clone()
		if nilInterface(proxy) || proxy.GetBaseConfig() == nil {
			return nil, ErrInvalidFRPConfig
		}
		base := proxy.GetBaseConfig()
		if base.Plugin.Type != "" {
			return nil, ErrFRPUnsupported
		}
		if _, exists := proxyNames[base.Name]; exists {
			return nil, ErrInvalidFRPConfig
		}
		proxyNames[base.Name] = struct{}{}
		proxies = append(proxies, proxy)
	}
	visitors := make([]v1.VisitorConfigurer, 0, len(options.Visitors))
	visitorNames := make(map[string]struct{}, len(options.Visitors))
	for _, original := range options.Visitors {
		if nilInterface(original) {
			return nil, ErrInvalidFRPConfig
		}
		visitor := original.Clone()
		if nilInterface(visitor) || visitor.GetBaseConfig() == nil {
			return nil, ErrInvalidFRPConfig
		}
		base := visitor.GetBaseConfig()
		if base.Plugin.Type != "" {
			return nil, ErrFRPUnsupported
		}
		if _, exists := visitorNames[base.Name]; exists {
			return nil, ErrInvalidFRPConfig
		}
		visitorNames[base.Name] = struct{}{}
		visitors = append(visitors, visitor)
	}

	if err := common.CompleteWithoutProxyEnvironment(); err != nil {
		return nil, ErrInvalidFRPConfig
	}
	// Keep preflight aligned with the service constructor's transport allowlist.
	switch common.Transport.Protocol {
	case "tcp", "kcp", "quic", "websocket", "wss":
	default:
		return nil, ErrFRPUnsupported
	}
	activeProxies, activeVisitors := config.FilterClientConfigurers(&common, proxies, visitors)
	activeProxies = config.CompleteProxyConfigurers(activeProxies)
	activeVisitors = config.CompleteVisitorConfigurers(activeVisitors)
	if _, err := validation.ValidateAllClientConfig(&common, activeProxies, activeVisitors, nil); err != nil {
		return nil, ErrInvalidFRPConfig
	}

	configSource := source.NewConfigSource()
	if err := configSource.ReplaceAll(proxies, visitors); err != nil {
		return nil, ErrInvalidFRPConfig
	}
	return &preparedFRPClient{common: common, source: configSource}, nil
}

// NewFRPClient creates a service and a separate in-memory configuration source
// for one instance. It does not start the service or connect to the server.
// Errors intentionally omit raw upstream error text and configuration values.
func NewFRPClient(options FRPClientOptions) (*Client, error) {
	prepared, err := prepareFRPClient(options)
	if err != nil {
		return nil, err
	}
	service, err := frpclient.NewService(frpclient.ServiceOptions{
		Common:                 &prepared.common,
		Isolated:               true,
		Traffic:                options.Traffic,
		LogSink:                options.LogSink,
		ConfigSourceAggregator: source.NewAggregator(prepared.source),
	})
	if err != nil {
		return nil, ErrFRPCreate
	}
	client, err := NewClient(&frpService{service: service})
	if err != nil {
		service.Close()
		return nil, err
	}
	return client, nil
}

type frpService struct{ service *frpclient.Service }

func (s *frpService) Run(ctx context.Context) error {
	err := s.service.Run(ctx)
	if ctx.Err() == context.Canceled && errors.Is(err, frpclient.ErrServiceClosed) {
		// Stop may close the service before its Run goroutine starts.
		return context.Canceled
	}
	return err
}
func (s *frpService) Close() { s.service.Close() }
func (s *frpService) Ready() <-chan struct{} { return s.service.Ready() }
func (s *frpService) Exiting() <-chan struct{} { return s.service.Exiting() }

func (s *frpService) ProxyPhase(name string) (string, bool) {
	status, ok := s.proxyDetails(name)
	return status.Phase, ok
}

func (s *frpService) proxyDetails(name string) (Registration, bool) {
	status, ok := s.service.StatusExporter().GetProxyStatus(name)
	if !ok || status == nil {
		return Registration{}, false
	}
	result := Registration{Phase: status.Phase}
	if status.Err != "" {
		// Server-supplied errors can contain arbitrary details or credentials.
		result.Error = "核心报告代理错误"
	}
	if status.Err == "" || status.Phase == "running" {
		result.RemoteAddr = status.RemoteAddr
	}
	return result, true
}
