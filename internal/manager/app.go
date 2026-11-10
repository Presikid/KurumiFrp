package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Presikid/KurumiFrp/internal/update"
)

type App struct {
	quit     chan struct{}
	quitOnce sync.Once
	runtime  RuntimeBackend
	updater  *update.Manager
	mu       sync.Mutex
	// Take the appropriate node gate before mu; never hold both node gates.
	nodeAccessGate   sync.RWMutex
	nodeControlGate  sync.RWMutex
	nodeControlEpoch string
	// Protected by mu; changes when a valid instance operation is attempted.
	nodeOperationVersions map[string]string

	store            *Store
	addr             string
	auth             auth
	procs            map[string]*process
	traffic          map[string]*trafficSamples // protected by mu, keyed by instance and run ID
	trafficDaily     *trafficDailyHistory
	assets           http.Handler
	webhooks         *webhookDispatcher
	loginHistory     *loginHistory
	firewall         *firewallManager
	updateNotices    updateNoticeState

	// Lifecycle reservations are protected by mu. Slow operations run without it.
	operations          map[string]*runtimeOperation
	operationWG         sync.WaitGroup
	closing             bool
	closeOnce           sync.Once
	closeErr            error
	stateCache          *stateResponseCache
	activeProbes        int
	activeNodeChecks    int
	nodeMonitors        map[string]nodeMonitorObservation
	activeBackup        bool
	activeRegistration  bool
	activeHealth        bool
	startupDone         chan struct{}
	startupCancel       context.CancelFunc
	startupErrors       map[string]string
	autoStartSuppressed map[string]bool
	scheduleCancel      context.CancelFunc
	scheduleDone        chan struct{}
	firewallDone        chan struct{}
	nodeMonitorDone     chan struct{}
	scheduleBlocks      map[string]string
	generations         map[string]uint64
	logStreams          int
	logStreamsByID      map[string]int
}

func New(dir, bin, addr string, assets fs.FS) (*App, error) {
	return NewWithRuntime(dir, bin, addr, assets, nil)
}

// NewWithRuntime installs the backend before restoring AutoStart instances.
// A nil backend selects the in-process runtime used by normal builds.
func NewWithRuntime(dir, bin, addr string, assets fs.FS, backend RuntimeBackend) (*App, error) {
	if backend == nil {
		backend = NewEmbeddedRuntime()
	}
	if e := loopbackListen(addr); e != nil {
		return nil, e
	}
	s, e := newStore(dir)
	if e != nil {
		return nil, e
	}
	// A damaged account file disables accounts and account mutations, but the
	// existing local administrator must still be able to start the workbench.
	accounts, accountsRevision, accountLoadErr := loadAccounts(s.dir)
	if accountLoadErr == nil {
		for _, account := range accounts {
			for _, id := range account.Instances {
				if _, exists := s.DB.State.Instance(id); !exists {
					accountLoadErr = fmt.Errorf("账户授权引用了不存在的实例")
					accounts, accountsRevision = nil, 0
					break
				}
			}
			if accountLoadErr != nil {
				break
			}
		}
	}
	tokenPath := filepath.Join(s.dir, "access.token")
	token, e := privateFile(tokenPath)
	if os.IsNotExist(e) {
		token = []byte(randomHex(32))
		e = atomicWrite(tokenPath, token)
	}
	if e != nil {
		return nil, e
	}
	if len(token) != 64 {
		return nil, fmt.Errorf("invalid access token file")
	}
	app := &App{nodeControlEpoch: randomHex(16), quit: make(chan struct{}), runtime: backend, updater: update.New(s.dir, "source"), store: s, addr: addr, auth: auth{token: string(token), attempts: map[string]attempt{}, sessions: map[string]authSession{}, accounts: accounts, accountsRevision: accountsRevision, accountLoadErr: accountLoadErr, accountPath: filepath.Join(s.dir, "accounts.json"), hashSlots: make(chan struct{}, 2)}, procs: map[string]*process{}, assets: newEmbeddedAssetHandler(assets), startupDone: make(chan struct{}), startupErrors: make(map[string]string)}
	if e = app.recoverSchedules(); e != nil {
		return nil, e
	}
	app.trafficDaily = loadTrafficDaily(s.dir, s.DB.State, s.DB.LocalTrafficIDs)
	app.firewall = loadFirewall(s.dir)
	s.beforeCommit = func(before, after State) error {
		for id, op := range app.operations {
			if op.scheduled && scheduleDraftChanged(before, after, id) {
				return fmt.Errorf("定时操作正在执行，请等待结束后修改该实例配置")
			}
		}
		return nil
	}
	app.webhooks = newWebhookDispatcher()
	app.loginHistory = loadLoginHistory(s.dir)
	app.auth.onLoginLimit = app.webhooks.emitLoginLimit
	app.configureWebhooks()
	s.onCommit = app.webhooks.emitConfig
	ctx, cancel := context.WithCancel(context.Background())
	app.startupCancel = cancel
	instances := append([]Instance(nil), s.DB.State.Instances...)
	go app.restoreAutoStart(ctx, instances)
	scheduleCtx, scheduleCancel := context.WithCancel(context.Background())
	app.scheduleCancel = scheduleCancel
	app.scheduleDone = make(chan struct{})
	go app.scheduleLoop(scheduleCtx)
	app.firewallDone = make(chan struct{})
	go app.firewallRefreshLoop(scheduleCtx)
	app.nodeMonitorDone = make(chan struct{})
	go app.nodeMonitorLoop(scheduleCtx)
	go app.runTrafficDaily()
	return app, nil
}
func (a *App) SetDistribution(distribution string) {
	a.updater = update.New(a.store.dir, update.DetectDistribution(distribution))
}
func (a *App) AccessToken() string { return a.auth.token }
func HTTPServer(addr string, h http.Handler) *http.Server {
	return &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 45 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
}
func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, e error) {
	reply(w, status, map[string]any{"error": e.Error()})
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		return fmt.Errorf("需要 application/json")
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return fmt.Errorf("请求 JSON 无效：%w", e)
	}
	if e := d.Decode(&struct{}{}); e != io.EOF {
		return fmt.Errorf("请求只能包含一个 JSON 对象")
	}
	return nil
}
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; object-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	_, port, _ := net.SplitHostPort(a.addr)
	if r.Host != a.addr && r.Host != "localhost:"+port {
		fail(w, 403, fmt.Errorf("Host 不允许；请使用本机或 SSH 转发地址"))
		return
	}
	if !sameOrigin(r) {
		fail(w, 403, fmt.Errorf("跨站请求被拒绝"))
		return
	}
	if r.URL.Path == "/api/launch-login" && r.Method == "POST" {
		a.launchLogin(w, r)
		return
	}
	if r.URL.Path == "/api/login" && r.Method == "POST" {
		var input struct {
			Token    string `json:"token"`
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if e := decode(w, r, &input); e != nil {
			fail(w, 400, e)
			return
		}
		sid, e := a.auth.login(r, input.Token, input.Username, input.Password)
		a.loginHistory.record(r, a.loginAccountLabel(input.Username), e == nil)
		if e != nil {
			fail(w, 401, e)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "kurumi_session", Value: sid, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 43200})
		reply(w, 200, map[string]bool{"ok": true})
		return
	}
	// The remote summary has its own narrow, read-only credential.
	if r.URL.Path == "/api/node/summary" {
		a.nodeSummaryAPI(w, r)
		return
	}
	if r.URL.Path == "/api/node/control" {
		a.nodeControlAPI(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		var p principal
		var ok bool
		var unlock func()
		// Account mutations take the target's exclusive gate themselves after
		// expensive password work. Streams acquire it separately per frame.
		ungated := (r.Method == http.MethodPost && (r.URL.Path == "/api/accounts" || r.URL.Path == "/api/password" || r.URL.Path == "/api/logout")) || (r.Method == http.MethodGet && r.URL.Path == "/api/logs/stream")
		if ungated {
			p, ok = a.auth.identity(r)
		} else {
			p, unlock, ok = a.auth.lockRequest(r)
		}
		if !ok {
			fail(w, 401, fmt.Errorf("请先登录"))
			return
		}
		if unlock != nil {
			defer unlock()
		}
		if !routeAllowed(p, r) {
			fail(w, 403, fmt.Errorf("没有此操作的权限"))
			return
		}
		r = withPrincipal(r, p)
		if r.URL.Path == "/api/firewall" {
			a.firewallAPI(w, r)
			return
		}
		if r.URL.Path == "/api/firewall/seen" {
			a.firewallSeenAPI(w, r)
			return
		}
		if r.URL.Path == "/api/firewall/asn" {
			a.firewallASNAPI(w, r)
			return
		}
		if r.URL.Path == "/api/firewall/ip-asn" {
			a.firewallIPASNAPI(w, r)
			return
		}
		if r.URL.Path == "/api/firewall/import" {
			a.firewallImportAPI(w, r)
			return
		}
		if r.URL.Path == "/api/firewall/list" {
			a.firewallListAPI(w, r)
			return
		}
		if r.URL.Path == "/api/accounts" {
			a.accountsAPI(w, r)
			return
		}
		if r.URL.Path == "/api/login-history" {
			a.loginHistoryAPI(w, r)
			return
		}
		if r.URL.Path == "/api/password" {
			a.passwordAPI(w, r)
			return
		}
		if r.URL.Path == "/api/quit" && r.Method == "POST" {
			a.RequestShutdown()
			reply(w, 200, map[string]bool{"ok": true})
			return
		}
		if r.URL.Path == "/api/browser-ticket" && r.Method == "POST" {
			if !a.auth.localToken(r) {
				fail(w, http.StatusForbidden, fmt.Errorf("仅本机管理令牌可签发启动凭据"))
				return
			}
			reply(w, 200, map[string]string{"url": a.BrowserURL()})
			return
		}
		if r.URL.Path == "/api/logout" && r.Method == "POST" {
			// A Bearer credential may accompany a different browser cookie.
			// Drain the cookie's session, not the Bearer principal's gate.
			cookieRequest := r.Clone(r.Context())
			cookieRequest.Header = r.Header.Clone()
			cookieRequest.Header.Del("Authorization")
			cookiePrincipal, cookieValid := a.auth.identity(cookieRequest)
			if cookieValid && cookiePrincipal.gate != nil {
				cookiePrincipal.gate.Lock()
				defer cookiePrincipal.gate.Unlock()
				current, valid := a.auth.identity(cookieRequest)
				if !valid || current.gate != cookiePrincipal.gate {
					fail(w, 401, fmt.Errorf("请先登录"))
					return
				}
			}
			a.auth.logout(r)
			http.SetCookie(w, &http.Cookie{Name: "kurumi_session", Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
			reply(w, 200, map[string]bool{"ok": true})
			return
		}
		if r.URL.Path == "/api/probe" && r.Method == "POST" {
			a.probeAPI(w, r)
			return
		}
		if r.URL.Path == "/api/registration" {
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			a.registrationAPI(w, r)
			return
		}
		if r.URL.Path == "/api/health" {
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			a.healthAPI(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/update/") {
			a.updateAPI(w, r)
			return
		}
		if r.URL.Path == "/api/control" || r.URL.Path == "/api/validate" {
			a.runtimeAPI(w, r)
			return
		}
		if r.URL.Path == "/api/schedules" {
			a.scheduleAPI(w, r)
			return
		}
		if r.URL.Path == "/api/tunnels/export" {
			a.batchTunnelExportAPI(w, r)
			return
		}
		if r.URL.Path == "/api/nodes" || r.URL.Path == "/api/nodes/check" || r.URL.Path == "/api/nodes/control" || r.URL.Path == "/api/nodes/resolve" || r.URL.Path == "/api/nodes/confirm" {
			a.nodesAPI(w, r)
			return
		}
		if r.URL.Path == "/api/webhook" {
			a.webhookAPI(w, r)
			return
		}
		if r.URL.Path == "/api/webhooks" {
			a.webhookTargetsAPI(w, r)
			return
		}
		if r.URL.Path == "/api/backup/export" || r.URL.Path == "/api/backup/import" {
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if r.URL.Path == "/api/backup/export" {
				a.backupExport(w, r)
			} else {
				a.backupImport(w, r)
			}
			return
		}
		if r.URL.Path == "/api/traffic-history" {
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			a.trafficDailyAPI(w, r)
			return
		}
		if r.URL.Path == "/api/server-traffic" {
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			a.serverTrafficAPI(w, r)
			return
		}
		if r.URL.Path == "/api/client-traffic" {
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			a.clientRuleTrafficAPI(w, r)
			return
		}
		if r.URL.Path == "/api/traffic" {
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			a.trafficAPI(w, r)
			return
		}
		if r.URL.Path == "/api/state" && r.Method == "GET" {
			a.handleState(w, r)
			return
		}
		if r.URL.Path == "/api/logs" || r.URL.Path == "/api/logs/stream" {
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if r.URL.Path == "/api/logs" {
				a.pollLogs(w, r)
			} else {
				a.streamLogs(w, r)
			}
			return
		}
		if r.URL.Path == "/api/import" {
			a.importConfigAPI(w, r)
			return
		}
		// Do not create IDs while an unreadable account file could contain old
		// grants for them. Instance replacement and deletion have the same
		// protection in lockStateReplacement.
		if r.URL.Path == "/api/save" || r.URL.Path == "/api/duplicate" || r.URL.Path == "/api/import/toml" {
			a.auth.mu.Lock()
			unavailable := a.auth.accountLoadErr != nil
			a.auth.mu.Unlock()
			if unavailable {
				fail(w, http.StatusServiceUnavailable, fmt.Errorf("账户文件无法读取，修复前不能新增或修改实例"))
				return
			}
		}
		if r.URL.Path == "/api/delete" || r.URL.Path == "/api/restore" {
			buffered, err := bufferMutationBody(w, r, 1<<20)
			if err != nil {
				fail(w, 400, err)
				return
			}
			r = buffered
			release, err := a.lockStateReplacement()
			if err != nil {
				fail(w, 409, err)
				return
			}
			defer release()
		} else {
			a.mu.Lock()
			defer a.mu.Unlock()
		}
		a.api(w, r)
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		w.WriteHeader(405)
		return
	}
	a.assets.ServeHTTP(w, r)
}
func (a *App) api(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		if (r.URL.Path == "/api/config" || r.URL.Path == "/api/diff") && !requestPrincipal(r).canView(r.URL.Query().Get("id")) {
			fail(w, http.StatusForbidden, fmt.Errorf("没有此实例的查看权限"))
			return
		}
		switch r.URL.Path {
		case "/api/export":
			a.export(w, r)
			return
		case "/api/config":
			v, e := a.store.render(r.URL.Query().Get("id"), true)
			if e != nil {
				fail(w, 404, e)
				return
			}
			reply(w, 200, map[string]any{"toml": v.Text, "redacted": true})
			return
		case "/api/diff":
			id := r.URL.Query().Get("id")
			i, ok := a.store.DB.State.Instance(id)
			if !ok {
				fail(w, 404, fmt.Errorf("实例不存在"))
				return
			}
			p := a.procs[id]
			if p == nil || p.appliedConfig == "" || p.view().Applied == "" {
				fail(w, 409, fmt.Errorf("该实例尚无可比较的已应用配置；请先成功启动一次"))
				return
			}
			current, err := a.store.render(id, false)
			if err != nil {
				fail(w, 400, err)
				return
			}
			reply(w, 200, compareConfig(i.ID, i.Name, a.store.DB.Revision, p.appliedConfig, current.Text, p.appliedSecretDigest != current.SecretDigest))
			return
		default:
			fail(w, 404, fmt.Errorf("接口不存在"))
			return
		}
	}
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	if r.URL.Path == "/api/import/toml" || r.URL.Path == "/api/import/toml/preview" {
		a.tomlImportAPI(w, r)
		return
	}
	if r.URL.Path == "/api/duplicate" {
		a.duplicateAPI(w, r)
		return
	}
	if r.URL.Path == "/api/tunnels/batch-enabled" {
		a.batchTunnelEnabledAPI(w, r)
		return
	}
	if r.URL.Path == "/api/tunnels/batch-delete" {
		a.batchTunnelDeleteAPI(w, r)
		return
	}
	if r.URL.Path == "/api/tunnels/batch" {
		a.createTunnelBatchAPI(w, r)
		return
	}
	var req struct {
		Revision     uint64          `json:"revision"`
		Kind         string          `json:"kind"`
		ID           string          `json:"id"`
		Action       string          `json:"action"`
		Target       uint64          `json:"target"`
		HistoryLimit *int            `json:"historyLimit"`
		Value        json.RawMessage `json:"value"`
	}
	if e := decode(w, r, &req); e != nil {
		fail(w, 400, e)
		return
	}
	if req.Revision != a.store.DB.Revision {
		fail(w, 409, fmt.Errorf("配置已在其他窗口更新，请刷新后重试"))
		return
	}
	var err error
	switch r.URL.Path {
	case "/api/history-settings":
		if req.HistoryLimit == nil {
			err = fmt.Errorf("请指定快照保留数量")
		} else {
			err = a.store.setHistoryLimit(*req.HistoryLimit)
		}
	case "/api/save":
		st := a.store.DB.State.clone()
		switch req.Kind {
		case "instance":
			var v Instance
			if err = decodeStrictJSON(req.Value, &v); err != nil {
				break
			}
			old, exists := st.Instance(v.ID)
			if exists {
				// Older editors omit newer protocol fields. Preserve omitted values
				// while treating an omitted or empty credential as "keep".
				v = old
				v.Token = ""
				if err = json.Unmarshal(req.Value, &v); err != nil {
					break
				}
			}
			if v.ID == "" {
				v.ID = randomHex(8)
			} else if !exists {
				err = fmt.Errorf("实例不存在")
				break
			}
			if exists && old.Kind != v.Kind {
				err = fmt.Errorf("已有实例不能改变类型")
				break
			}
			v.HasToken = false
			if v.Token == "" {
				v.Token = old.Token
			} else {
				v.Token, err = a.store.encrypt(v.Token)
				if err != nil {
					break
				}
			}
			if exists {
				for j := range st.Instances {
					if st.Instances[j].ID == v.ID {
						st.Instances[j] = v
					}
				}
			} else {
				st.Instances = append(st.Instances, v)
			}
		case "tunnel":
			var edit struct {
				Tunnel
				ClearHTTPPassword bool            `json:"clearHTTPPassword,omitempty"`
				RequestHeaders    json.RawMessage `json:"requestHeaders"`
				ResponseHeaders   json.RawMessage `json:"responseHeaders"`
			}
			if err = decodeStrictJSON(req.Value, &edit); err != nil {
				break
			}
			v := edit.Tunnel
			old, exists := st.Tunnel(v.ID)
			if exists {
				v = old
				v.Secret = ""
				v.HTTPPassword = ""
				// Do not let json.Unmarshal reuse and mutate the old header slices.
				v.RequestHeaders, v.ResponseHeaders = nil, nil
				if err = json.Unmarshal(req.Value, &v); err != nil {
					break
				}
			}
			if v.Type == "tcp" || v.Type == "udp" {
				var fields struct {
					RemotePort json.RawMessage `json:"remotePort"`
				}
				if err = json.Unmarshal(req.Value, &fields); err != nil {
					break
				}
				present := len(fields.RemotePort) != 0
				if (present && strings.TrimSpace(string(fields.RemotePort)) == "null") || (!present && (!exists || old.Type != v.Type)) {
					err = fmt.Errorf("请指定远端端口；填 0 可请求动态分配")
					break
				}
			}
			if v.ID == "" {
				v.ID = randomHex(8)
			} else if !exists {
				err = fmt.Errorf("规则不存在")
				break
			}
			if exists && ((!legacyEditorProtocol(old.Type) && v.Type != old.Type) ||
				(old.Type == "stcp-visitor" && old.LocalPort < 0 && (v.Type != old.Type || v.LocalPort != old.LocalPort))) {
				err = fmt.Errorf("当前暂不支持更改此规则的协议类型或回退监听端口")
				break
			}
			if exists {
				if err = prepareVisitorTransfer(st, old, &v); err != nil {
					break
				}
			}
			v.RequestHeaders, err = a.store.mergeHTTPHeaders(edit.RequestHeaders, old.RequestHeaders)
			if err != nil {
				break
			}
			v.ResponseHeaders, err = a.store.mergeHTTPHeaders(edit.ResponseHeaders, old.ResponseHeaders)
			if err != nil {
				break
			}
			v.HasSecret = false
			v.HasHTTPPassword = false
			if !usesPairingSecret(v.Type) {
				// A previously imported state may contain an unused encrypted
				// value. Preserve it on same-type edits until explicitly changed.
				if exists && old.Type == v.Type {
					v.Secret = old.Secret
				} else {
					v.Secret = ""
				}
			} else if v.Secret == "" {
				v.Secret = old.Secret
			} else {
				v.Secret, err = a.store.encrypt(v.Secret)
				if err != nil {
					break
				}
			}
			if edit.ClearHTTPPassword && v.HTTPPassword != "" {
				err = fmt.Errorf("不能同时设置和清除 HTTP 密码")
				break
			}
			if exists && old.Type != v.Type && old.HTTPPassword != "" && !edit.ClearHTTPPassword {
				err = fmt.Errorf("更改协议前请确认清除已保存的 HTTP 密码")
				break
			}
			if edit.ClearHTTPPassword {
				v.HTTPPassword = ""
			} else if v.Type != "tcpmux" && v.Type != "http" {
				if exists && old.Type == v.Type {
					v.HTTPPassword = old.HTTPPassword
				} else {
					v.HTTPPassword = ""
				}
			} else if v.HTTPPassword != "" {
				v.HTTPPassword, err = a.store.encrypt(v.HTTPPassword)
				if err != nil {
					break
				}
			} else if exists && old.Type == v.Type {
				v.HTTPPassword = old.HTTPPassword
			}
			if exists {
				for j := range st.Tunnels {
					if st.Tunnels[j].ID == v.ID {
						st.Tunnels[j] = v
					}
				}
			} else {
				st.Tunnels = append(st.Tunnels, v)
			}
		default:
			err = fmt.Errorf("未知对象")
		}
		if err == nil {
			err = a.store.commit(st, "保存 "+req.Kind+" 草稿")
		}
	case "/api/delete":
		st := a.store.DB.State.clone()
		found := false
		if req.Kind == "instance" {
			if a.closing || a.operations[req.ID] != nil {
				err = fmt.Errorf("实例操作尚未结束，请稍后重试")
				break
			}
			if p := a.procs[req.ID]; p != nil {
				select {
				case <-p.done:
				default:
					err = fmt.Errorf("请先停止实例并等待其完全退出")
				}
				if err != nil {
					break
				}
			}
			for _, t := range st.Tunnels {
				if t.InstanceID == req.ID {
					err = fmt.Errorf("请先删除该实例下的规则")
					break
				}
			}
			if err != nil {
				break
			}
			for j, i := range st.Instances {
				if i.ID == req.ID {
					st.Instances = append(st.Instances[:j], st.Instances[j+1:]...)
					found = true
					break
				}
			}
		} else if req.Kind == "tunnel" {
			for j, t := range st.Tunnels {
				if t.ID == req.ID {
					st.Tunnels = append(st.Tunnels[:j], st.Tunnels[j+1:]...)
					found = true
					break
				}
			}
		}
		if !found {
			err = fmt.Errorf("对象不存在")
		} else {
			if req.Kind == "instance" {
				if err = a.clearInstanceGrantsLocked(req.ID); err != nil {
					break
				}
			}
			err = a.store.commit(st, "删除 "+req.Kind+" 草稿")
			if err == nil && req.Kind == "instance" {
				delete(a.procs, req.ID)
				delete(a.traffic, req.ID)
				delete(a.startupErrors, req.ID)
				delete(a.autoStartSuppressed, req.ID)
				delete(a.generations, req.ID)
				delete(a.nodeOperationVersions, req.ID)
				delete(a.scheduleBlocks, req.ID)
				a.stateCache = nil
			}
		}
	case "/api/restore":
		if err = a.canReplaceStateLocked(); err != nil {
			break
		}
		err = fmt.Errorf("历史版本不存在")
		for _, h := range a.store.DB.History {
			if h.Revision == req.Target {
				if err = a.clearInstanceGrantsLocked(""); err != nil {
					break
				}
				err = a.store.commitRestoring(h.State.clone(), fmt.Sprintf("恢复草稿版本 %d", h.Revision), true)
				if err == nil {
					a.discardStoppedRuntimeLocked()
				}
				break
			}
		}

	default:
		fail(w, 404, fmt.Errorf("接口不存在"))
		return
	}
	if err != nil {
		fail(w, 400, err)
		return
	}
	// A removed instance, or one whose auto-start was disabled, no longer has
	// an applicable automatic-restoration failure.
	for id := range a.startupErrors {
		if i, exists := a.store.DB.State.Instance(id); !exists || !i.AutoStart {
			delete(a.startupErrors, id)
		}
	}
	reply(w, 200, map[string]any{"ok": true, "revision": a.store.DB.Revision})
}

func (a *App) Done() <-chan struct{} { return a.quit }

// RequestShutdown wakes long-lived log streams before HTTP shutdown waits.
func (a *App) RequestShutdown() { a.quitOnce.Do(func() { close(a.quit) }) }
