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

const firewallMaxSources = 4096
const firewallMaxSourceBytes = 256 << 10
const firewallMaxRules = 16

// Firewall settings are local machine authority and are not portable backups.
// The kernel table is changed only when an administrator explicitly applies.
type firewallConfig struct {
	Version       int            `json:"version"`
	Revision      uint64         `json:"revision"`
	TableID       string         `json:"tableId"`
	Enabled       bool           `json:"enabled"`
	RecordBlocked bool           `json:"recordBlocked,omitempty"`
	Mode          string         `json:"mode"`
	Ports         string         `json:"ports"`
	Sources       string         `json:"sources"`
	Rules         []firewallRule `json:"rules,omitempty"`
}

type firewallRule struct {
	Name    string `json:"name"`
	Enabled *bool  `json:"enabled,omitempty"`
	Mode    string `json:"mode"`
	Ports   string `json:"ports"`
	Sources string `json:"sources"`
}

// Version 1 initially stored one rule directly on the config. Keep reading it
// until an administrator explicitly saves the multi-rule representation.
func firewallRules(c firewallConfig) []firewallRule {
	if len(c.Rules) != 0 {
		return c.Rules
	}
	return []firewallRule{{Mode: c.Mode, Ports: c.Ports, Sources: c.Sources}}
}

type firewallManager struct {
	mu        sync.Mutex
	lookupMu  sync.Mutex
	path      string
	config    firewallConfig
	loadError bool
	last      string // outcome of the last attempt in this process only
}

func loadFirewall(dir string) *firewallManager {
	f := &firewallManager{path: filepath.Join(dir, "firewall.json"), last: "unknown"}
	data, err := privateFileLimit(f.path, 512<<10)
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
	if !c.Enabled && len(c.Rules) == 0 && c.Mode == "" && c.Ports == "" && c.Sources == "" && !c.RecordBlocked {
		return nil
	}
	if !c.Enabled && len(c.Rules) == 0 {
		return fmt.Errorf("关闭防火墙时保留的规则无效")
	}
	if len(c.Rules) > firewallMaxRules || (len(c.Rules) != 0 && (c.Mode != "" || c.Ports != "" || c.Sources != "")) {
		return fmt.Errorf("防火墙规则数量或格式无效")
	}
	sourceCount, sourceBytes, active := 0, 0, 0
	for index, rule := range firewallRules(c) {
		if rule.Enabled == nil || *rule.Enabled {
			active++
		}
		if rule.Name != "" && !nodeNameOK(rule.Name) {
			return fmt.Errorf("第 %d 条规则名称无效", index+1)
		}
		if rule.Mode != "allow" && rule.Mode != "block" {
			return fmt.Errorf("第 %d 条规则请选择允许或阻止", index+1)
		}
		policy, err := parseSourceAccess(rule.Mode, rule.Sources, firewallMaxSourceBytes, firewallMaxSources)
		if err != nil {
			return fmt.Errorf("第 %d 条规则：%w", index+1, err)
		}
		sourceCount += len(policy.prefixes)
		sourceBytes += len(rule.Sources)
		if sourceCount > firewallMaxSources || sourceBytes > firewallMaxSourceBytes {
			return fmt.Errorf("所有规则的来源地址总数或长度超过上限")
		}
		ports, err := parseServerAllowedPorts(rule.Ports)
		if err != nil {
			return fmt.Errorf("第 %d 条规则：%w", index+1, err)
		}
		if len(ports) == 0 {
			return fmt.Errorf("第 %d 条规则请指定 TCP/UDP 端口", index+1)
		}
	}
	if c.Enabled && active == 0 {
		return fmt.Errorf("请至少启用一条规则，或关闭端口来源过滤")
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
	fmt.Fprintf(&b, "table inet %s {\n", table)
	writeSet := func(name, kind string, entries []string) {
		fmt.Fprintf(&b, "  set %s { type %s; flags interval; auto-merge;", name, kind)
		if len(entries) != 0 {
			fmt.Fprintf(&b, " elements = { %s }", strings.Join(entries, ", "))
		}
		b.WriteString(" }\n")
	}
	type compiledRule struct {
		allowOnly bool
		ipv4      []string
		ipv6      []string
	}
	rules := firewallRules(c)
	compiled := make([]compiledRule, 0, len(rules))
	for _, rule := range rules {
		if rule.Enabled != nil && !*rule.Enabled {
			continue
		}
		index := len(compiled)
		policy, _ := parseSourceAccess(rule.Mode, rule.Sources, firewallMaxSourceBytes, firewallMaxSources)
		ranges, _ := parseServerAllowedPorts(rule.Ports)
		ports := make([]string, 0, len(ranges))
		for _, r := range ranges {
			if r.start == r.end {
				ports = append(ports, fmt.Sprintf("%d", r.start))
			} else {
				ports = append(ports, fmt.Sprintf("%d-%d", r.start, r.end))
			}
		}
		next := compiledRule{allowOnly: policy.allowOnly}
		for _, prefix := range policy.prefixes {
			if prefix.Addr().Is4() {
				next.ipv4 = append(next.ipv4, prefix.String())
			} else {
				next.ipv6 = append(next.ipv6, prefix.String())
			}
		}
		fmt.Fprintf(&b, "  set r%d_ports { type inet_service; flags interval; auto-merge; elements = { %s } }\n", index, strings.Join(ports, ", "))
		writeSet(fmt.Sprintf("r%d_sources4", index), "ipv4_addr", next.ipv4)
		writeSet(fmt.Sprintf("r%d_sources6", index), "ipv6_addr", next.ipv6)
		compiled = append(compiled, next)
	}
	if c.RecordBlocked {
		b.WriteString("  set seen4 { type ipv4_addr . inet_proto . inet_service; flags dynamic, timeout; timeout 1h; size 1024; }\n")
		b.WriteString("  set seen6 { type ipv6_addr . inet_proto . inet_service; flags dynamic, timeout; timeout 1h; size 1024; }\n")
	}
	b.WriteString("  chain filter {\n    type filter hook prerouting priority -150; policy accept;\n")
	b.WriteString("    iifname \"lo\" return\n    ct state != new return\n    fib daddr type != local return\n    meta l4proto != { tcp, udp } return\n")
	writeRule := func(index int, family, source, suffix, op string) {
		match := fmt.Sprintf("meta nfproto %s th dport @r%d_ports %s saddr %s@r%d_sources%s", family, index, source, op, index, suffix)
		if c.RecordBlocked {
			fmt.Fprintf(&b, "    %s update @seen%s { %s saddr . meta l4proto . th dport }\n", match, suffix, source)
		}
		// A full record set must not prevent the following rule from dropping.
		fmt.Fprintf(&b, "    %s counter drop\n", match)
	}
	for index, rule := range compiled {
		op := ""
		if rule.allowOnly {
			op = "!= "
		}
		if rule.allowOnly || len(rule.ipv4) != 0 {
			writeRule(index, "ipv4", "ip", "4", op)
		}
		if rule.allowOnly || len(rule.ipv6) != 0 {
			writeRule(index, "ipv6", "ip6", "6", op)
		}
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
		Revision      uint64          `json:"revision"`
		Enabled       *bool           `json:"enabled"`
		RecordBlocked *bool           `json:"recordBlocked"`
		Mode          string          `json:"mode"`
		Ports         string          `json:"ports"`
		Sources       string          `json:"sources"`
		Rules         *[]firewallRule `json:"rules"`
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
	if req.Rules == nil && len(f.config.Rules) != 0 {
		fail(w, 409, fmt.Errorf("请刷新到支持多条规则的管理界面"))
		return
	}
	if req.Rules != nil && (req.Mode != "" || req.Ports != "" || req.Sources != "") {
		fail(w, 400, fmt.Errorf("不能同时提交旧版和多条规则"))
		return
	}
	tableID := f.config.TableID
	if tableID == "" {
		tableID = randomHex(8)
	}
	next := firewallConfig{Version: 1, Revision: f.config.Revision + 1, TableID: tableID, Enabled: *req.Enabled}
	if *req.Enabled {
		if req.Rules != nil {
			next.Rules = append([]firewallRule{}, (*req.Rules)...)
			for index := range next.Rules {
				next.Rules[index].Name = strings.TrimSpace(next.Rules[index].Name)
				next.Rules[index].Ports = strings.TrimSpace(next.Rules[index].Ports)
				next.Rules[index].Sources = strings.TrimSpace(next.Rules[index].Sources)
			}
		} else {
			next.Mode, next.Ports, next.Sources = req.Mode, strings.TrimSpace(req.Ports), strings.TrimSpace(req.Sources)
		}
		if req.RecordBlocked != nil {
			next.RecordBlocked = *req.RecordBlocked
		} else {
			next.RecordBlocked = f.config.RecordBlocked
		}
	} else if f.config.Enabled || len(f.config.Rules) != 0 {
		// Disabling removes the table but keeps the last saved policy for a
		// future re-enable. Unapplied editor changes are not silently stored.
		next.Rules = append([]firewallRule{}, firewallRules(f.config)...)
		next.RecordBlocked = f.config.RecordBlocked
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

// Accept a text snapshot without applying or saving a firewall configuration.
// Comments begin with #; all other entries must be IP addresses or CIDRs.
func (a *App) firewallImportAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.RawQuery != "" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if a.firewall == nil || !firewallSupported() {
		fail(w, 400, fmt.Errorf("当前系统不支持端口访问控制"))
		return
	}
	var req struct {
		Text string `json:"text"`
	}
	if err := decode(w, r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	if len(req.Text) > firewallMaxSourceBytes {
		fail(w, 400, fmt.Errorf("来源文件超过大小上限"))
		return
	}
	lines := strings.Split(strings.TrimPrefix(req.Text, "\ufeff"), "\n")
	for index, line := range lines {
		line, _, _ = strings.Cut(line, "#")
		lines[index] = strings.TrimSpace(line)
	}
	policy, err := parseSourceAccess("block", strings.Join(lines, "\n"), firewallMaxSourceBytes, firewallMaxSources)
	if err != nil {
		fail(w, 400, err)
		return
	}
	prefixes := make([]string, 0, len(policy.prefixes))
	for _, prefix := range policy.prefixes {
		prefixes = append(prefixes, prefix.String())
	}
	reply(w, 200, map[string]any{"prefixes": prefixes})
}
