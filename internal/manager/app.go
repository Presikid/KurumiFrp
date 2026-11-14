package manager

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Presikid/KurumiFrp/internal/version"
	"github.com/Presikid/KurumiFrp/web"
)

// App wires together single-user AuthManager, self-contained Store, Official FRP RuntimeSupervisor, and WebUI static assets.
type App struct {
	dataDir    string
	auth       *AuthManager
	store      *Store
	runtime    *RuntimeSupervisor
	updater    *UpdateManager
	staticFS   http.Handler
	stateSeq   atomic.Uint64
	listenSpec string
	origArgs   []string
	onShutdown func()
}

// NewApp initializes the complete KurumiFrp backend application.
func NewApp(dataDir string, onShutdown func()) (*App, error) {
	auth, err := NewAuthManager(dataDir)
	if err != nil {
		return nil, err
	}
	store, err := NewStore(dataDir)
	if err != nil {
		return nil, err
	}
	app := &App{
		dataDir:    dataDir,
		auth:       auth,
		store:      store,
		onShutdown: onShutdown,
	}
	app.stateSeq.Store(1)

	rt, err := NewRuntimeSupervisor(dataDir, store, func() {
		app.stateSeq.Add(1)
	})
	if err != nil {
		return nil, err
	}
	app.runtime = rt
	app.updater = NewUpdateManager(dataDir, rt, onShutdown)

	subFS, err := fs.Sub(web.Files, ".")
	if err != nil {
		return nil, err
	}
	app.staticFS = http.FileServer(http.FS(subFS))
	return app, nil
}

// SetListenAddr informs RuntimeSupervisor of the local callback port, records CLI restart metadata, and triggers AutoStart profiles.
func (a *App) SetListenAddr(addr string) {
	a.runtime.SetCallbackAddr(addr)
	a.runtime.StartAutoStarts()
}

// SetRuntimeMeta records the bound TCP listener address and CLI arguments for seamless self-updates.
func (a *App) SetRuntimeMeta(listenSpec string, origArgs []string) {
	a.listenSpec = listenSpec
	a.origArgs = append([]string(nil), origArgs...)
}

// Close gracefully stops all running frpc and frps child processes.
func (a *App) Close() {
	if a.runtime != nil {
		a.runtime.StopAll()
	}
}

// Auth exposes the AuthManager for CLI maintenance commands.
func (a *App) Auth() *AuthManager {
	return a.auth
}

func (a *App) bumpState() {
	a.stateSeq.Add(1)
}

// ServeHTTP dispatches internal FRP webhooks, REST APIs, and embedded Tokisaki Kurumi WebUI assets.
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")

	path := r.URL.Path

	// 1. Internal official frps HTTP Server Plugin callback (authenticated by per-process random secret)
	if strings.HasPrefix(path, "/internal/frps-plugin/") {
		srvID := strings.TrimPrefix(path, "/internal/frps-plugin/")
		a.runtime.HandleFRPSPlugin(w, r, srvID)
		return
	}

	// 2. One-time local browser launch ticket (--open)
	if path == "/launch" && r.Method == http.MethodGet {
		ticket := r.URL.Query().Get("ticket")
		if sid, ok := a.auth.RedeemBrowserTicket(ticket); ok {
			setSessionCookie(w, sid)
		}
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	// 3. Public session check & login/logout endpoints
	if path == "/api/session" && r.Method == http.MethodGet {
		user, ok := a.auth.Authenticated(r)
		writeJSON(w, http.StatusOK, map[string]any{
			"authenticated":      ok,
			"username":           nonEmptyStr(user, a.auth.Username()),
			"defaultUsername":    a.auth.Username(),
			"needsPasswordSetup": a.auth.NeedsPasswordSetup(),
			"version":            version.Current,
		})
		return
	}

	if path == "/api/login" && r.Method == http.MethodPost {
		if !sameOriginRequest(r) {
			writeError(w, http.StatusForbidden, "跨站请求已被拦截")
			return
		}
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := readJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		sid, err := a.auth.Login(clientIPFromRequest(r), req.Username, req.Password)
		if err != nil {
			writeError(w, http.StatusUnauthorized, err.Error())
			return
		}
		setSessionCookie(w, sid)
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":                 true,
			"username":           a.auth.Username(),
			"needsPasswordSetup": a.auth.NeedsPasswordSetup(),
		})
		return
	}

	if path == "/api/logout" && r.Method == http.MethodPost {
		a.auth.Logout(r)
		clearSessionCookie(w)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}

	if path == "/api/browser-ticket" && r.Method == http.MethodPost {
		if !a.auth.VerifyLocalToken(r) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		ticket, err := a.auth.IssueBrowserTicket()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ticket": ticket})
		return
	}

	// 4. Authenticated API routes
	if strings.HasPrefix(path, "/api/") {
		username, ok := a.auth.Authenticated(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "登录会话已失效，请重新输入用户名与密码")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !a.auth.VerifyLocalToken(r) && !sameOriginRequest(r) {
			writeError(w, http.StatusForbidden, "跨站修改请求已被拦截")
			return
		}
		a.handleAPI(w, r, username)
		return
	}

	// 5. Static Tokisaki Kurumi WebUI assets
	a.staticFS.ServeHTTP(w, r)
}

func (a *App) handleAPI(w http.ResponseWriter, r *http.Request, username string) {
	switch r.URL.Path {
	case "/api/state":
		etag := fmt.Sprintf(`"s-%d"`, a.stateSeq.Load())
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		activeFile, doc := a.store.Snapshot()
		files, _ := a.store.ListConfigFiles()
		writeJSON(w, http.StatusOK, map[string]any{
			"version":            version.Current,
			"username":           username,
			"needsPasswordSetup": a.auth.NeedsPasswordSetup(),
			"activeConfigFile":   activeFile,
			"configFiles":        files,
			"config":             doc,
			"runtime":            a.runtime.Views(doc),
			"core":               LocateBinaries(a.dataDir),
			"updateProxy":        a.updater.ProxyURL(),
		})

	case "/api/account":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			CurrentPassword string `json:"currentPassword"`
			NewUsername     string `json:"newUsername"`
			NewPassword     string `json:"newPassword"`
		}
		if err := readJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := a.auth.UpdateCredentials(req.CurrentPassword, req.NewUsername, req.NewPassword); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		a.bumpState()
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":                 true,
			"username":           a.auth.Username(),
			"needsPasswordSetup": a.auth.NeedsPasswordSetup(),
		})

	// ==================== Section 1: Server (frps) & Connection Dashboard ====================
	case "/api/servers/save":
		var srv ServerProfile
		if err := readJSONBody(r, &srv); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if strings.TrimSpace(srv.Name) == "" {
			writeError(w, http.StatusBadRequest, "转发端配置名称不能为空")
			return
		}
		if srv.BindPort <= 0 || srv.BindPort > 65535 {
			writeError(w, http.StatusBadRequest, "监听端口需在 1~65535 之间")
			return
		}
		err := a.store.Update(func(doc *ConfigDocument) error {
			if srv.ID == "" {
				srv.ID = newShortID("srv")
				srv.ConnPolicies = make(map[string]ConnPolicy)
				doc.Servers = append(doc.Servers, srv)
				return nil
			}
			for i := range doc.Servers {
				if doc.Servers[i].ID == srv.ID {
					if srv.ConnPolicies == nil {
						srv.ConnPolicies = doc.Servers[i].ConnPolicies
					}
					doc.Servers[i] = srv
					return nil
				}
			}
			doc.Servers = append(doc.Servers, srv)
			return nil
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		a.bumpState()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": srv.ID})

	case "/api/servers/duplicate":
		var req struct {
			ID string `json:"id"`
		}
		if err := readJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		var newID string
		err := a.store.Update(func(doc *ConfigDocument) error {
			for _, s := range doc.Servers {
				if s.ID == req.ID {
					cp := s
					cp.ID = newShortID("srv")
					cp.Name = s.Name + " (副本)"
					cp.AutoStart = false
					if cp.BindPort < 65500 {
						cp.BindPort++
					}
					cp.VhostHTTPPort = 0
					cp.VhostHTTPSPort = 0
					cp.ConnPolicies = make(map[string]ConnPolicy)
					doc.Servers = append(doc.Servers, cp)
					newID = cp.ID
					return nil
				}
			}
			return errors.New("未找到要复制的转发端配置")
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		a.bumpState()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": newID})

	case "/api/servers/delete":
		var req struct {
			ID string `json:"id"`
		}
		if err := readJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		_ = a.runtime.StopInstance(req.ID)
		err := a.store.Update(func(doc *ConfigDocument) error {
			out := make([]ServerProfile, 0, len(doc.Servers))
			for _, s := range doc.Servers {
				if s.ID != req.ID {
					out = append(out, s)
				}
			}
			doc.Servers = out
			return nil
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		a.bumpState()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	case "/api/servers/control":
		var req struct {
			ID     string `json:"id"`
			Action string `json:"action"` // start, stop, restart
		}
		if err := readJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		_, doc := a.store.Snapshot()
		var target *ServerProfile
		for i := range doc.Servers {
			if doc.Servers[i].ID == req.ID {
				target = &doc.Servers[i]
				break
			}
		}
		if target == nil {
			writeError(w, http.StatusNotFound, "未找到该转发端配置")
			return
		}
		var err error
		switch req.Action {
		case "start", "restart":
			err = a.runtime.StartServer(*target)
		case "stop":
			err = a.runtime.StopInstance(target.ID)
		default:
			err = errors.New("未知控制动作")
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		a.bumpState()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	case "/api/servers/toml":
		id := r.URL.Query().Get("id")
		mask := r.URL.Query().Get("mask") == "1"
		_, doc := a.store.Snapshot()
		for _, s := range doc.Servers {
			if s.ID == id {
				rendered := RenderServerTOML(s, ServerRuntimeInjections{Preview: true, MaskSecrets: mask})
				writeJSON(w, http.StatusOK, map[string]any{"name": s.Name, "toml": rendered.TOML})
				return
			}
		}
		writeError(w, http.StatusNotFound, "未找到该转发端配置")

	case "/api/servers/conn-policy":
		var req struct {
			ServerID       string `json:"serverId"`
			ProxyName      string `json:"proxyName"`
			Enabled        bool   `json:"enabled"`
			TrafficLimitMB int64  `json:"trafficLimitMB"`
			SpeedLimitKBps int64  `json:"speedLimitKBps"`
			ExpiresAt      string `json:"expiresAt"`
			Note           string `json:"note"`
			ResetTraffic   bool   `json:"resetTraffic"`
		}
		if err := readJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		req.ProxyName = strings.TrimSpace(req.ProxyName)
		if req.ProxyName == "" {
			writeError(w, http.StatusBadRequest, "连接名称不能为空")
			return
		}
		if strings.TrimSpace(req.ExpiresAt) != "" {
			if _, err := time.Parse(time.RFC3339, strings.TrimSpace(req.ExpiresAt)); err != nil {
				writeError(w, http.StatusBadRequest, "限时截止时间格式无效（需为 ISO8601/RFC3339）")
				return
			}
		}
		err := a.store.Update(func(doc *ConfigDocument) error {
			for i := range doc.Servers {
				if doc.Servers[i].ID == req.ServerID {
					if doc.Servers[i].ConnPolicies == nil {
						doc.Servers[i].ConnPolicies = make(map[string]ConnPolicy)
					}
					prev := doc.Servers[i].ConnPolicies[req.ProxyName]
					pol := ConnPolicy{
						ProxyName:        req.ProxyName,
						Enabled:          req.Enabled,
						TrafficLimitMB:   maxInt64(0, req.TrafficLimitMB),
						SpeedLimitKBps:   maxInt64(0, req.SpeedLimitKBps),
						ExpiresAt:        strings.TrimSpace(req.ExpiresAt),
						RecordedInBytes:  prev.RecordedInBytes,
						RecordedOutBytes: prev.RecordedOutBytes,
						Note:             strings.TrimSpace(req.Note),
					}
					if req.ResetTraffic {
						pol.RecordedInBytes = 0
						pol.RecordedOutBytes = 0
					}
					doc.Servers[i].ConnPolicies[req.ProxyName] = pol
					return nil
				}
			}
			return errors.New("未找到对应转发端配置")
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if !req.Enabled {
			a.runtime.CloseOfflineProxies(req.ServerID)
		}
		a.bumpState()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	case "/api/servers/conn-delete":
		var req struct {
			ServerID  string `json:"serverId"`
			ProxyName string `json:"proxyName"`
		}
		if err := readJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		err := a.store.Update(func(doc *ConfigDocument) error {
			for i := range doc.Servers {
				if doc.Servers[i].ID == req.ServerID {
					delete(doc.Servers[i].ConnPolicies, req.ProxyName)
					return nil
				}
			}
			return errors.New("未找到对应转发端配置")
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		a.bumpState()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	// ==================== Section 2: Client (frpc) & Tunnels ====================
	case "/api/clients/save":
		var cli ClientProfile
		if err := readJSONBody(r, &cli); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if strings.TrimSpace(cli.Name) == "" {
			writeError(w, http.StatusBadRequest, "客户端配置名称不能为空")
			return
		}
		if strings.TrimSpace(cli.ServerAddr) == "" {
			writeError(w, http.StatusBadRequest, "远端服务器地址不能为空")
			return
		}
		if cli.ServerPort <= 0 || cli.ServerPort > 65535 {
			writeError(w, http.StatusBadRequest, "服务器端口需在 1~65535 之间")
			return
		}
		var saved ClientProfile
		err := a.store.Update(func(doc *ConfigDocument) error {
			if cli.ID == "" {
				cli.ID = newShortID("cli")
				if cli.Tunnels == nil {
					cli.Tunnels = []TunnelRule{}
				}
				doc.Clients = append(doc.Clients, cli)
				saved = cli
				return nil
			}
			for i := range doc.Clients {
				if doc.Clients[i].ID == cli.ID {
					if cli.Tunnels == nil {
						cli.Tunnels = doc.Clients[i].Tunnels
					}
					doc.Clients[i] = cli
					saved = cli
					return nil
				}
			}
			doc.Clients = append(doc.Clients, cli)
			saved = cli
			return nil
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		_ = a.runtime.ReloadOrRestartClient(saved)
		a.bumpState()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": saved.ID})

	case "/api/clients/duplicate":
		var req struct {
			ID string `json:"id"`
		}
		if err := readJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		var newID string
		err := a.store.Update(func(doc *ConfigDocument) error {
			for _, c := range doc.Clients {
				if c.ID == req.ID {
					raw, _ := json.Marshal(c)
					var cp ClientProfile
					_ = json.Unmarshal(raw, &cp)
					cp.ID = newShortID("cli")
					cp.Name = c.Name + " (副本)"
					cp.AutoStart = false
					for i := range cp.Tunnels {
						cp.Tunnels[i].ID = newShortID("tun")
						cp.Tunnels[i].Enabled = false
					}
					doc.Clients = append(doc.Clients, cp)
					newID = cp.ID
					return nil
				}
			}
			return errors.New("未找到要复制的客户端配置")
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		a.bumpState()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": newID})

	case "/api/clients/delete":
		var req struct {
			ID string `json:"id"`
		}
		if err := readJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		_ = a.runtime.StopInstance(req.ID)
		err := a.store.Update(func(doc *ConfigDocument) error {
			out := make([]ClientProfile, 0, len(doc.Clients))
			for _, c := range doc.Clients {
				if c.ID != req.ID {
					out = append(out, c)
				}
			}
			doc.Clients = out
			return nil
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		a.bumpState()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	case "/api/clients/control":
		var req struct {
			ID     string `json:"id"`
			Action string `json:"action"` // start, stop, restart, reload
		}
		if err := readJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		_, doc := a.store.Snapshot()
		var target *ClientProfile
		for i := range doc.Clients {
			if doc.Clients[i].ID == req.ID {
				target = &doc.Clients[i]
				break
			}
		}
		if target == nil {
			writeError(w, http.StatusNotFound, "未找到该客户端配置")
			return
		}
		var err error
		switch req.Action {
		case "start", "restart":
			err = a.runtime.StartClient(*target)
		case "reload":
			err = a.runtime.ReloadOrRestartClient(*target)
		case "stop":
			err = a.runtime.StopInstance(target.ID)
		default:
			err = errors.New("未知控制动作")
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		a.bumpState()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	case "/api/clients/tunnel-save":
		var req struct {
			ClientID string     `json:"clientId"`
			Tunnel   TunnelRule `json:"tunnel"`
		}
		if err := readJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		req.Tunnel.Name = strings.TrimSpace(req.Tunnel.Name)
		if req.Tunnel.Name == "" {
			writeError(w, http.StatusBadRequest, "隧道规则名称不能为空")
			return
		}
		var updatedCli *ClientProfile
		err := a.store.Update(func(doc *ConfigDocument) error {
			for i := range doc.Clients {
				if doc.Clients[i].ID == req.ClientID {
					for _, existing := range doc.Clients[i].Tunnels {
						if existing.Name == req.Tunnel.Name && existing.ID != req.Tunnel.ID {
							return fmt.Errorf("同一客户端内已存在同名规则 %q", req.Tunnel.Name)
						}
					}
					if req.Tunnel.ID == "" {
						req.Tunnel.ID = newShortID("tun")
						doc.Clients[i].Tunnels = append(doc.Clients[i].Tunnels, req.Tunnel)
					} else {
						replaced := false
						for j := range doc.Clients[i].Tunnels {
							if doc.Clients[i].Tunnels[j].ID == req.Tunnel.ID {
								doc.Clients[i].Tunnels[j] = req.Tunnel
								replaced = true
								break
							}
						}
						if !replaced {
							doc.Clients[i].Tunnels = append(doc.Clients[i].Tunnels, req.Tunnel)
						}
					}
					cp := doc.Clients[i]
					updatedCli = &cp
					return nil
				}
			}
			return errors.New("未找到所属客户端配置")
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if updatedCli != nil {
			_ = a.runtime.ReloadOrRestartClient(*updatedCli)
		}
		a.bumpState()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": req.Tunnel.ID})

	case "/api/clients/tunnel-toggle":
		var req struct {
			ClientID string `json:"clientId"`
			TunnelID string `json:"tunnelId"`
			Enabled  bool   `json:"enabled"`
		}
		if err := readJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		var updatedCli *ClientProfile
		err := a.store.Update(func(doc *ConfigDocument) error {
			for i := range doc.Clients {
				if doc.Clients[i].ID == req.ClientID {
					for j := range doc.Clients[i].Tunnels {
						if doc.Clients[i].Tunnels[j].ID == req.TunnelID {
							doc.Clients[i].Tunnels[j].Enabled = req.Enabled
							cp := doc.Clients[i]
							updatedCli = &cp
							return nil
						}
					}
				}
			}
			return errors.New("未找到对应隧道规则")
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if updatedCli != nil {
			_ = a.runtime.ReloadOrRestartClient(*updatedCli)
		}
		a.bumpState()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	case "/api/clients/tunnel-delete":
		var req struct {
			ClientID string `json:"clientId"`
			TunnelID string `json:"tunnelId"`
		}
		if err := readJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		var updatedCli *ClientProfile
		err := a.store.Update(func(doc *ConfigDocument) error {
			for i := range doc.Clients {
				if doc.Clients[i].ID == req.ClientID {
					out := make([]TunnelRule, 0, len(doc.Clients[i].Tunnels))
					for _, t := range doc.Clients[i].Tunnels {
						if t.ID != req.TunnelID {
							out = append(out, t)
						}
					}
					doc.Clients[i].Tunnels = out
					cp := doc.Clients[i]
					updatedCli = &cp
					return nil
				}
			}
			return errors.New("未找到所属客户端配置")
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if updatedCli != nil {
			_ = a.runtime.ReloadOrRestartClient(*updatedCli)
		}
		a.bumpState()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	case "/api/clients/toml":
		id := r.URL.Query().Get("id")
		mask := r.URL.Query().Get("mask") == "1"
		_, doc := a.store.Snapshot()
		for _, c := range doc.Clients {
			if c.ID == id {
				rendered := RenderClientTOML(c, ClientRuntimeInjections{Preview: true, MaskSecrets: mask})
				writeJSON(w, http.StatusOK, map[string]any{"name": c.Name, "toml": rendered.TOML})
				return
			}
		}
		writeError(w, http.StatusNotFound, "未找到该客户端配置")

	// ==================== Section 3: App Control (Software-Internal Configs, Logs, Update) ====================
	case "/api/configs/select":
		var req struct {
			FileName    string `json:"fileName"`
			DisplayName string `json:"displayName"`
		}
		if err := readJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		a.runtime.StopAll()
		rt, err := NewRuntimeSupervisor(a.dataDir, a.store, func() { a.stateSeq.Add(1) })
		if err == nil {
			a.runtime = rt
		}
		doc, err := a.store.SelectOrCreateConfig(req.FileName, req.DisplayName)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		a.runtime.StartAutoStarts()
		a.bumpState()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "config": doc})

	case "/api/configs/import-json":
		var req struct {
			FileName string          `json:"fileName"`
			Content  json.RawMessage `json:"content"`
			Activate bool            `json:"activate"`
		}
		if err := readJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if req.Activate {
			a.runtime.StopAll()
			if rt, err := NewRuntimeSupervisor(a.dataDir, a.store, func() { a.stateSeq.Add(1) }); err == nil {
				a.runtime = rt
			}
		}
		savedFile, err := a.store.ImportConfigDocument(req.FileName, req.Content, req.Activate)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if req.Activate {
			a.runtime.StartAutoStarts()
		}
		a.bumpState()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "fileName": savedFile})

	case "/api/configs/export-json":
		activeFile, doc := a.store.Snapshot()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, activeFile))
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(doc)

	case "/api/configs/delete":
		var req struct {
			FileName string `json:"fileName"`
		}
		if err := readJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := a.store.DeleteConfigFile(req.FileName); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		a.bumpState()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	case "/api/configs/import-toml":
		var req struct {
			Name string `json:"name"`
			Kind string `json:"kind"` // "frps", "frpc", or "" (auto-detect)
			TOML string `json:"toml"`
		}
		if err := readJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		srv, cli, err := ParseTOMLAsProfile(req.Name, req.Kind, req.TOML)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		err = a.store.Update(func(doc *ConfigDocument) error {
			if srv != nil {
				doc.Servers = append(doc.Servers, *srv)
			}
			if cli != nil {
				doc.Clients = append(doc.Clients, *cli)
			}
			return nil
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		a.bumpState()
		kindOut := "frpc"
		if srv != nil {
			kindOut = "frps"
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "kind": kindOut})

	case "/api/logs":
		id := r.URL.Query().Get("id")
		lines, _ := a.runtime.Logs(id)
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "lines": lines})

	case "/api/logs/stream":
		id := r.URL.Query().Get("id")
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeError(w, http.StatusInternalServerError, "streaming unsupported")
			return
		}
		lines, buf := a.runtime.Logs(id)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		for _, line := range lines {
			fmt.Fprintf(w, "data: %s\n\n", strings.ReplaceAll(line, "\n", " "))
		}
		flusher.Flush()
		if buf == nil {
			return
		}
		ch, unsub := buf.Subscribe()
		defer unsub()
		keepAlive := time.NewTicker(15 * time.Second)
		defer keepAlive.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case line, open := <-ch:
				if !open {
					return
				}
				fmt.Fprintf(w, "data: %s\n\n", strings.ReplaceAll(line, "\n", " "))
				flusher.Flush()
			case <-keepAlive.C:
				fmt.Fprint(w, ": ping\n\n")
				flusher.Flush()
			}
		}

	case "/api/update/settings":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			ProxyURL string `json:"proxyURL"`
		}
		if err := readJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		saved, err := a.updater.SaveProxyURL(req.ProxyURL)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		a.bumpState()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "proxyURL": saved})

	case "/api/update/check":
		var proxyOverride *string
		if r.Method == http.MethodPost {
			var req struct {
				ProxyURL *string `json:"proxyURL"`
			}
			if err := readJSONBody(r, &req); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			proxyOverride = req.ProxyURL
		}
		res, err := a.updater.CheckAll(r.Context(), proxyOverride)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if proxyOverride != nil {
			a.bumpState()
		}
		writeJSON(w, http.StatusOK, res)

	case "/api/update/apply-frp":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			ProxyURL *string `json:"proxyURL"`
		}
		if r.ContentLength > 0 {
			if err := readJSONBody(r, &req); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		coreInfo, restarted, err := a.updater.ApplyFRPUpdate(r.Context(), req.ProxyURL)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		a.bumpState()
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":        true,
			"core":      coreInfo,
			"restarted": restarted,
		})

	case "/api/update/apply-app":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			ProxyURL *string `json:"proxyURL"`
		}
		if r.ContentLength > 0 {
			if err := readJSONBody(r, &req); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		targetVer, err := a.updater.ApplyAppUpdate(r.Context(), req.ProxyURL, a.listenSpec, a.origArgs)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":            true,
			"restarting":    true,
			"targetVersion": targetVer,
		})

	case "/api/quit":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		if a.onShutdown != nil {
			go func() {
				time.Sleep(100 * time.Millisecond)
				a.onShutdown()
			}()
		}

	default:
		writeError(w, http.StatusNotFound, "未找到请求的 API 路径")
	}
}

func setSessionCookie(w http.ResponseWriter, sid string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    sid,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func sameOriginRequest(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

func readJSONBody(r *http.Request, dst any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxConfigBytes))
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("解析请求 JSON 失败: %w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": msg})
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func compareSemVer(a, b string) int {
	clean := func(s string) []string {
		s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "v"))
		if idx := strings.IndexAny(s, "-+"); idx >= 0 {
			s = s[:idx]
		}
		return strings.Split(s, ".")
	}
	pa := clean(a)
	pb := clean(b)
	n := len(pa)
	if len(pb) > n {
		n = len(pb)
	}
	if n < 3 {
		n = 3
	}
	for i := 0; i < n; i++ {
		var va, vb int
		if i < len(pa) {
			fmt.Sscanf(pa[i], "%d", &va)
		}
		if i < len(pb) {
			fmt.Sscanf(pb[i], "%d", &vb)
		}
		if va != vb {
			if va > vb {
				return 1
			}
			return -1
		}
	}
	return 0
}
