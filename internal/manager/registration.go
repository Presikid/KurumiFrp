package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Registration reports frpc's own proxy phases. It does not test whether an
// external user can actually reach the published service.
type TunnelRegistration struct {
	State      string `json:"state"`
	RemoteAddr string `json:"remoteAddr,omitempty"`
	Error      string `json:"error,omitempty"`
	Applied    bool   `json:"applied"`
	Pending    bool   `json:"pending"`
}

type RegistrationReport struct {
	CheckedAt string                        `json:"checkedAt"`
	Tunnels   map[string]TunnelRegistration `json:"tunnels"`
}

type appliedTunnel struct {
	Name string
	Type string
}

type frpcProxyStatus struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Status     string `json:"status"`
	Err        string `json:"err"`
	RemoteAddr string `json:"remote_addr"`
}

type clientRegistrationSnapshot struct {
	proc     *process
	status   RuntimeStatus
	port     int
	password string
	applied  map[string]appliedTunnel
	secrets  []string
}

type clientRegistrationResult struct {
	proxies map[string]frpcProxyStatus
	err     error
}

// The status HTTP server is a private, randomly authenticated listener spawned
// on frpc's loopback interface only. The generated config holds an environment
// template, never the password. Never allow callers to specify its destination.
func enableClientRegistration(r Rendered) (Rendered, int, string, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return r, 0, "", fmt.Errorf("无法分配本机 frpc 状态接口端口：%w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		return r, 0, "", err
	}
	password := randomHex(32)
	prefix := fmt.Sprintf("webServer.addr = \"127.0.0.1\"\nwebServer.port = %d\nwebServer.user = \"kurumi\"\nwebServer.password = {{ .Envs.KURUMI_STATUS_PASSWORD_TOML }}\n", port)
	r.Text = prefix + r.Text
	r.Env = append(r.Env, "KURUMI_STATUS_PASSWORD_TOML="+quote(password))
	r.Secrets = append(r.Secrets, password)
	// Keep r.Digest unchanged: it identifies the user's configuration, not the
	// per-start ephemeral management port or password.
	return r, port, password, nil
}

func fetchClientRegistration(ctx context.Context, port int, password string) (map[string]frpcProxyStatus, error) {
	if port < 1 || port > 65535 || password == "" {
		return nil, fmt.Errorf("状态接口未配置")
	}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/api/status", nil)
	if err != nil {
		return nil, err
	}
	request.SetBasicAuth("kurumi", password)
	client := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			Proxy:             nil,
			DisableKeepAlives: true,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp4", addr)
			},
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("本机 frpc 状态接口不可连接")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("本机 frpc 状态接口返回 HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20+1))
	if err != nil || len(body) > 1<<20 {
		return nil, fmt.Errorf("frpc 状态数据过大或读取失败")
	}
	var groups map[string][]frpcProxyStatus
	if err := json.Unmarshal(body, &groups); err != nil {
		return nil, fmt.Errorf("frpc 状态数据格式无效")
	}
	out := make(map[string]frpcProxyStatus)
	for kind, proxies := range groups {
		if len(proxies) > 512 || len(out)+len(proxies) > 512 {
			return nil, fmt.Errorf("frpc 状态数据条目过多")
		}
		for _, p := range proxies {
			if p.Type == kind && p.Name != "" {
				out[kind+"\x00"+p.Name] = p
			}
		}
	}
	return out, nil
}

func safeRegistrationText(s string, secrets []string) string {
	s = redact(s, secrets)
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	runes := []rune(strings.TrimSpace(s))
	if len(runes) > 200 {
		return string(runes[:200]) + "…"
	}
	return string(runes)
}

func (a *App) registration(ctx context.Context) RegistrationReport {
	report := RegistrationReport{CheckedAt: now(), Tunnels: map[string]TunnelRegistration{}}
	a.mu.Lock()
	tunnels := a.store.DB.State.clone().Tunnels
	runtimes := a.statuses()
	clients := map[string]clientRegistrationSnapshot{}
	for _, i := range a.store.DB.State.Instances {
		if i.Kind != "frpc" {
			continue
		}
		if p := a.procs[i.ID]; p != nil {
			p.logs.mu.Lock()
			secrets := append([]string(nil), p.logs.secrets...)
			p.logs.mu.Unlock()
			clients[i.ID] = clientRegistrationSnapshot{
				proc: p, status: p.view(), port: p.statusPort,
				password: p.statusPassword, applied: p.applied, secrets: secrets,
			}
		}
	}
	a.mu.Unlock()

	results := map[string]clientRegistrationResult{}
	var resultsMu sync.Mutex
	var wg sync.WaitGroup
	limit := make(chan struct{}, 8)
	for id, client := range clients {
		if client.status.State != "running" || client.port == 0 {
			continue
		}
		wg.Add(1)
		go func(id string, client clientRegistrationSnapshot) {
			defer wg.Done()
			select {
			case limit <- struct{}{}:
				defer func() { <-limit }()
			case <-ctx.Done():
				return
			}
			status, err := fetchClientRegistration(ctx, client.port, client.password)
			resultsMu.Lock()
			results[id] = clientRegistrationResult{proxies: status, err: err}
			resultsMu.Unlock()
		}(id, client)
	}
	wg.Wait()

	for _, t := range tunnels {
		client, exists := clients[t.InstanceID]
		applied, active := client.applied[t.ID]
		_, running := runtimes[t.InstanceID]
		v := TunnelRegistration{State: "stopped", Pending: running && runtimes[t.InstanceID].Pending}
		v.Applied = exists && active && applied.Name == t.Name && applied.Type == t.Type
		switch {
		case !t.Enabled && active && client.status.State == "running":
			v.State = "pending-stop" // Draft disabled; the old proxy remains active.
		case !t.Enabled:
			v.State = "disabled"
		case !exists || client.status.State != "running" || client.proc.view().State != "running":
			v.State = "stopped"
		case !v.Applied:
			v.State = "pending"
		case t.Type == "stcp-visitor":
			v.State = "visitor" // frpc /api/status only lists proxies.
		case client.port == 0:
			v.State = "unavailable"
		default:
			result, ok := results[t.InstanceID]
			if !ok || result.err != nil {
				v.State = "unavailable"
			} else if ps, found := result.proxies[t.Type+"\x00"+t.Name]; found {
				if strings.EqualFold(ps.Status, "running") {
					v.State = "registered"
					v.RemoteAddr = safeRegistrationText(ps.RemoteAddr, client.secrets)
				} else if ps.Err != "" {
					v.State = "failed"
					v.Error = safeRegistrationText(ps.Err, client.secrets)
				} else {
					v.State = "registering"
				}
			} else {
				v.State = "registering"
			}
		}
		report.Tunnels[t.ID] = v
	}
	return report
}
