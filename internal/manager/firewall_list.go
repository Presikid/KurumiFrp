package manager

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
	"unicode/utf8"
)

// Download is an explicit, one-time operation. It never saves a URL or
// changes a firewall rule; the caller receives a snapshot for the editor.
func (a *App) firewallListAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.RawQuery != "" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if a.firewall == nil || !firewallSupported() {
		fail(w, 400, fmt.Errorf("当前系统不支持端口访问控制"))
		return
	}
	var req struct {
		URL string `json:"url"`
	}
	if err := decode(w, r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	if !validPublicHTTPSURL(req.URL) {
		fail(w, 400, fmt.Errorf("请输入有效的公网 HTTPS 清单地址"))
		return
	}
	if !a.firewall.lookupMu.TryLock() {
		fail(w, 429, fmt.Errorf("已有地址查询正在进行"))
		return
	}
	defer a.firewall.lookupMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	prefixes, err := fetchFirewallList(ctx, req.URL)
	if err != nil {
		fail(w, 502, err)
		return
	}
	reply(w, 200, map[string]any{"prefixes": prefixes, "fetchedAt": time.Now().UTC().Format(time.RFC3339)})
}

func fetchFirewallList(ctx context.Context, target string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("无法准备清单请求")
	}
	req.Header.Set("Accept", "text/plain")
	transport := &http.Transport{
		Proxy:                  nil,
		DialContext:            dialPublicHTTPS,
		TLSHandshakeTimeout:    5 * time.Second,
		ResponseHeaderTimeout:  10 * time.Second,
		MaxResponseHeaderBytes: 16 << 10,
		DisableKeepAlives:      true,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("无法连接清单地址；来源列表未更改")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("清单服务未返回成功响应；来源列表未更改")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, firewallMaxSourceBytes+1))
	if err != nil || len(body) > firewallMaxSourceBytes || !utf8.Valid(body) {
		return nil, fmt.Errorf("清单无法读取、不是 UTF-8 或超过大小上限")
	}
	prefixes, err := parseFirewallList(string(body))
	if err != nil {
		return nil, fmt.Errorf("清单无效：%w", err)
	}
	return prefixes, nil
}
