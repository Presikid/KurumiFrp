package manager

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

type registrationItem struct {
	State      string `json:"state"`
	Pending    bool   `json:"pending"`
	RemoteAddr string `json:"remoteAddr,omitempty"`
	Error      string `json:"error,omitempty"`
}

type registrationSnapshot struct {
	proc       *process
	status     *privateStatus
	applied    map[string]Tunnel
	signatures map[string][32]byte
	secrets    []string
	reason     string
}

func safeStatusText(s string, secrets []string) string {
	s = redact(s, secrets)
	s = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, s)
	if len(s) > 512 {
		s = s[:512]
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
		if p == nil || p.view().State != "running" {
			continue
		}
		p.logs.mu.Lock()
		secrets := append([]string(nil), p.logs.secrets...)
		p.logs.mu.Unlock()
		clients[i.ID] = registrationSnapshot{proc: p, status: p.privateStatus, applied: p.appliedTunnels, signatures: p.appliedTunnelSignatures, secrets: secrets, reason: p.monitorReason}
	}
	a.mu.Unlock()
	defer func() { a.mu.Lock(); a.activeRegistration = false; a.mu.Unlock() }()
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	type response struct {
		values map[string]observedProxy
		err    error
	}
	results := map[string]response{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	limit := make(chan struct{}, 4)
	for id, item := range clients {
		wg.Add(1)
		go func(id string, item registrationSnapshot) {
			defer wg.Done()
			select {
			case limit <- struct{}{}:
				defer func() { <-limit }()
			case <-ctx.Done():
				return
			}
			values, err := item.status.read(ctx)
			mu.Lock()
			results[id] = response{values: values, err: err}
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
	items := map[string]registrationItem{}
	for _, tunnel := range state.Tunnels {
		client, exists := clients[tunnel.InstanceID]
		_, wasApplied := client.applied[tunnel.ID]
		draft, hasDraftSignature := draftSignatures[tunnel.ID]
		applied, hasAppliedSignature := client.signatures[tunnel.ID]
		value := results[tunnel.InstanceID]
		running := exists && a.procs[tunnel.InstanceID] == client.proc && client.proc.view().State == "running"
		item := classifyRegistration(registrationContext{
			tunnel: tunnel, pending: runtimes[tunnel.InstanceID].Pending, running: running,
			wasApplied: wasApplied, appliedSignature: applied, hasAppliedSignature: hasAppliedSignature,
			draftSignature: draft, hasDraftSignature: hasDraftSignature,
			observed: value.values, observedErr: value.err, reason: client.reason, secrets: client.secrets,
		})
		items[tunnel.ID] = item
	}
	reply(w, http.StatusOK, map[string]any{"revision": revision, "tunnels": items})
}

type registrationContext struct {
	tunnel                                 Tunnel
	pending, running, wasApplied           bool
	appliedSignature, draftSignature       [32]byte
	hasAppliedSignature, hasDraftSignature bool
	observed                               map[string]observedProxy
	observedErr                            error
	reason                                 string
	secrets                                []string
}

// classifyRegistration describes only state attributable to the current applied process.
func classifyRegistration(c registrationContext) registrationItem {
	tunnel := c.tunnel
	item := registrationItem{State: "stopped", Pending: c.pending}
	if !tunnel.Enabled {
		item.State = "disabled"
		if c.running && c.wasApplied {
			item.State = "pending-stop"
		}
	} else if !c.running {
		item.State = "stopped"
	} else if !c.wasApplied || !c.hasAppliedSignature || !c.hasDraftSignature || c.draftSignature != c.appliedSignature {
		item.State = "pending"
	} else if tunnel.Type == "stcp-visitor" {
		item.State = "not-reported"
	} else if c.observedErr != nil || c.observed == nil {
		item.State = "unavailable"
		item.Error = c.reason
		if item.Error == "" {
			item.Error = "本机安全状态接口不可用或身份验证失败"
		}
	} else if value, ok := c.observed[tunnel.Type+"\x00"+tunnel.Name]; !ok {
		item.State = "registering"
	} else if strings.EqualFold(value.Status, "running") {
		item.State = "registered"
		item.RemoteAddr = safeStatusText(value.RemoteAddr, c.secrets)
	} else if value.Error != "" {
		item.State = "failed"
		item.Error = safeStatusText(value.Error, c.secrets)
	} else {
		item.State = "registering"
	}
	return item
}
