package manager

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/Presikid/KurumiFrp/internal/version"
)

// The cache key is sampled from the state actually used by the response. It
// needs no mutation counter that a future save, import, or exit path can forget
// to increment. Log lines are a separate /api/logs resource.
type stateProcessSnapshot struct {
	status       RuntimeStatus
	coreID       string
	startupError string
	present      bool
}

type stateResponseCache struct {
	revision    uint64
	coreID      string
	frpc        bool
	frps        bool
	runtimeFRPC bool
	runtimeFRPS bool
	runtimeMode string
	procs       map[string]stateProcessSnapshot
	body        []byte
}

func (c *stateResponseCache) matches(next *stateResponseCache) bool {
	if c == nil || c.revision != next.revision || c.coreID != next.coreID || c.frpc != next.frpc || c.frps != next.frps || c.runtimeFRPC != next.runtimeFRPC || c.runtimeFRPS != next.runtimeFRPS || c.runtimeMode != next.runtimeMode || len(c.procs) != len(next.procs) {
		return false
	}
	for id, p := range next.procs {
		if old, ok := c.procs[id]; !ok || old != p {
			return false
		}
	}
	return true
}

// Both the response cache and other status consumers use the same observation.
// These helpers require a.mu; process liveness is sampled under the process lock.
func (a *App) observeRuntimeLocked(i Instance) stateProcessSnapshot {
	var observed stateProcessSnapshot
	if p := a.procs[i.ID]; p != nil {
		observed.status, observed.coreID, observed.present = p.view(), p.coreID, true
	}
	if i.AutoStart {
		observed.startupError = a.startupErrors[i.ID]
	}
	return observed
}

func (a *App) observedRuntimeStatusLocked(i Instance, observed stateProcessSnapshot) RuntimeStatus {
	v := RuntimeStatus{State: "stopped"}
	if observed.present {
		v = observed.status
		rendered, err := a.store.render(i.ID, false)
		v.Pending = err != nil || v.Applied != rendered.Digest || observed.coreID != a.activeCoreID()
	}
	if observed.startupError != "" && v.State != "running" {
		v.State, v.Exit = "autostart-failed", observed.startupError
	}
	return v
}

// stateResponseLocked requires a.mu. Cached bytes and their key are immutable
// after publication, so response writes can safely happen outside the lock.
func (a *App) stateResponseLocked() (*stateResponseCache, error) {
	_, ec := a.core.Binary("frpc")
	_, es := a.core.Binary("frps")
	next := &stateResponseCache{
		revision:    a.store.DB.Revision,
		frpc:        ec == nil,
		frps:        es == nil,
		runtimeFRPC: ec == nil,
		runtimeFRPS: es == nil,
		runtimeMode: "external",
		procs:       make(map[string]stateProcessSnapshot),
	}
	if _, external := a.runtime.(*externalRuntime); !external {
		_, ec = a.resolveRuntime("frpc")
		_, es = a.resolveRuntime("frps")
		next.runtimeFRPC = ec == nil
		next.runtimeFRPS = es == nil
		next.runtimeMode = "custom"
		if backend, ok := a.runtime.(interface{ runtimeMode() string }); ok {
			next.runtimeMode = backend.runtimeMode()
		}
	}
	if a.store.DB.ActiveCore != nil {
		next.coreID = *a.store.DB.ActiveCore
	}
	for _, i := range a.store.DB.State.Instances {
		observed := a.observeRuntimeLocked(i)
		if observed.present || observed.startupError != "" {
			next.procs[i.ID] = observed
		}
	}
	if a.stateCache.matches(next) {
		return a.stateCache, nil
	}
	runtime := make(map[string]RuntimeStatus, len(a.store.DB.State.Instances))
	for _, i := range a.store.DB.State.Instances {
		runtime[i.ID] = a.observedRuntimeStatusLocked(i, next.procs[i.ID])
	}
	history := make([]map[string]any, 0, len(a.store.DB.History))
	for _, h := range a.store.DB.History {
		history = append(history, map[string]any{"revision": h.Revision, "at": h.At, "summary": h.Summary})
	}
	warnings := make([]string, 0)
	for _, warning := range possibleProxyConflicts(a.store.DB.State) {
		warnings = append(warnings, warning)
	}
	sort.Strings(warnings)
	if len(warnings) > 10 {
		warnings = append(warnings[:10], "还有其他可能的端口冲突，请检查全部规则。")
	}
	body, err := json.Marshal(map[string]any{
		"version":          version.Current,
		"revision":         a.store.DB.Revision,
		"state":            a.store.DB.State.public(),
		"portWarnings":     warnings,
		"runtime":          runtime,
		"runtimeBackend":   next.runtimeMode,
		"runtimeAvailable": map[string]bool{"frpc": next.runtimeFRPC, "frps": next.runtimeFRPS},
		"history":          history,
		"historyLimit":     a.store.DB.HistoryLimit,
		"events":           a.store.DB.Events,
		"binaries":         map[string]bool{"frpc": next.frpc, "frps": next.frps},
	})
	if err != nil {
		return nil, err
	}
	next.body = append(body, '\n')
	a.stateCache = next
	return next, nil
}

// GET uses weak comparison, including a list of validators or '*'. Our own
// tags contain only hexadecimal characters, never a comma inside the quotes.
func matchesStateETag(header, etag string) bool {
	for _, item := range strings.Split(header, ",") {
		item = strings.TrimSpace(item)
		if item == "*" || strings.TrimPrefix(item, "W/") == etag {
			return true
		}
	}
	return false
}

// ServeHTTP performs authentication and same-origin checks before routing here.
func (a *App) handleState(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	cached, err := a.stateResponseLocked()
	a.mu.Unlock()
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	// The shared cache contains administrator data. Build a fresh, explicit
	// per-identity projection before deriving the validator or sending bytes.
	var body map[string]json.RawMessage
	if err = json.Unmarshal(cached.body, &body); err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	p := requestPrincipal(r)
	if !p.admin() {
		var state State
		var runtime map[string]RuntimeStatus
		if json.Unmarshal(body["state"], &state) != nil || json.Unmarshal(body["runtime"], &runtime) != nil {
			fail(w, http.StatusInternalServerError, fmt.Errorf("无法读取实例状态"))
			return
		}
		visible := State{Instances: []Instance{}, Tunnels: []Tunnel{}}
		statuses := make(map[string]RuntimeStatus)
		for _, instance := range state.Instances {
			if p.canView(instance.ID) {
				visible.Instances = append(visible.Instances, instance)
				v := runtime[instance.ID]
				statuses[instance.ID] = RuntimeStatus{State: v.State, Pending: v.Pending, RunID: v.RunID, Generation: v.Generation}
			}
		}
		for _, tunnel := range state.Tunnels {
			if p.canView(tunnel.InstanceID) {
				visible.Tunnels = append(visible.Tunnels, tunnel)
			}
		}
		visibleState, _ := json.Marshal(visible)
		visibleRuntime, _ := json.Marshal(statuses)
		body = map[string]json.RawMessage{
			"version": body["version"], "revision": body["revision"],
			"state": visibleState, "runtime": visibleRuntime,
			"runtimeBackend": body["runtimeBackend"], "runtimeAvailable": body["runtimeAvailable"], "binaries": body["binaries"],
			"history": json.RawMessage(`[]`), "events": json.RawMessage(`[]`), "portWarnings": json.RawMessage(`[]`), "historyLimit": json.RawMessage(`0`),
		}
	}
	body["identity"], _ = json.Marshal(p)
	encoded, err := json.Marshal(body)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	encoded = append(encoded, '\n')
	digest := sha256.Sum256(encoded)
	etag := `"` + hex.EncodeToString(digest[:]) + `"`
	w.Header().Set("ETag", etag)
	if matchesStateETag(strings.Join(r.Header.Values("If-None-Match"), ","), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(encoded)
}
