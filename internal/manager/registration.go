package manager

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type registrationItem struct {
	State       string `json:"state"`
	Pending     bool   `json:"pending"`
	RunID       string `json:"runId,omitempty"`
	Phase       string `json:"phase,omitempty"`
	RemoteAddr  string `json:"remoteAddr,omitempty"`
	PortPending bool   `json:"portPending,omitempty"`
	Error       string `json:"error,omitempty"`
}

type registrationSnapshot struct {
	proc       *process
	runtime    RuntimeStatus
	status     *privateStatus
	reader     ProxyRegistrationReader
	applied    map[string]Tunnel
	signatures map[string][32]byte
	secrets    []string
	reason     string
	queries    []registrationQuery
}

func safeStatusText(s string, secrets []string) string {
	// Refuse oversized input before redaction; truncating first could expose a
	// fragment of a credential that crosses the truncation point.
	if len(s) > 4096 {
		return "[状态内容过长，已隐藏]"
	}
	// Normalize both sides before matching: removing control characters after
	// redaction could otherwise reassemble a credential in the output.
	normalize := func(value string) string {
		value = ansi.ReplaceAllString(value, "")
		return strings.Map(func(r rune) rune {
			if r < 32 || r == 127 {
				return -1
			}
			return r
		}, value)
	}
	s = normalize(s)
	redactionSecrets := make([]string, 0, 2*len(secrets))
	redactionSecrets = append(redactionSecrets, secrets...)
	for _, secret := range secrets {
		if normalized := normalize(secret); normalized != "" && normalized != secret {
			redactionSecrets = append(redactionSecrets, normalized)
		}
	}
	s = redact(s, redactionSecrets)
	if len(s) > 512 {
		end := 512
		for end > 0 && !utf8.RuneStart(s[end]) {
			end--
		}
		s = s[:end]
	}
	return s
}

func (a *App) registrationAPI(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	if a.activeRegistration || a.closing {
		a.mu.Unlock()
		fail(w, http.StatusTooManyRequests, fmt.Errorf("状态读取正在进行或工作台正在关闭"))
		return
	}
	a.activeRegistration = true
	state := a.store.DB.State.clone()
	revision := a.store.DB.Revision
	runtimes := a.statuses()
	clients := map[string]registrationSnapshot{}
	draftSignatures := map[string][32]byte{}
	for _, t := range state.Tunnels {
		if t.Enabled {
			if signature, err := a.store.tunnelSignature(t); err == nil {
				draftSignatures[t.ID] = signature
			}
		}
	}
	for _, i := range state.Instances {
		if i.Kind != "frpc" {
			continue
		}
		p := a.procs[i.ID]
		client := registrationSnapshot{proc: p, runtime: runtimes[i.ID]}
		if p != nil && client.runtime.State == "running" {
			p.logs.mu.Lock()
			client.secrets = append([]string(nil), p.logs.secrets...)
			p.logs.mu.Unlock()
			client.status, client.reader = p.privateStatus, p.registrationReader
			client.applied, client.signatures, client.reason = p.appliedTunnels, p.appliedTunnelSignatures, p.monitorReason
		}
		clients[i.ID] = client
	}
	for _, tunnel := range state.Tunnels {
		client, exists := clients[tunnel.InstanceID]
		if query, ok := registrationQueryFor(tunnel); exists && client.runtime.State == "running" && ok {
			client.queries = append(client.queries, query)
			clients[tunnel.InstanceID] = client
		}
	}
	a.mu.Unlock()
	defer func() { a.mu.Lock(); a.activeRegistration = false; a.mu.Unlock() }()
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	results := map[string]registrationReadings{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	limit := make(chan struct{}, 4)
	for id, item := range clients {
		if len(item.queries) == 0 {
			continue
		}
		wg.Add(1)
		go func(id string, item registrationSnapshot) {
			defer wg.Done()
			select {
			case limit <- struct{}{}:
				defer func() { <-limit }()
			case <-ctx.Done():
				return
			}
			values := readRegistrations(ctx, item.reader, item.status, item.queries)
			mu.Lock()
			results[id] = values
			mu.Unlock()
		}(id, item)
	}
	wg.Wait()
	a.mu.Lock()
	defer a.mu.Unlock()
	if revision != a.store.DB.Revision {
		fail(w, http.StatusConflict, fmt.Errorf("读取期间配置已变化，请重试"))
		return
	}
	for id, client := range clients {
		if a.procs[id] != client.proc {
			fail(w, http.StatusConflict, fmt.Errorf("读取期间实例运行代次已变化，请重试"))
			return
		}
		if client.proc != nil {
			current := client.proc.view()
			if current.State != client.runtime.State || current.RunID != client.runtime.RunID {
				fail(w, http.StatusConflict, fmt.Errorf("读取期间实例运行状态已变化，请重试"))
				return
			}
		}
	}
	items := map[string]registrationItem{}
	for _, tunnel := range state.Tunnels {
		client, exists := clients[tunnel.InstanceID]
		_, wasApplied := client.applied[tunnel.ID]
		draft, hasDraftSignature := draftSignatures[tunnel.ID]
		applied, hasAppliedSignature := client.signatures[tunnel.ID]
		value, read := results[tunnel.InstanceID]
		if !read && len(client.queries) != 0 {
			value.err = fmt.Errorf("状态读取已取消或超时")
		}
		running := exists && client.proc != nil && client.runtime.State == "running"
		item := classifyRegistration(registrationContext{
			tunnel: tunnel, pending: runtimes[tunnel.InstanceID].Pending, running: running, runID: client.runtime.RunID,
			wasApplied: wasApplied, appliedSignature: applied, hasAppliedSignature: hasAppliedSignature,
			draftSignature: draft, hasDraftSignature: hasDraftSignature,
			observed: value.forTunnel(tunnel), reason: client.reason, secrets: client.secrets,
		})
		items[tunnel.ID] = item
	}
	reply(w, http.StatusOK, map[string]any{"revision": revision, "tunnels": items})
}

type registrationContext struct {
	tunnel                                 Tunnel
	pending, running, wasApplied           bool
	runID                                  string
	appliedSignature, draftSignature       [32]byte
	hasAppliedSignature, hasDraftSignature bool
	observed                               proxyObservation
	reason                                 string
	secrets                                []string
}

func knownProxyPhase(s string) string {
	if len(s) > 32 {
		return ""
	}
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "new", "wait start", "start error", "running", "check failed", "closed":
		return s
	default:
		return ""
	}
}

// A status source may report a bare port or host:port. Never infer an
// allocation from zero, a malformed address, or text altered by truncation.
func validAllocatedAddress(addr string) bool {
	if addr == "" || len(addr) > 512 || strings.TrimSpace(addr) != addr {
		return false
	}
	port := addr
	if strings.Contains(addr, ":") {
		host, p, err := net.SplitHostPort(addr)
		if err != nil || (host != "" && !hostOK(host)) {
			return false
		}
		port = p
	}
	if port == "" {
		return false
	}
	for _, c := range port {
		if c < '0' || c > '9' {
			return false
		}
	}
	n, err := strconv.Atoi(port)
	return err == nil && portOK(n)
}

// classifyRegistration describes only state attributable to the current applied run.
func classifyRegistration(c registrationContext) registrationItem {
	tunnel := c.tunnel
	item := registrationItem{State: "stopped", Pending: c.pending, RunID: c.runID}
	if !tunnel.Enabled {
		item.State = "disabled"
		if c.running && c.wasApplied {
			item.State = "pending-stop"
		}
	} else if !c.running {
		item.State = "stopped"
	} else if !c.wasApplied || !c.hasAppliedSignature || !c.hasDraftSignature || c.draftSignature != c.appliedSignature {
		item.State = "pending"
	} else if isVisitor(tunnel.Type) {
		item.State = "not-reported"
	} else if c.observed.err != nil {
		item.State = "unavailable"
		item.Error = safeStatusText(c.reason, c.secrets)
		if item.Error == "" {
			item.Error = "代理状态接口不可用"
		}
	} else if !c.observed.found {
		item.State = "registering"
	} else {
		value := c.observed.status
		item.Phase = knownProxyPhase(value.Phase)
		switch {
		case item.Phase == "":
			item.State = "unavailable"
			item.Error = "代理状态无法识别"
		case item.Phase == "running":
			item.State = "registered"
			addr := safeStatusText(value.RemoteAddr, c.secrets)
			if (tunnel.Type == "tcp" || tunnel.Type == "udp") && tunnel.RemotePort == 0 &&
				(value.Error != "" || !validAllocatedAddress(value.RemoteAddr) || !validAllocatedAddress(addr)) {
				item.PortPending = true
			} else {
				item.RemoteAddr = addr
			}
			item.Error = safeStatusText(value.Error, c.secrets)
		case value.Error != "" || item.Phase == "start error" || item.Phase == "check failed":
			item.State = "failed"
			item.Error = safeStatusText(value.Error, c.secrets)
			if item.Phase == "check failed" && item.Error == "" {
				item.Error = "本地目标未通过自动健康检查"
			}
		case item.Phase == "closed":
			item.State = "unavailable"
		default:
			item.State = "registering"
		}
	}
	return item
}

