package manager

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Presikid/KurumiFrp/internal/childprocess"
)

// remoteClientSession tracks a live KurumiFrp client (frpc) instance connected to a local frps server.
type remoteClientSession struct {
	serverID       string
	clientID       string
	clientName     string
	frpUser        string
	hostname       string
	osName         string
	arch           string
	appVersion     string
	frpVersion     string
	remoteAddr     string
	runID          string
	ctrlProxyName  string
	ctrlSecret     string
	agentToken     string
	directAgentURL string // optional test/same-host loopback override
	connectedAt    time.Time
	lastPingAt     time.Time
	ctrlReady      bool
	online         bool
}

// visitorBridge maintains an ephemeral loopback frpc visitor process tunneling to a remote frpc's __kurumi_ctrl_<id> STCP endpoint.
type visitorBridge struct {
	key        string
	bindPort   int
	ctrlProxy  string
	ctrlSecret string
	cmd        *exec.Cmd
	done       chan struct{}
	cfgPath    string
	lastUsed   time.Time
}

// RemoteClientSnapshot is returned when an frps instance inspects one of its connected frpc instances.
type RemoteClientSnapshot struct {
	Summary RemoteClientSummary `json:"summary"`
	Profile ClientProfile       `json:"profile"`
	Runtime ProcessRuntimeView  `json:"runtime"`
	TOML    string              `json:"toml"`
	Logs    []string            `json:"logs"`
}

// P2PPairRequest orchestrates an XTCP (or STCP/SUDP) P2P pairing across connected/local frpc instances via an frps hub.
type P2PPairRequest struct {
	ServerID           string `json:"serverId"`
	TunnelBaseName     string `json:"tunnelBaseName"`
	P2PType            string `json:"p2pType"` // "xtcp" (default), "stcp", "sudp"
	SecretKey          string `json:"secretKey"`
	HostClientID       string `json:"hostClientId"`       // e.g., Client C (exposes local service)
	HostLocalIP        string `json:"hostLocalIP"`        // default 127.0.0.1
	HostLocalPort      int    `json:"hostLocalPort"`      // e.g., 3389 / 25565 / 7777
	VisitorClientID    string `json:"visitorClientId"`    // optional: e.g., Client E (if also controlled via Server A; empty if E configures locally)
	VisitorBindIP      string `json:"visitorBindIP"`      // default 127.0.0.1
	VisitorBindPort    int    `json:"visitorBindPort"`    // e.g., 17777
	KeepTunnelOpen     bool   `json:"keepTunnelOpen"`     // keep XTCP hole-punched link warm
	EnableSTCPFallback bool   `json:"enableStcpFallback"` // auto-create companion STCP fallback tunnel + fallbackTo
	Overwrite          bool   `json:"overwrite,omitempty"`
}

func (rs *RuntimeSupervisor) startLoopbackAgentServer() error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	rs.agentLn = ln
	rs.agentPort = ln.Addr().(*net.TCPAddr).Port

	mux := http.NewServeMux()
	mux.HandleFunc("/internal/client-agent/", rs.handleClientAgentHTTP)
	// A05: Serve frps HTTP plugin webhooks on the persistent loopback agent port so WebUI port changes never interrupt active frps instances!
	mux.HandleFunc("/internal/frps-plugin/", func(w http.ResponseWriter, r *http.Request) {
		srvID := strings.TrimPrefix(r.URL.Path, "/internal/frps-plugin/")
		rs.HandleFRPSPlugin(w, r, srvID)
	})
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	rs.agentSrv = srv
	go func() {
		_ = srv.Serve(ln)
	}()
	return nil
}

func (rs *RuntimeSupervisor) stopLoopbackAgentAndBridges() {
	if rs.agentSrv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = rs.agentSrv.Shutdown(ctx)
		cancel()
	}
	if rs.agentLn != nil {
		_ = rs.agentLn.Close()
	}
	rs.bridgeMu.Lock()
	bridges := make([]*visitorBridge, 0, len(rs.bridges))
	for _, b := range rs.bridges {
		bridges = append(bridges, b)
	}
	rs.bridges = make(map[string]*visitorBridge)
	rs.bridgeMu.Unlock()

	for _, b := range bridges {
		terminateVisitorBridge(b)
	}
}

func terminateVisitorBridge(b *visitorBridge) {
	if b == nil || b.cmd == nil {
		return
	}
	_ = childprocess.Terminate(b.cmd)
	select {
	case <-b.done:
	case <-time.After(1500 * time.Millisecond):
		_ = childprocess.Kill(b.cmd)
	}
	if b.cfgPath != "" {
		_ = os.Remove(b.cfgPath)
	}
}

// handleClientAgentHTTP runs on the client host's 127.0.0.1:<agentPort> and strictly scopes operations
// to the single ClientProfile whose active frpc tunnel delivered the request.
func (rs *RuntimeSupervisor) handleClientAgentHTTP(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/internal/client-agent/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) < 2 || strings.TrimSpace(parts[0]) == "" {
		writeError(w, http.StatusNotFound, "invalid client agent path")
		return
	}
	clientID := strings.TrimSpace(parts[0])
	action := strings.TrimSpace(parts[1])
	gotTok := strings.TrimSpace(r.Header.Get("X-Kurumi-Agent-Token"))

	_, doc := rs.store.Snapshot()
	var target *ClientProfile
	for i := range doc.Clients {
		if doc.Clients[i].ID == clientID {
			target = &doc.Clients[i]
			break
		}
	}
	if target == nil {
		writeError(w, http.StatusNotFound, "远端未找到该客户端实例")
		return
	}
	if target.RemoteCtrlEnabled != nil && !*target.RemoteCtrlEnabled {
		writeError(w, http.StatusForbidden, "该客户端实例已在本地关闭服务端远控授权")
		return
	}
	if target.AgentToken == "" || subtle.ConstantTimeCompare([]byte(gotTok), []byte(target.AgentToken)) != 1 {
		writeError(w, http.StatusForbidden, "客户端连接凭证校验失败")
		return
	}

	switch action {
	case "snapshot":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		views := rs.Views(doc)
		lines, _ := rs.Logs(clientID)
		sanitized := *target
		sanitized.CtrlSecret = ""
		sanitized.AgentToken = ""
		rendered := RenderClientTOML(*target, ClientRuntimeInjections{Preview: true, MaskSecrets: false})
		writeJSON(w, http.StatusOK, map[string]any{
			"profile": sanitized,
			"runtime": views[clientID],
			"toml":    rendered.TOML,
			"logs":    lines,
		})

	case "save":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			Profile             ClientProfile `json:"profile"`
			AllowUpstreamChange bool          `json:"allowUpstreamChange"`
		}
		if err := readJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		var saved ClientProfile
		err := rs.store.Update(func(d *ConfigDocument) error {
			for i := range d.Clients {
				if d.Clients[i].ID == clientID {
					prev := d.Clients[i]
					upd := req.Profile
					upd.ID = prev.ID
					upd.RemoteCtrlEnabled = prev.RemoteCtrlEnabled
					upd.CtrlSecret = prev.CtrlSecret
					upd.AgentToken = prev.AgentToken
					if strings.TrimSpace(upd.Name) == "" {
						upd.Name = prev.Name
					}
					if !req.AllowUpstreamChange {
						upd.ServerAddr = prev.ServerAddr
						upd.ServerPort = prev.ServerPort
						upd.AuthMethod = prev.AuthMethod
						upd.AuthToken = prev.AuthToken
						upd.Protocol = prev.Protocol
						upd.TLSEnable = prev.TLSEnable
						upd.TLSServerName = prev.TLSServerName
					} else if upd.AuthToken == "********" {
						upd.AuthToken = prev.AuthToken
					}
					if upd.Tunnels == nil {
						upd.Tunnels = []TunnelRule{}
					}
					for j := range upd.Tunnels {
						if upd.Tunnels[j].ID == "" {
							upd.Tunnels[j].ID = newShortID("tun")
						}
					}
					d.Clients[i] = upd
					saved = upd
					return nil
				}
			}
			return errors.New("未找到客户端实例")
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := rs.ReloadOrRestartClient(saved); err != nil {
			rs.notifyChange()
			writeError(w, http.StatusBadRequest, "远端配置预检或重载失败: "+err.Error())
			return
		}
		rs.notifyChange()
		sanitized := saved
		sanitized.CtrlSecret = ""
		sanitized.AgentToken = ""
		rendered := RenderClientTOML(saved, ClientRuntimeInjections{Preview: true, MaskSecrets: false})
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"profile": sanitized,
			"toml":    rendered.TOML,
		})

	case "save-toml":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			TOML                string `json:"toml"`
			AllowUpstreamChange bool   `json:"allowUpstreamChange"`
		}
		if err := readJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		var saved ClientProfile
		err := rs.store.Update(func(d *ConfigDocument) error {
			for i := range d.Clients {
				if d.Clients[i].ID == clientID {
					prev := d.Clients[i]
					upd, parseErr := ApplyTOMLToClient(prev, req.TOML)
					if parseErr != nil {
						return parseErr
					}
					if !req.AllowUpstreamChange {
						upd.ServerAddr = prev.ServerAddr
						upd.ServerPort = prev.ServerPort
						upd.AuthMethod = prev.AuthMethod
						upd.AuthToken = prev.AuthToken
					}
					d.Clients[i] = upd
					saved = upd
					return nil
				}
			}
			return errors.New("未找到客户端实例")
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := rs.ReloadOrRestartClient(saved); err != nil {
			rs.notifyChange()
			writeError(w, http.StatusBadRequest, "远端配置预检或重载失败: "+err.Error())
			return
		}
		rs.notifyChange()
		sanitized := saved
		sanitized.CtrlSecret = ""
		sanitized.AgentToken = ""
		rendered := RenderClientTOML(saved, ClientRuntimeInjections{Preview: true, MaskSecrets: false})
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"profile": sanitized,
			"toml":    rendered.TOML,
		})

	case "reload":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if err := rs.ReloadOrRestartClient(*target); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	default:
		writeError(w, http.StatusNotFound, "unknown client agent action")
	}
}

// recordRemoteClientEvent ingests Login / Ping / NewProxy metadata from official frps HTTP Server Plugin callbacks.
func (rs *RuntimeSupervisor) recordRemoteClientEvent(serverID string, proc *managedProc, op string, content map[string]any) {
	if proc == nil || content == nil {
		return
	}
	var metas map[string]any
	frpUser := stringField(content, "user")
	runID := stringField(content, "run_id")
	remoteAddr := stringField(content, "client_address")
	frpVer := stringField(content, "version")
	osName := stringField(content, "os")
	arch := stringField(content, "arch")
	host := stringField(content, "hostname")

	if m, ok := content["metas"].(map[string]any); ok {
		metas = m
	}
	if uObj, ok := content["user"].(map[string]any); ok {
		if frpUser == "" {
			frpUser = stringField(uObj, "user")
		}
		if runID == "" {
			runID = stringField(uObj, "run_id")
		}
		if m, ok2 := uObj["metas"].(map[string]any); ok2 && metas == nil {
			metas = m
		}
	}

	clientID := stringField(metas, "kurumi_client_id")
	if clientID == "" {
		return
	}
	clientName := nonEmptyStr(stringField(metas, "kurumi_client_name"), clientID)
	rawCtrlProxy := nonEmptyStr(stringField(metas, "kurumi_ctrl_proxy"), internalCtrlProxyPrefix+clientID)
	qualifiedCtrl := rawCtrlProxy
	if frpUser != "" && !strings.HasPrefix(rawCtrlProxy, frpUser+".") {
		qualifiedCtrl = frpUser + "." + rawCtrlProxy
	}
	ctrlKey := stringField(metas, "kurumi_ctrl_key")
	agentTok := stringField(metas, "kurumi_agent_token")
	metaHost := stringField(metas, "kurumi_hostname")
	if host == "" {
		host = metaHost
	}
	appVer := stringField(metas, "kurumi_app_ver")

	now := time.Now()
	proc.mu.Lock()
	if proc.remoteClients == nil {
		proc.remoteClients = make(map[string]*remoteClientSession)
	}
	sess, exists := proc.remoteClients[clientID]
	if !exists {
		sess = &remoteClientSession{
			serverID:    serverID,
			clientID:    clientID,
			connectedAt: now,
		}
		proc.remoteClients[clientID] = sess
	}
	sess.clientName = clientName
	if frpUser != "" {
		sess.frpUser = frpUser
	}
	if host != "" {
		sess.hostname = host
	}
	if osName != "" {
		sess.osName = osName
	}
	if arch != "" {
		sess.arch = arch
	}
	if appVer != "" {
		sess.appVersion = appVer
	}
	if frpVer != "" {
		sess.frpVersion = frpVer
	}
	if remoteAddr != "" {
		sess.remoteAddr = remoteAddr
	}
	if runID != "" {
		sess.runID = runID
	}
	sess.ctrlProxyName = qualifiedCtrl
	if ctrlKey != "" {
		sess.ctrlSecret = ctrlKey
	}
	if agentTok != "" {
		sess.agentToken = agentTok
	}
	if op == "Login" {
		sess.connectedAt = now
	}
	sess.lastPingAt = now
	sess.online = true

	proxyName := stringField(content, "proxy_name")
	if op == "NewProxy" && isInternalCtrlProxy(proxyName) {
		sess.ctrlReady = true
	}
	if op == "CloseProxy" && isInternalCtrlProxy(proxyName) {
		sess.ctrlReady = false
	}
	proc.mu.Unlock()
	rs.notifyChange()
}

func buildRemoteClientSummaries(serverID string, proc *managedProc) []RemoteClientSummary {
	if proc == nil {
		return nil
	}
	now := time.Now()
	out := make([]RemoteClientSummary, 0, len(proc.remoteClients))
	for _, s := range proc.remoteClients {
		pCount := 0
		for pName := range proc.conns {
			if s.frpUser != "" && strings.HasPrefix(pName, s.frpUser+".") {
				pCount++
			} else if s.frpUser == "" && !strings.Contains(pName, ".") {
				pCount++
			}
		}
		online := s.online && now.Sub(s.lastPingAt) < 120*time.Second
		out = append(out, RemoteClientSummary{
			ServerID:      serverID,
			ClientID:      s.clientID,
			ClientName:    s.clientName,
			FRPUser:       s.frpUser,
			Hostname:      s.hostname,
			OS:            s.osName,
			Arch:          s.arch,
			AppVersion:    s.appVersion,
			FRPVersion:    s.frpVersion,
			RemoteAddr:    s.remoteAddr,
			RunID:         s.runID,
			ConnectedAt:   s.connectedAt.UTC().Format(time.RFC3339),
			LastPingAt:    s.lastPingAt.UTC().Format(time.RFC3339),
			CtrlProxyName: s.ctrlProxyName,
			CtrlReady:     s.ctrlReady,
			Online:        online,
			ProxyCount:    pCount,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Online != out[j].Online {
			return out[i].Online
		}
		return out[i].ClientName < out[j].ClientName
	})
	return out
}

// resolveRemoteAgentEndpoint returns the loopback base URL and agent token to reach a connected frpc instance.
func (rs *RuntimeSupervisor) resolveRemoteAgentEndpoint(serverID, clientID string) (string, *remoteClientSession, error) {
	rs.mu.RLock()
	proc, ok := rs.procs[serverID]
	rs.mu.RUnlock()
	if !ok || proc == nil {
		return "", nil, errors.New("所属 frps 服务端实例未在运行，无法建立连接远控")
	}

	proc.mu.Lock()
	var sessCopy remoteClientSession
	if s, exists := proc.remoteClients[clientID]; exists && s != nil {
		sessCopy = *s
	}
	proc.mu.Unlock()

	if sessCopy.clientID == "" {
		return "", nil, errors.New("该客户端实例尚未连接到当前 frps 服务端")
	}
	if sessCopy.agentToken == "" || sessCopy.ctrlSecret == "" {
		return "", nil, errors.New("该客户端缺少 KurumiFrp 远控握手凭证（请确认客户端版本为 v0.9.4+ 且已开启远控）")
	}

	if sessCopy.directAgentURL != "" {
		return strings.TrimRight(sessCopy.directAgentURL, "/"), &sessCopy, nil
	}

	if rs.agentPort > 0 {
		_, doc := rs.store.Snapshot()
		for _, c := range doc.Clients {
			if c.ID == clientID && subtle.ConstantTimeCompare([]byte(c.AgentToken), []byte(sessCopy.agentToken)) == 1 {
				return fmt.Sprintf("http://127.0.0.1:%d", rs.agentPort), &sessCopy, nil
			}
		}
	}

	_, doc := rs.store.Snapshot()
	var srv *ServerProfile
	for i := range doc.Servers {
		if doc.Servers[i].ID == serverID {
			srv = &doc.Servers[i]
			break
		}
	}
	if srv == nil {
		return "", nil, errors.New("未找到所属 frps 配置")
	}

	port, err := rs.ensureVisitorBridge(*srv, sessCopy, proc)
	if err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("http://127.0.0.1:%d", port), &sessCopy, nil
}

func (rs *RuntimeSupervisor) ensureVisitorBridge(srv ServerProfile, sess remoteClientSession, owner *managedProc) (int, error) {
	key := srv.ID + "::" + sess.clientID

	rs.bridgeMu.Lock()
	if existing, ok := rs.bridges[key]; ok && existing != nil {
		select {
		case <-existing.done:
			if existing.cfgPath != "" {
				_ = os.Remove(existing.cfgPath)
			}
			delete(rs.bridges, key)
		default:
			if existing.ctrlProxy == sess.ctrlProxyName && existing.ctrlSecret == sess.ctrlSecret {
				existing.lastUsed = time.Now()
				port := existing.bindPort
				rs.bridgeMu.Unlock()
				return port, nil
			}
			delete(rs.bridges, key)
			go terminateVisitorBridge(existing)
		}
	}
	rs.bridgeMu.Unlock()

	bins := LocateBinaries(rs.dataDir)
	if !bins.FrpcFound {
		return 0, errors.New("服务端主机缺少内置 frpc 组件，无法建立反向 STCP 远控桥接")
	}

	bindPort, err := pickFreeLoopbackPort()
	if err != nil {
		return 0, err
	}

	srvHost := strings.TrimSpace(srv.BindAddr)
	if srvHost == "" || srvHost == "0.0.0.0" || srvHost == "::" {
		srvHost = "127.0.0.1"
	}
	srvPort := srv.BindPort
	if srvPort <= 0 {
		srvPort = 7000
	}

	var tb strings.Builder
	var env []string
	fmt.Fprintf(&tb, "serverAddr = %s\n", tomlQuote(srvHost))
	fmt.Fprintf(&tb, "serverPort = %d\n", srvPort)
	tb.WriteString("loginFailExit = true\n")
	if srv.TLSForce {
		tb.WriteString("transport.tls.enable = true\n")
	}
	authMethod := strings.ToLower(strings.TrimSpace(srv.AuthMethod))
	if authMethod == "oidc" || (authMethod == "" && strings.TrimSpace(srv.OIDCIssuer) != "") {
		return 0, errors.New("当前 frps 启用了 OIDC 鉴权，服务端无法自动签发客户端 OIDC 令牌建立反向桥接；请改用 Token / 令牌文件鉴权或在客户端本机管理")
	}
	if scopes := cleanStringSlice(srv.AuthAdditionalScopes); len(scopes) > 0 {
		fmt.Fprintf(&tb, "auth.additionalScopes = %s\n", tomlStringArray(scopes))
	}
	if strings.TrimSpace(srv.AuthTokenSourceFile) != "" {
		tb.WriteString("auth.method = \"token\"\n")
		tb.WriteString("auth.tokenSource.type = \"file\"\n")
		fmt.Fprintf(&tb, "auth.tokenSource.file.path = %s\n", tomlQuote(strings.TrimSpace(srv.AuthTokenSourceFile)))
	} else if strings.TrimSpace(srv.AuthToken) != "" {
		tb.WriteString("auth.method = \"token\"\n")
		tb.WriteString("auth.token = \"{{ .Envs.KURUMI_BRIDGE_AUTH }}\"\n")
		env = append(env, "KURUMI_BRIDGE_AUTH="+tomlInnerEscape(srv.AuthToken))
	}
	tb.WriteString("\n[[visitors]]\n")
	fmt.Fprintf(&tb, "name = %s\n", tomlQuote("__kurumi_mgr_"+sess.clientID+"_"+randomHex(3)))
	tb.WriteString("type = \"stcp\"\n")
	fmt.Fprintf(&tb, "serverName = %s\n", tomlQuote(sess.ctrlProxyName))
	tb.WriteString("secretKey = \"{{ .Envs.KURUMI_BRIDGE_KEY }}\"\n")
	env = append(env, "KURUMI_BRIDGE_KEY="+tomlInnerEscape(sess.ctrlSecret))
	tb.WriteString("bindAddr = \"127.0.0.1\"\n")
	fmt.Fprintf(&tb, "bindPort = %d\n", bindPort)
	tb.WriteString("transport.useEncryption = true\n")

	cfgPath := filepath.Join(rs.runtimeDir, fmt.Sprintf("bridge_%s_%s.toml", srv.ID, sess.clientID))
	if err := os.WriteFile(cfgPath, []byte(tb.String()), 0o600); err != nil {
		return 0, err
	}

	cmd := exec.Command(bins.FrpcPath, "-c", cfgPath)
	cmd.Dir = rs.runtimeDir
	cmd.Env = append(os.Environ(), env...)
	childprocess.Configure(cmd)
	if err := cmd.Start(); err != nil {
		_ = os.Remove(cfgPath)
		return 0, fmt.Errorf("启动远控桥接进程失败: %w", err)
	}

	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()

	br := &visitorBridge{
		key:        key,
		bindPort:   bindPort,
		ctrlProxy:  sess.ctrlProxyName,
		ctrlSecret: sess.ctrlSecret,
		cmd:        cmd,
		done:       done,
		cfgPath:    cfgPath,
		lastUsed:   time.Now(),
	}

	deadline := time.Now().Add(3500 * time.Millisecond)
	ready := false
	for time.Now().Before(deadline) {
		select {
		case <-done:
			_ = os.Remove(cfgPath)
			return 0, errors.New("远控桥接握手失败，请检查客户端实例是否在线")
		default:
		}
		conn, dialErr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", bindPort), 150*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			ready = true
			break
		}
		time.Sleep(80 * time.Millisecond)
	}
	if !ready {
		terminateVisitorBridge(br)
		return 0, errors.New("等待客户端远控通道就绪超时")
	}

	rs.mu.RLock()
	ownerAlive := (owner == nil || rs.procs[srv.ID] == owner)
	rs.mu.RUnlock()
	if !ownerAlive {
		terminateVisitorBridge(br)
		return 0, errors.New("所属服务端实例已停止，已取消远控桥接")
	}

	rs.bridgeMu.Lock()
	if existing, ok := rs.bridges[key]; ok && existing != nil {
		select {
		case <-existing.done:
			if existing.cfgPath != "" {
				_ = os.Remove(existing.cfgPath)
			}
		default:
			if existing.ctrlProxy == sess.ctrlProxyName && existing.ctrlSecret == sess.ctrlSecret {
				existing.lastUsed = time.Now()
				p := existing.bindPort
				rs.bridgeMu.Unlock()
				terminateVisitorBridge(br)
				return p, nil
			}
			go terminateVisitorBridge(existing)
		}
	}
	rs.bridges[key] = br
	rs.bridgeMu.Unlock()
	return bindPort, nil
}

func (rs *RuntimeSupervisor) doRemoteClientJSON(ctx context.Context, serverID, clientID, method, subPath string, reqBody any, out any) (*remoteClientSession, error) {
	baseURL, sess, err := rs.resolveRemoteAgentEndpoint(serverID, clientID)
	if err != nil {
		return nil, err
	}
	var bodyReader io.Reader
	if reqBody != nil {
		raw, mErr := json.Marshal(reqBody)
		if mErr != nil {
			return nil, mErr
		}
		bodyReader = bytes.NewReader(raw)
	}
	reqCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()

	fullURL := fmt.Sprintf("%s/internal/client-agent/%s/%s", baseURL, clientID, subPath)
	req, err := http.NewRequestWithContext(reqCtx, method, fullURL, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Kurumi-Agent-Token", sess.agentToken)
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: 6 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("通过连接访问远端客户端实例失败: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxConfigBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		var errObj struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(respBytes, &errObj) == nil && errObj.Error != "" {
			return nil, errors.New(errObj.Error)
		}
		return nil, fmt.Errorf("远端客户端返回 HTTP %d", resp.StatusCode)
	}
	if out != nil {
		if err := json.Unmarshal(respBytes, out); err != nil {
			return nil, err
		}
	}
	return sess, nil
}

// FetchRemoteClient retrieves the live ClientProfile, runtime status, generated TOML, and logs of a connected frpc instance.
func (rs *RuntimeSupervisor) FetchRemoteClient(ctx context.Context, serverID, clientID string) (RemoteClientSnapshot, error) {
	var raw struct {
		Profile ClientProfile      `json:"profile"`
		Runtime ProcessRuntimeView `json:"runtime"`
		TOML    string             `json:"toml"`
		Logs    []string           `json:"logs"`
	}
	sess, err := rs.doRemoteClientJSON(ctx, serverID, clientID, http.MethodGet, "snapshot", nil, &raw)
	if err != nil {
		return RemoteClientSnapshot{}, err
	}
	return RemoteClientSnapshot{
		Summary: RemoteClientSummary{
			ServerID:      serverID,
			ClientID:      sess.clientID,
			ClientName:    nonEmptyStr(raw.Profile.Name, sess.clientName),
			FRPUser:       nonEmptyStr(raw.Profile.User, sess.frpUser),
			Hostname:      sess.hostname,
			OS:            sess.osName,
			Arch:          sess.arch,
			AppVersion:    sess.appVersion,
			FRPVersion:    sess.frpVersion,
			RemoteAddr:    sess.remoteAddr,
			RunID:         sess.runID,
			ConnectedAt:   sess.connectedAt.UTC().Format(time.RFC3339),
			LastPingAt:    sess.lastPingAt.UTC().Format(time.RFC3339),
			CtrlProxyName: sess.ctrlProxyName,
			CtrlReady:     sess.ctrlReady,
			Online:        true,
		},
		Profile: raw.Profile,
		Runtime: raw.Runtime,
		TOML:    raw.TOML,
		Logs:    raw.Logs,
	}, nil
}

// SaveRemoteClient pushes a structured ClientProfile update over the active connection and hot-reloads the remote frpc instance.
func (rs *RuntimeSupervisor) SaveRemoteClient(ctx context.Context, serverID, clientID string, profile ClientProfile, allowUpstreamChange bool) (ClientProfile, string, error) {
	var out struct {
		Profile ClientProfile `json:"profile"`
		TOML    string        `json:"toml"`
	}
	_, err := rs.doRemoteClientJSON(ctx, serverID, clientID, http.MethodPost, "save", map[string]any{
		"profile":             profile,
		"allowUpstreamChange": allowUpstreamChange,
	}, &out)
	if err != nil {
		return ClientProfile{}, "", err
	}
	return out.Profile, out.TOML, nil
}

// SaveRemoteClientTOML pushes edited raw frpc.toml code over the active connection, parses it on the remote client, and hot-reloads.
func (rs *RuntimeSupervisor) SaveRemoteClientTOML(ctx context.Context, serverID, clientID, rawTOML string, allowUpstreamChange bool) (ClientProfile, string, error) {
	var out struct {
		Profile ClientProfile `json:"profile"`
		TOML    string        `json:"toml"`
	}
	_, err := rs.doRemoteClientJSON(ctx, serverID, clientID, http.MethodPost, "save-toml", map[string]any{
		"toml":                rawTOML,
		"allowUpstreamChange": allowUpstreamChange,
	}, &out)
	if err != nil {
		return ClientProfile{}, "", err
	}
	return out.Profile, out.TOML, nil
}

// ReloadRemoteClient triggers a zero-downtime hot reload on a connected remote frpc instance.
func (rs *RuntimeSupervisor) ReloadRemoteClient(ctx context.Context, serverID, clientID string) error {
	_, err := rs.doRemoteClientJSON(ctx, serverID, clientID, http.MethodPost, "reload", map[string]any{}, nil)
	return err
}

// OrchestrateRemoteP2P configures a P2P tunnel (XTCP/STCP/SUDP) on HostClientID (e.g. Home PC C) and optionally VisitorClientID (e.g. Friend PC E)
// through their active connections to ServerID (Cloud Server A), returning ready-to-use visitor config & TOML if VisitorClientID is external.
func (rs *RuntimeSupervisor) OrchestrateRemoteP2P(ctx context.Context, req P2PPairRequest) (map[string]any, error) {
	baseName := strings.TrimSpace(req.TunnelBaseName)
	if baseName == "" {
		baseName = "p2p-link"
	}
	p2pType := strings.ToLower(strings.TrimSpace(req.P2PType))
	if p2pType != "xtcp" && p2pType != "stcp" && p2pType != "sudp" {
		p2pType = "xtcp"
	}
	secret := strings.TrimSpace(req.SecretKey)
	if secret == "" {
		secret = randomTokenHex(12)
	}
	hostLocalIP := nonEmptyStr(req.HostLocalIP, "127.0.0.1")
	if req.HostLocalPort <= 0 || req.HostLocalPort > 65535 {
		return nil, errors.New("被访问端本地端口需在 1~65535 之间")
	}

	// Pre-read both endpoints up front so we can check collisions (F17), align cross-user allowUsers (F16), and rollback cleanly on partial failure (F18)
	var (
		hasHost     = strings.TrimSpace(req.HostClientID) != ""
		hasVisitor  = strings.TrimSpace(req.VisitorClientID) != ""
		origHost    ClientProfile
		hostProf    ClientProfile
		vProf       ClientProfile
		hostUser    string
		visitorUser string
	)

	if hasHost {
		snap, err := rs.FetchRemoteClient(ctx, req.ServerID, req.HostClientID)
		if err != nil {
			return nil, fmt.Errorf("读取被访问端 (%s) 配置失败: %w", req.HostClientID, err)
		}
		origHost = cloneClientProfile(snap.Profile)
		hostProf = cloneClientProfile(snap.Profile)
		hostUser = strings.TrimSpace(hostProf.User)
		if !req.Overwrite {
			for _, t := range hostProf.Tunnels {
				if t.Name == baseName || (p2pType == "xtcp" && req.EnableSTCPFallback && t.Name == baseName+"-fallback") {
					return nil, fmt.Errorf("被访问端已存在同名隧道 %q（如需覆盖已有规则请勾选允许覆盖同名规则或更换标识基名）", t.Name)
				}
			}
		}
	}

	if hasVisitor {
		vSnap, err := rs.FetchRemoteClient(ctx, req.ServerID, req.VisitorClientID)
		if err != nil {
			return nil, fmt.Errorf("读取访问发起端 (%s) 配置失败: %w", req.VisitorClientID, err)
		}
		vProf = cloneClientProfile(vSnap.Profile)
		visitorUser = strings.TrimSpace(vProf.User)
		if !req.Overwrite {
			for _, t := range vProf.Tunnels {
				if t.Name == baseName+"-visitor" || (p2pType == "xtcp" && req.EnableSTCPFallback && t.Name == baseName+"-fb-visitor") {
					return nil, fmt.Errorf("访问发起端已存在同名访问端规则 %q（如需覆盖已有规则请勾选允许覆盖同名规则或更换标识基名）", t.Name)
				}
			}
		}
	}

	// F16: Compute STCP/XTCP/SUDP allowUsers when FRP user namespaces differ (or when generating offline snippet for unknown peer user)
	var hostAllowUsers []string
	if !hasVisitor {
		hostAllowUsers = []string{"*"}
	} else if visitorUser != hostUser {
		if visitorUser == "" {
			hostAllowUsers = []string{"*"}
		} else {
			hostAllowUsers = []string{visitorUser}
		}
	}

	hostApplied := false
	if hasHost {
		upsertTunnelRule(&hostProf.Tunnels, TunnelRule{
			Name:           baseName,
			Mode:           "proxy",
			Type:           p2pType,
			Enabled:        true,
			LocalIP:        hostLocalIP,
			LocalPort:      req.HostLocalPort,
			SecretKey:      secret,
			AllowUsers:     hostAllowUsers,
			UseEncryption:  true,
			UseCompression: true,
			Note:           "由服务端 P2P 协同中心下发",
		})
		if p2pType == "xtcp" && req.EnableSTCPFallback {
			upsertTunnelRule(&hostProf.Tunnels, TunnelRule{
				Name:           baseName + "-fallback",
				Mode:           "proxy",
				Type:           "stcp",
				Enabled:        true,
				LocalIP:        hostLocalIP,
				LocalPort:      req.HostLocalPort,
				SecretKey:      secret,
				AllowUsers:     hostAllowUsers,
				UseEncryption:  true,
				UseCompression: true,
				Note:           "XTCP 打洞回退保底通道",
			})
		}
		if _, _, err := rs.SaveRemoteClient(ctx, req.ServerID, req.HostClientID, hostProf, false); err != nil {
			return nil, fmt.Errorf("下发被访问端 (%s) P2P 配置失败: %w", req.HostClientID, err)
		}
		hostApplied = true
	}

	visitorBindIP := nonEmptyStr(req.VisitorBindIP, "127.0.0.1")
	visitorBindPort := req.VisitorBindPort
	if visitorBindPort <= 0 {
		visitorBindPort = 10000 + (req.HostLocalPort % 50000)
	}

	visitorRule := TunnelRule{
		Name:           baseName + "-visitor",
		Mode:           "visitor",
		Type:           p2pType,
		Enabled:        true,
		ServerName:     baseName,
		ServerUser:     hostUser,
		SecretKey:      secret,
		LocalIP:        visitorBindIP,
		LocalPort:      visitorBindPort,
		KeepTunnelOpen: req.KeepTunnelOpen,
		UseEncryption:  true,
		UseCompression: true,
		Note:           "P2P 访问发起端规则",
	}
	var fallbackVisitor *TunnelRule
	if p2pType == "xtcp" && req.EnableSTCPFallback {
		fbName := baseName + "-fb-visitor"
		visitorRule.FallbackTo = fbName
		visitorRule.FallbackTimeoutMs = 800
		fallbackVisitor = &TunnelRule{
			Name:           fbName,
			Mode:           "visitor",
			Type:           "stcp",
			Enabled:        true,
			ServerName:     baseName + "-fallback",
			ServerUser:     hostUser,
			SecretKey:      secret,
			LocalIP:        visitorBindIP,
			LocalPort:      -1,
			UseEncryption:  true,
			UseCompression: true,
			Note:           "XTCP 自动回退 STCP 访问端（无独立端口）",
		}
	}

	visitorApplied := false
	if hasVisitor {
		if fallbackVisitor != nil {
			upsertTunnelRule(&vProf.Tunnels, *fallbackVisitor)
		}
		upsertTunnelRule(&vProf.Tunnels, visitorRule)
		if _, _, err := rs.SaveRemoteClient(ctx, req.ServerID, req.VisitorClientID, vProf, false); err != nil {
			// F18: Automatically rollback step-1 host changes so half-configured rules are never left behind!
			if hostApplied {
				_, _, rbErr := rs.SaveRemoteClient(ctx, req.ServerID, req.HostClientID, origHost, false)
				if rbErr != nil {
					return nil, fmt.Errorf("下发访问发起端失败 (%v)，且回滚被访问端失败 (%v)", err, rbErr)
				}
			}
			return nil, fmt.Errorf("下发访问发起端 (%s) P2P 配置失败（已自动回滚被访问端变更）: %w", req.VisitorClientID, err)
		}
		visitorApplied = true
	}

	sampleCli := ClientProfile{
		Name:       "P2P-Peer",
		ServerAddr: "127.0.0.1",
		ServerPort: 7000,
		Tunnels:    []TunnelRule{visitorRule},
	}
	if fallbackVisitor != nil {
		sampleCli.Tunnels = append([]TunnelRule{*fallbackVisitor}, sampleCli.Tunnels...)
	}
	visitorPreview := RenderClientTOML(sampleCli, ClientRuntimeInjections{Preview: true, MaskSecrets: false})
	idx := strings.Index(visitorPreview.TOML, "[[visitors]]")
	visitorSnippet := visitorPreview.TOML
	if idx >= 0 {
		visitorSnippet = strings.TrimSpace(visitorPreview.TOML[idx:])
	}

	return map[string]any{
		"ok":                 true,
		"tunnelBaseName":     baseName,
		"p2pType":            p2pType,
		"secretKey":          secret,
		"serverUser":         hostUser,
		"hostApplied":        hostApplied,
		"visitorApplied":     visitorApplied,
		"visitorRule":        visitorRule,
		"visitorTomlSnippet": visitorSnippet,
	}, nil
}

func cloneClientProfile(in ClientProfile) ClientProfile {
	raw, _ := json.Marshal(in)
	var out ClientProfile
	_ = json.Unmarshal(raw, &out)
	return out
}

func upsertTunnelRule(list *[]TunnelRule, rule TunnelRule) {
	for i := range *list {
		if (*list)[i].Name == rule.Name {
			rule.ID = (*list)[i].ID
			(*list)[i] = rule
			return
		}
	}
	if rule.ID == "" {
		rule.ID = newShortID("tun")
	}
	*list = append(*list, rule)
}

// terminateBridgesForServer cleans up any visitorBridge processes tied to a stopped/deleted frps server (A06).
func (rs *RuntimeSupervisor) terminateBridgesForServer(serverID string) {
	prefix := serverID + "::"
	rs.bridgeMu.Lock()
	var victim []*visitorBridge
	for k, b := range rs.bridges {
		if strings.HasPrefix(k, prefix) {
			victim = append(victim, b)
			delete(rs.bridges, k)
		}
	}
	rs.bridgeMu.Unlock()
	for _, b := range victim {
		terminateVisitorBridge(b)
	}
}

// reapStaleBridges removes exited or >10m idle visitorBridge subprocesses during periodic polling (A06).
func (rs *RuntimeSupervisor) reapStaleBridges() {
	now := time.Now()
	rs.bridgeMu.Lock()
	var victim []*visitorBridge
	for k, b := range rs.bridges {
		if b == nil {
			delete(rs.bridges, k)
			continue
		}
		exited := false
		select {
		case <-b.done:
			exited = true
		default:
		}
		if exited || now.Sub(b.lastUsed) > 10*time.Minute {
			victim = append(victim, b)
			delete(rs.bridges, k)
		}
	}
	rs.bridgeMu.Unlock()
	for _, b := range victim {
		terminateVisitorBridge(b)
	}
}
