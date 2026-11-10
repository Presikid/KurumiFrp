package manager

import (
	"fmt"
	"net/http"
	"sort"
	"sync"
)

// Every API method has an explicit policy. Unknown methods and future routes
// are denied for all identities until they are deliberately added here.
func routeAllowed(p principal, r *http.Request) bool {
	if r.Method == http.MethodPost && (r.URL.Path == "/api/logout" || r.URL.Path == "/api/password") {
		return true
	}
	if r.Method == http.MethodGet {
		switch r.URL.Path {
		case "/api/login-history", "/api/firewall", "/api/firewall/seen":
			return p.admin()
		case "/api/webhooks":
			return p.admin()
		case "/api/nodes":
			return p.admin() || p.Role == "operator" || p.Role == "viewer"
		case "/api/state":
			return p.admin() || p.Role == "operator" || p.Role == "viewer"
		case "/api/config", "/api/diff":
			return p.canReadConfig() && (p.admin() || p.Role == "operator" || p.Role == "viewer")
		case "/api/traffic", "/api/traffic-history", "/api/server-traffic":
			return p.canReadTraffic() && (p.admin() || p.Role == "operator" || p.Role == "viewer")
		case "/api/logs", "/api/logs/stream":
			return p.canReadLogs(r.URL.Query().Get("id"))
		case "/api/registration", "/api/core", "/api/core/releases", "/api/core/release", "/api/update/status", "/api/update/releases", "/api/export", "/api/webhook", "/api/accounts":
			return p.admin()
		}
	}
	if r.Method == http.MethodPost {
		switch r.URL.Path {
		case "/api/login-history", "/api/firewall", "/api/firewall/asn":
			return p.admin()
		case "/api/webhooks":
			return p.admin()
		case "/api/tunnels/export", "/api/schedules":
			return p.admin()
		case "/api/nodes/check":
			return p.admin() || p.Role == "operator" || p.Role == "viewer"
		case "/api/control", "/api/validate", "/api/nodes/control":
			return p.admin() || p.Role == "operator"
		case "/api/accounts", "/api/quit", "/api/browser-ticket", "/api/probe", "/api/health", "/api/webhook", "/api/backup/export", "/api/backup/import", "/api/core/install", "/api/core/cancel", "/api/core/activate", "/api/update/install", "/api/import", "/api/import/toml", "/api/import/toml/preview", "/api/nodes", "/api/nodes/resolve", "/api/nodes/confirm", "/api/duplicate", "/api/tunnels/batch-enabled", "/api/tunnels/batch-delete", "/api/tunnels/batch", "/api/save", "/api/delete", "/api/restore", "/api/history-settings":
			return p.admin()
		}
	}
	return false
}

// A request holds only its own account's read gate. A permission change waits
// for that account's admitted requests, not for unrelated users. The identity
// must be resolved again after waiting because a writer may have revoked it.
func (a *auth) lockRequest(r *http.Request) (principal, func(), bool) {
	p, ok := a.identity(r)
	if !ok {
		return principal{}, nil, false
	}
	if p.gate == nil {
		return p, func() {}, true
	}
	p.gate.RLock()
	current, ok := a.identity(r)
	if !ok || current.gate != p.gate {
		p.gate.RUnlock()
		return principal{}, nil, false
	}
	return current, p.gate.RUnlock, true
}

// Acquire every restricted account gate before App.mu. These critical
// sections protect instance deletion or replacement from reviving stale grants.
// A changed account revision aborts, rather than trusting an incomplete list.
func (a *App) lockStateReplacement() (func(), error) {
	type gateEntry struct {
		id   string
		gate *sync.RWMutex
	}
	a.auth.mu.Lock()
	if a.auth.accountLoadErr != nil {
		a.auth.mu.Unlock()
		return nil, fmt.Errorf("账户文件无法读取，修复前不能替换或删除实例")
	}
	revision := a.auth.accountsRevision
	gates := make([]gateEntry, 0, len(a.auth.accounts))
	for _, u := range a.auth.accounts {
		if u.Role != "admin" {
			gates = append(gates, gateEntry{u.ID, u.gate})
		}
	}
	a.auth.mu.Unlock()
	sort.Slice(gates, func(i, j int) bool { return gates[i].id < gates[j].id })
	for _, entry := range gates {
		entry.gate.Lock()
	}
	release := func() {
		for i := len(gates) - 1; i >= 0; i-- {
			gates[i].gate.Unlock()
		}
	}
	a.mu.Lock()
	a.auth.mu.Lock()
	valid := a.auth.accountLoadErr == nil && a.auth.accountsRevision == revision
	a.auth.mu.Unlock()
	if !valid {
		a.mu.Unlock()
		release()
		return nil, fmt.Errorf("账户授权已变化，请刷新后重试")
	}
	return func() { a.mu.Unlock(); release() }, nil
}
