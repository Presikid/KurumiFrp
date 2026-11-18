package manager

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
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
	assets     *AssetPackManager
	panel      *PanelManager
	staticFS   http.Handler
	stateSeq   atomic.Uint64
	exitCode   atomic.Int32
	metaMu     sync.RWMutex
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
	// An open WebUI can send the old process's ETag after an upgrade.
	app.stateSeq.Store(uint64(time.Now().UnixNano()))

	rt, err := NewRuntimeSupervisor(dataDir, store, func() {
		app.stateSeq.Add(1)
	})
	if err != nil {
		return nil, err
	}
	app.runtime = rt
	app.updater = NewUpdateManager(dataDir, rt, func(code int) {
		app.exitCode.Store(int32(code))
		if onShutdown != nil {
			onShutdown()
		}
	})
	app.assets = NewAssetPackManager(dataDir, app.updater, func() {
		app.stateSeq.Add(1)
	})
	app.panel = NewPanelManager(dataDir)

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

// UpdateCallbackAddr updates RuntimeSupervisor's fallback callback address after a live panel port switch.
func (a *App) UpdateCallbackAddr(addr string) {
	a.runtime.SetCallbackAddr(addr)
	a.bumpState()
}

// SetPanelRebindHandler registers the live WebUI listener rebinding callback from cmd/kurumi/main.go.
func (a *App) SetPanelRebindHandler(listenAddr string, fn PanelRebindFunc) {
	a.panel.SetRebindHandler(listenAddr, fn)
}

// SetRuntimeMeta records the bound TCP listener address and CLI arguments for seamless self-updates.
func (a *App) SetRuntimeMeta(listenSpec string, origArgs []string) {
	a.metaMu.Lock()
	a.listenSpec = listenSpec
	a.origArgs = append([]string(nil), origArgs...)
	a.metaMu.Unlock()
	if a.panel != nil {
		a.panel.RecordBoundAddr(listenSpec)
	}
}

func (a *App) getRuntimeMeta() (string, []string) {
	a.metaMu.RLock()
	defer a.metaMu.RUnlock()
	return a.listenSpec, append([]string(nil), a.origArgs...)
}

// Close gracefully stops all running frpc and frps child processes.
func (a *App) Close() {
	if a.runtime != nil {
		a.runtime.StopAll()
	}
}

// ExitCode returns any requested process exit status (e.g. 42 for systemd self-update restart).
func (a *App) ExitCode() int {
	return int(a.exitCode.Load())
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
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")

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
			setSessionCookie(w, r, sid)
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
			"activeAssetTier":    a.assets.ActiveTier(),
			"assetCacheToken":    a.assets.CacheToken(),
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
		setSessionCookie(w, r, sid)
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

	// 4. Strictly Authenticated API routes (zero access before login!)
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

	// 5. Static WebUI assets (transparently overlays downloaded GitHub HD artworks when enabled)
	if (strings.HasPrefix(path, "/assets/k_") || strings.HasPrefix(path, "/assets/s_")) && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		if a.assets != nil && a.assets.ServeIfHD(w, r) {
			return
		}
		w.Header().Set("X-Kurumi-Asset-Tier", "embedded")
		w.Header().Set("Cache-Control", "public, max-age=60")
	}
	a.staticFS.ServeHTTP(w, r)
}

func sanitizeDocForBrowser(doc ConfigDocument) ConfigDocument {
	for i := range doc.Clients {
		doc.Clients[i].CtrlSecret = ""
		doc.Clients[i].AgentToken = ""
	}
	return doc
}

// Local mutations read their request before taking the lifecycle lock and release
// it before sending the response. Both the store edit and its process action
// therefore belong to one activation, even when two files reuse an instance ID.
type localConfigResponse struct {
	http.ResponseWriter
	release func()
}

func (w *localConfigResponse) WriteHeader(status int) {
	w.release()
	w.ResponseWriter.WriteHeader(status)
}

func (w *localConfigResponse) Write(data []byte) (int, error) {
	w.release()
	return w.ResponseWriter.Write(data)
}

func readConfigPOST(w http.ResponseWriter, r *http.Request, dst any) bool {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return false
	}
	if err := readJSONBody(r, dst); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return false
	}
	return true
}

func (a *App) beginLocalConfigChange(w http.ResponseWriter, r *http.Request, dst any) (*localConfigResponse, bool) {
	expected := a.store.activeGeneration()
	if !readConfigPOST(w, r, dst) {
		return nil, false
	}
	if header := r.Header.Get("X-Config-Generation"); header != "" {
		var err error
		expected, err = strconv.ParseUint(header, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "配置方案版本标识无效")
			return nil, false
		}
	}
	a.runtime.lifecycleMu.Lock()
	if expected != a.store.activeGeneration() {
		a.runtime.lifecycleMu.Unlock()
		writeError(w, http.StatusConflict, "当前配置方案已变更，请刷新后重试")
		return nil, false
	}
	return &localConfigResponse{w, sync.OnceFunc(a.runtime.lifecycleMu.Unlock)}, true
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
		activeFile, doc, generation := a.store.snapshotWithGeneration()
		files, _ := a.store.listConfigFiles(activeFile)
		views := a.runtime.viewsForGeneration(doc, generation)
		writeJSON(w, http.StatusOK, map[string]any{
			"version":            version.Current,
			"username":           username,
			"needsPasswordSetup": a.auth.NeedsPasswordSetup(),
			"activeConfigFile":   activeFile,
			"configGeneration":   generation,
			"configFiles":        files,
			"config":             sanitizeDocForBrowser(doc),
			"runtime":            views,
			"core":               LocateBinaries(a.dataDir),
			"updateProxy":        a.updater.ProxyURL(),
			"systemProxy":        a.updater.DetectedSystemProxy(),
			"hdAssets":           a.assets.Status(),
			"panel":              a.panel.Status(),
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
			ConfirmPassword string `json:"confirmPassword"`
		}
		if err := readJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := a.auth.UpdateCredentialsConfirmed(req.CurrentPassword, req.NewUsername, req.NewPassword, req.ConfirmPassword); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		clearSessionCookie(w)
		a.bumpState()
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":                 true,
			"username":           a.auth.Username(),
			"needsPasswordSetup": a.auth.NeedsPasswordSetup(),
		})

	case "/api/panel/settings":
		if r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, a.panel.Status())
			return
		}
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			Port     int    `json:"port"`
			BindHost string `json:"bindHost"`
		}
		if err := readJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		st, changed, err := a.panel.UpdatePort(req.Port, req.BindHost, a.store, a.runtime)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		a.bumpState()
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"changed": changed,
			"panel":   st,
		})

	// ==================== Bidirectional Live TOML Preview & Parse ====================
	case "/api/toml/preview":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			Kind   string         `json:"kind"`
			Mask   bool           `json:"mask"`
			Server *ServerProfile `json:"server,omitempty"`
			Client *ClientProfile `json:"client,omitempty"`
		}
		if err := readJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if req.Kind == "frps" && req.Server != nil {
			res := RenderServerTOML(*req.Server, ServerRuntimeInjections{Preview: true, MaskSecrets: req.Mask})
			writeJSON(w, http.StatusOK, map[string]any{"toml": res.TOML})
			return
		}
		if req.Client != nil {
			res := RenderClientTOML(*req.Client, ClientRuntimeInjections{Preview: true, MaskSecrets: req.Mask})
			writeJSON(w, http.StatusOK, map[string]any{"toml": res.TOML})
			return
		}
		writeError(w, http.StatusBadRequest, "缺少要预览的实例数据")

	case "/api/toml/parse":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			Kind       string         `json:"kind"`
			TOML       string         `json:"toml"`
			PrevServer *ServerProfile `json:"prevServer,omitempty"`
			PrevClient *ClientProfile `json:"prevClient,omitempty"`
		}
		if err := readJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if req.Kind == "frps" {
			base := ServerProfile{BindAddr: "0.0.0.0", BindPort: 7000}
			if req.PrevServer != nil {
				base = *req.PrevServer
			}
			upd, err := ApplyTOMLToServer(base, req.TOML)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"server": upd})
			return
		}
		baseCli := ClientProfile{ServerAddr: "127.0.0.1", ServerPort: 7000, Protocol: "tcp", TLSEnable: true}
		if req.PrevClient != nil {
			baseCli = *req.PrevClient
		}
		updCli, err := ApplyTOMLToClient(baseCli, req.TOML)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		updCli.CtrlSecret = ""
		updCli.AgentToken = ""
		writeJSON(w, http.StatusOK, map[string]any{"client": updCli})

	// ==================== Section 1: Server (frps) & Connection Dashboard ====================
	case "/api/servers/save":
		var srv ServerProfile
		w, ok := a.beginLocalConfigChange(w, r, &srv)
		if !ok {
			return
		}
		defer w.release()
		if strings.TrimSpace(srv.Name) == "" {
			writeError(w, http.StatusBadRequest, "转发端配置名称不能为空")
			return
		}
		if srv.BindPort <= 0 || srv.BindPort > 65535 {
			writeError(w, http.StatusBadRequest, "监听端口需在 1~65535 之间")
			return
		}
		if _, err := ValidateAllowPortsSpec(srv.AllowPorts); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		var saved ServerProfile
		err := a.store.Update(func(doc *ConfigDocument) error {
			if srv.ID == "" {
				srv.ID = newShortID("srv")
				srv.ConnPolicies = make(map[string]ConnPolicy)
				doc.Servers = append(doc.Servers, srv)
				saved = srv
				return nil
			}
			for i := range doc.Servers {
				if doc.Servers[i].ID == srv.ID {
					// F33: Always preserve authoritative live ConnPolicies from Store so stale server form drafts never clobber traffic/quotas!
					srv.ConnPolicies = doc.Servers[i].ConnPolicies
					if srv.AuthToken == "********" {
						srv.AuthToken = doc.Servers[i].AuthToken
					}
					doc.Servers[i] = srv
					saved = srv
					return nil
				}
			}
			doc.Servers = append(doc.Servers, srv)
			saved = srv
			return nil
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if a.runtime.IsInstanceRunning(saved.ID) {
			if err := a.runtime.startServer(saved); err != nil {
				a.bumpState()
				writeError(w, http.StatusBadRequest, "配置已保存，应用到运行实例失败: "+err.Error())
				return
			}
		}
		a.bumpState()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": saved.ID})

	case "/api/servers/save-toml":
		var req struct {
			ID   string `json:"id"`
			TOML string `json:"toml"`
		}
		w, ok := a.beginLocalConfigChange(w, r, &req)
		if !ok {
			return
		}
		defer w.release()
		var saved ServerProfile
		err := a.store.Update(func(doc *ConfigDocument) error {
			for i := range doc.Servers {
				if doc.Servers[i].ID == req.ID {
					upd, parseErr := ApplyTOMLToServer(doc.Servers[i], req.TOML)
					if parseErr != nil {
						return parseErr
					}
					if upd.BindPort <= 0 || upd.BindPort > 65535 {
						return errors.New("TOML 中监听端口 bindPort 需在 1~65535 之间")
					}
					doc.Servers[i] = upd
					saved = upd
					return nil
				}
			}
			return errors.New("未找到该转发端实例")
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if a.runtime.IsInstanceRunning(saved.ID) {
			if err := a.runtime.startServer(saved); err != nil {
				a.bumpState()
				writeError(w, http.StatusBadRequest, "TOML 配置已保存，应用到运行实例失败: "+err.Error())
				return
			}
		}
		a.bumpState()
		rendered := RenderServerTOML(saved, ServerRuntimeInjections{Preview: true, MaskSecrets: false})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "server": saved, "toml": rendered.TOML})

	case "/api/servers/duplicate":
		var req struct {
			ID string `json:"id"`
		}
		w, ok := a.beginLocalConfigChange(w, r, &req)
		if !ok {
			return
		}
		defer w.release()
		var newID string
		err := a.store.Update(func(doc *ConfigDocument) error {
			for _, s := range doc.Servers {
				if s.ID == req.ID {
					raw, _ := json.Marshal(s)
					var cp ServerProfile
					_ = json.Unmarshal(raw, &cp)
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
		w, ok := a.beginLocalConfigChange(w, r, &req)
		if !ok {
			return
		}
		defer w.release()
		if err := a.runtime.stopInstance(req.ID); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		a.runtime.ClearInstanceError(req.ID)
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
		w, ok := a.beginLocalConfigChange(w, r, &req)
		if !ok {
			return
		}
		defer w.release()
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
			err = a.runtime.startServer(*target)
		case "stop":
			err = a.runtime.stopInstance(target.ID)
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
		w, ok := a.beginLocalConfigChange(w, r, &req)
		if !ok {
			return
		}
		defer w.release()
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
					rawIn, rawOut := a.runtime.CurrentProxyRawTraffic(req.ServerID, req.ProxyName)
					pol := ConnPolicy{
						ProxyName:        req.ProxyName,
						Enabled:          req.Enabled,
						TrafficLimitMB:   maxInt64(0, req.TrafficLimitMB),
						SpeedLimitKBps:   maxInt64(0, req.SpeedLimitKBps),
						ExpiresAt:        strings.TrimSpace(req.ExpiresAt),
						RecordedInBytes:  prev.RecordedInBytes,
						RecordedOutBytes: prev.RecordedOutBytes,
						LastRawInBytes:   prev.LastRawInBytes,
						LastRawOutBytes:  prev.LastRawOutBytes,
						Note:             strings.TrimSpace(req.Note),
					}
					if req.ResetTraffic {
						pol.RecordedInBytes = 0
						pol.RecordedOutBytes = 0
						pol.LastRawInBytes = rawIn
						pol.LastRawOutBytes = rawOut
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
		w, ok := a.beginLocalConfigChange(w, r, &req)
		if !ok {
			return
		}
		defer w.release()
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
		w, ok := a.beginLocalConfigChange(w, r, &cli)
		if !ok {
			return
		}
		defer w.release()
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
				cli.CtrlSecret = randomTokenHex(16)
				cli.AgentToken = randomTokenHex(20)
				if cli.Tunnels == nil {
					cli.Tunnels = []TunnelRule{}
				}
				doc.Clients = append(doc.Clients, cli)
				saved = cli
				return nil
			}
			for i := range doc.Clients {
				if doc.Clients[i].ID == cli.ID {
					prev := doc.Clients[i]
					if cli.Tunnels == nil {
						cli.Tunnels = prev.Tunnels
					}
					if cli.CtrlSecret == "" {
						cli.CtrlSecret = prev.CtrlSecret
					}
					if cli.AgentToken == "" {
						cli.AgentToken = prev.AgentToken
					}
					if cli.AuthToken == "********" {
						cli.AuthToken = prev.AuthToken
					}
					if cli.OIDCClientSecret == "********" {
						cli.OIDCClientSecret = prev.OIDCClientSecret
					}
					doc.Clients[i] = cli
					saved = cli
					return nil
				}
			}
			cli.CtrlSecret = randomTokenHex(16)
			cli.AgentToken = randomTokenHex(20)
			doc.Clients = append(doc.Clients, cli)
			saved = cli
			return nil
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := a.runtime.reloadOrRestartClient(saved); err != nil {
			a.bumpState()
			writeError(w, http.StatusBadRequest, "客户端配置校验或热重载失败: "+err.Error())
			return
		}
		a.bumpState()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": saved.ID})

	case "/api/clients/save-toml":
		var req struct {
			ID   string `json:"id"`
			TOML string `json:"toml"`
		}
		w, ok := a.beginLocalConfigChange(w, r, &req)
		if !ok {
			return
		}
		defer w.release()
		var saved ClientProfile
		err := a.store.Update(func(doc *ConfigDocument) error {
			for i := range doc.Clients {
				if doc.Clients[i].ID == req.ID {
					upd, parseErr := ApplyTOMLToClient(doc.Clients[i], req.TOML)
					if parseErr != nil {
						return parseErr
					}
					if strings.TrimSpace(upd.ServerAddr) == "" || upd.ServerPort <= 0 || upd.ServerPort > 65535 {
						return errors.New("TOML 中 serverAddr 不能为空且 serverPort 需在 1~65535 之间")
					}
					doc.Clients[i] = upd
					saved = upd
					return nil
				}
			}
			return errors.New("未找到该客户端实例")
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := a.runtime.reloadOrRestartClient(saved); err != nil {
			a.bumpState()
			writeError(w, http.StatusBadRequest, "TOML 配置校验或热重载失败: "+err.Error())
			return
		}
		a.bumpState()
		rendered := RenderClientTOML(saved, ClientRuntimeInjections{Preview: true, MaskSecrets: false})
		sanitized := saved
		sanitized.CtrlSecret = ""
		sanitized.AgentToken = ""
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "client": sanitized, "toml": rendered.TOML})

	case "/api/clients/duplicate":
		var req struct {
			ID string `json:"id"`
		}
		w, ok := a.beginLocalConfigChange(w, r, &req)
		if !ok {
			return
		}
		defer w.release()
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
					cp.CtrlSecret = randomTokenHex(16)
					cp.AgentToken = randomTokenHex(20)
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
		w, ok := a.beginLocalConfigChange(w, r, &req)
		if !ok {
			return
		}
		defer w.release()
		if err := a.runtime.stopInstance(req.ID); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		a.runtime.ClearInstanceError(req.ID)
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
		w, ok := a.beginLocalConfigChange(w, r, &req)
		if !ok {
			return
		}
		defer w.release()
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
			err = a.runtime.startClient(*target)
		case "reload":
			err = a.runtime.reloadOrRestartClient(*target)
		case "stop":
			err = a.runtime.stopInstance(target.ID)
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
		w, ok := a.beginLocalConfigChange(w, r, &req)
		if !ok {
			return
		}
		defer w.release()
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
			if err := a.runtime.reloadOrRestartClient(*updatedCli); err != nil {
				a.bumpState()
				writeError(w, http.StatusBadRequest, "隧道规则已保存，但热重载预检失败（原运行实例未中断）: "+err.Error())
				return
			}
		}
		a.bumpState()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": req.Tunnel.ID})

	case "/api/clients/tunnel-toggle":
		var req struct {
			ClientID string `json:"clientId"`
			TunnelID string `json:"tunnelId"`
			Enabled  bool   `json:"enabled"`
		}
		w, ok := a.beginLocalConfigChange(w, r, &req)
		if !ok {
			return
		}
		defer w.release()
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
			if err := a.runtime.reloadOrRestartClient(*updatedCli); err != nil {
				a.bumpState()
				writeError(w, http.StatusBadRequest, "隧道状态已保存，但热重载预检失败: "+err.Error())
				return
			}
		}
		a.bumpState()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	case "/api/clients/tunnel-delete":
		var req struct {
			ClientID string `json:"clientId"`
			TunnelID string `json:"tunnelId"`
		}
		w, ok := a.beginLocalConfigChange(w, r, &req)
		if !ok {
			return
		}
		defer w.release()
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
			if err := a.runtime.reloadOrRestartClient(*updatedCli); err != nil {
				a.bumpState()
				writeError(w, http.StatusBadRequest, "隧道已移除，但热重载预检失败: "+err.Error())
				return
			}
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

	// ==================== Section 2B: Connection-Based Remote Client Control & P2P ====================
	case "/api/remote/client":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		serverID := strings.TrimSpace(r.URL.Query().Get("serverId"))
		clientID := strings.TrimSpace(r.URL.Query().Get("clientId"))
		snap, err := a.runtime.FetchRemoteClient(r.Context(), serverID, clientID)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, snap)

	case "/api/remote/client/save":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			ServerID            string        `json:"serverId"`
			ClientID            string        `json:"clientId"`
			Profile             ClientProfile `json:"profile"`
			AllowUpstreamChange bool          `json:"allowUpstreamChange"`
		}
		if err := readJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		saved, tomlOut, err := a.runtime.SaveRemoteClient(r.Context(), req.ServerID, req.ClientID, req.Profile, req.AllowUpstreamChange)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		a.bumpState()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "profile": saved, "toml": tomlOut})

	case "/api/remote/client/save-toml":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			ServerID            string `json:"serverId"`
			ClientID            string `json:"clientId"`
			TOML                string `json:"toml"`
			AllowUpstreamChange bool   `json:"allowUpstreamChange"`
		}
		if err := readJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		saved, tomlOut, err := a.runtime.SaveRemoteClientTOML(r.Context(), req.ServerID, req.ClientID, req.TOML, req.AllowUpstreamChange)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		a.bumpState()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "profile": saved, "toml": tomlOut})

	case "/api/remote/client/reload":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			ServerID string `json:"serverId"`
			ClientID string `json:"clientId"`
		}
		if err := readJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := a.runtime.ReloadRemoteClient(r.Context(), req.ServerID, req.ClientID); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		a.bumpState()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	case "/api/remote/p2p-pair":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req P2PPairRequest
		if err := readJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		res, err := a.runtime.OrchestrateRemoteP2P(r.Context(), req)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		a.bumpState()
		writeJSON(w, http.StatusOK, res)

	// ==================== Section 3: App Control (Software-Internal Configs, Logs, Update) ====================
	case "/api/configs/select":
		var req struct {
			FileName    string `json:"fileName"`
			DisplayName string `json:"displayName"`
		}
		if !readConfigPOST(w, r, &req) {
			return
		}
		var doc ConfigDocument
		err := a.runtime.switchConfig(func() error {
			var err error
			doc, err = a.store.SelectOrCreateConfig(req.FileName, req.DisplayName)
			return err
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		a.bumpState()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "config": sanitizeDocForBrowser(doc)})

	case "/api/configs/import-json":
		var req struct {
			FileName string          `json:"fileName"`
			Content  json.RawMessage `json:"content"`
			Activate bool            `json:"activate"`
		}
		if !readConfigPOST(w, r, &req) {
			return
		}
		var savedFile string
		var err error
		save := func() error {
			var saveErr error
			savedFile, saveErr = a.store.ImportConfigDocument(req.FileName, req.Content, req.Activate)
			return saveErr
		}
		if req.Activate {
			err = a.runtime.switchConfig(save)
		} else {
			err = save()
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		a.bumpState()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "fileName": savedFile})

	case "/api/configs/export-json":
		activeFile, doc := a.store.Snapshot()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, activeFile))
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(sanitizeDocForBrowser(doc))

	case "/api/configs/delete":
		var req struct {
			FileName string `json:"fileName"`
		}
		if !readConfigPOST(w, r, &req) {
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
		w, ok := a.beginLocalConfigChange(w, r, &req)
		if !ok {
			return
		}
		defer w.release()
		srv, cli, err := ParseTOMLAsProfile(req.Name, req.Kind, req.TOML)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		var createdID string
		err = a.store.Update(func(doc *ConfigDocument) error {
			if srv != nil {
				doc.Servers = append(doc.Servers, *srv)
				createdID = srv.ID
			}
			if cli != nil {
				doc.Clients = append(doc.Clients, *cli)
				createdID = cli.ID
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
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "kind": kindOut, "id": createdID})

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
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
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
		if err := readJSONBody(r, &req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		coreInfo, restarted, err := a.updater.ApplyFRPUpdate(r.Context(), req.ProxyURL)
		if errors.Is(err, errFRPUpToDate) {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "upToDate": true, "core": coreInfo, "restarted": 0})
			return
		}
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
		if err := readJSONBody(r, &req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		lsSpec, oArgs := a.getRuntimeMeta()
		targetVer, err := a.updater.ApplyAppUpdate(r.Context(), req.ProxyURL, lsSpec, oArgs)
		if errors.Is(err, errAppUpToDate) {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "upToDate": true, "targetVersion": targetVer})
			return
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":            true,
			"restarting":    true,
			"targetVersion": targetVer,
		})

	// ==================== On-Demand GitHub HD Asset Pack Control ====================
	case "/api/assets/hd/status":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		writeJSON(w, http.StatusOK, a.assets.Status())

	case "/api/assets/hd/pull":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			ProxyURL *string `json:"proxyURL"`
			Ref      string  `json:"ref"`
			PackID   string  `json:"packId"`
		}
		if err := readJSONBody(r, &req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		st, err := a.assets.StartPull(req.ProxyURL, req.Ref, req.PackID)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		a.bumpState()
		writeJSON(w, http.StatusOK, st)

	case "/api/assets/hd/cancel":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		st := a.assets.CancelPull()
		a.bumpState()
		writeJSON(w, http.StatusOK, st)

	case "/api/assets/hd/mode":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			PreferHD bool `json:"preferHD"`
		}
		if err := readJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		st, err := a.assets.SetPreferHD(req.PreferHD)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		a.bumpState()
		writeJSON(w, http.StatusOK, st)

	case "/api/assets/hd/purge":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		st, err := a.assets.Purge()
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		a.bumpState()
		writeJSON(w, http.StatusOK, st)

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

func setSessionCookie(w http.ResponseWriter, r *http.Request, sid string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    sid,
		Path:     "/",
		HttpOnly: true,
		Secure:   r != nil && r.TLS != nil,
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

// sameOriginRequest blocks Cross-Site Request Forgery even when Origin is stripped by no-referrer policies or HTML forms (A02).
func sameOriginRequest(r *http.Request) bool {
	if sfs := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site"))); sfs == "cross-site" {
		return false
	}
	ct := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if strings.HasPrefix(ct, "application/x-www-form-urlencoded") || strings.HasPrefix(ct, "multipart/form-data") || strings.HasPrefix(ct, "text/plain") {
		return false
	}
	if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
		if origin == "null" {
			return false
		}
		u, err := url.Parse(origin)
		if err != nil || !strings.EqualFold(u.Host, r.Host) {
			return false
		}
		return true
	}
	if ref := strings.TrimSpace(r.Header.Get("Referer")); ref != "" {
		u, err := url.Parse(ref)
		if err != nil || !strings.EqualFold(u.Host, r.Host) {
			return false
		}
		return true
	}
	if r.Header.Get("X-Kurumi-Requested-With") != "" || strings.HasPrefix(ct, "application/json") || r.ContentLength == 0 {
		return true
	}
	return false
}

func readJSONBody(r *http.Request, dst any) error {
	return decodeJSONBody(r, dst, maxConfigBytes)
}

func decodeJSONBody(r *http.Request, dst any, limit int64) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, limit))
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("解析请求 JSON 失败: %w", err)
	}
	if err := dec.Decode(new(json.RawMessage)); err != io.EOF {
		return errors.New("JSON 请求包含多余内容或超出大小限制")
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
