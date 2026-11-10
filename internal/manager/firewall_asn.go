package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"
)

// normalizeASN accepts only a numeric ASN, so it cannot alter the fixed URL.
func normalizeASN(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && strings.EqualFold(value[:2], "AS") {
		value = value[2:]
	}
	if len(value) == 0 || len(value) > 10 {
		return "", fmt.Errorf("请输入有效 AS 号")
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return "", fmt.Errorf("请输入有效 AS 号")
		}
	}
	n, err := strconv.ParseUint(value, 10, 32)
	if err != nil || n == 0 {
		return "", fmt.Errorf("请输入有效 AS 号")
	}
	return "AS" + strconv.FormatUint(n, 10), nil
}

// The response is an editable snapshot; fetching it does not change rules.
func (a *App) firewallASNAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.RawQuery != "" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if a.firewall == nil || !firewallSupported() {
		fail(w, 400, fmt.Errorf("当前系统不支持端口访问控制"))
		return
	}
	var req struct {
		ASN string `json:"asn"`
	}
	if err := decode(w, r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	asn, err := normalizeASN(req.ASN)
	if err != nil {
		fail(w, 400, err)
		return
	}
	if !a.firewall.lookupMu.TryLock() {
		fail(w, 429, fmt.Errorf("已有 AS 网段查询正在进行"))
		return
	}
	defer a.firewall.lookupMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	prefixes, err := fetchASNPrefixes(ctx, asn)
	if err != nil {
		fail(w, 502, err)
		return
	}
	reply(w, 200, map[string]any{"asn": asn, "prefixes": prefixes, "queriedAt": time.Now().UTC().Format(time.RFC3339)})
}

func fetchASNPrefixes(ctx context.Context, asn string) ([]string, error) {
	endpoint := "https://stat.ripe.net/data/announced-prefixes/data.json?resource=" + asn + "&sourceapp=KurumiFRP"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("无法准备 AS 网段查询")
	}
	req.Header.Set("Accept", "application/json")
	transport := &http.Transport{
		Proxy:                  nil,
		DialContext:            (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		TLSHandshakeTimeout:    5 * time.Second,
		ResponseHeaderTimeout:  10 * time.Second,
		MaxResponseHeaderBytes: 16 << 10,
		DisableKeepAlives:      true,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("无法连接 RIPEstat；来源列表未更改")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("RIPEstat 暂不可用；来源列表未更改")
	}
	const maxResponse = 8 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil || len(body) > maxResponse {
		return nil, fmt.Errorf("RIPEstat 响应无法读取或超过大小限制")
	}
	var result struct {
		Status string `json:"status"`
		Data   struct {
			Resource string `json:"resource"`
			Prefixes []struct {
				Prefix string `json:"prefix"`
			} `json:"prefixes"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &result) != nil || result.Status != "ok" {
		return nil, fmt.Errorf("RIPEstat 返回无效结果；来源列表未更改")
	}
	resource, err := normalizeASN(result.Data.Resource)
	if err != nil || resource != asn || len(result.Data.Prefixes) == 0 {
		return nil, fmt.Errorf("未获得匹配 AS 号的网段；来源列表未更改")
	}
	seen := make(map[netip.Prefix]bool)
	for _, item := range result.Data.Prefixes {
		prefix, err := netip.ParsePrefix(item.Prefix)
		if err != nil || prefix.Addr().Is4In6() {
			return nil, fmt.Errorf("RIPEstat 返回不支持的网段；来源列表未更改")
		}
		seen[prefix.Masked()] = true
		if len(seen) > firewallMaxSources {
			return nil, fmt.Errorf("此 AS 的网段超过 %d 项上限；未导入部分结果", firewallMaxSources)
		}
	}
	prefixes := make([]string, 0, len(seen))
	for prefix := range seen {
		prefixes = append(prefixes, prefix.String())
	}
	sort.Strings(prefixes)
	return prefixes, nil
}
