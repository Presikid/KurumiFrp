package manager

import (
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/net/http/httpguts"
)

func isVisitor(kind string) bool {
	return kind == "stcp-visitor" || kind == "xtcp-visitor" || kind == "sudp-visitor"
}

func validSubdomain(value string) bool {
	return value == "" || (hostOK(value) && !strings.Contains(value, ".") && net.ParseIP(value) == nil)
}

func validHostHeaderRewrite(value string) bool {
	return len(value) <= 512 && protocolTextOK(value) && httpguts.ValidHostHeader(value)
}

func validateHTTPTypeLocations(t Tunnel) error {
	if len(t.Locations) == 0 {
		return nil
	}
	if t.Type != "http" || len(t.Locations) > 64 {
		return fmt.Errorf("路径前缀仅适用于 HTTP 规则，最多 64 条")
	}
	seen := make(map[string]bool, len(t.Locations))
	for _, location := range t.Locations {
		if len(location) > 1024 || !strings.HasPrefix(location, "/") || strings.TrimSpace(location) != location || !protocolTextOK(location) {
			return fmt.Errorf("HTTP 路径前缀无效：每条须以 / 开头且不超过 1024 字节")
		}
		if seen[location] {
			return fmt.Errorf("HTTP 路径前缀不能重复")
		}
		seen[location] = true
	}
	return nil
}

// Preserve the effective namespace when an existing Visitor changes client.
// A fallback name is local to its client, so it must be configured separately
// after the move instead of being silently rebound to a same-named Visitor.
func prepareVisitorTransfer(s State, original Tunnel, next *Tunnel) error {
	if original.InstanceID == next.InstanceID || !isVisitor(original.Type) || !isVisitor(next.Type) {
		return nil
	}
	source, ok := s.Instance(original.InstanceID)
	if !ok || source.Kind != "frpc" {
		return fmt.Errorf("源规则所属的客户端实例不存在")
	}
	target, ok := s.Instance(next.InstanceID)
	if !ok || target.Kind != "frpc" {
		return fmt.Errorf("目标必须是已保存的 frpc 客户端实例")
	}
	if original.Type == "xtcp-visitor" && original.FallbackTo != "" {
		return fmt.Errorf("含回退目标的 XTCP Visitor 暂不支持跨实例移动或复制；请先清除待移动规则或停用副本的回退，再在目标实例设置")
	}
	if next.Type == "xtcp-visitor" && next.FallbackTo != "" {
		return fmt.Errorf("跨实例移动时请保持 XTCP 回退目标为空，移动后再在目标实例设置")
	}
	if next.ServerUser != "" {
		return nil
	}
	if original.ServerUser != "" {
		return fmt.Errorf("跨实例移动时不能同时清空已有服务端用户；请先移动，再单独修改服务端用户")
	}
	if source.User == "" && target.User != "" {
		return fmt.Errorf("源 Visitor 的服务名没有用户前缀，目标客户端用户非空时无法保留该目标；请将目标客户端用户留空或明确选择其他服务")
	}
	// frp uses the current client's user when serverUser is omitted.
	next.ServerUser = source.User
	return nil
}

// Older editors can only represent these protocol types. Keep newer types
// stable until the editor can intentionally represent them.
func legacyEditorProtocol(kind string) bool {
	switch kind {
	case "tcp", "udp", "http", "https", "stcp", "stcp-visitor":
		return true
	default:
		return false
	}
}

func supportsUnboundVisitor(kind string) bool {
	return kind == "stcp-visitor" || kind == "xtcp-visitor"
}

func visitorNetwork(kind string) string {
	if kind == "sudp-visitor" {
		return "udp"
	}
	return "tcp"
}

func usesPairingSecret(kind string) bool {
	switch kind {
	case "stcp", "xtcp", "sudp", "stcp-visitor", "xtcp-visitor", "sudp-visitor":
		return true
	}
	return false
}

// These values are emitted into frp's template-expanded configuration.
func protocolTextOK(s string) bool {
	if len(s) > 1024 {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || r == '{' || r == '}' {
			return false
		}
	}
	return true
}

func vhostDomainOK(domain string) bool {
	if domain == "*" {
		return true
	}
	domain = strings.TrimPrefix(domain, "*.")
	return hostOK(domain) && net.ParseIP(domain) == nil
}

// dnsServerAddress normalizes an IP literal and optional port for frpc.
func dnsServerAddress(value string) (string, bool) {
	if value == "" {
		return "", true
	}
	host, port := value, "53"
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		host = value[1 : len(value)-1]
	} else if _, err := netip.ParseAddr(value); err != nil {
		var splitErr error
		host, port, splitErr = net.SplitHostPort(value)
		if splitErr != nil {
			return "", false
		}
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return "", false
	}
	n, err := strconv.Atoi(port)
	if err != nil || !portOK(n) {
		return "", false
	}
	return net.JoinHostPort(ip.String(), strconv.Itoa(n)), true
}

func validateProtocolInstance(i Instance) error {
	if i.Kind == "frpc" {
		switch i.ClientTransport {
		case "", "tcp", "websocket", "wss", "kcp", "quic":
		default:
			return fmt.Errorf("客户端连接协议无效")
		}
		if _, ok := dnsServerAddress(i.DNSServer); !ok {
			return fmt.Errorf("DNS 服务器须为 IP 地址，可选端口")
		}
		if i.ServerAllowPlaintext {
			return fmt.Errorf("客户端不能设置服务端 TLS 选项")
		}
		if i.ClientTLSDisabled && (i.ClientTransport == "wss" || i.ClientTransport == "quic") {
			return fmt.Errorf("WSS 和 QUIC 连接必须使用 TLS")
		}
		if i.User != "" && !namePattern.MatchString(i.User) {
			return fmt.Errorf("客户端用户名称无效")
		}
		if i.NatHoleSTUNServer != "" {
			host, port, err := net.SplitHostPort(i.NatHoleSTUNServer)
			n, parseErr := strconv.Atoi(port)
			if err != nil || parseErr != nil || !hostOK(host) || !portOK(n) {
				return fmt.Errorf("STUN 地址须为有效的主机名或 IP 加端口")
			}
		}
		if i.TCPMuxPassthrough || i.SubDomainHost != "" {
			return fmt.Errorf("客户端不能设置服务端 TCPMUX 或子域名选项")
		}
	} else {
		if i.User != "" || i.NatHoleSTUNServer != "" || i.DNSServer != "" || i.ClientTransport != "" || i.LoginFailExit || i.ClientTLSDisabled {
			return fmt.Errorf("服务端不能设置客户端连接、用户、STUN 或 DNS 选项")
		}
		if i.TCPMuxPassthrough && i.TCPMuxHTTPConnectPort == 0 {
			return fmt.Errorf("TCPMUX 透传需要设置 HTTP CONNECT 监听端口")
		}
		if i.SubDomainHost != "" && (!hostOK(i.SubDomainHost) || net.ParseIP(i.SubDomainHost) != nil) {
			return fmt.Errorf("服务端子域名后缀无效")
		}
	}
	return nil
}

func validateRuleHealthCheck(t Tunnel) error {
	if t.HealthCheckType != "" && t.HealthCheckType != "tcp" && t.HealthCheckType != "http" {
		return fmt.Errorf("健康检查类型仅支持 TCP 或 HTTP")
	}
	if isVisitor(t.Type) && t.HealthCheckType != "" {
		return fmt.Errorf("Visitor 不支持代理健康检查")
	}
	if t.HealthCheckTimeout < 0 || t.HealthCheckTimeout > 86400 ||
		t.HealthCheckInterval < 0 || t.HealthCheckInterval > 604800 ||
		t.HealthCheckMaxFailed < 0 || t.HealthCheckMaxFailed > 1000 {
		return fmt.Errorf("健康检查间隔、超时或失败次数超出范围")
	}
	if t.HealthCheckType == "http" && (t.HealthCheckPath == "" || !probePathOK(t.HealthCheckPath)) {
		return fmt.Errorf("HTTP 健康检查需要以 / 开头的有效路径")
	}
	return nil
}

func validateProtocolTunnel(t Tunnel, endpoint string, claim func(string, string) error) error {
	if usesPairingSecret(t.Type) && t.Secret == "" {
		return fmt.Errorf("%s 配对密钥不能为空", strings.ToUpper(t.Type))
	}
	if isVisitor(t.Type) {
		if !namePattern.MatchString(t.ServerName) || (t.ServerUser != "" && !namePattern.MatchString(t.ServerUser)) {
			return fmt.Errorf("Visitor 需要有效的服务端规则名和用户名称")
		}
		if t.BandwidthKB != 0 {
			return fmt.Errorf("Visitor 不支持此带宽字段")
		}
	} else if t.Type != "tcpmux" {
		if len(t.AllowUsers) > 64 {
			return fmt.Errorf("允许访问的用户最多 64 个")
		}
		for _, user := range t.AllowUsers {
			// An empty entry is an explicit frp user with no prefix; an empty
			// list instead means only the proxy owner's user is allowed.
			if user != "" && user != "*" && !namePattern.MatchString(user) {
				return fmt.Errorf("允许访问的用户名称无效")
			}
		}
	}
	if t.Type == "xtcp-visitor" {
		if t.Protocol != "" && t.Protocol != "quic" && t.Protocol != "kcp" {
			return fmt.Errorf("XTCP Visitor 协议仅支持 quic 或 kcp")
		}
		if t.MaxRetriesAnHour < 0 || t.MaxRetriesAnHour > 1000000 || t.MinRetryInterval < 0 || t.MinRetryInterval > 2147483 || t.FallbackTimeoutMs < 0 || t.FallbackTimeoutMs > 2000000000 {
			return fmt.Errorf("XTCP 重试或回退参数超出范围")
		}
		if t.FallbackTo != "" && !namePattern.MatchString(t.FallbackTo) {
			return fmt.Errorf("XTCP 回退 Visitor 名称无效")
		}
	}
	if t.Type != "tcpmux" {
		return nil
	}
	if t.Multiplexer != "" && t.Multiplexer != "httpconnect" {
		return fmt.Errorf("TCPMUX 仅支持 httpconnect")
	}
	if len(t.Domains) > 16 || (len(t.Domains) == 0 && t.Subdomain == "") {
		return fmt.Errorf("TCPMUX 需要自定义域名或子域名，最多 16 个自定义域名")
	}
	if !validSubdomain(t.Subdomain) {
		return fmt.Errorf("TCPMUX 子域名无效")
	}
	if !protocolTextOK(t.HTTPUser) || !protocolTextOK(t.RouteByHTTPUser) {
		return fmt.Errorf("TCPMUX 用户名无效")
	}
	if t.HTTPPassword != "" && t.HTTPUser == "" {
		return fmt.Errorf("TCPMUX 密码需要设置 HTTP 用户名")
	}
	for _, domain := range t.Domains {
		if !vhostDomainOK(domain) {
			return fmt.Errorf("TCPMUX 域名无效")
		}
		if t.Enabled {
			if err := claim(endpoint+"tcpmux/domain/"+strings.ToLower(domain)+"/"+t.RouteByHTTPUser, t.Name); err != nil {
				return err
			}
		}
	}
	if t.Enabled && t.Subdomain != "" {
		return claim(endpoint+"tcpmux/subdomain/"+strings.ToLower(t.Subdomain)+"/"+t.RouteByHTTPUser, t.Name)
	}
	return nil
}

// Only enabled rules are wired together. Disabled drafts may be assembled in
// any order, but an enabled chain must terminate at a TCP-capable visitor.
func validateVisitorFallbacks(s State) error {
	visitors := make(map[string]Tunnel)
	for _, t := range s.Tunnels {
		if t.Enabled && isVisitor(t.Type) {
			visitors[t.InstanceID+"/"+t.Name] = t
		}
	}
	marks := make(map[string]uint8)
	var visit func(string) error
	visit = func(key string) error {
		if marks[key] == 1 {
			return fmt.Errorf("XTCP Visitor 回退链不能形成循环")
		}
		if marks[key] == 2 {
			return nil
		}
		marks[key] = 1
		t := visitors[key]
		if t.Type == "xtcp-visitor" && t.FallbackTo != "" {
			next := t.InstanceID + "/" + t.FallbackTo
			target, ok := visitors[next]
			if !ok || !supportsUnboundVisitor(target.Type) {
				return fmt.Errorf("XTCP 回退目标必须是同一实例中已启用的 STCP 或 XTCP Visitor")
			}
			if err := visit(next); err != nil {
				return err
			}
		}
		marks[key] = 2
		return nil
	}
	for key := range visitors {
		if err := visit(key); err != nil {
			return err
		}
	}
	return nil
}
