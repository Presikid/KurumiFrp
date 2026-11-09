package manager

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type healthEndpoint struct {
	State     string `json:"state"`
	Message   string `json:"message"`
	Address   string `json:"address,omitempty"`
	LatencyMS int64  `json:"latencyMs,omitempty"`
}
type healthRule struct {
	ID           string           `json:"id"`
	Name         string           `json:"name"`
	Type         string           `json:"type"`
	Registration registrationItem `json:"registration"`
	Target       ProbeResult      `json:"target"`
}
type healthReport struct {
	Revision   uint64         `json:"revision"`
	CheckedAt  string         `json:"checkedAt"`
	Instance   string         `json:"instance"`
	Kind       string         `json:"kind"`
	Runtime    string         `json:"runtime"`
	RunID      string         `json:"runId,omitempty"`
	Pending    bool           `json:"pending"`
	ProbeScope string         `json:"probeScope"`
	Endpoint   healthEndpoint `json:"endpoint"`
	Rules      []healthRule   `json:"rules"`
	Note       string         `json:"note"`
}

func (a *App) reserveHealthProbe() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.activeProbes >= 12 || a.closing {
		return false
	}
	a.activeProbes++
	return true
}
func (a *App) releaseHealthProbe() { a.mu.Lock(); a.activeProbes--; a.mu.Unlock() }

func checkInstanceEndpoint(ctx context.Context, i Instance) healthEndpoint {
	host := i.Host
	if i.Kind == "frps" {
		if host == "" || host == "0.0.0.0" || host == "::" {
			host = "127.0.0.1"
		} else if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return healthEndpoint{State: "unsupported", Message: "服务端绑定地址不是本机回环或通配地址，未检测"}
		}
	}
	if !hostOK(host) || !portOK(i.Port) {
		return healthEndpoint{State: "invalid", Message: "地址或端口无效"}
	}
	addr := net.JoinHostPort(host, strconv.Itoa(i.Port))
	start := time.Now()
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	c, err := (&net.Dialer{}).DialContext(probeCtx, "tcp", addr)
	if err != nil {
		return healthEndpoint{State: "unreachable", Address: addr, Message: "TCP 端口无法连接"}
	}
	_ = c.Close()
	return healthEndpoint{State: "reachable", Address: addr, LatencyMS: time.Since(start).Milliseconds(), Message: "TCP 端口可连接；不代表 FRP 认证、代理注册或公网可用"}
}

func (a *App) healthAPI(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Revision uint64 `json:"revision"`
		ID       string `json:"id"`
	}
	if err := decode(w, r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	a.mu.Lock()
	if a.activeHealth || a.closing {
		a.mu.Unlock()
		fail(w, 429, fmt.Errorf("链路检测正在进行或工作台正在关闭"))
		return
	}
	if req.Revision != a.store.DB.Revision {
		a.mu.Unlock()
		fail(w, 409, fmt.Errorf("配置已变化，请刷新"))
		return
	}
	i, ok := a.store.DB.State.Instance(req.ID)
	if !ok {
		a.mu.Unlock()
		fail(w, 404, fmt.Errorf("实例不存在"))
		return
	}
	a.activeHealth = true
	st := a.store.DB.State.clone()
	p := a.procs[i.ID]
	runtime := a.statuses()[i.ID]
	secrets := []string{}
	draftSignatures := map[string][32]byte{}
	if v, err := a.store.decrypt(i.Token); err == nil {
		secrets = append(secrets, v)
	}
	for _, t := range st.Tunnels {
		if t.InstanceID == i.ID && t.Enabled {
			if signature, err := a.store.tunnelSignature(t); err == nil {
				draftSignatures[t.ID] = signature
			}
		}
		if t.InstanceID == i.ID && t.Secret != "" {
			if v, err := a.store.decrypt(t.Secret); err == nil {
				secrets = append(secrets, v)
			}
		}
		if t.InstanceID == i.ID && t.HTTPPassword != "" {
			if v, err := a.store.decrypt(t.HTTPPassword); err == nil {
				secrets = append(secrets, v)
			}
		}
	}
	if p != nil {
		p.logs.mu.Lock()
		secrets = append(secrets, p.logs.secrets...)
		p.logs.mu.Unlock()
	}
	a.mu.Unlock()
	defer func() { a.mu.Lock(); a.activeHealth = false; a.mu.Unlock() }()
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	report := healthReport{Revision: req.Revision, CheckedAt: now(), Instance: safeStatusText(i.Name, secrets), Kind: i.Kind, Runtime: runtime.State, RunID: runtime.RunID, Pending: runtime.Pending, ProbeScope: "draft", Rules: []healthRule{}, Note: "端口与本地目标按当前草稿检测；进程与注册状态对应已应用配置。待应用时两者可能不同。检测不代表公网可达；报告含网络地址，请妥善保管。"}
	var tunnels []Tunnel
	for _, t := range st.Tunnels {
		if t.InstanceID == i.ID {
			tunnels = append(tunnels, t)
		}
	}
	var observed registrationReadings
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if !a.reserveHealthProbe() {
			report.Endpoint = healthEndpoint{State: "busy", Message: "检测任务过多，请重试"}
			return
		}
		defer a.releaseHealthProbe()
		report.Endpoint = checkInstanceEndpoint(ctx, i)
	}()
	queries := registrationQueries(tunnels)
	if p != nil && runtime.State == "running" && i.Kind == "frpc" && len(queries) != 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			observed = readRegistrations(ctx, p.registrationReader, p.privateStatus, queries)
		}()
	}
	for _, t := range tunnels {
		report.Rules = append(report.Rules, healthRule{ID: t.ID, Name: safeStatusText(t.Name, secrets), Type: t.Type, Target: ProbeResult{Status: "not-checked", Message: "未检测"}})
	}
	jobs := make(chan int)
	for n := 0; n < 4; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				t := tunnels[index]
				if !t.Enabled {
					report.Rules[index].Target = ProbeResult{Status: "skipped", Message: "规则已停用"}
				} else if isVisitor(t.Type) {
					report.Rules[index].Target = ProbeResult{Status: "unsupported", Message: "Visitor 监听端口不是远端服务"}
				} else {
					if !a.reserveHealthProbe() {
						report.Rules[index].Target = ProbeResult{Status: "busy", Message: "检测任务过多，请重试"}
						continue
					}
					report.Rules[index].Target = probeTargetContext(ctx, t)
					a.releaseHealthProbe()
				}
			}
		}()
	}
	for n := range tunnels {
		select {
		case jobs <- n:
		case <-ctx.Done():
			break
		}
		if ctx.Err() != nil {
			break
		}
	}
	close(jobs)
	wg.Wait()
	a.mu.Lock()
	defer a.mu.Unlock()
	if req.Revision != a.store.DB.Revision || a.procs[i.ID] != p {
		fail(w, 409, fmt.Errorf("检测期间配置或进程已变化，请重试"))
		return
	}
	if a.statuses()[i.ID] != runtime {
		fail(w, 409, fmt.Errorf("检测期间实例运行状态或代次已变化，请重试"))
		return
	}
	for n, t := range tunnels {
		row := &report.Rules[n]
		draftSignature, hasDraftSignature := draftSignatures[t.ID]
		row.Target.Message = safeStatusText(row.Target.Message, secrets)
		row.Target.Address = safeStatusText(row.Target.Address, secrets)
		row.Target.Host = safeStatusText(row.Target.Host, secrets)
		var applied [32]byte
		var wasApplied, hasAppliedSignature bool
		var reason string
		running := p != nil && runtime.State == "running" && i.Kind == "frpc"
		if running {
			_, wasApplied = p.appliedTunnels[t.ID]
			applied, hasAppliedSignature = p.appliedTunnelSignatures[t.ID]
			reason = p.monitorReason
		}
		item := classifyRegistration(registrationContext{
			tunnel: t, pending: runtime.Pending, running: running, runID: runtime.RunID,
			wasApplied: wasApplied, appliedSignature: applied, hasAppliedSignature: hasAppliedSignature,
			draftSignature: draftSignature, hasDraftSignature: hasDraftSignature,
			observed: observed.forTunnel(t), reason: reason, secrets: secrets,
		})
		row.Registration = item
	}
	report.Endpoint.Address = safeStatusText(report.Endpoint.Address, secrets)
	report.Endpoint.Message = safeStatusText(report.Endpoint.Message, secrets)
	reply(w, 200, report)
}
