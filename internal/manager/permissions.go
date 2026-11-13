package manager

import "net/http"

// Every API method has an explicit policy. Unknown methods and future routes
// are denied for all identities until they are deliberately added here.
func routeAllowed(p principal, r *http.Request) bool {
	if r.URL.Path == "/api/node-agents" && (r.Method == http.MethodGet || r.Method == http.MethodPost) {
		return p.admin()
	}
	if r.Method == http.MethodPost && (r.URL.Path == "/api/logout" || r.URL.Path == "/api/password") {
		return true
	}
	if r.Method == http.MethodGet {
		switch r.URL.Path {
		case "/api/login-history", "/api/logs/history", "/api/firewall", "/api/firewall/seen":
			return p.admin()
		case "/api/webhooks":
			return p.admin()
		case "/api/nodes":
			return p.admin() || p.Role == "operator" || p.Role == "viewer"
		case "/api/state", "/api/themes":
			return p.admin() || p.Role == "operator" || p.Role == "viewer"
		case "/api/config", "/api/diff":
			return p.canReadConfig() && (p.admin() || p.Role == "operator" || p.Role == "viewer")
		case "/api/traffic", "/api/traffic-history", "/api/traffic-budget", "/api/server-traffic":
			return p.canReadTraffic() && (p.admin() || p.Role == "operator" || p.Role == "viewer")
		case "/api/client-traffic":
			// Applied rule names are configuration details as well as traffic data.
			return p.canReadTraffic() && p.canReadConfig() && (p.admin() || p.Role == "operator" || p.Role == "viewer")
		case "/api/logs", "/api/logs/stream":
			return p.canReadLogs(r.URL.Query().Get("id"))
		case "/api/registration", "/api/update/status", "/api/update/releases", "/api/export", "/api/webhook":
			return p.admin()
		}
	}
	if r.Method == http.MethodPost {
		switch r.URL.Path {
		case "/api/login-history", "/api/logs/history", "/api/firewall", "/api/firewall/asn", "/api/firewall/ip-asn", "/api/firewall/import", "/api/firewall/list":
			return p.admin()
		case "/api/webhooks":
			return p.admin()
		case "/api/tunnels/export", "/api/schedules":
			return p.admin()
		case "/api/nodes/check":
			return p.admin() || p.Role == "operator" || p.Role == "viewer"
		case "/api/control", "/api/validate", "/api/nodes/control":
			return p.admin() || p.Role == "operator"
		case "/api/quit", "/api/browser-ticket", "/api/probe", "/api/health", "/api/webhook", "/api/backup/export", "/api/backup/import", "/api/update/install", "/api/themes/install", "/api/import", "/api/import/toml", "/api/import/toml/preview", "/api/nodes", "/api/nodes/resolve", "/api/nodes/confirm", "/api/duplicate", "/api/tunnels/batch-enabled", "/api/tunnels/batch-delete", "/api/tunnels/batch", "/api/save", "/api/delete", "/api/restore", "/api/history-settings", "/api/traffic-budget":
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

// State replacement is serialized with other app mutations.
func (a *App) lockStateReplacement() (func(), error) {
	a.mu.Lock()
	return a.mu.Unlock, nil
}
