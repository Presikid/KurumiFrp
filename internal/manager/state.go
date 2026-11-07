package manager

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Presikid/KurumiFrp/internal/version"
)

// The cache key is sampled from the state actually used by the response. It
// needs no mutation counter that a future save, import, or exit path can forget
// to increment. Log lines are a separate /api/logs resource.
type stateProcessSnapshot struct {
	status RuntimeStatus
	coreID string
}

type stateResponseCache struct {
	revision uint64
	coreID   string
	frpc     bool
	frps     bool
	procs    map[string]stateProcessSnapshot
	body     []byte
	etag     string
}

func (c *stateResponseCache) matches(next *stateResponseCache) bool {
	if c == nil || c.revision != next.revision || c.coreID != next.coreID || c.frpc != next.frpc || c.frps != next.frps || len(c.procs) != len(next.procs) {
		return false
	}
	for id, p := range next.procs {
		if old, ok := c.procs[id]; !ok || old != p {
			return false
		}
	}
	return true
}

// stateResponseLocked requires a.mu. Cached bytes and their key are immutable
// after publication, so response writes can safely happen outside the lock.
func (a *App) stateResponseLocked() (*stateResponseCache, error) {
	_, ec := a.binary("frpc")
	_, es := a.binary("frps")
	next := &stateResponseCache{
		revision: a.store.DB.Revision,
		frpc:     ec == nil,
		frps:     es == nil,
		procs:    make(map[string]stateProcessSnapshot),
	}
	if a.store.DB.ActiveCore != nil {
		next.coreID = *a.store.DB.ActiveCore
	}
	for _, i := range a.store.DB.State.Instances {
		if p := a.procs[i.ID]; p != nil {
			next.procs[i.ID] = stateProcessSnapshot{status: p.view(), coreID: p.coreID}
		}
	}
	if a.stateCache.matches(next) {
		return a.stateCache, nil
	}
	runtime := make(map[string]RuntimeStatus, len(a.store.DB.State.Instances))
	for _, i := range a.store.DB.State.Instances {
		v := RuntimeStatus{State: "stopped"}
		if p, ok := next.procs[i.ID]; ok {
			v = p.status
			rendered, err := a.store.render(i.ID, false)
			v.Pending = err != nil || v.Applied != rendered.Digest || p.coreID != next.coreID
		}
		runtime[i.ID] = v
	}
	history := make([]map[string]any, 0, len(a.store.DB.History))
	for _, h := range a.store.DB.History {
		history = append(history, map[string]any{"revision": h.Revision, "at": h.At, "summary": h.Summary})
	}
	body, err := json.Marshal(map[string]any{
		"version":      version.Current,
		"revision":     a.store.DB.Revision,
		"state":        a.store.DB.State.public(),
		"runtime":      runtime,
		"history":      history,
		"historyLimit": a.store.DB.HistoryLimit,
		"events":       a.store.DB.Events,
		"binaries":     map[string]bool{"frpc": next.frpc, "frps": next.frps},
	})
	if err != nil {
		return nil, err
	}
	next.body = append(body, '\n')
	digest := sha256.Sum256(next.body)
	next.etag = `"` + hex.EncodeToString(digest[:]) + `"`
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
	w.Header().Set("ETag", cached.etag)
	if matchesStateETag(strings.Join(r.Header.Values("If-None-Match"), ","), cached.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(cached.body)
}
