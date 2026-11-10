package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"time"
)

// An explicit query, never part of polling the blocked-record list. The IP is
// sent to RIPEstat and the result is not saved or applied to firewall rules.
func (a *App) firewallIPASNAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.RawQuery != "" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if a.firewall == nil || !firewallSupported() {
		fail(w, 400, fmt.Errorf("当前系统不支持端口访问控制"))
		return
	}
	var req struct {
		IP string `json:"ip"`
	}
	if err := decode(w, r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	ip, err := netip.ParseAddr(req.IP)
	if err != nil || ip.Zone() != "" || !publicDestinationIP(ip) {
		fail(w, 400, fmt.Errorf("只能查询公网 IP 地址"))
		return
	}
	displayedIP := ip.String()
	ip = ip.Unmap()
	if !a.firewall.lookupMu.TryLock() {
		fail(w, 429, fmt.Errorf("已有地址查询正在进行"))
		return
	}
	defer a.firewall.lookupMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	prefix, asns, err := fetchIPASN(ctx, ip)
	if err != nil {
		fail(w, 502, err)
		return
	}
	reply(w, 200, map[string]any{"ip": displayedIP, "prefix": prefix, "asns": asns, "queriedAt": time.Now().UTC().Format(time.RFC3339)})
}

func fetchIPASN(ctx context.Context, ip netip.Addr) (string, []string, error) {
	endpoint := "https://stat.ripe.net/data/network-info/data.json?resource=" + url.QueryEscape(ip.String()) + "&sourceapp=KurumiFRP"
	body, err := fetchRIPEstat(ctx, endpoint, 256<<10)
	if err != nil {
		return "", nil, err
	}
	var result struct {
		Status string `json:"status"`
		Data *struct {
			Prefix string            `json:"prefix"`
			ASNs   []json.RawMessage `json:"asns"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &result) != nil || result.Status != "ok" || result.Data == nil || len(result.Data.ASNs) > 64 {
		return "", nil, fmt.Errorf("RIPEstat 返回无效结果")
	}
	prefix := ""
	if result.Data.Prefix != "" {
		parsed, err := netip.ParsePrefix(result.Data.Prefix)
		if err != nil || parsed.Addr().Is4In6() || !parsed.Contains(ip) {
			return "", nil, fmt.Errorf("RIPEstat 返回的网段与地址不匹配")
		}
		prefix = parsed.Masked().String()
	}
	seen := make(map[string]bool)
	for _, raw := range result.Data.ASNs {
		var value string
		if json.Unmarshal(raw, &value) != nil {
			var number uint32
			if json.Unmarshal(raw, &number) != nil {
				return "", nil, fmt.Errorf("RIPEstat 返回无效 AS 号")
			}
			value = strconv.FormatUint(uint64(number), 10)
		}
		asn, err := normalizeASN(value)
		if err != nil {
			return "", nil, fmt.Errorf("RIPEstat 返回无效 AS 号")
		}
		seen[asn] = true
	}
	asns := make([]string, 0, len(seen))
	for asn := range seen {
		asns = append(asns, asn)
	}
	sort.Strings(asns)
	return prefix, asns, nil
}
