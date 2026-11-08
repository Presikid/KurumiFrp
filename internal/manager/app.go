package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Presikid/KurumiFrp/internal/core"
	"github.com/Presikid/KurumiFrp/internal/update"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type App struct {
	quit     chan struct{}
	quitOnce sync.Once
	core     *core.Manager
	updater  *update.Manager
	mu       sync.Mutex
	store    *Store
	bin      string
	addr     string
	auth     auth
	procs    map[string]*process
	assets   http.Handler

	// Lifecycle reservations are protected by mu. Slow operations run without it.
	operations    map[string]*runtimeOperation
	operationWG   sync.WaitGroup
	closing       bool
	closeOnce     sync.Once
	closeErr      error
	stateCache    *stateResponseCache
	activeProbes  int
	startupDone   chan struct{}
	startupCancel context.CancelFunc
	startupErrors map[string]string
}

func New(dir, bin, addr string, assets fs.FS) (*App, error) {
	if e := loopbackListen(addr); e != nil {
		return nil, e
	}
	s, e := newStore(dir)
	if e != nil {
		return nil, e
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
	bin, e = filepath.Abs(bin)
	if e != nil {
		return nil, e
	}
	cores, e := core.New(bin)
	if e != nil {
		return nil, e
	}
	if s.DB.ActiveCore == nil {
		status, err := cores.Status()
		if err != nil {
			return nil, err
		}
		id := status.Active
		db := s.DB
		db.ActiveCore = &id
		if err = s.write(db); err != nil {
			return nil, err
		}
		s.DB = db
		if err = cores.RemoveLegacyActive(); err != nil {
			return nil, err
		}
	}
	if e = cores.UseConfigActive(*s.DB.ActiveCore, func(id string) error {
		db := s.DB
		db.ActiveCore = &id
		db.Revision++
		if err := s.write(db); err != nil {
			return err
		}
		s.DB = db
		return nil
	}); e != nil {
		return nil, e
	}
	app := &App{quit: make(chan struct{}), core: cores, updater: update.New(s.dir, "source"), store: s, bin: bin, addr: addr, auth: auth{token: string(token), attempts: map[string]attempt{}, sessions: map[string]time.Time{}}, procs: map[string]*process{}, assets: newEmbeddedAssetHandler(assets), startupDone: make(chan struct{}), startupErrors: make(map[string]string)}
	ctx, cancel := context.WithCancel(context.Background())
	app.startupCancel = cancel
	instances := append([]Instance(nil), s.DB.State.Instances...)
	go app.restoreAutoStart(ctx, instances)
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
			Token string `json:"token"`
		}
		if e := decode(w, r, &input); e != nil {
			fail(w, 400, e)
			return
		}
		sid, e := a.auth.login(r, input.Token)
		if e != nil {
			fail(w, 401, e)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "kurumi_session", Value: sid, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 43200})
		reply(w, 200, map[string]bool{"ok": true})
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		if !a.auth.valid(r) {
			fail(w, 401, fmt.Errorf("请先登录"))
			return
		}
		if r.URL.Path == "/api/quit" && r.Method == "POST" {
			a.quitOnce.Do(func() { close(a.quit) })
			reply(w, 200, map[string]bool{"ok": true})
			return
		}
		if r.URL.Path == "/api/browser-ticket" && r.Method == "POST" {
			reply(w, 200, map[string]string{"url": a.BrowserURL()})
			return
		}
		if r.URL.Path == "/api/logout" && r.Method == "POST" {
			a.auth.logout(r)
			http.SetCookie(w, &http.Cookie{Name: "kurumi_session", Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
			reply(w, 200, map[string]bool{"ok": true})
			return
		}
		if r.URL.Path == "/api/probe" && r.Method == "POST" {
			a.probeAPI(w, r)
			return
		}
		if r.URL.Path == "/api/core" || strings.HasPrefix(r.URL.Path, "/api/core/") {
			a.coreAPI(w, r)
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
		if r.URL.Path == "/api/state" && r.Method == "GET" {
			a.handleState(w, r)
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
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
			if p == nil || p.appliedConfig == "" {
				fail(w, 409, fmt.Errorf("该实例尚无可比较的已应用配置；请先成功启动一次"))
				return
			}
			current, err := a.store.render(id, false)
			if err != nil {
				fail(w, 400, err)
				return
			}
			coreID := ""
			if a.store.DB.ActiveCore != nil {
				coreID = *a.store.DB.ActiveCore
			}
			reply(w, 200, compareConfig(i.ID, i.Name, a.store.DB.Revision, p.appliedConfig, current.Text, p.appliedSecretDigest != current.SecretDigest, p.coreID != coreID))
			return
		case "/api/logs":
			lines := []string{}
			if p := a.procs[r.URL.Query().Get("id")]; p != nil {
				lines = p.logs.read()
			}
			reply(w, 200, map[string]any{"lines": lines})
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
	if r.URL.Path == "/api/import" {
		a.importConfig(w, r)
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
			if err = json.Unmarshal(req.Value, &v); err != nil {
				break
			}
			old, exists := st.Instance(v.ID)
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
			var v Tunnel
			if err = json.Unmarshal(req.Value, &v); err != nil {
				break
			}
			old, exists := st.Tunnel(v.ID)
			if v.ID == "" {
				v.ID = randomHex(8)
			} else if !exists {
				err = fmt.Errorf("规则不存在")
				break
			}
			v.HasSecret = false
			if v.Secret == "" {
				v.Secret = old.Secret
			} else {
				v.Secret, err = a.store.encrypt(v.Secret)
				if err != nil {
					break
				}
			}
			if v.Type != "stcp" && v.Type != "stcp-visitor" {
				v.Secret = ""
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
			if p := a.procs[req.ID]; p != nil && p.view().State == "running" {
				err = fmt.Errorf("请先停止实例")
				break
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
			err = a.store.commit(st, "删除 "+req.Kind+" 草稿")
		}
	case "/api/restore":
		for _, p := range a.procs {
			if p.view().State == "running" {
				err = fmt.Errorf("恢复历史前请停止所有托管实例")
				break
			}
		}
		if err != nil {
			break
		}
		err = fmt.Errorf("历史版本不存在")
		for _, h := range a.store.DB.History {
			if h.Revision == req.Target {
				err = a.store.commit(h.State.clone(), fmt.Sprintf("恢复草稿版本 %d", h.Revision))
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
	reply(w, 200, map[string]any{"ok": true, "revision": a.store.DB.Revision})
}

func (a *App) Done() <-chan struct{} { return a.quit }
