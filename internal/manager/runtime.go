package manager

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
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
	"sync"
	"sync/atomic"
	"time"

	"github.com/Presikid/KurumiFrp/internal/childprocess"
)

const logRingCapacity = 500

// ringLogBuffer stores masked log lines in a fixed-size ring buffer and broadcasts live lines to SSE subscribers.
type ringLogBuffer struct {
	mu       sync.Mutex
	lines    []string
	head     int
	count    int
	replacer *strings.Replacer
	subs     map[chan string]struct{}
}

func newRingLogBuffer(secrets []string) *ringLogBuffer {
	var pairs []string
	seen := make(map[string]bool)
	// Sort longer secrets first so overlapping substrings never leak partial secrets.
	sorted := append([]string(nil), secrets...)
	sort.Slice(sorted, func(i, j int) bool { return len(sorted[i]) > len(sorted[j]) })
	for _, s := range sorted {
		s = strings.TrimSpace(s)
		if len(s) >= 3 && !seen[s] {
			seen[s] = true
			pairs = append(pairs, s, "********")
		}
	}
	var rep *strings.Replacer
	if len(pairs) > 0 {
		rep = strings.NewReplacer(pairs...)
	}
	return &ringLogBuffer{
		lines:    make([]string, logRingCapacity),
		replacer: rep,
		subs:     make(map[chan string]struct{}),
	}
}

func (b *ringLogBuffer) Append(line string) {
	if b.replacer != nil {
		line = b.replacer.Replace(line)
	}
	stamped := time.Now().Format("15:04:05") + " " + line
	b.mu.Lock()
	b.lines[b.head] = stamped
	b.head = (b.head + 1) % len(b.lines)
	if b.count < len(b.lines) {
		b.count++
	}
	for ch := range b.subs {
		select {
		case ch <- stamped:
		default:
		}
	}
	b.mu.Unlock()
}

func (b *ringLogBuffer) Snapshot() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, b.count)
	start := (b.head - b.count + len(b.lines)) % len(b.lines)
	for i := 0; i < b.count; i++ {
		out = append(out, b.lines[(start+i)%len(b.lines)])
	}
	return out
}

func (b *ringLogBuffer) Subscribe() (chan string, func()) {
	ch := make(chan string, 64)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		if _, ok := b.subs[ch]; ok {
			delete(b.subs, ch)
			close(ch)
		}
		b.mu.Unlock()
	}
}

type proxyRateSample struct {
	inBytes  int64
	outBytes int64
	at       time.Time
}

type managedProc struct {
	id           string
	kind         string // "frps" or "frpc"
	cfgPath      string
	adminPort    int
	adminUser    string
	adminPass    string
	pluginSecret string
	cmd          *exec.Cmd
	startedAt    time.Time
	done         chan struct{}
	logs         *ringLogBuffer

	mu          sync.Mutex
	state       string
	lastErr     string
	conns       map[string]ConnLiveItem
	rateSamples map[string]proxyRateSample
	tunnelStats []TunnelLiveStatus
}

// RuntimeSupervisor manages official frps & frpc child processes and Connection Dashboard polling.
type RuntimeSupervisor struct {
	mu           sync.RWMutex
	dataDir      string
	runtimeDir   string
	store        *Store
	procs        map[string]*managedProc
	historyLogs  map[string]*ringLogBuffer
	callbackAddr atomic.Value // stores string "127.0.0.1:<port>"
	onChange     func()
	stopPollCh   chan struct{}
	httpClient   *http.Client
}

// NewRuntimeSupervisor creates the official FRP process supervisor.
func NewRuntimeSupervisor(dataDir string, store *Store, onChange func()) (*RuntimeSupervisor, error) {
	rtDir := filepath.Join(dataDir, "runtime")
	if err := os.MkdirAll(rtDir, 0o700); err != nil {
		return nil, err
	}
	rs := &RuntimeSupervisor{
		dataDir:     dataDir,
		runtimeDir:  rtDir,
		store:       store,
		procs:       make(map[string]*managedProc),
		historyLogs: make(map[string]*ringLogBuffer),
		onChange:    onChange,
		stopPollCh:  make(chan struct{}),
		httpClient:  &http.Client{Timeout: 2 * time.Second},
	}
	go rs.pollLoop()
	return rs, nil
}

// SetCallbackAddr records the local HTTP listener address so frps HTTP plugins can call back into KurumiFrp.
func (rs *RuntimeSupervisor) SetCallbackAddr(addr string) {
	rs.callbackAddr.Store(addr)
}

func (rs *RuntimeSupervisor) notifyChange() {
	if rs.onChange != nil {
		rs.onChange()
	}
}

// StartAutoStarts launches all ServerProfiles and ClientProfiles configured with AutoStart: true.
func (rs *RuntimeSupervisor) StartAutoStarts() {
	_, doc := rs.store.Snapshot()
	for _, srv := range doc.Servers {
		if srv.AutoStart {
			srvCopy := srv
			go func() { _ = rs.StartServer(srvCopy) }()
		}
	}
	for _, cli := range doc.Clients {
		if cli.AutoStart {
			cliCopy := cli
			go func() { _ = rs.StartClient(cliCopy) }()
		}
	}
}

// StopAll gracefully terminates all running frps and frpc child processes.
func (rs *RuntimeSupervisor) StopAll() {
	select {
	case <-rs.stopPollCh:
	default:
		close(rs.stopPollCh)
	}
	rs.mu.RLock()
	ids := make([]string, 0, len(rs.procs))
	for id := range rs.procs {
		ids = append(ids, id)
	}
	rs.mu.RUnlock()

	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(pid string) {
			defer wg.Done()
			_ = rs.StopInstance(pid)
		}(id)
	}
	wg.Wait()
}

// StartServer verifies and launches an official frps subprocess for srv.
func (rs *RuntimeSupervisor) StartServer(srv ServerProfile) error {
	bins := LocateBinaries(rs.dataDir)
	if !bins.FrpsFound {
		return errors.New("未找到内置 frps 核心程序，请确认安装包完整或将官方 frps 放入 data/bin 目录")
	}
	_ = rs.StopInstance(srv.ID)

	adminPort, err := pickFreeLoopbackPort()
	if err != nil {
		return fmt.Errorf("分配本地管理端口失败: %w", err)
	}
	adminPass := randomHex(16)
	pluginSecret := randomHex(20)
	cbAddr, _ := rs.callbackAddr.Load().(string)

	rendered := RenderServerTOML(srv, ServerRuntimeInjections{
		Preview:        false,
		AdminPort:      adminPort,
		AdminUser:      "kurumi",
		AdminPass:      adminPass,
		KurumiCallback: cbAddr,
		PluginPath:     fmt.Sprintf("/internal/frps-plugin/%s?token=%s", srv.ID, pluginSecret),
	})

	cfgPath := filepath.Join(rs.runtimeDir, srv.ID+".toml")
	if _, err := writeIfChanged(cfgPath, []byte(rendered.TOML), 0o600); err != nil {
		return fmt.Errorf("写入运行配置失败: %w", err)
	}

	if err := verifyConfigBinary(bins.FrpsPath, cfgPath, rendered.Env); err != nil {
		return err
	}

	return rs.spawnProcess(srv.ID, "frps", bins.FrpsPath, cfgPath, rendered, adminPort, "kurumi", adminPass, pluginSecret)
}

// StartClient verifies and launches an official frpc subprocess for cli.
func (rs *RuntimeSupervisor) StartClient(cli ClientProfile) error {
	bins := LocateBinaries(rs.dataDir)
	if !bins.FrpcFound {
		return errors.New("未找到内置 frpc 核心程序，请确认安装包完整或将官方 frpc 放入 data/bin 目录")
	}
	_ = rs.StopInstance(cli.ID)

	adminPort, err := pickFreeLoopbackPort()
	if err != nil {
		return fmt.Errorf("分配本地管理端口失败: %w", err)
	}
	adminPass := randomHex(16)

	rendered := RenderClientTOML(cli, ClientRuntimeInjections{
		Preview:   false,
		AdminPort: adminPort,
		AdminUser: "kurumi",
		AdminPass: adminPass,
	})

	cfgPath := filepath.Join(rs.runtimeDir, cli.ID+".toml")
	if _, err := writeIfChanged(cfgPath, []byte(rendered.TOML), 0o600); err != nil {
		return fmt.Errorf("写入运行配置失败: %w", err)
	}

	if err := verifyConfigBinary(bins.FrpcPath, cfgPath, rendered.Env); err != nil {
		return err
	}

	return rs.spawnProcess(cli.ID, "frpc", bins.FrpcPath, cfgPath, rendered, adminPort, "kurumi", adminPass, "")
}

// ReloadOrRestartClient hot-reloads a running frpc instance via official Admin API (`frpc reload`) without dropping active tunnels.
func (rs *RuntimeSupervisor) ReloadOrRestartClient(cli ClientProfile) error {
	rs.mu.RLock()
	p, ok := rs.procs[cli.ID]
	rs.mu.RUnlock()
	if !ok || p == nil {
		return nil
	}
	p.mu.Lock()
	running := p.state == "running"
	adminPort := p.adminPort
	adminUser := p.adminUser
	adminPass := p.adminPass
	cfgPath := p.cfgPath
	p.mu.Unlock()
	if !running {
		return nil
	}

	bins := LocateBinaries(rs.dataDir)
	if !bins.FrpcFound {
		return errors.New("未找到 frpc 核心程序")
	}

	rendered := RenderClientTOML(cli, ClientRuntimeInjections{
		Preview:   false,
		AdminPort: adminPort,
		AdminUser: adminUser,
		AdminPass: adminPass,
	})
	if _, err := writeIfChanged(cfgPath, []byte(rendered.TOML), 0o600); err != nil {
		return err
	}
	if err := verifyConfigBinary(bins.FrpcPath, cfgPath, rendered.Env); err != nil {
		return err
	}

	// Attempt zero-downtime hot reload via local Admin API first; fall back to clean restart if transport params changed.
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/api/reload", adminPort), nil)
	if err == nil {
		req.SetBasicAuth(adminUser, adminPass)
		resp, err := rs.httpClient.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				p.logs.Append("[KurumiFrp] 已通过官方热重载接口无缝更新隧道配置")
				rs.notifyChange()
				return nil
			}
		}
	}
	return rs.StartClient(cli)
}

func (rs *RuntimeSupervisor) spawnProcess(id, kind, binPath, cfgPath string, rendered RenderedConfig, adminPort int, adminUser, adminPass, pluginSecret string) error {
	cmd := exec.Command(binPath, "-c", cfgPath)
	cmd.Dir = rs.runtimeDir
	cmd.Env = append(os.Environ(), rendered.Env...)
	childprocess.Configure(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	logs := newRingLogBuffer(rendered.Secrets)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 %s 进程失败: %w", kind, err)
	}

	proc := &managedProc{
		id:           id,
		kind:         kind,
		cfgPath:      cfgPath,
		adminPort:    adminPort,
		adminUser:    adminUser,
		adminPass:    adminPass,
		pluginSecret: pluginSecret,
		cmd:          cmd,
		startedAt:    time.Now(),
		done:         make(chan struct{}),
		logs:         logs,
		state:        "running",
		conns:        make(map[string]ConnLiveItem),
		rateSamples:  make(map[string]proxyRateSample),
	}

	logs.Append(fmt.Sprintf("[KurumiFrp] 已启动官方 %s 核心 (PID=%d)", kind, cmd.Process.Pid))

	rs.mu.Lock()
	rs.procs[id] = proc
	rs.historyLogs[id] = logs
	rs.mu.Unlock()
	rs.notifyChange()

	go streamPipeLines(stdout, logs)
	go streamPipeLines(stderr, logs)
	go func() {
		waitErr := cmd.Wait()
		close(proc.done)
		proc.mu.Lock()
		if waitErr != nil && proc.state != "stopping" {
			proc.state = "error"
			proc.lastErr = waitErr.Error()
			logs.Append(fmt.Sprintf("[KurumiFrp] %s 异常退出: %v", kind, waitErr))
		} else {
			proc.state = "stopped"
			logs.Append(fmt.Sprintf("[KurumiFrp] %s 已停止", kind))
		}
		proc.mu.Unlock()

		rs.mu.Lock()
		if cur, exists := rs.procs[id]; exists && cur == proc {
			delete(rs.procs, id)
		}
		rs.mu.Unlock()
		rs.notifyChange()
	}()

	return nil
}

// StopInstance stops a running frps or frpc instance outside the global mutex so UI requests never hang.
func (rs *RuntimeSupervisor) StopInstance(id string) error {
	rs.mu.RLock()
	p, ok := rs.procs[id]
	rs.mu.RUnlock()
	if !ok || p == nil {
		return nil
	}

	p.mu.Lock()
	p.state = "stopping"
	p.mu.Unlock()

	_ = childprocess.Terminate(p.cmd)
	select {
	case <-p.done:
	case <-time.After(4 * time.Second):
		_ = childprocess.Kill(p.cmd)
		select {
		case <-p.done:
		case <-time.After(1 * time.Second):
		}
	}

	rs.mu.Lock()
	if cur, exists := rs.procs[id]; exists && cur == p {
		delete(rs.procs, id)
	}
	rs.mu.Unlock()
	rs.notifyChange()
	return nil
}

// Views returns live runtime views for all ServerProfiles and ClientProfiles in doc.
func (rs *RuntimeSupervisor) Views(doc ConfigDocument) map[string]ProcessRuntimeView {
	rs.mu.RLock()
	defer rs.mu.RUnlock()

	out := make(map[string]ProcessRuntimeView)
	for _, srv := range doc.Servers {
		view := ProcessRuntimeView{
			ID:          srv.ID,
			Kind:        "frps",
			State:       "stopped",
			Connections: buildMergedConnections(srv, nil),
		}
		if p, ok := rs.procs[srv.ID]; ok {
			p.mu.Lock()
			view.Running = p.state == "running"
			view.State = p.state
			if p.cmd != nil && p.cmd.Process != nil {
				view.PID = p.cmd.Process.Pid
			}
			view.StartedAt = p.startedAt.UTC().Format(time.RFC3339)
			view.LastError = p.lastErr
			view.AdminPort = p.adminPort
			view.Connections = buildMergedConnections(srv, p.conns)
			p.mu.Unlock()
		}
		out[srv.ID] = view
	}

	for _, cli := range doc.Clients {
		view := ProcessRuntimeView{
			ID:    cli.ID,
			Kind:  "frpc",
			State: "stopped",
		}
		if p, ok := rs.procs[cli.ID]; ok {
			p.mu.Lock()
			view.Running = p.state == "running"
			view.State = p.state
			if p.cmd != nil && p.cmd.Process != nil {
				view.PID = p.cmd.Process.Pid
			}
			view.StartedAt = p.startedAt.UTC().Format(time.RFC3339)
			view.LastError = p.lastErr
			view.AdminPort = p.adminPort
			view.TunnelStats = append([]TunnelLiveStatus(nil), p.tunnelStats...)
			p.mu.Unlock()
		}
		out[cli.ID] = view
	}
	return out
}

// Logs returns buffered log lines and an optional SSE subscription channel for an instance.
func (rs *RuntimeSupervisor) Logs(id string) ([]string, *ringLogBuffer) {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	if p, ok := rs.procs[id]; ok {
		return p.logs.Snapshot(), p.logs
	}
	if hist, ok := rs.historyLogs[id]; ok {
		return hist.Snapshot(), hist
	}
	return []string{}, nil
}

// HandleFRPSPlugin implements the official fatedier/frp HTTP Server Plugin webhook to enforce
// Connection Dashboard controls: 开关 (Enabled), 限流 (TrafficLimitMB), 限速 (SpeedLimitKBps), and 限时 (ExpiresAt).
func (rs *RuntimeSupervisor) HandleFRPSPlugin(w http.ResponseWriter, r *http.Request, serverID string) {
	rs.mu.RLock()
	proc, ok := rs.procs[serverID]
	rs.mu.RUnlock()
	if !ok || proc == nil {
		http.Error(w, "unknown server instance", http.StatusNotFound)
		return
	}
	gotToken := r.URL.Query().Get("token")
	if subtle.ConstantTimeCompare([]byte(gotToken), []byte(proc.pluginSecret)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	op := r.URL.Query().Get("op")
	var reqPayload struct {
		Content map[string]any `json:"content"`
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	_ = json.Unmarshal(body, &reqPayload)
	if op == "" {
		op = stringField(reqPayload.Content, "op")
	}

	proxyName := stringField(reqPayload.Content, "proxy_name")
	if proxyName == "" {
		if pMap, ok := reqPayload.Content["proxy_name"].(string); ok {
			proxyName = pMap
		}
	}

	if proxyName != "" && (op == "NewProxy" || op == "NewUserConn") {
		if reject, reason := rs.evaluateConnPolicy(serverID, proc, proxyName); reject {
			writeJSON(w, http.StatusOK, map[string]any{
				"reject":        true,
				"reject_reason": reason,
			})
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"reject":   false,
		"unchange": true,
	})
}

func (rs *RuntimeSupervisor) evaluateConnPolicy(serverID string, proc *managedProc, proxyName string) (bool, string) {
	_, doc := rs.store.Snapshot()
	var srv *ServerProfile
	for i := range doc.Servers {
		if doc.Servers[i].ID == serverID {
			srv = &doc.Servers[i]
			break
		}
	}
	if srv == nil {
		return false, ""
	}
	pol, exists := srv.ConnPolicies[proxyName]
	if !exists {
		// Auto-register newly discovered connection with Enabled: true on the Connection Dashboard.
		_ = rs.store.Update(func(d *ConfigDocument) error {
			for i := range d.Servers {
				if d.Servers[i].ID == serverID {
					if d.Servers[i].ConnPolicies == nil {
						d.Servers[i].ConnPolicies = make(map[string]ConnPolicy)
					}
					if _, has := d.Servers[i].ConnPolicies[proxyName]; !has {
						d.Servers[i].ConnPolicies[proxyName] = ConnPolicy{
							ProxyName: proxyName,
							Enabled:   true,
						}
					}
				}
			}
			return nil
		})
		return false, ""
	}

	if !pol.Enabled {
		return true, "该连接已在 KurumiFrp 连接看板关闭"
	}
	if strings.TrimSpace(pol.ExpiresAt) != "" {
		if exp, err := time.Parse(time.RFC3339, strings.TrimSpace(pol.ExpiresAt)); err == nil {
			if time.Now().After(exp) {
				return true, "该连接已到达限时截止时间 (" + exp.Local().Format("2006-01-02 15:04") + ")"
			}
		}
	}
	if pol.TrafficLimitMB > 0 {
		limitBytes := pol.TrafficLimitMB * 1024 * 1024
		totalUsed := pol.RecordedInBytes + pol.RecordedOutBytes
		if proc != nil {
			proc.mu.Lock()
			if live, has := proc.conns[proxyName]; has {
				totalUsed = live.TotalInBytes + live.TotalOutBytes
			}
			proc.mu.Unlock()
		}
		if totalUsed >= limitBytes {
			return true, fmt.Sprintf("该连接已达限流上限 (%d MB)", pol.TrafficLimitMB)
		}
	}
	return false, ""
}

// CloseOfflineProxies calls official frps Admin API DELETE /api/proxies?status=offline to purge closed proxies.
func (rs *RuntimeSupervisor) CloseOfflineProxies(serverID string) {
	rs.mu.RLock()
	p, ok := rs.procs[serverID]
	rs.mu.RUnlock()
	if !ok || p == nil {
		return
	}
	req, err := http.NewRequest(http.MethodDelete, fmt.Sprintf("http://127.0.0.1:%d/api/proxies?status=offline", p.adminPort), nil)
	if err != nil {
		return
	}
	req.SetBasicAuth(p.adminUser, p.adminPass)
	if resp, err := rs.httpClient.Do(req); err == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

func (rs *RuntimeSupervisor) pollLoop() {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-rs.stopPollCh:
			return
		case <-ticker.C:
			rs.pollOnce()
		}
	}
}

func (rs *RuntimeSupervisor) pollOnce() {
	rs.mu.RLock()
	list := make([]*managedProc, 0, len(rs.procs))
	for _, p := range rs.procs {
		list = append(list, p)
	}
	rs.mu.RUnlock()

	_, doc := rs.store.Snapshot()
	srvMap := make(map[string]ServerProfile, len(doc.Servers))
	for _, s := range doc.Servers {
		srvMap[s.ID] = s
	}

	changed := false
	for _, p := range list {
		if p.kind == "frps" {
			if srv, ok := srvMap[p.id]; ok {
				if rs.pollServerAdmin(p, srv) {
					changed = true
				}
			}
		} else if p.kind == "frpc" {
			if rs.pollClientAdmin(p) {
				changed = true
			}
		}
	}
	if changed {
		rs.notifyChange()
	}
}

func (rs *RuntimeSupervisor) pollServerAdmin(p *managedProc, srv ServerProfile) bool {
	proxyTypes := []string{"tcp", "udp", "http", "https", "stcp", "xtcp", "tcpmux"}
	now := time.Now()
	discovered := make(map[string]ConnLiveItem)

	for _, pt := range proxyTypes {
		url := fmt.Sprintf("http://127.0.0.1:%d/api/proxy/%s", p.adminPort, pt)
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			continue
		}
		req.SetBasicAuth(p.adminUser, p.adminPass)
		resp, err := rs.httpClient.Do(req)
		if err != nil {
			continue
		}
		var payload struct {
			Proxies []struct {
				Name            string         `json:"name"`
				TodayTrafficIn  int64          `json:"todayTrafficIn"`
				TodayTrafficOut int64          `json:"todayTrafficOut"`
				CurConns        int64          `json:"curConns"`
				LastStartTime   string         `json:"lastStartTime"`
				LastCloseTime   string         `json:"lastCloseTime"`
				Status          string         `json:"status"`
				ClientVersion   string         `json:"clientVersion"`
				Conf            map[string]any `json:"conf"`
			} `json:"proxies"`
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&payload)
		resp.Body.Close()
		if err != nil {
			continue
		}

		p.mu.Lock()
		for _, item := range payload.Proxies {
			if item.Name == "" {
				continue
			}
			pol, hasPol := srv.ConnPolicies[item.Name]
			if !hasPol {
				pol = ConnPolicy{ProxyName: item.Name, Enabled: true}
			}
			totalIn := pol.RecordedInBytes + item.TodayTrafficIn
			totalOut := pol.RecordedOutBytes + item.TodayTrafficOut

			var speedIn, speedOut int64
			if prev, ok := p.rateSamples[item.Name]; ok {
				dt := now.Sub(prev.at).Seconds()
				if dt >= 0.5 {
					if dIn := item.TodayTrafficIn - prev.inBytes; dIn > 0 {
						speedIn = int64(float64(dIn) / dt)
					}
					if dOut := item.TodayTrafficOut - prev.outBytes; dOut > 0 {
						speedOut = int64(float64(dOut) / dt)
					}
				}
			}
			p.rateSamples[item.Name] = proxyRateSample{
				inBytes:  item.TodayTrafficIn,
				outBytes: item.TodayTrafficOut,
				at:       now,
			}

			// Enforce real-time bandwidth speed alert/block status if configured
			status := item.Status
			blockReason := ""
			if !pol.Enabled {
				status = "blocked"
				blockReason = "已手动关闭"
			} else if strings.TrimSpace(pol.ExpiresAt) != "" {
				if exp, err := time.Parse(time.RFC3339, strings.TrimSpace(pol.ExpiresAt)); err == nil && now.After(exp) {
					status = "blocked"
					blockReason = "限时已到期"
				}
			}
			if blockReason == "" && pol.TrafficLimitMB > 0 && (totalIn+totalOut) >= pol.TrafficLimitMB*1024*1024 {
				status = "blocked"
				blockReason = fmt.Sprintf("已达限流 (%d MB)", pol.TrafficLimitMB)
			}
			if blockReason == "" && pol.SpeedLimitKBps > 0 && (speedIn+speedOut) > pol.SpeedLimitKBps*1024 {
				blockReason = fmt.Sprintf("超限速阈值 (%d KB/s)", pol.SpeedLimitKBps)
			}

			discovered[item.Name] = ConnLiveItem{
				ProxyName:      item.Name,
				ProxyType:      pt,
				ClientVersion:  item.ClientVersion,
				Status:         status,
				BlockReason:    blockReason,
				CurConns:       item.CurConns,
				TotalInBytes:   totalIn,
				TotalOutBytes:  totalOut,
				SpeedInBps:     speedIn,
				SpeedOutBps:    speedOut,
				Enabled:        pol.Enabled,
				TrafficLimitMB: pol.TrafficLimitMB,
				SpeedLimitKBps: pol.SpeedLimitKBps,
				ExpiresAt:      pol.ExpiresAt,
				Note:           pol.Note,
				LastSyncAt:     now.UTC().Format(time.RFC3339),
			}
		}
		p.mu.Unlock()
	}

	p.mu.Lock()
	p.conns = discovered
	p.mu.Unlock()
	return len(discovered) > 0
}

func (rs *RuntimeSupervisor) pollClientAdmin(p *managedProc) bool {
	url := fmt.Sprintf("http://127.0.0.1:%d/api/status", p.adminPort)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	req.SetBasicAuth(p.adminUser, p.adminPass)
	resp, err := rs.httpClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	var statusMap map[string][]TunnelLiveStatus
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&statusMap); err != nil {
		return false
	}
	var flat []TunnelLiveStatus
	for _, group := range statusMap {
		flat = append(flat, group...)
	}
	sort.Slice(flat, func(i, j int) bool { return flat[i].Name < flat[j].Name })

	p.mu.Lock()
	p.tunnelStats = flat
	p.mu.Unlock()
	return true
}

func buildMergedConnections(srv ServerProfile, live map[string]ConnLiveItem) []ConnLiveItem {
	merged := make(map[string]ConnLiveItem)
	for name, pol := range srv.ConnPolicies {
		status := "offline"
		reason := ""
		if !pol.Enabled {
			status = "blocked"
			reason = "已手动关闭"
		} else if strings.TrimSpace(pol.ExpiresAt) != "" {
			if exp, err := time.Parse(time.RFC3339, strings.TrimSpace(pol.ExpiresAt)); err == nil && time.Now().After(exp) {
				status = "blocked"
				reason = "限时已到期"
			}
		}
		if reason == "" && pol.TrafficLimitMB > 0 && (pol.RecordedInBytes+pol.RecordedOutBytes) >= pol.TrafficLimitMB*1024*1024 {
			status = "blocked"
			reason = fmt.Sprintf("已达限流 (%d MB)", pol.TrafficLimitMB)
		}
		merged[name] = ConnLiveItem{
			ProxyName:      name,
			ProxyType:      "-",
			Status:         status,
			BlockReason:    reason,
			TotalInBytes:   pol.RecordedInBytes,
			TotalOutBytes:  pol.RecordedOutBytes,
			Enabled:        pol.Enabled,
			TrafficLimitMB: pol.TrafficLimitMB,
			SpeedLimitKBps: pol.SpeedLimitKBps,
			ExpiresAt:      pol.ExpiresAt,
			Note:           pol.Note,
		}
	}
	for name, item := range live {
		merged[name] = item
	}
	out := make([]ConnLiveItem, 0, len(merged))
	for _, v := range merged {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ProxyName < out[j].ProxyName })
	return out
}

func verifyConfigBinary(binPath, cfgPath string, extraEnv []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binPath, "verify", "-c", cfgPath)
	cmd.Env = append(os.Environ(), extraEnv...)
	childprocess.Configure(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(bytes.TrimSpace(out)))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("官方核心配置预检未通过: %s", msg)
	}
	return nil
}

func streamPipeLines(r io.Reader, buf *ringLogBuffer) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 4096), 256*1024)
	for sc.Scan() {
		buf.Append(sc.Text())
	}
}

func pickFreeLoopbackPort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

func randomHex(nBytes int) string {
	b := make([]byte, nBytes)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func stringField(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}
