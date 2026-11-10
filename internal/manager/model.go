package manager

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

type Instance struct {
	ID                    string `json:"id"`
	Name                  string `json:"name"`
	Kind                  string `json:"kind"`
	Host                  string `json:"host"`
	Port                  int    `json:"port"`
	HTTPPort              int    `json:"httpPort"`
	HTTPSPort             int    `json:"httpsPort"`
	Token                 string `json:"token,omitempty"`
	HasToken              bool   `json:"hasToken,omitempty"`
	AutoStart             bool   `json:"autoStart,omitempty"`
	User                  string `json:"user,omitempty"`
	NatHoleSTUNServer     string `json:"natHoleStunServer,omitempty"`
	DNSServer             string `json:"dnsServer,omitempty"`
	ClientTransport       string `json:"clientTransport,omitempty"`
	LoginFailExit         bool   `json:"loginFailExit,omitempty"`
	ClientTLSDisabled     bool   `json:"clientTLSDisabled,omitempty"`
	ServerAllowPlaintext  bool   `json:"serverAllowPlaintext,omitempty"`
	KCPBindPort           int    `json:"kcpBindPort,omitempty"`
	QUICBindPort          int    `json:"quicBindPort,omitempty"`
	TCPMuxHTTPConnectPort int    `json:"tcpmuxHTTPConnectPort,omitempty"`
	TCPMuxPassthrough     bool   `json:"tcpmuxPassthrough,omitempty"`
	SubDomainHost         string `json:"subDomainHost,omitempty"`
	MaxPortsPerClient     int    `json:"maxPortsPerClient,omitempty"`
	ServerAllowedPorts    string `json:"serverAllowedPorts,omitempty"`
	ServerAccessMode      string `json:"serverAccessMode,omitempty"`
	ServerAccessList      string `json:"serverAccessList,omitempty"`
	ProxyBindAddr         string `json:"proxyBindAddr,omitempty"`
	VhostHTTPTimeout      int    `json:"vhostHTTPTimeout,omitempty"`
	UserConnTimeout       int    `json:"userConnTimeout,omitempty"`
	ClientPoolCount       int    `json:"clientPoolCount,omitempty"`
	ServerMaxPoolCount    int    `json:"serverMaxPoolCount,omitempty"`
	TransportMuxDisabled  bool   `json:"transportMuxDisabled,omitempty"`
}

func (i Instance) proxyBindHost() string {
	if i.ProxyBindAddr != "" {
		return i.ProxyBindAddr
	}
	return i.Host
}

type Tunnel struct {
	ID                   string   `json:"id"`
	InstanceID           string   `json:"instanceId"`
	Name                 string   `json:"name"`
	Type                 string   `json:"type"`
	LocalHost            string   `json:"localHost"`
	LocalPort            int      `json:"localPort"`
	RemotePort           int      `json:"remotePort"`
	Domains              []string `json:"domains"`
	Locations            []string `json:"locations,omitempty"`
	HostHeaderRewrite    string   `json:"hostHeaderRewrite,omitempty"`
	ProbePath            string   `json:"probePath,omitempty"`
	ProbeMethod          string   `json:"probeMethod,omitempty"`
	HealthCheckType      string   `json:"healthCheckType,omitempty"`
	HealthCheckPath      string   `json:"healthCheckPath,omitempty"`
	HealthCheckTimeout   int      `json:"healthCheckTimeoutSeconds,omitempty"`
	HealthCheckInterval  int      `json:"healthCheckIntervalSeconds,omitempty"`
	HealthCheckMaxFailed int      `json:"healthCheckMaxFailed,omitempty"`
	ServerName           string   `json:"serverName"`
	Secret               string   `json:"secret,omitempty"`
	HasSecret            bool     `json:"hasSecret,omitempty"`
	BandwidthKB          int      `json:"bandwidthKB"`
	BandwidthMode        string   `json:"bandwidthMode,omitempty"`
	ProxyProtocolVersion string   `json:"proxyProtocolVersion,omitempty"`
	Enabled              bool     `json:"enabled"`
	AllowUsers           []string `json:"allowUsers,omitempty"`
	ServerUser           string   `json:"serverUser,omitempty"`
	UseEncryption        bool     `json:"useEncryption,omitempty"`
	UseCompression       bool     `json:"useCompression,omitempty"`
	Protocol             string   `json:"protocol,omitempty"`
	KeepTunnelOpen       bool     `json:"keepTunnelOpen,omitempty"`
	MaxRetriesAnHour     int      `json:"maxRetriesAnHour,omitempty"`
	MinRetryInterval     int      `json:"minRetryInterval,omitempty"`
	FallbackTo           string   `json:"fallbackTo,omitempty"`
	FallbackTimeoutMs    int      `json:"fallbackTimeoutMs,omitempty"`
	DisableAssistedAddrs bool     `json:"disableAssistedAddrs,omitempty"`
	Subdomain            string   `json:"subdomain,omitempty"`
	Multiplexer          string   `json:"multiplexer,omitempty"`
	HTTPAuthEnabled      bool     `json:"httpAuthEnabled,omitempty"`
	// Kept separate from the TCPMUX field so old HTTP drafts do not activate previously ignored settings.
	HTTPRouteByUser      string   `json:"httpRouteByUser,omitempty"`
	HTTPUser             string   `json:"httpUser,omitempty"`
	HTTPPassword         string   `json:"httpPassword,omitempty"`
	HasHTTPPassword      bool     `json:"hasHTTPPassword,omitempty"`
	RouteByHTTPUser      string   `json:"routeByHTTPUser,omitempty"`

	RequestHeaders  []HTTPHeader `json:"requestHeaders,omitempty"`
	ResponseHeaders []HTTPHeader `json:"responseHeaders,omitempty"`
}
type State struct {
	Instances []Instance `json:"instances"`
	Tunnels   []Tunnel   `json:"tunnels"`
}

func (s State) Instance(id string) (Instance, bool) {
	for _, i := range s.Instances {
		if i.ID == id {
			return i, true
		}
	}
	return Instance{}, false
}
func (s State) Tunnel(id string) (Tunnel, bool) {
	for _, t := range s.Tunnels {
		if t.ID == id {
			return t, true
		}
	}
	return Tunnel{}, false
}
func (s State) clone() State {
	n := State{Instances: append([]Instance{}, s.Instances...), Tunnels: append([]Tunnel{}, s.Tunnels...)}
	for j := range n.Tunnels {
		n.Tunnels[j].Domains = append([]string{}, n.Tunnels[j].Domains...)
		n.Tunnels[j].Locations = append([]string(nil), n.Tunnels[j].Locations...)
		n.Tunnels[j].AllowUsers = append([]string(nil), n.Tunnels[j].AllowUsers...)
		n.Tunnels[j].RequestHeaders = append([]HTTPHeader(nil), n.Tunnels[j].RequestHeaders...)
		n.Tunnels[j].ResponseHeaders = append([]HTTPHeader(nil), n.Tunnels[j].ResponseHeaders...)
	}
	return n
}
func (s State) public() State {
	n := s.clone()
	for j := range n.Instances {
		n.Instances[j].HasToken = n.Instances[j].Token != ""
		n.Instances[j].Token = ""
	}
	for j := range n.Tunnels {
		n.Tunnels[j].HasSecret = n.Tunnels[j].Secret != ""
		n.Tunnels[j].Secret = ""
		n.Tunnels[j].HasHTTPPassword = n.Tunnels[j].HTTPPassword != ""
		n.Tunnels[j].HTTPPassword = ""
		for _, headers := range [][]HTTPHeader{n.Tunnels[j].RequestHeaders, n.Tunnels[j].ResponseHeaders} {
			for k := range headers {
				headers[k].HasValue = headers[k].Value != ""
				headers[k].Value = ""
			}
		}
	}
	return n
}

var idPattern = regexp.MustCompile(`^[a-f0-9]{16}$`)
var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
var domainPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?$`)

const maxTunnelCount = 512

func probePathOK(s string) bool {
	if s == "" {
		return true
	}
	if len(s) > 512 || !strings.HasPrefix(s, "/") || strings.HasPrefix(s, "//") {
		return false
	}
	u, err := url.ParseRequestURI(s)
	return err == nil && !u.IsAbs() && u.Host == "" && u.Fragment == ""
}

func probeMethodOK(s string) bool {
	return s == "" || s == "HEAD" || s == "GET"
}
func hostOK(s string) bool {
	if net.ParseIP(s) != nil {
		return true
	}
	if len(s) == 0 || len(s) > 253 || !domainPattern.MatchString(s) {
		return false
	}
	for _, part := range strings.Split(s, ".") {
		if len(part) == 0 || len(part) > 63 || part[0] == '-' || part[len(part)-1] == '-' {
			return false
		}
	}
	return true
}
func labelOK(s string) bool {
	if strings.TrimSpace(s) == "" || len([]rune(s)) > 80 {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || r == '{' || r == '}' {
			return false
		}
	}
	return true
}
func portOK(n int) bool { return n >= 1 && n <= 65535 }

// Only treat numeric bindings as conflicting when overlap is certain. DNS
// names and IPv4/IPv6 dual-stack behavior are not known from the draft.
func listenerHostsOverlap(a, b string) bool {
	ai, bi := net.ParseIP(a), net.ParseIP(b)
	if ai == nil || bi == nil {
		return false
	}
	if ai.Equal(bi) {
		return true
	}
	return (ai.To4() != nil) == (bi.To4() != nil) && (ai.IsUnspecified() || bi.IsUnspecified())
}

// Collect conflicts without resolving DNS, so validation never depends on the network.
func visitorListenerConflicts(s State) map[string]string {
	type listener struct {
		host    string
		port    int
		name    string
		id      string
		network string
	}
	var listeners []listener
	conflicts := map[string]string{}
	for _, i := range s.Instances {
		if i.Kind == "frps" {
			listeners = append(listeners, listener{i.Host, i.Port, i.Name, i.ID, "tcp"})
			for _, p := range []int{i.HTTPPort, i.HTTPSPort, i.TCPMuxHTTPConnectPort} {
				if p != 0 {
					listeners = append(listeners, listener{i.proxyBindHost(), p, i.Name, i.ID, "tcp"})
				}
			}
			for _, p := range []int{i.KCPBindPort, i.QUICBindPort} {
				if p != 0 {
					listeners = append(listeners, listener{i.Host, p, i.Name, i.ID, "udp"})
				}
			}
		}
	}
	for _, t := range s.Tunnels {
		if !t.Enabled || !isVisitor(t.Type) || t.LocalPort < 0 {
			continue
		}
		network := visitorNetwork(t.Type)
		for _, l := range listeners {
			if l.network == network && l.port == t.LocalPort && listenerHostsOverlap(l.host, t.LocalHost) {
				a, b := l.id, t.ID
				if a > b {
					a, b = b, a
				}
				key := fmt.Sprintf("%s/%s/%s/%d", a, b, network, l.port)
				conflicts[key] = fmt.Sprintf("本地 %s 监听地址或端口冲突：%s / %s", strings.ToUpper(network), l.name, t.Name)
			}
		}
		listeners = append(listeners, listener{t.LocalHost, t.LocalPort, t.Name, t.ID, network})
	}
	return conflicts
}

func validateVisitorListeners(old, next State) error {
	existing := visitorListenerConflicts(old)
	for key, message := range visitorListenerConflicts(next) {
		if _, ok := existing[key]; !ok {
			return fmt.Errorf("%s", message)
		}
	}
	return nil
}

func validate(s State) error {
	if len(s.Instances) > 64 || len(s.Tunnels) > maxTunnelCount {
		return fmt.Errorf("最多支持 64 个实例、512 条规则")
	}
	ids := map[string]bool{}
	labels := map[string]bool{}
	ports := map[string]string{}
	names := map[string]bool{}
	headerCounts := map[string]int{}
	type serverListener struct {
		host string
		port int
		name string
	}
	var serverListeners, serverUDPListeners []serverListener
	claim := func(key, label string) error {
		if old, ok := ports[key]; ok {
			return fmt.Errorf("端口或域名冲突：%s / %s", old, label)
		}
		ports[key] = label
		return nil
	}
	for _, i := range s.Instances {
		if !idPattern.MatchString(i.ID) || ids[i.ID] {
			return fmt.Errorf("实例 ID 无效或重复")
		}
		ids[i.ID] = true
		if !labelOK(i.Name) || labels[i.Name] {
			return fmt.Errorf("实例名称为空、过长或重复")
		}
		labels[i.Name] = true
		if i.Kind != "frpc" && i.Kind != "frps" {
			return fmt.Errorf("实例类型必须为 frpc 或 frps")
		}
		if !hostOK(i.Host) || !portOK(i.Port) {
			return fmt.Errorf("实例地址或端口无效")
		}
		if i.Token == "" {
			return fmt.Errorf("必须设置实例认证令牌")
		}
		if err := validateProtocolInstance(i); err != nil {
			return err
		}
		if i.Kind == "frpc" && (i.HTTPPort != 0 || i.HTTPSPort != 0 || i.TCPMuxHTTPConnectPort != 0 || i.KCPBindPort != 0 || i.QUICBindPort != 0) {
			return fmt.Errorf("客户端不能设置服务端监听端口")
		}
		if i.Kind == "frps" {
			seen := map[int]bool{}
			bindings := []serverListener{{i.Host, i.Port, i.Name}, {i.proxyBindHost(), i.HTTPPort, i.Name}, {i.proxyBindHost(), i.HTTPSPort, i.Name}, {i.proxyBindHost(), i.TCPMuxHTTPConnectPort, i.Name}}
			for _, binding := range bindings {
				if binding.port == 0 {
					continue
				}
				if !portOK(binding.port) || seen[binding.port] {
					return fmt.Errorf("服务端监听端口无效或重复")
				}
				seen[binding.port] = true
				for _, other := range serverListeners {
					if other.port == binding.port && listenerHostsOverlap(other.host, binding.host) {
						return fmt.Errorf("端口或域名冲突：%s / %s", other.name, i.Name)
					}
				}
				serverListeners = append(serverListeners, binding)
			}
			seenUDP := map[int]bool{}
			for _, p := range []int{i.KCPBindPort, i.QUICBindPort} {
				if p == 0 {
					continue
				}
				if !portOK(p) || seenUDP[p] {
					return fmt.Errorf("服务端 UDP 监听端口无效或重复")
				}
				seenUDP[p] = true
				for _, other := range serverUDPListeners {
					if other.port == p && listenerHostsOverlap(other.host, i.Host) {
						return fmt.Errorf("UDP 监听端口冲突：%s / %s", other.name, i.Name)
					}
				}
				serverUDPListeners = append(serverUDPListeners, serverListener{i.Host, p, i.Name})
			}
		}
	}
	for _, t := range s.Tunnels {
		if !idPattern.MatchString(t.ID) || ids[t.ID] {
			return fmt.Errorf("规则 ID 无效或重复")
		}
		ids[t.ID] = true
		i, ok := s.Instance(t.InstanceID)
		if !ok || i.Kind != "frpc" {
			return fmt.Errorf("规则必须归属有效 frpc 实例")
		}
		if !namePattern.MatchString(t.Name) {
			return fmt.Errorf("规则名称限 1-64 位英文、数字、点、横线或下划线")
		}
		key := t.InstanceID + "/" + t.Name
		if names[key] {
			return fmt.Errorf("同一实例的规则名称不能重复")
		}
		names[key] = true
		if !hostOK(t.LocalHost) || (!portOK(t.LocalPort) && !(supportsUnboundVisitor(t.Type) && t.LocalPort < 0)) {
			return fmt.Errorf("本地地址或端口无效")
		}
		if t.BandwidthKB < 0 || t.BandwidthKB > 10000000 {
			return fmt.Errorf("带宽限制须为 0-10000000 KB/s")
		}
		if t.BandwidthMode != "" && t.BandwidthMode != "client" && t.BandwidthMode != "server" {
			return fmt.Errorf("带宽限制位置仅支持客户端或服务端")
		}
		if isVisitor(t.Type) && t.BandwidthMode != "" {
			return fmt.Errorf("Visitor 不支持带宽限制位置")
		}
		if t.ProxyProtocolVersion != "" && t.ProxyProtocolVersion != "v1" && t.ProxyProtocolVersion != "v2" {
			return fmt.Errorf("PROXY 协议版本仅支持 v1 或 v2")
		}
		if (t.Type == "udp" || t.Type == "sudp") && t.ProxyProtocolVersion != "" {
			return fmt.Errorf("当前 UDP 代理暂不支持配置 PROXY 协议")
		}
		if isVisitor(t.Type) && t.ProxyProtocolVersion != "" {
			return fmt.Errorf("Visitor 不支持 PROXY 协议")
		}
		if err := validateRuleHealthCheck(t); err != nil {
			return err
		}
		if err := validateHTTPHeaders(t); err != nil {
			return err
		}
		headerCounts[t.InstanceID] += len(t.RequestHeaders) + len(t.ResponseHeaders)
		if headerCounts[t.InstanceID] > maxHTTPHeadersPerInstance {
			return fmt.Errorf("每个客户端实例最多支持 128 个自定义 HTTP 头")
		}
		if t.HostHeaderRewrite != "" && (t.Type != "http" || !validHostHeaderRewrite(t.HostHeaderRewrite)) {
			return fmt.Errorf("HTTP Host 重写值无效或不适用于此协议")
		}
		if err := validateHTTPTypeLocations(t); err != nil {
			return err
		}
		if t.Type == "http" && t.HTTPAuthEnabled {
			if t.HTTPUser == "" || t.HTTPPassword == "" || !protocolTextOK(t.HTTPUser) || strings.Contains(t.HTTPUser, ":") {
				return fmt.Errorf("HTTP Basic 认证需要有效用户名和密码；用户名不能包含冒号")
			}
		}
		if t.Type == "http" && (!protocolTextOK(t.HTTPRouteByUser) || strings.Contains(t.HTTPRouteByUser, ":")) {
			return fmt.Errorf("HTTP 路由用户名无效或包含冒号")
		}
		endpoint := strings.ToLower(i.Host) + fmt.Sprintf(":%d/", i.Port)
		if ip := net.ParseIP(i.Host); ip != nil {
			endpoint = ip.String() + fmt.Sprintf(":%d/", i.Port)
		}
		switch t.Type {
		case "tcp", "udp":
			if t.RemotePort < 0 || t.RemotePort > 65535 {
				return fmt.Errorf("TCP/UDP 远端端口须为 0-65535；0 表示动态分配")
			}
			if t.Enabled && t.RemotePort != 0 {
				if err := claim(endpoint+t.Type+fmt.Sprint(t.RemotePort), t.Name); err != nil {
					return err
				}
			}
		case "http", "https":
			if !probePathOK(t.ProbePath) || !probeMethodOK(t.ProbeMethod) {
				return fmt.Errorf("HTTP 检测路径或方法无效（路径须以单个 / 开头，方法仅支持 HEAD/GET）")
			}
			if len(t.Domains) > 16 || (len(t.Domains) == 0 && t.Subdomain == "") {
				return fmt.Errorf("HTTP/HTTPS 需要自定义域名或子域名，最多 16 个自定义域名")
			}
			if !validSubdomain(t.Subdomain) {
				return fmt.Errorf("HTTP/HTTPS 子域名无效")
			}
			claimRoute := func(domain string) error {
				key := endpoint + t.Type + "/" + domain
				if t.Type == "https" {
					return claim(key, t.Name)
				}
				locations := t.Locations
				if len(locations) == 0 {
					locations = []string{""}
				}
				for _, location := range locations {
					route := fmt.Sprintf("%s/location/%d:%s/user/%d:%s", key, len(location), location, len(t.HTTPRouteByUser), t.HTTPRouteByUser)
					if err := claim(route, t.Name); err != nil {
						return err
					}
				}
				return nil
			}
			for _, d := range t.Domains {
				if !vhostDomainOK(d) {
					return fmt.Errorf("虚拟主机域名无效")
				}
				if t.Enabled {
					if err := claimRoute(strings.ToLower(d)); err != nil {
						return err
					}
				}
			}
			if t.Enabled && t.Subdomain != "" {
				if err := claimRoute("subdomain/"+strings.ToLower(t.Subdomain)); err != nil {
					return err
				}
			}
		case "stcp", "xtcp", "sudp", "stcp-visitor", "xtcp-visitor", "sudp-visitor", "tcpmux":
			if err := validateProtocolTunnel(t, endpoint, claim); err != nil {
				return err
			}
		default:
			return fmt.Errorf("不支持的协议")
		}
		if t.Enabled && !isVisitor(t.Type) {
			wireName := t.Name
			if i.User != "" {
				wireName = i.User + "." + wireName
			}
			if err := claim(endpoint+"proxy/"+wireName, t.Name); err != nil {
				return err
			}
		}
	}
	return validateVisitorFallbacks(s)
}
