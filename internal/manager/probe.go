package manager

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ProbeResult reports local target reachability. It never claims that a remote
// frp proxy has registered or that the public tunnel is reachable.
type ProbeResult struct {
	Status     string `json:"status"`
	Protocol   string `json:"protocol"`
	Address    string `json:"address,omitempty"`
	Host       string `json:"host,omitempty"`
	HTTPStatus int    `json:"httpStatus,omitempty"`
	LatencyMS  int64  `json:"latencyMs"`
	CheckedAt  string `json:"checkedAt"`
	Message    string `json:"message"`
}

func stampProbe(result ProbeResult) ProbeResult {
	result.CheckedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return result
}

func probeTarget(t Tunnel) ProbeResult {
	return probeTargetWithRoots(t, nil)
}

// probeTargetWithRoots keeps production TLS verification enabled. The optional
// root pool exists so tests can trust an ephemeral httptest certificate.
func probeTargetWithRoots(t Tunnel, roots *x509.CertPool) ProbeResult {
	return probeTargetContext(context.Background(), t, roots)
}

func probeTargetContext(parent context.Context, t Tunnel, roots *x509.CertPool) ProbeResult {
	result := ProbeResult{Protocol: t.Type}
	if parent.Err() != nil {
		result.Status = "cancelled"
		result.Message = "检测已取消"
		return stampProbe(result)
	}
	if !hostOK(t.LocalHost) || !portOK(t.LocalPort) {
		result.Status = "invalid"
		result.Message = "本地目标地址或端口无效"
		return stampProbe(result)
	}

	result.Address = net.JoinHostPort(t.LocalHost, strconv.Itoa(t.LocalPort))
	if t.Type == "udp" {
		result.Status = "unsupported"
		result.Message = "UDP 没有通用的连接握手；发送探测数据也无法可靠判断服务是否可用"
		return stampProbe(result)
	}

	switch t.Type {
	case "tcp", "stcp", "stcp-visitor":
		ctx, cancel := context.WithTimeout(parent, 2*time.Second)
		defer cancel()
		started := time.Now()
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", result.Address)
		if err != nil {
			if parent.Err() != nil {
				result.Status = "cancelled"
				result.Message = "检测已取消"
				return stampProbe(result)
			}
			result.Status = "unreachable"
			result.Message = fmt.Sprintf("本地 TCP 目标不可连接：%v", err)
			return stampProbe(result)
		}
		_ = conn.Close()
		result.Status = "reachable"
		result.LatencyMS = time.Since(started).Milliseconds()
		if t.Type == "stcp-visitor" {
			result.Message = "STCP Visitor 本地监听端口可连接；这不代表远端 STCP 服务或公网隧道可达"
		} else {
			result.Message = "本地 TCP 端口可连接；这不代表 FRP 远端隧道已注册或公网可达"
		}
		return stampProbe(result)
	case "http", "https":
		return probeHTTP(parent, t, result, roots)
	default:
		result.Status = "unsupported"
		result.Message = "当前协议不支持本地目标探测"
		return stampProbe(result)
	}
}

func probeHTTP(parent context.Context, t Tunnel, result ProbeResult, roots *x509.CertPool) ProbeResult {
	host := t.LocalHost
	if len(t.Domains) > 0 {
		host = t.Domains[0]
	}
	if !hostOK(host) {
		result.Status = "invalid"
		result.Message = "HTTP 探测域名无效"
		return stampProbe(result)
	}
	result.Host = host

	scheme := "http"
	if t.Type == "https" {
		scheme = "https"
	}
	hostHeader := host
	if ip := net.ParseIP(host); ip != nil && strings.Contains(host, ":") {
		hostHeader = "[" + host + "]"
	}

	path := t.ProbePath
	if path == "" {
		path = "/"
	}
	method := t.ProbeMethod
	if method == "" {
		method = http.MethodHead
	}
	if !probePathOK(path) || !probeMethodOK(method) {
		result.Status = "invalid"
		result.Message = "HTTP 检测路径或方法无效"
		return stampProbe(result)
	}
	parsedPath, err := url.ParseRequestURI(path)
	if err != nil {
		result.Status = "invalid"
		result.Message = "HTTP 检测路径无效"
		return stampProbe(result)
	}
	probeURL := (&url.URL{
		Scheme: scheme, Host: "kurumifrp-probe.invalid",
		Path: parsedPath.Path, RawPath: parsedPath.RawPath, RawQuery: parsedPath.RawQuery,
	}).String()

	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, probeURL, nil)
	if err != nil {
		result.Status = "invalid"
		result.Message = "无法构造 HTTP 探测请求"
		return stampProbe(result)
	}
	// Keep the configured vhost/SNI while pinning the socket dial to the
	// configured local target. No proxy environment variables or redirects.
	request.Host = hostHeader
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, result.Address)
		},
		TLSClientConfig:       &tls.Config{ServerName: host, RootCAs: roots},
		TLSHandshakeTimeout:   1500 * time.Millisecond,
		ResponseHeaderTimeout: 1500 * time.Millisecond,
		DisableKeepAlives:     true,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		Timeout:   2 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	started := time.Now()
	response, err := client.Do(request)
	if err != nil {
		if parent.Err() != nil {
			result.Status = "cancelled"
			result.Message = "检测已取消"
			return stampProbe(result)
		}
		result.Status = "unreachable"
		result.Message = fmt.Sprintf("%s 本地服务检测失败：%v", strings.ToUpper(t.Type), err)
		return stampProbe(result)
	}
	defer response.Body.Close()

	result.LatencyMS = time.Since(started).Milliseconds()
	result.HTTPStatus = response.StatusCode
	switch {
	case response.StatusCode >= 200 && response.StatusCode < 300:
		result.Status = "healthy"
		result.Message = fmt.Sprintf("%s 本地服务已响应 HTTP %d；仅代表本地服务可响应，不代表 FRP 远端隧道已注册", strings.ToUpper(t.Type), response.StatusCode)
	case response.StatusCode >= 300 && response.StatusCode < 400 && response.StatusCode != http.StatusNotModified:
		result.Status = "redirect"
		result.Message = fmt.Sprintf("%s 本地服务返回重定向 HTTP %d；本次检测不会自动跟随跳转", strings.ToUpper(t.Type), response.StatusCode)
	default:
		result.Status = "responding"
		result.Message = fmt.Sprintf("%s 本地服务已响应 HTTP %d；端口可达，但应用返回非成功状态（也可能不支持 HEAD）", strings.ToUpper(t.Type), response.StatusCode)
	}
	return stampProbe(result)
}

// probeAPI snapshots the selected rule under the application mutex, then
// performs network I/O without holding that mutex so normal UI/API requests
// remain responsive while a target is slow or unreachable.
func (a *App) probeAPI(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Revision uint64 `json:"revision"`
		ID       string `json:"id"`
	}
	if err := decode(w, r, &req); err != nil {
		fail(w, 400, err)
		return
	}

	a.mu.Lock()
	if req.Revision != a.store.DB.Revision {
		a.mu.Unlock()
		fail(w, 409, fmt.Errorf("配置已在其他窗口更新，请刷新后重试"))
		return
	}
	tunnel, ok := a.store.DB.State.Tunnel(req.ID)
	if !ok {
		a.mu.Unlock()
		fail(w, 404, fmt.Errorf("规则不存在"))
		return
	}
	if a.activeProbes >= 12 {
		a.mu.Unlock()
		fail(w, http.StatusTooManyRequests, fmt.Errorf("检测任务过多，请稍后重试"))
		return
	}
	a.activeProbes++
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.activeProbes--
		a.mu.Unlock()
	}()

	result := probeTargetContext(r.Context(), tunnel, nil)

	a.mu.Lock()
	unchanged := req.Revision == a.store.DB.Revision
	a.mu.Unlock()
	if !unchanged {
		fail(w, 409, fmt.Errorf("探测期间配置发生变化，请刷新后重新检测"))
		return
	}
	reply(w, 200, result)
}
