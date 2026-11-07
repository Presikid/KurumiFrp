package manager

import (
	"fmt"
	"net"
	"regexp"
	"strings"
	"unicode"
)

type Instance struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	HTTPPort  int    `json:"httpPort"`
	HTTPSPort int    `json:"httpsPort"`
	Token     string `json:"token,omitempty"`
	HasToken  bool   `json:"hasToken,omitempty"`
}
type Tunnel struct {
	ID          string   `json:"id"`
	InstanceID  string   `json:"instanceId"`
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	LocalHost   string   `json:"localHost"`
	LocalPort   int      `json:"localPort"`
	RemotePort  int      `json:"remotePort"`
	Domains     []string `json:"domains"`
	ServerName  string   `json:"serverName"`
	Secret      string   `json:"secret,omitempty"`
	HasSecret   bool     `json:"hasSecret,omitempty"`
	BandwidthKB int      `json:"bandwidthKB"`
	Enabled     bool     `json:"enabled"`
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
	}
	return n
}

var idPattern = regexp.MustCompile(`^[a-f0-9]{16}$`)
var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
var domainPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?$`)

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

// Unknown hostnames may resolve to different addresses over time, so treat
// them conservatively. localhost may bind either IPv4 or IPv6 loopback.
func listenerHostsOverlap(a, b string) bool {
	isLocalhost := func(s string) bool {
		return strings.EqualFold(strings.TrimSuffix(s, "."), "localhost")
	}
	ai, bi := net.ParseIP(a), net.ParseIP(b)
	if isLocalhost(a) && bi != nil {
		return bi.IsLoopback() || bi.IsUnspecified()
	}
	if isLocalhost(b) && ai != nil {
		return ai.IsLoopback() || ai.IsUnspecified()
	}
	if ai == nil || bi == nil {
		return true
	}
	return ai.IsUnspecified() || bi.IsUnspecified() || ai.Equal(bi)
}

// Collect conflicts without resolving DNS, so validation never depends on the network.
func visitorListenerConflicts(s State) map[string]string {
	type listener struct {
		host string
		port int
		name string
		id   string
	}
	var listeners []listener
	conflicts := map[string]string{}
	for _, i := range s.Instances {
		if i.Kind == "frps" {
			// frps vhost listeners use the default wildcard address.
			listeners = append(listeners, listener{i.Host, i.Port, i.Name, i.ID})
			for _, p := range []int{i.HTTPPort, i.HTTPSPort} {
				if p != 0 {
					listeners = append(listeners, listener{"0.0.0.0", p, i.Name, i.ID})
				}
			}
		}
	}
	for _, t := range s.Tunnels {
		if !t.Enabled || t.Type != "stcp-visitor" {
			continue
		}
		for _, l := range listeners {
			if l.port == t.LocalPort && listenerHostsOverlap(l.host, t.LocalHost) {
				a, b := l.id, t.ID
				if a > b {
					a, b = b, a
				}
				key := fmt.Sprintf("%s/%s/%d", a, b, l.port)
				conflicts[key] = fmt.Sprintf("本地 TCP 监听地址或端口冲突：%s / %s", l.name, t.Name)
			}
		}
		listeners = append(listeners, listener{t.LocalHost, t.LocalPort, t.Name, t.ID})
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
	if len(s.Instances) > 64 || len(s.Tunnels) > 512 {
		return fmt.Errorf("最多支持 64 个实例、512 条规则")
	}
	ids := map[string]bool{}
	labels := map[string]bool{}
	ports := map[string]string{}
	names := map[string]bool{}
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
		if i.Kind == "frpc" && (i.HTTPPort != 0 || i.HTTPSPort != 0) {
			return fmt.Errorf("客户端不能设置服务端虚拟主机端口")
		}
		if i.Kind == "frps" {
			seen := map[int]bool{}
			for _, p := range []int{i.Port, i.HTTPPort, i.HTTPSPort} {
				if p == 0 {
					continue
				}
				if !portOK(p) || seen[p] {
					return fmt.Errorf("服务端监听端口无效或重复")
				}
				seen[p] = true
				// Conservative: local frps listeners may not share a TCP port, including wildcard binds.
				if err := claim(fmt.Sprintf("listen/tcp/%d", p), i.Name); err != nil {
					return err
				}
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
		if !hostOK(t.LocalHost) || !portOK(t.LocalPort) {
			return fmt.Errorf("本地地址或端口无效")
		}
		if t.BandwidthKB < 0 || t.BandwidthKB > 10000000 {
			return fmt.Errorf("带宽限制须为 0-10000000 KB/s")
		}
		endpoint := strings.ToLower(i.Host) + fmt.Sprintf(":%d/", i.Port)
		if ip := net.ParseIP(i.Host); ip != nil {
			endpoint = ip.String() + fmt.Sprintf(":%d/", i.Port)
		}
		switch t.Type {
		case "tcp", "udp":
			if !portOK(t.RemotePort) {
				return fmt.Errorf("TCP/UDP 必须设置远端端口")
			}
			if t.Enabled {
				if err := claim(endpoint+t.Type+fmt.Sprint(t.RemotePort), t.Name); err != nil {
					return err
				}
			}
		case "http", "https":
			if len(t.Domains) == 0 || len(t.Domains) > 16 {
				return fmt.Errorf("HTTP/HTTPS 需要 1-16 个域名")
			}
			for _, d := range t.Domains {
				if !hostOK(d) || net.ParseIP(d) != nil {
					return fmt.Errorf("虚拟主机域名无效")
				}
				if t.Enabled {
					if err := claim(endpoint+t.Type+"/"+strings.ToLower(d), t.Name); err != nil {
						return err
					}
				}
			}
		case "stcp", "stcp-visitor":
			if t.Secret == "" {
				return fmt.Errorf("STCP 配对密钥不能为空")
			}
			if t.Type == "stcp-visitor" {
				if !namePattern.MatchString(t.ServerName) {
					return fmt.Errorf("Visitor 需要有效的服务端规则名")
				}
				if t.BandwidthKB != 0 {
					return fmt.Errorf("Visitor 不支持此带宽字段")
				}
			}
		default:
			return fmt.Errorf("不支持的协议")
		}
		if t.Enabled && t.Type != "stcp-visitor" {
			if err := claim(endpoint+"proxy/"+t.Name, t.Name); err != nil {
				return err
			}
		}
	}
	return nil
}
