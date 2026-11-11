package manager

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const maxImportTOMLBytes = 256 << 10

type tomlImportRequest struct {
	Revision    uint64 `json:"revision"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Content     string `json:"content"`
	PreviewHash string `json:"previewHash,omitempty"`
}
type tomlImportIssue struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}
type tomlImportPreview struct {
	Revision    uint64            `json:"revision"`
	CanImport   bool              `json:"canImport"`
	Instance    Instance          `json:"instance"`
	Tunnels     []Tunnel          `json:"tunnels"`
	Issues      []tomlImportIssue `json:"issues"`
	PreviewHash string            `json:"previewHash,omitempty"`
}
type tomlMapper struct{ issues []tomlImportIssue }

func (m *tomlMapper) issue(path, message string) {
	if len(m.issues) < 500 {
		m.issues = append(m.issues, tomlImportIssue{path, message})
	} else if len(m.issues) == 500 {
		m.issues = append(m.issues, tomlImportIssue{"配置", "问题过多，后续字段未列出；此配置不能导入"})
	}
}
func (m *tomlMapper) take(t *tomlTable, path, prefix string) (any, bool) {
	parts := strings.Split(path, ".")
	for n, part := range parts[:len(parts)-1] {
		v, ok := t.values[part]
		if !ok {
			return nil, false
		}
		next, ok := v.(*tomlTable)
		if !ok {
			m.issue(prefix+strings.Join(parts[:n+1], "."), "需要 TOML 表")
			delete(t.values, part)
			return nil, false
		}
		t = next
	}
	key := parts[len(parts)-1]
	v, ok := t.values[key]
	delete(t.values, key)
	return v, ok
}
func (m *tomlMapper) str(t *tomlTable, field, prefix, fallback string) string {
	v, ok := m.take(t, field, prefix)
	if !ok {
		return fallback
	}
	s, ok := v.(string)
	if !ok {
		m.issue(prefix+field, "需要字符串")
		return fallback
	}
	return s
}
func (m *tomlMapper) integer(t *tomlTable, field, prefix string, fallback int) int {
	v, ok := m.take(t, field, prefix)
	if !ok {
		return fallback
	}
	n, ok := v.(int64)
	if !ok || int64(int(n)) != n {
		m.issue(prefix+field, "需要范围内的整数")
		return fallback
	}
	return int(n)
}
func (m *tomlMapper) boolean(t *tomlTable, field, prefix string, fallback bool) bool {
	v, ok := m.take(t, field, prefix)
	if !ok {
		return fallback
	}
	b, ok := v.(bool)
	if !ok {
		m.issue(prefix+field, "需要布尔值")
		return fallback
	}
	return b
}
func (m *tomlMapper) strings(t *tomlTable, field, prefix string) []string {
	v, ok := m.take(t, field, prefix)
	if !ok {
		return nil
	}
	a, ok := v.([]any)
	if !ok {
		m.issue(prefix+field, "需要字符串数组")
		return nil
	}
	out := make([]string, 0, len(a))
	for _, v := range a {
		s, ok := v.(string)
		if !ok {
			m.issue(prefix+field, "需要字符串数组")
			return nil
		}
		out = append(out, s)
	}
	return out
}
func (m *tomlMapper) sameString(t *tomlTable, field, prefix, expected string) {
	s := m.str(t, field, prefix, expected)
	if s != "" && s != expected {
		m.issue(prefix+field, "当前产品不能保留此设置")
	}
}
func (m *tomlMapper) secret(s *Store, value, path string) string {
	if value == "<redacted>" {
		m.issue(path, "脱敏占位符不能作为密钥导入")
		return ""
	}
	enc, err := s.encrypt(value)
	if err != nil {
		m.issue(path, "需要 8–1024 字节、无控制字符的实际密钥")
		return ""
	}
	return enc
}

func (m *tomlMapper) pluginSecret(s *Store, value, path string) string {
	enc, err := s.encryptPluginPassword(value)
	if err != nil {
		m.issue(path, "需要 1–255 字节、无控制字符的实际插件密码")
		return ""
	}
	return enc
}

func (m *tomlMapper) httpHeaders(store *Store, t *tomlTable, field, prefix string) []HTTPHeader {
	raw, ok := m.take(t, field+".set", prefix)
	if !ok {
		return nil
	}
	table, ok := raw.(*tomlTable)
	if !ok {
		m.issue(prefix+field+".set", "需要由 HTTP 头名称和字符串值组成的表")
		return nil
	}
	if len(table.values) > maxHTTPHeadersPerDirection {
		m.issue(prefix+field+".set", "最多支持 32 个 HTTP 头")
		return nil
	}
	names := make([]string, 0, len(table.values))
	for name := range table.values {
		names = append(names, name)
	}
	sort.Strings(names)
	seen := make(map[string]bool, len(names))
	result := make([]HTTPHeader, 0, len(names))
	for _, name := range names {
		path := prefix + field + ".set." + safeImportKey(name)
		if !validHTTPHeaderName(name) || seen[strings.ToLower(name)] {
			m.issue(path, "HTTP 头名称无效或与同方向其他名称重复")
			continue
		}
		seen[strings.ToLower(name)] = true
		value, ok := table.values[name].(string)
		if !ok || value == "<redacted>" || !validHTTPHeaderValue(value) {
			m.issue(path, "需要有效的实际 HTTP 头字符串，不能使用脱敏占位符")
			continue
		}
		encrypted, err := store.encryptHTTPHeaderValue(value)
		if err != nil {
			m.issue(path, "无法保存 HTTP 头")
			continue
		}
		result = append(result, HTTPHeader{Name: name, Value: encrypted})
	}
	return result
}

var safeTOMLKey = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

func safeImportKey(s string) string {
	if safeTOMLKey.MatchString(s) {
		return s
	}
	return "<特殊键>"
}
func (m *tomlMapper) unknown(t *tomlTable, prefix string) {
	keys := make([]string, 0, len(t.values))
	for k := range t.values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		path := prefix + safeImportKey(key)
		v := t.values[key]
		switch x := v.(type) {
		case *tomlTable:
			if len(x.values) > 0 {
				m.unknown(x, path+".")
				continue
			}
		case []any:
			if len(x) > 0 {
				for i, child := range x {
					table, ok := child.(*tomlTable)
					if !ok || len(table.values) == 0 {
						m.issue(fmt.Sprintf("%s[%d]", path, i), "当前产品不能保留此配置")
					} else {
						m.unknown(table, fmt.Sprintf("%s[%d].", path, i))
					}
				}
				continue
			}
		}
		m.issue(path, "当前产品不能保留此字段")
	}
}
func pruneImportTables(t *tomlTable, names ...string) {
	for _, path := range names {
		parts := strings.Split(path, ".")
		nodes := []*tomlTable{t}
		for _, part := range parts {
			next, ok := nodes[len(nodes)-1].values[part].(*tomlTable)
			if !ok {
				break
			}
			nodes = append(nodes, next)
		}
		for i := len(nodes) - 1; i > 0; i-- {
			if len(nodes[i].values) == 0 {
				delete(nodes[i-1].values, parts[i-1])
			}
		}
	}
}
func (m *tomlMapper) ruleTables(root *tomlTable, field string) []*tomlTable {
	v, ok := m.take(root, field, "")
	if !ok {
		return nil
	}
	if a, ok := v.([]any); ok {
		out := make([]*tomlTable, 0, len(a))
		for _, v := range a {
			t, ok := v.(*tomlTable)
			if !ok {
				m.issue(field, "需要表数组")
				return nil
			}
			out = append(out, t)
		}
		return out
	}
	m.issue(field, "需要表数组")
	return nil
}
func bandwidthKB(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, true
	}
	base := float64(1024)
	if strings.HasSuffix(s, "MB") {
		base *= 1024
		s = strings.TrimSuffix(s, "MB")
	} else if strings.HasSuffix(s, "KB") {
		s = strings.TrimSuffix(s, "KB")
	} else {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	bytes := f * base
	if err != nil || math.IsNaN(bytes) || math.IsInf(bytes, 0) || bytes < 0 || bytes > 10000000*1024 || bytes != math.Trunc(bytes) {
		return 0, false
	}
	n := int64(bytes)
	if n%1024 != 0 {
		return 0, false
	}
	return int(n / 1024), true
}

func (m *tomlMapper) mapRule(store *Store, t *tomlTable, prefix, instanceID string, visitor bool, start map[string]bool) Tunnel {
	r := Tunnel{ID: randomHex(8), InstanceID: instanceID}
	r.Name = m.str(t, "name", prefix, "")
	typ := m.str(t, "type", prefix, "")
	if visitor {
		if typ != "stcp" && typ != "xtcp" && typ != "sudp" {
			m.issue(prefix+"type", "仅支持 STCP、XTCP 和 SUDP visitor")
		}
		r.Type = typ + "-visitor"
	} else {
		r.Type = typ
		switch typ {
		case "tcp", "udp", "http", "https", "stcp", "xtcp", "sudp", "tcpmux":
		default:
			m.issue(prefix+"type", "当前产品不支持此代理类型")
		}
	}
	r.Enabled = m.boolean(t, "enabled", prefix, true)
	if len(start) > 0 && !start[r.Name] {
		r.Enabled = false
	}
	if !namePattern.MatchString(r.Name) {
		m.issue(prefix+"name", "规则名称不符合当前产品的格式")
	}
	if visitor {
		r.LocalHost = m.str(t, "bindAddr", prefix, "127.0.0.1")
		if r.LocalHost == "" {
			r.LocalHost = "127.0.0.1"
		}
		r.LocalPort = m.integer(t, "bindPort", prefix, 0)
		r.ServerName = m.str(t, "serverName", prefix, "")
		r.ServerUser = m.str(t, "serverUser", prefix, "")
		if !namePattern.MatchString(r.ServerName) {
			m.issue(prefix+"serverName", "需要有效的服务端规则名")
		}
		if typ == "xtcp" {
			r.Protocol = m.str(t, "protocol", prefix, "")
			r.KeepTunnelOpen = m.boolean(t, "keepTunnelOpen", prefix, false)
			r.MaxRetriesAnHour = m.integer(t, "maxRetriesAnHour", prefix, 0)
			r.MinRetryInterval = m.integer(t, "minRetryInterval", prefix, 0)
			r.FallbackTo = m.str(t, "fallbackTo", prefix, "")
			r.FallbackTimeoutMs = m.integer(t, "fallbackTimeoutMs", prefix, 0)
		}
	} else {
		pluginType := m.str(t, "plugin.type", prefix, "")
		_, hasLocalIP := t.values["localIP"]
		_, hasLocalPort := t.values["localPort"]
		r.LocalHost = m.str(t, "localIP", prefix, "127.0.0.1")
		if r.LocalHost == "" {
			r.LocalHost = "127.0.0.1"
		}
		r.LocalPort = m.integer(t, "localPort", prefix, 0)
		switch pluginType {
		case "":
		case "unix_domain_socket":
			r.UnixSocketPath = m.str(t, "plugin.unixPath", prefix, "")
			if typ != "tcp" || !strings.HasPrefix(r.UnixSocketPath, "/") || !protocolTextOK(r.UnixSocketPath) {
				m.issue(prefix+"plugin", "仅支持 TCP 规则使用绝对路径的 Unix 套接字")
			}
			if r.LocalPort != 0 || r.LocalHost != "127.0.0.1" {
				m.issue(prefix+"localIP / localPort", "Unix 套接字规则不能同时保留本地 TCP 地址和端口")
			}
			r.LocalHost, r.LocalPort = "", 0
		case socks5PluginType:
			r.PluginType = socks5PluginType
			r.PluginUser = m.str(t, "plugin.username", prefix, "")
			password := m.str(t, "plugin.password", prefix, "")
			if typ != "tcp" {
				m.issue(prefix+"plugin.type", "SOCKS5 插件仅支持 TCP 规则")
			}
			if hasLocalIP || hasLocalPort {
				m.issue(prefix+"localIP / localPort", "SOCKS5 插件不能同时指定本地地址或端口")
			}
			if !validPluginCredential(r.PluginUser) {
				m.issue(prefix+"plugin.username", "需要 1–255 字节、无控制字符的用户名")
			}
			r.PluginPassword = m.pluginSecret(store, password, prefix+"plugin.password")
			r.LocalHost, r.LocalPort = "", 0
		default:
			m.issue(prefix+"plugin.type", "当前产品不支持此客户端插件")
		}
		if typ == "tcp" || typ == "udp" {
			r.RemotePort = m.integer(t, "remotePort", prefix, 0)
			if r.RemotePort < 0 || r.RemotePort > 65535 {
				m.issue(prefix+"remotePort", "端口需要在 0–65535")
			}
		}
		if typ == "http" || typ == "https" || typ == "tcpmux" {
			r.Domains = m.strings(t, "customDomains", prefix)
			r.Subdomain = m.str(t, "subdomain", prefix, "")
			if (typ == "http" || typ == "https") && (len(r.Domains) > 16 || (len(r.Domains) == 0 && r.Subdomain == "")) {
				m.issue(prefix+"customDomains / subdomain", "需要自定义域名或子域名，最多 16 个自定义域名")
			}
		}
		if typ == "tcpmux" {
			r.Multiplexer = m.str(t, "multiplexer", prefix, "")
			r.RouteByHTTPUser = m.str(t, "routeByHTTPUser", prefix, "")
		}
		if typ == "http" || typ == "tcpmux" {
			r.HTTPUser = m.str(t, "httpUser", prefix, "")
			password := m.str(t, "httpPassword", prefix, "")
			if password != "" {
				r.HTTPPassword = m.secret(store, password, prefix+"httpPassword")
			}
			if typ == "http" {
				r.HTTPAuthEnabled = r.HTTPUser != "" || password != ""
			}
		}
		if typ == "http" {
			r.RequestHeaders = m.httpHeaders(store, t, "requestHeaders", prefix)
			r.ResponseHeaders = m.httpHeaders(store, t, "responseHeaders", prefix)
			r.HostHeaderRewrite = m.str(t, "hostHeaderRewrite", prefix, "")
			r.Locations = m.strings(t, "locations", prefix)
			r.HTTPRouteByUser = m.str(t, "routeByHTTPUser", prefix, "")
		}
		if typ == "stcp" || typ == "xtcp" || typ == "sudp" {
			r.AllowUsers = m.strings(t, "allowUsers", prefix)
		}
		limit := m.str(t, "transport.bandwidthLimit", prefix, "")
		var ok bool
		r.BandwidthKB, ok = bandwidthKB(limit)
		if !ok {
			m.issue(prefix+"transport.bandwidthLimit", "只能精确保留 0–10000000 KB/s 的整数 KB 带宽")
		}
		r.BandwidthMode = m.str(t, "transport.bandwidthLimitMode", prefix, "")
		r.ProxyProtocolVersion = m.str(t, "transport.proxyProtocolVersion", prefix, "")
		r.HealthCheckType = m.str(t, "healthCheck.type", prefix, "")
		r.HealthCheckPath = m.str(t, "healthCheck.path", prefix, "")
		r.HealthCheckTimeout = m.integer(t, "healthCheck.timeoutSeconds", prefix, 0)
		r.HealthCheckInterval = m.integer(t, "healthCheck.intervalSeconds", prefix, 0)
		r.HealthCheckMaxFailed = m.integer(t, "healthCheck.maxFailed", prefix, 0)
		if r.HealthCheckPath != "" && r.HealthCheckType != "http" {
			m.issue(prefix+"healthCheck.path", "当前产品仅保留 HTTP 健康检查路径")
		}
		if r.HealthCheckType == "" && (r.HealthCheckTimeout != 0 || r.HealthCheckInterval != 0 || r.HealthCheckMaxFailed != 0) {
			m.issue(prefix+"healthCheck", "请先指定健康检查类型，否则无法保留参数")
		}
	}
	r.UseEncryption = m.boolean(t, "transport.useEncryption", prefix, false)
	r.UseCompression = m.boolean(t, "transport.useCompression", prefix, false)
	if typ == "xtcp" {
		r.DisableAssistedAddrs = m.boolean(t, "natTraversal.disableAssistedAddrs", prefix, false)
	}
	if typ == "stcp" || typ == "xtcp" || typ == "sudp" {
		r.Secret = m.secret(store, m.str(t, "secretKey", prefix, ""), prefix+"secretKey")
	}
	if r.UnixSocketPath == "" && r.PluginType == "" && !hostOK(r.LocalHost) {
		m.issue(prefix+"localIP / bindAddr", "地址不符合当前产品的格式")
	}
	if r.UnixSocketPath == "" && r.PluginType == "" && !portOK(r.LocalPort) && !(visitor && supportsUnboundVisitor(r.Type) && r.LocalPort < 0) {
		m.issue(prefix+"localPort / bindPort", "端口无效")
	}
	pruneImportTables(t, "transport", "healthCheck", "natTraversal", "requestHeaders", "responseHeaders", "plugin")
	m.unknown(t, prefix)
	return r
}

func (a *App) prepareTOML(req tomlImportRequest) (tomlImportPreview, State) {
	p := tomlImportPreview{Revision: req.Revision, Issues: []tomlImportIssue{}, Tunnels: []Tunnel{}}
	m := &tomlMapper{}
	if req.Kind != "frpc" && req.Kind != "frps" {
		m.issue("kind", "请选择 frpc 或 frps")
	}
	if !labelOK(req.Name) {
		m.issue("name", "实例名称为空、过长或包含不支持的字符")
	}
	if len(req.Content) > maxImportTOMLBytes {
		m.issue("TOML", "配置超过 256 KiB")
		p.Issues = m.issues
		return p, State{}
	}
	root, err := parseImportTOML(req.Content)
	if err != nil {
		m.issue("TOML", err.Error())
		p.Issues = m.issues
		return p, State{}
	}
	i := Instance{ID: randomHex(8), Name: req.Name, Kind: req.Kind}
	if req.Kind == "frps" {
		i.Host = m.str(root, "bindAddr", "", "0.0.0.0")
		if i.Host == "" {
			i.Host = "0.0.0.0"
		}
		i.Port = m.integer(root, "bindPort", "", 7000)
		if i.Port == 0 {
			i.Port = 7000
		}
		i.HTTPPort = m.integer(root, "vhostHTTPPort", "", 0)
		i.HTTPSPort = m.integer(root, "vhostHTTPSPort", "", 0)
		i.KCPBindPort = m.integer(root, "kcpBindPort", "", 0)
		i.QUICBindPort = m.integer(root, "quicBindPort", "", 0)
		i.TCPMuxHTTPConnectPort = m.integer(root, "tcpmuxHTTPConnectPort", "", 0)
		i.TCPMuxPassthrough = m.boolean(root, "tcpmuxPassthrough", "", false)
		i.SubDomainHost = m.str(root, "subDomainHost", "", "")
		i.MaxPortsPerClient = m.integer(root, "maxPortsPerClient", "", 0)
		var allowedPorts []string
		for n, entry := range m.ruleTables(root, "allowPorts") {
			if n >= 128 {
				m.issue("allowPorts", "最多支持 128 项")
				break
			}
			prefix := fmt.Sprintf("allowPorts[%d].", n)
			single := m.integer(entry, "single", prefix, 0)
			start := m.integer(entry, "start", prefix, 0)
			end := m.integer(entry, "end", prefix, 0)
			switch {
			case portOK(single) && start == 0 && end == 0:
				allowedPorts = append(allowedPorts, strconv.Itoa(single))
			case single == 0 && portOK(start) && portOK(end) && end >= start:
				allowedPorts = append(allowedPorts, fmt.Sprintf("%d-%d", start, end))
			default:
				m.issue(prefix, "需要一个有效端口，或有效的起始和结束端口")
			}
			m.unknown(entry, prefix)
		}
		i.ServerAllowedPorts = strings.Join(allowedPorts, ",")
		i.ProxyBindAddr = m.str(root, "proxyBindAddr", "", "")
		i.VhostHTTPTimeout = m.integer(root, "vhostHTTPTimeout", "", 0)
		i.UserConnTimeout = m.integer(root, "userConnTimeout", "", 0)
		i.ServerMaxPoolCount = m.integer(root, "transport.maxPoolCount", "", 0)
		i.TCPKeepalive = m.integer(root, "transport.tcpKeepalive", "", 0)
		i.ServerAllowPlaintext = !m.boolean(root, "transport.tls.force", "", false)
	} else {
		i.Host = m.str(root, "serverAddr", "", "0.0.0.0")
		if i.Host == "" {
			i.Host = "0.0.0.0"
		}
		i.Port = m.integer(root, "serverPort", "", 7000)
		if i.Port == 0 {
			i.Port = 7000
		}
		i.LoginFailExit = m.boolean(root, "loginFailExit", "", true)
		i.ClientTLSDisabled = !m.boolean(root, "transport.tls.enable", "", true)
		i.User = m.str(root, "user", "", "")
		i.NatHoleSTUNServer = m.str(root, "natHoleStunServer", "", "")
		i.DNSServer = m.str(root, "dnsServer", "", "")
		i.ClientTransport = m.str(root, "transport.protocol", "", "")
		i.ClientBindIP = m.str(root, "transport.connectServerLocalIP", "", "")
		i.ClientDialTimeout = m.integer(root, "transport.dialServerTimeout", "", 0)
		i.TCPKeepalive = m.integer(root, "transport.dialServerKeepalive", "", 0)
		i.ClientPoolCount = m.integer(root, "transport.poolCount", "", 0)
		m.sameString(root, "transport.wireProtocol", "", "v1")
	}
	i.TransportMuxDisabled = !m.boolean(root, "transport.tcpMux", "", true)
	i.MuxKeepalive = m.integer(root, "transport.tcpMuxKeepaliveInterval", "", 0)
	if !hostOK(i.Host) {
		m.issue("serverAddr / bindAddr", "地址不符合当前产品的格式")
	}
	if !portOK(i.Port) {
		m.issue("serverPort / bindPort", "端口需要在 1–65535")
	}
	m.sameString(root, "auth.method", "", "token")
	i.Token = m.secret(a.store, m.str(root, "auth.token", "", ""), "auth.token")
	for _, scope := range m.strings(root, "auth.additionalScopes", "") {
		switch scope {
		case "HeartBeats":
			i.AuthHeartbeats = true
		case "NewWorkConns":
			i.AuthNewWorkConns = true
		default:
			m.issue("auth.additionalScopes", "只支持 HeartBeats 和 NewWorkConns")
		}
	}
	m.sameString(root, "log.to", "", "console")
	m.sameString(root, "log.level", "", "info")
	// The application disables console colors. Absence of this cosmetic
	// option does not prevent importing otherwise compatible configurations.
	if raw, ok := m.take(root, "log.disablePrintColor", ""); ok {
		if value, valid := raw.(bool); !valid || !value {
			m.issue("log.disablePrintColor", "当前工作台输出不使用颜色")
		}
	}
	if days := m.integer(root, "log.maxDays", "", 3); days != 0 && days != 3 {
		m.issue("log.maxDays", "当前产品不能保留此日志设置")
	}
	st := a.store.DB.State.clone()
	st.Instances = append(st.Instances, i)
	if req.Kind == "frpc" {
		startList := m.strings(root, "start", "")
		if len(startList) > 0 {
			m.issue("start", "当前产品不能保留全局规则筛选列表；预览仅展示当前启停结果")
		}
		start := make(map[string]bool, len(startList))
		for _, name := range startList {
			start[name] = true
		}
		matched := map[string]bool{}
		for _, field := range []string{"proxies", "visitors"} {
			for n, table := range m.ruleTables(root, field) {
				if n >= 512 {
					m.issue(field, "最多支持 512 条规则")
					break
				}
				t := m.mapRule(a.store, table, fmt.Sprintf("%s[%d].", field, n), i.ID, field == "visitors", start)
				matched[t.Name] = true
				st.Tunnels = append(st.Tunnels, t)
				p.Tunnels = append(p.Tunnels, t)
			}
		}
		for name := range start {
			if !matched[name] {
				m.issue("start", "包含本文件中无法找到的规则名")
				break
			}
		}
	}
	pruneImportTables(root, "auth", "log", "transport.tls", "transport")
	m.unknown(root, "")
	if len(m.issues) == 0 {
		if err := validate(st); err != nil {
			m.issue("配置", err.Error())
		}
		if err := validateVisitorListeners(a.store.DB.State, st); err != nil {
			m.issue("配置", err.Error())
		}
	}
	p.Instance = (State{Instances: []Instance{i}}).public().Instances[0]
	p.Tunnels = (State{Tunnels: p.Tunnels}).public().Tunnels
	// Preview objects are not committed; IDs are allocated again on import.
	p.Instance.ID = ""
	for j := range p.Tunnels {
		p.Tunnels[j].ID = ""
		p.Tunnels[j].InstanceID = ""
	}
	p.Issues = m.issues
	if p.Issues == nil {
		p.Issues = []tomlImportIssue{}
	}
	p.CanImport = len(p.Issues) == 0
	return p, st
}

// A preview ticket binds the exact input and revision without storing plaintext.
func (a *App) tomlPreviewHash(req tomlImportRequest) string {
	req.PreviewHash = ""
	b, _ := json.Marshal(req)
	mac := hmac.New(sha256.New, a.store.key)
	_, _ = mac.Write(b)
	return hex.EncodeToString(mac.Sum(nil))
}

// Called under the application mutex after authentication and origin checks.
func (a *App) tomlImportAPI(w http.ResponseWriter, r *http.Request) {
	var req tomlImportRequest
	var raw json.RawMessage
	if err := decode(w, r, &raw); err != nil {
		fail(w, http.StatusBadRequest, fmt.Errorf("导入请求无效或超过大小限制"))
		return
	}
	var fields struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &fields) != nil || !validPluginJSONText(fields.Content) || decodeStrictJSON(raw, &req) != nil {
		fail(w, http.StatusBadRequest, fmt.Errorf("导入请求无效或包含无效 Unicode 文本"))
		return
	}
	if a.closing || req.Revision != a.store.DB.Revision {
		fail(w, http.StatusConflict, fmt.Errorf("配置已更新或工作台正在关闭，请刷新后重新预览"))
		return
	}
	if r.URL.Path == "/api/import/toml" {
		given, err := hex.DecodeString(req.PreviewHash)
		expected, _ := hex.DecodeString(a.tomlPreviewHash(req))
		if err != nil || !hmac.Equal(given, expected) {
			fail(w, http.StatusConflict, fmt.Errorf("配置或预览已改变，请重新预览"))
			return
		}
	}
	preview, state := a.prepareTOML(req)
	if r.URL.Path == "/api/import/toml/preview" {
		if preview.CanImport {
			preview.PreviewHash = a.tomlPreviewHash(req)
		}
		reply(w, http.StatusOK, preview)
		return
	}
	if !preview.CanImport {
		reply(w, http.StatusBadRequest, preview)
		return
	}
	id := state.Instances[len(state.Instances)-1].ID
	if err := a.store.commit(state, "导入 frp TOML 草稿"); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	reply(w, http.StatusOK, map[string]any{"ok": true, "revision": a.store.DB.Revision, "id": id, "rulesImported": len(preview.Tunnels)})
}
