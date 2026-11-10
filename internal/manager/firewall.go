// The nftables rule layout is adapted from FRP Manager's panel/firewall.go
// (https://github.com/Gogowwww/frp-manager), licensed under Apache-2.0.
// KurumiFRP changes it to use an explicit port set, a per-profile table and
// manual application. The license text is in internal/embedded/LICENSE-frp.
package manager

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Firewall settings are local machine authority and are not portable backups.
// The kernel table is changed only when an administrator explicitly applies.
type firewallConfig struct {
	Version       int    `json:"version"`
	Revision      uint64 `json:"revision"`
	TableID       string `json:"tableId"`
	Enabled       bool   `json:"enabled"`
	RecordBlocked bool   `json:"recordBlocked,omitempty"`
	Mode          string `json:"mode"`
	Ports         string `json:"ports"`
	Sources       string `json:"sources"`
}

type firewallManager struct {
	mu        sync.Mutex
	path      string
	config    firewallConfig
	loadError bool
	last      string // outcome of the last attempt in this process only
}

func loadFirewall(dir string) *firewallManager {
	f := &firewallManager{path: filepath.Join(dir, "firewall.json"), last: "unknown"}
	data, err := privateFileLimit(f.path, 32<<10)
	if os.IsNotExist(err) {
		return f
	}
	if err != nil || decodeStrictJSON(data, &f.config) != nil || validateFirewallConfig(f.config) != nil {
		f.loadError = true
	}
	return f
}

func validateFirewallConfig(c firewallConfig) error {
	if c.Version != 1 || c.Revision == 0 || c.Revision > maxJSONRevision || !idPattern.MatchString(c.TableID) {
		return fmt.Errorf("防火墙配置格式无效")
	}
	if !c.Enabled {
		if c.Mode != "" || c.Ports != "" || c.Sources != "" || c.RecordBlocked {
			return fmt.Errorf("关闭防火墙时配置应为空")
		}
		return nil
	}
	if c.Mode != "allow" && c.Mode != "block" {
		return fmt.Errorf("请选择允许列表或阻止列表")
	}
	if _, err := parseControlAccess(c.Mode, c.Sources); err != nil {
		return err
	}
	ports, err := parseServerAllowedPorts(c.Ports)
	if err != nil {
		return err
	}
	if len(ports) == 0 {
		return fmt.Errorf("请指定需要过滤的 TCP/UDP 端口")
	}
	return nil
}

// Render only validated IP literals, CIDRs, ports and our random table ID.
// The nft input replaces the owned table in one kernel transaction.
func renderFirewall(c firewallConfig) (string, error) {
	if err := validateFirewallConfig(c); err != nil {
		return "", err
	}
	table := "kurumi_" + c.TableID
	var b strings.Builder
	fmt.Fprintf(&b, "table inet %s\ndelete table inet %s\n", table, table)
	if !c.Enabled {
		return b.String(), nil
	}
	policy, _ := parseControlAccess(c.Mode, c.Sources)
	ranges, _ := parseServerAllowedPorts(c.Ports)
	ports := make([]string, 0, len(ranges))
	for _, r := range ranges {
		if r.start == r.end {
			ports = append(ports, fmt.Sprintf("%d", r.start))
		} else {
			ports = append(ports, fmt.Sprintf("%d-%d", r.start, r.end))
		}
	}
	var ipv4, ipv6 []string
	for _, p := range policy.prefixes {
		if p.Addr().Is4() {
			ipv4 = append(ipv4, p.String())
		} else {
			ipv6 = append(ipv6, p.String())
		}
	}
	fmt.Fprintf(&b, "table inet %s {\n", table)
	fmt.Fprintf(&b, "  set ports { type inet_service; flags interval; auto-merge; elements = { %s } }\n", strings.Join(ports, ", "))
	writeSet := func(name, kind string, entries []string) {
		fmt.Fprintf(&b, "  set %s { type %s; flags interval; auto-merge;", name, kind)
		if len(entries) != 0 {
			fmt.Fprintf(&b, " elements = { %s }", strings.Join(entries, ", "))
		}
		b.WriteString(" }\n")
	}
	writeSet("sources4", "ipv4_addr", ipv4)
	writeSet("sources6", "ipv6_addr", ipv6)
	if c.RecordBlocked {
		b.WriteString("  set seen4 { type ipv4_addr . inet_proto . inet_service; flags dynamic, timeout; timeout 1h; size 1024; }\n")
		b.WriteString("  set seen6 { type ipv6_addr . inet_proto . inet_service; flags dynamic, timeout; timeout 1h; size 1024; }\n")
	}
	b.WriteString("  chain filter {\n    type filter hook prerouting priority -150; policy accept;\n")
	b.WriteString("    iifname \"lo\" return\n    ct state != new return\n    fib daddr type != local return\n    meta l4proto != { tcp, udp } return\n")
	op := ""
	if policy.allowOnly {
		op = "!= "
	}
	writeRule := func(family, source, suffix string) {
		match := fmt.Sprintf("meta nfproto %s th dport @ports %s saddr %s@sources%s", family, source, op, suffix)
		if c.RecordBlocked {
			fmt.Fprintf(&b, "    %s update @seen%s { %s saddr . meta l4proto . th dport }\n", match, suffix, source)
		}
		// A full record set must not prevent the following rule from dropping.
		fmt.Fprintf(&b, "    %s counter drop\n", match)
	}
	if policy.allowOnly || len(ipv4) != 0 {
		writeRule("ipv4", "ip", "4")
	}
	if policy.allowOnly || len(ipv6) != 0 {
		writeRule("ipv6", "ip6", "6")
	}
	b.WriteString("  }\n}\n")
	return b.String(), nil
}

func (a *App) firewallAPI(w http.ResponseWriter, r *http.Request) {
	f := a.firewall
	if f == nil {
		fail(w, 503, fmt.Errorf("防火墙管理不可用"))
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.loadError {
		fail(w, 503, fmt.Errorf("防火墙配置无法读取；原文件已保留，内核规则状态未知"))
		return
	}
	if r.Method == http.MethodGet {
		reply(w, 200, map[string]any{"supported": firewallSupported(), "config": f.config, "lastResult": f.last})
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !firewallSupported() {
		fail(w, 400, fmt.Errorf("内核防火墙目前仅支持 Linux nftables"))
		return
	}
	a.mu.Lock()
	closing := a.closing
	a.mu.Unlock()
	if closing {
		fail(w, 503, fmt.Errorf("工作台正在关闭，未提交内核规则"))
		return
	}
	var req struct {
		Revision      uint64 `json:"revision"`
		Enabled       *bool  `json:"enabled"`
		RecordBlocked *bool `json:"recordBlocked"`
		Mode          string `json:"mode"`
		Ports         string `json:"ports"`
		Sources       string `json:"sources"`
	}
	if err := decode(w, r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	if req.Enabled == nil {
		fail(w, 400, fmt.Errorf("请明确指定是否启用防火墙"))
		return
	}
	if req.Revision != f.config.Revision || f.config.Revision >= maxJSONRevision {
		fail(w, 409, fmt.Errorf("防火墙配置已变化或版本达到上限，请刷新"))
		return
	}
	tableID := f.config.TableID
	if tableID == "" {
		tableID = randomHex(8)
	}
	next := firewallConfig{Version: 1, Revision: f.config.Revision + 1, TableID: tableID, Enabled: *req.Enabled}
	if *req.Enabled {
		next.Mode, next.Ports, next.Sources = req.Mode, strings.TrimSpace(req.Ports), strings.TrimSpace(req.Sources)
		if req.RecordBlocked != nil {
			next.RecordBlocked = *req.RecordBlocked
		} else {
			next.RecordBlocked = f.config.RecordBlocked
		}
	}
	text, err := renderFirewall(next)
	if err != nil {
		fail(w, 400, err)
		return
	}
	data, err := json.Marshal(next)
	if err == nil {
		_, err = writeIfChanged(f.path, data)
	}
	if err != nil {
		fail(w, 500, fmt.Errorf("无法保存防火墙配置；未提交内核规则"))
		return
	}
	f.config, f.last = next, "unknown"
	if err = syncMigrationDir(filepath.Dir(f.path)); err != nil {
		fail(w, 500, fmt.Errorf("防火墙配置已写入，但目录持久化失败；未提交内核规则，请刷新核对"))
		return
	}
	if err = applyFirewall(r.Context(), text); err != nil {
		fail(w, 502, fmt.Errorf("配置已保存，内核规则结果未确认：%v", err))
		return
	}
	f.last = "applied"
	reply(w, 200, map[string]any{"config": f.config, "lastResult": f.last})
}

// A read is an observation of the named table, not proof that the desired
// configuration is currently active. The same lock serializes it with applies.
func (a *App) firewallSeenAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.RawQuery != "" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	f := a.firewall
	if f == nil {
		fail(w, 503, fmt.Errorf("防火墙管理不可用"))
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.loadError || !firewallSupported() {
		fail(w, 503, fmt.Errorf("防火墙配置或记录不可读取"))
		return
	}
	if !f.config.Enabled || !f.config.RecordBlocked {
		reply(w, 200, map[string]any{"enabled": false, "records": []firewallSeen{}})
		return
	}
	if err := validateFirewallConfig(f.config); err != nil {
		fail(w, 503, fmt.Errorf("防火墙配置无效"))
		return
	}
	records, partial, err := readFirewallSeen(r.Context(), "kurumi_"+f.config.TableID)
	if err != nil {
		fail(w, 502, fmt.Errorf("无法读取内核记录，实际规则状态未知"))
		return
	}
	reply(w, 200, map[string]any{"enabled": true, "records": records, "partial": partial})
}

type firewallSeen struct {
	Source   string `json:"source"`
	Protocol string `json:"protocol"`
	Port     int    `json:"port"`
}
