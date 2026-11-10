package manager

import (
	"fmt"
	"net"
	"net/netip"
	"strings"
)

// controlAccessPolicy applies to the peer of a server control connection.
// It is immutable after construction and may be shared by connection workers.
type controlAccessPolicy struct {
	allowOnly bool
	prefixes  []netip.Prefix
}

// A custom runtime must explicitly opt in before it receives a restricted
// configuration; otherwise a successful start could silently bypass the rule.
func (a *App) checkControlAccessBackend(i Instance) error {
	if i.ServerAccessMode == "" {
		return nil
	}
	backend, ok := a.runtime.(ControlAccessCapable)
	if !ok || !backend.SupportsControlAccess() {
		return fmt.Errorf("当前运行后端不支持控制入口来源限制")
	}
	return nil
}

func parseControlAccess(mode, entries string) (*controlAccessPolicy, error) {
	if mode == "" {
		if strings.TrimSpace(entries) != "" {
			return nil, fmt.Errorf("关闭来源限制时请清空地址列表")
		}
		return nil, nil
	}
	if mode != "allow" && mode != "block" {
		return nil, fmt.Errorf("服务端控制入口的来源限制模式无效")
	}
	if len(entries) > 8192 {
		return nil, fmt.Errorf("来源地址列表过长")
	}
	parts := strings.FieldsFunc(entries, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' })
	if len(parts) == 0 || len(parts) > 128 {
		return nil, fmt.Errorf("来源地址列表须包含 1–128 项")
	}
	policy := &controlAccessPolicy{allowOnly: mode == "allow"}
	seen := make(map[netip.Prefix]bool, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("来源地址不能为空")
		}
		prefix, err := netip.ParsePrefix(part)
		if err != nil {
			ip, addrErr := netip.ParseAddr(part)
			if addrErr != nil || ip.Zone() != "" {
				return nil, fmt.Errorf("来源地址须为 IP 地址或 CIDR 网段")
			}
			prefix = netip.PrefixFrom(ip, ip.BitLen())
		}
		if prefix.Addr().Is4In6() {
			if prefix.Bits() < 96 {
				return nil, fmt.Errorf("IPv4 映射地址的网段前缀不能小于 96")
			}
			prefix = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96)
		}
		prefix = prefix.Masked()
		if !seen[prefix] {
			policy.prefixes = append(policy.prefixes, prefix)
			seen[prefix] = true
		}
	}
	return policy, nil
}

func (p *controlAccessPolicy) permits(addr net.Addr) bool {
	if p == nil {
		return true
	}
	if addr == nil {
		return false
	}
	switch value := addr.(type) {
	case *net.TCPAddr:
		if value == nil {
			return false
		}
	case *net.UDPAddr:
		if value == nil {
			return false
		}
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return false
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	ip = ip.WithZone("").Unmap()
	matched := false
	for _, prefix := range p.prefixes {
		if prefix.Contains(ip) {
			matched = true
			break
		}
	}
	return matched == p.allowOnly
}

func (p *controlAccessPolicy) summary(mode string) string {
	parts := make([]string, 0, len(p.prefixes))
	for _, prefix := range p.prefixes {
		parts = append(parts, prefix.String())
	}
	return mode + " " + strings.Join(parts, ", ")
}
