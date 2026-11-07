package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type HealthCheck struct {
	Status    string `json:"status"`
	Address   string `json:"address,omitempty"`
	LatencyMS int64  `json:"latencyMs,omitempty"`
	Message   string `json:"message"`
}

type HealthInstance struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
	Host string `json:"host"`
	Port int    `json:"port"`
}

type HealthProxy struct {
	ID                 string      `json:"id"`
	Name               string      `json:"name"`
	Type               string      `json:"type"`
	Enabled            bool        `json:"enabled"`
	Registration       string      `json:"registration"`
	RegistrationStatus string      `json:"registrationStatus,omitempty"`
	RegistrationError  string      `json:"registrationError,omitempty"`
	LocalAddr          string      `json:"localAddr,omitempty"`
	RemoteAddr         string      `json:"remoteAddr,omitempty"`
	Target             ProbeResult `json:"target"`
}

type HealthSummary struct {
	TotalRules          int `json:"totalRules"`
	EnabledRules        int `json:"enabledRules"`
	Registered          int `json:"registered"`
	RegistrationErrors  int `json:"registrationErrors"`
	RegistrationMissing int `json:"registrationMissing"`
	RegistrationUnknown int `json:"registrationUnknown"`
	TargetsReachable    int `json:"targetsReachable"`
	TargetsUnreachable  int `json:"targetsUnreachable"`
	TargetsUnsupported  int `json:"targetsUnsupported"`
	TargetsInvalid      int `json:"targetsInvalid"`
}

type HealthReport struct {
	Revision  uint64         `json:"revision"`
	CheckedAt string         `json:"checkedAt"`
	Instance  HealthInstance `json:"instance"`
	Runtime   RuntimeStatus  `json:"runtime"`
	Endpoint  HealthCheck    `json:"endpoint"`
	AdminAPI  HealthCheck    `json:"adminApi"`
	Proxies   []HealthProxy  `json:"proxies"`
	Summary   HealthSummary  `json:"summary"`
}

type frpcProxyStatus struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Status     string `json:"status"`
	Err        string `json:"err"`
	LocalAddr  string `json:"localAddr"`
	RemoteAddr string `json:"remoteAddr"`
	Plugin     string `json:"plugin"`
}

func (a *App) healthAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		fail(w, http.StatusBadRequest, fmt.Errorf("请指定运行实例"))
		return
	}

	a.mu.Lock()
	instance, ok := a.store.DB.State.Instance(id)
	if !ok {
		a.mu.Unlock()
		fail(w, http.StatusNotFound, fmt.Errorf("实例不存在"))
		return
	}
	revision := a.store.DB.Revision
	tunnels := make([]Tunnel, 0)
	for _, tunnel := range a.store.DB.State.Tunnels {
		if tunnel.InstanceID == id {
			tunnel.Domains = append([]string(nil), tunnel.Domains...)
			tunnels = append(tunnels, tunnel)
		}
	}
	runtimeStatus := a.statuses()[id]
	proc := a.procs[id]
	a.mu.Unlock()

	ctx, cancel := context.WithTimeout(r.Context(), 38*time.Second)
	defer cancel()

	report := HealthReport{
		Revision:  revision,
		CheckedAt: now(),
		Instance: HealthInstance{
			ID: instance.ID, Name: instance.Name, Kind: instance.Kind,
			Host: instance.Host, Port: instance.Port,
		},
		Runtime: runtimeStatus,
		Proxies: make([]HealthProxy, len(tunnels)),
	}
	for index, tunnel := range tunnels {
		report.Proxies[index] = HealthProxy{
			ID: tunnel.ID, Name: tunnel.Name, Type: tunnel.Type,
			Enabled: tunnel.Enabled, Registration: "unknown",
			Target: ProbeResult{Status: "pending", Message: "等待检测"},
		}
		if !tunnel.Enabled {
			report.Proxies[index].Registration = "disabled"
			report.Proxies[index].Target = ProbeResult{Status: "skipped", Message: "规则未启用，跳过目标检测"}
		}
	}

	if instance.Kind == "frpc" {
		if proc != nil && runtimeStatus.State == "running" {
			report.AdminAPI = HealthCheck{Status: "checking", Message: "正在读取 frpc 代理状态"}
		} else {
			report.AdminAPI = HealthCheck{Status: "not-running", Message: "实例未运行，无法读取代理注册状态"}
		}
	} else {
		report.AdminAPI = HealthCheck{Status: "not-applicable", Message: "frps 使用本机监听检测；客户端代理注册状态由 frpc 提供"}
	}

	var (
		wg          sync.WaitGroup
		proxyStatus []frpcProxyStatus
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		report.Endpoint = probeInstanceEndpoint(ctx, instance)
	}()

	if instance.Kind == "frpc" && proc != nil && runtimeStatus.State == "running" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			report.AdminAPI, proxyStatus = fetchFRPCProxyStatus(ctx, proc)
		}()
	}

	runHealthTargetProbes(ctx, tunnels, report.Proxies)
	wg.Wait()

	var secrets []string
	if proc != nil && proc.logs != nil {
		secrets = append(secrets, proc.logs.secrets...)
	}
	report.Runtime.Exit = cleanDiagnosticText(report.Runtime.Exit, secrets)
	report.Endpoint.Message = cleanDiagnosticText(report.Endpoint.Message, secrets)
	report.AdminAPI.Message = cleanDiagnosticText(report.AdminAPI.Message, secrets)
	for index := range report.Proxies {
		report.Proxies[index].Target.Message = cleanDiagnosticText(report.Proxies[index].Target.Message, secrets)
	}

	statusByName := make(map[string]frpcProxyStatus, len(proxyStatus))
	for _, status := range proxyStatus {
		statusByName[status.Name] = status
	}
	for index, tunnel := range tunnels {
		row := &report.Proxies[index]
		if !tunnel.Enabled || tunnel.Type == "stcp-visitor" {
			if tunnel.Type == "stcp-visitor" && tunnel.Enabled {
				row.Registration = "not-reported"
				row.RegistrationError = "Visitor 的注册状态不包含在当前 frpc 代理状态摘要中"
			}
			continue
		}
		if instance.Kind != "frpc" || report.AdminAPI.Status != "reachable" {
			row.Registration = "unknown"
			if report.AdminAPI.Message != "" {
				row.RegistrationError = report.AdminAPI.Message
			}
			continue
		}
		status, found := statusByName[tunnel.Name]
		if !found {
			row.Registration = "missing"
			row.RegistrationError = "frpc Admin API 未报告此代理；可能尚未注册、配置尚未应用或正在重连"
			continue
		}
		row.RegistrationStatus = cleanDiagnosticText(status.Status, secrets)
		row.RegistrationError = cleanDiagnosticText(status.Err, secrets)
		row.LocalAddr = cleanDiagnosticText(status.LocalAddr, secrets)
		row.RemoteAddr = cleanDiagnosticText(status.RemoteAddr, secrets)
		row.Registration = normalizeRegistration(status.Status)
	}
	report.Summary = summarizeHealth(report.Proxies)
	reply(w, http.StatusOK, report)
}

func probeInstanceEndpoint(parent context.Context, instance Instance) HealthCheck {
	host := instance.Host
	message := "上游 frps 控制端口可建立 TCP 连接；不代表认证成功或代理已注册"
	if instance.Kind == "frps" {
		var ok bool
		host, ok = localBindProbeHost(host)
		if !ok {
			return HealthCheck{Status: "unsupported", Message: "frps 绑定地址不是本机 IP 或 localhost，跳过本机监听检测"}
		}
		message = "本机 frps 监听端口可接受 TCP 连接；不代表远端客户端已注册"
	}
	address := net.JoinHostPort(host, strconv.Itoa(instance.Port))
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	started := time.Now()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		if parent.Err() != nil {
			return HealthCheck{Status: "cancelled", Address: address, Message: "检测已取消"}
		}
		return HealthCheck{Status: "unreachable", Address: address, Message: fmt.Sprintf("TCP 端口不可连接：%v", err)}
	}
	_ = conn.Close()
	return HealthCheck{
		Status: "reachable", Address: address,
		LatencyMS: time.Since(started).Milliseconds(), Message: message,
	}
}

func localBindProbeHost(host string) (string, bool) {
	if strings.EqualFold(host, "localhost") {
		return "127.0.0.1", true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return "", false
	}
	if ip.IsUnspecified() {
		if ip.To4() != nil {
			return "127.0.0.1", true
		}
		return "::1", true
	}
	return host, true
}

func fetchFRPCProxyStatus(ctx context.Context, proc *process) (HealthCheck, []frpcProxyStatus) {
	port, user, password := proc.adminCredentials()
	if port < 1 || user == "" || password == "" {
		return HealthCheck{Status: "unavailable", Message: "frpc Admin API 凭据不可用"}, nil
	}
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	check := HealthCheck{Status: "unavailable"}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext,
		DisableKeepAlives: true,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		Timeout:   3 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/api/status", nil)
	if err != nil {
		check.Message = "无法构造 frpc Admin API 请求"
		return check, nil
	}
	req.SetBasicAuth(user, password)
	started := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			check.Status = "cancelled"
			check.Message = "frpc Admin API 检测已取消"
		} else {
			check.Message = fmt.Sprintf("无法读取 frpc Admin API：%v", err)
		}
		return check, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusUnauthorized {
			check.Message = "frpc Admin API 认证失败"
		} else {
			check.Message = fmt.Sprintf("frpc Admin API 返回 HTTP %d", resp.StatusCode)
		}
		return check, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		check.Message = "frpc Admin API 返回数据无法安全读取"
		return check, nil
	}
	var groups map[string][]frpcProxyStatus
	if err = json.Unmarshal(body, &groups); err != nil {
		check.Message = "frpc Admin API 返回的代理状态格式无法识别"
		return check, nil
	}
	out := make([]frpcProxyStatus, 0)
	for _, rows := range groups {
		out = append(out, rows...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].Type < out[j].Type
		}
		return out[i].Name < out[j].Name
	})
	check.Status = "reachable"
	check.LatencyMS = time.Since(started).Milliseconds()
	check.Message = fmt.Sprintf("已读取 %d 条 frpc 代理状态", len(out))
	return check, out
}

func runHealthTargetProbes(ctx context.Context, tunnels []Tunnel, rows []HealthProxy) {
	jobs := make([]int, 0, len(tunnels))
	for index, tunnel := range tunnels {
		if !tunnel.Enabled {
			continue
		}
		if tunnel.Type == "udp" || tunnel.Type == "stcp-visitor" {
			rows[index].Target = probeTargetContext(ctx, tunnel)
			continue
		}
		jobs = append(jobs, index)
	}
	if len(jobs) == 0 {
		return
	}
	workers := 32
	if len(jobs) < workers {
		workers = len(jobs)
	}
	queue := make(chan int)
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range queue {
				rows[index].Target = probeTargetContext(ctx, tunnels[index])
			}
		}()
	}
	for _, index := range jobs {
		select {
		case queue <- index:
		case <-ctx.Done():
			rows[index].Target = ProbeResult{Status: "cancelled", Message: "检测已取消"}
		}
	}
	close(queue)
	wg.Wait()
	for _, index := range jobs {
		if rows[index].Target.Status == "" {
			rows[index].Target = ProbeResult{Status: "cancelled", Message: "检测未完成"}
		}
	}
}

func normalizeRegistration(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running":
		return "registered"
	case "start error", "error", "failed":
		return "error"
	case "wait start", "waiting":
		return "waiting"
	case "closed", "stopped":
		return "stopped"
	case "":
		return "unknown"
	default:
		return "other"
	}
}

func summarizeHealth(proxies []HealthProxy) HealthSummary {
	summary := HealthSummary{TotalRules: len(proxies)}
	for _, proxy := range proxies {
		if proxy.Enabled {
			summary.EnabledRules++
		}
		switch proxy.Registration {
		case "registered":
			summary.Registered++
		case "error":
			summary.RegistrationErrors++
		case "missing":
			summary.RegistrationMissing++
		case "unknown", "waiting", "other":
			summary.RegistrationUnknown++
		}
		switch proxy.Target.Status {
		case "reachable", "healthy", "responding":
			summary.TargetsReachable++
		case "unreachable":
			summary.TargetsUnreachable++
		case "unsupported":
			summary.TargetsUnsupported++
		case "invalid":
			summary.TargetsInvalid++
		}
	}
	return summary
}

func cleanDiagnosticText(value string, secrets []string) string {
	value = redact(value, secrets)
	var builder strings.Builder
	for _, r := range value {
		if r == '\n' || r == '\r' || r == '\t' || (r >= 0x20 && r != 0x7f) {
			builder.WriteRune(r)
		}
	}
	runes := []rune(strings.TrimSpace(builder.String()))
	if len(runes) > 240 {
		runes = runes[:240]
	}
	return string(runes)
}
