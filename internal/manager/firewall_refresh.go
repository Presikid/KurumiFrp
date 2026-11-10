package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// A refresh replaces this rule's entire saved source snapshot. It is opt-in;
// the initial snapshot must already be reviewed and manually applied.
type firewallRefresh struct {
	Kind            string `json:"kind"`
	Value           string `json:"value"`
	IntervalMinutes int    `json:"intervalMinutes"`
}

func validateFirewallRefresh(refresh *firewallRefresh) error {
	if refresh == nil {
		return nil
	}
	if refresh.IntervalMinutes < 60 || refresh.IntervalMinutes > 10080 {
		return fmt.Errorf("自动刷新间隔须为 60–10080 分钟")
	}
	switch refresh.Kind {
	case "asn":
		_, err := normalizeASNs(refresh.Value)
		return err
	case "https":
		if !validPublicHTTPSURL(refresh.Value) {
			return fmt.Errorf("自动刷新须使用有效的公网 HTTPS 清单地址")
		}
		return nil
	default:
		return fmt.Errorf("自动刷新来源类型无效")
	}
}

// resetRefreshLocked starts intervals only after a successful manual apply.
// A process restart leaves kernel state unknown and requires a manual apply.
func (f *firewallManager) resetRefreshLocked(now time.Time) {
	f.nextRefresh = make(map[int]time.Time)
	f.refreshStatus = ""
	if !f.config.Enabled {
		return
	}
	for index, rule := range firewallRules(f.config) {
		if rule.Refresh != nil && (rule.Enabled == nil || *rule.Enabled) {
			f.nextRefresh[index] = now.Add(time.Duration(rule.Refresh.IntervalMinutes) * time.Minute)
		}
	}
	if len(f.nextRefresh) != 0 {
		f.refreshStatus = "自动刷新已启用；下次刷新按各规则间隔执行"
	}
}

func (a *App) firewallRefreshLoop(ctx context.Context) {
	defer close(a.firewallDone)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.quit:
			return
		case <-ticker.C:
			a.refreshOneFirewallRule(ctx)
		}
	}
}

// Network work runs outside the configuration lock. A revision check discards
// stale results; only complete, validated snapshots reach the nft transaction.
func (a *App) refreshOneFirewallRule(parent context.Context) {
	f := a.firewall
	if f == nil || !firewallSupported() {
		return
	}
	f.mu.Lock()
	if f.loadError || f.last != "applied" || !f.config.Enabled {
		f.mu.Unlock()
		return
	}
	now := time.Now()
	index := -1
	var refresh firewallRefresh
	for i, rule := range firewallRules(f.config) {
		due, scheduled := f.nextRefresh[i]
		if scheduled && !now.Before(due) && rule.Refresh != nil && (rule.Enabled == nil || *rule.Enabled) {
			index, refresh = i, *rule.Refresh
			break
		}
	}
	if index < 0 || !f.lookupMu.TryLock() {
		f.mu.Unlock()
		return
	}
	revision := f.config.Revision
	f.nextRefresh[index] = now.Add(time.Duration(refresh.IntervalMinutes) * time.Minute)
	f.mu.Unlock()
	defer f.lookupMu.Unlock()

	ctx, cancel := context.WithTimeout(parent, 40*time.Second)
	defer cancel()
	var prefixes []string
	var err error
	if refresh.Kind == "asn" {
		var asns []string
		asns, err = normalizeASNs(refresh.Value)
		if err == nil {
			prefixes, err = fetchASNBatch(ctx, asns)
		}
	} else if refresh.Kind == "https" && validPublicHTTPSURL(refresh.Value) {
		prefixes, err = fetchFirewallList(ctx, refresh.Value)
	} else {
		err = fmt.Errorf("刷新来源无效")
	}
	if err == nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	if err == nil && len(prefixes) == 0 {
		err = fmt.Errorf("来源列表为空")
	}
	if err == nil {
		sort.Strings(prefixes)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	select {
	case <-a.quit:
		return
	default:
	}
	if parent.Err() != nil || f.config.Revision != revision || f.last != "applied" || !f.config.Enabled {
		return
	}
	if err != nil {
		f.nextRefresh[index] = time.Now().Add(5 * time.Minute)
		f.refreshStatus = fmt.Sprintf("第 %d 条规则刷新失败，保留原列表，稍后重试", index+1)
		return
	}
	next := f.config
	next.Rules = append([]firewallRule(nil), firewallRules(f.config)...)
	sources := strings.Join(prefixes, "\n")
	if next.Rules[index].Sources == sources {
		f.refreshStatus = fmt.Sprintf("第 %d 条规则刷新完成，列表没有变化", index+1)
		return
	}
	if next.Revision >= maxJSONRevision {
		f.refreshStatus = "配置版本达到上限，自动刷新已停止"
		f.nextRefresh = nil
		return
	}
	next.Revision++
	next.Rules[index].Sources = sources
	rules, err := renderFirewall(next)
	if err != nil {
		f.refreshStatus = fmt.Sprintf("第 %d 条规则的新列表超出限制或无效，保留原列表", index+1)
		return
	}
	data, err := json.Marshal(next)
	if err == nil {
		_, err = writeIfChanged(f.path, data)
	}
	if err != nil {
		f.refreshStatus = "无法保存刷新结果，保留原规则"
		return
	}
	f.config, f.last = next, "unknown"
	if err = syncMigrationDir(filepath.Dir(f.path)); err != nil {
		f.refreshStatus = "刷新配置已保存但持久化未确认，自动刷新已暂停，请核对规则"
		return
	}
	if err = applyFirewall(parent, rules); err != nil {
		f.refreshStatus = "刷新配置已保存但内核结果未确认，自动刷新已暂停，请核对规则"
		return
	}
	f.last = "applied"
	f.refreshStatus = fmt.Sprintf("第 %d 条规则已刷新并应用", index+1)
}
