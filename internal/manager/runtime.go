package manager

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
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
	line = stripANSI(line)
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
	generation   uint64
	id           string
	kind         string // "frps" or "frpc"
	cfgPath      string
	adminPort    int
	adminUser    string
	adminPass    string
	pluginSecret string
	commonSig    string
	procEnv      []string
	cmd          *exec.Cmd
	startedAt    time.Time
	done         chan struct{}
	logs         *ringLogBuffer

	mu            sync.Mutex
	state         string
	lastErr       string
	conns         map[string]ConnLiveItem
	rateSamples   map[string]proxyRateSample
	remoteClients map[string]*remoteClientSession
	tunnelStats   []TunnelLiveStatus
}

// RuntimeSupervisor manages official frps & frpc child processes, Connection Dashboard polling, and Remote Instance Bridges.
type RuntimeSupervisor struct {
	lifecycleMu  sync.Mutex // serialize process ownership across configuration switches and core updates
	mu           sync.RWMutex
	dataDir      string
	runtimeDir   string
	store        *Store
	procs        map[string]*managedProc
	historyLogs  map[string]*ringLogBuffer
	lastErrors   map[string]instanceError
	callbackAddr atomic.Value // stores string "127.0.0.1:<port>"
	onChange     func()
	stopPollCh   chan struct{}
	httpClient   *http.Client

	agentLn   net.Listener
	agentSrv  *http.Server
	agentPort int
	bridgeMu  sync.Mutex
	bridges   map[string]*visitorBridge
}

type instanceError struct {
	generation uint64
	message    string
}

// NewRuntimeSupervisor creates the official FRP process supervisor and local loopback agent server.
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
		lastErrors:  make(map[string]instanceError),
		onChange:    onChange,
		stopPollCh:  make(chan struct{}),
		httpClient:  &http.Client{Timeout: 2 * time.Second},
		bridges:     make(map[string]*visitorBridge),
	}
	if err := rs.startLoopbackAgentServer(); err != nil {
		return nil, fmt.Errorf("启动本地远控代理服务失败: %w", err)
	}
	go rs.pollLoop()
	return rs, nil
}

// SetCallbackAddr records the local HTTP listener address so frps HTTP plugins can call back into KurumiFrp.
func (rs *RuntimeSupervisor) SetCallbackAddr(addr string) {
	rs.callbackAddr.Store(addr)
}

// CallbackAddr returns the currently stored local HTTP callback address.
func (rs *RuntimeSupervisor) CallbackAddr() string {
	v, _ := rs.callbackAddr.Load().(string)
	return v
}

// AgentPort returns the persistent internal loopback HTTP server port.
func (rs *RuntimeSupervisor) AgentPort() int {
	return rs.agentPort
}

// switchConfig settles the last sample while the old file is active. A failed
// activation leaves its running instances intact. Late polls carry their original generation.
func (rs *RuntimeSupervisor) switchConfig(activate func() error) error {
	rs.lifecycleMu.Lock()
	defer rs.lifecycleMu.Unlock()
	rs.mu.RLock()
	ids := make([]string, 0, len(rs.procs))
	for id := range rs.procs {
		ids = append(ids, id)
	}
	rs.mu.RUnlock()
	for _, id := range ids {
		if err := rs.flushServerTrafficSnapshot(id); err != nil {
			return err
		}
	}
	if err := activate(); err != nil {
		return err
	}
	if err := rs.resetForConfigSwitch(ids); err != nil {
		rs.notifyChange()
		return fmt.Errorf("新方案已启用，但停止旧实例失败: %w", err)
	}
	rs.scheduleAutoStarts()
	return nil
}

func (rs *RuntimeSupervisor) resetForConfigSwitch(ids []string) error {
	var stopErrors []error
	for _, id := range ids {
		if err := rs.stopInstance(id); err != nil {
			stopErrors = append(stopErrors, err)
		}
	}
	if err := errors.Join(stopErrors...); err != nil {
		return err
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

	rs.mu.Lock()
	rs.historyLogs = make(map[string]*ringLogBuffer)
	rs.lastErrors = make(map[string]instanceError)
	rs.mu.Unlock()
	return nil
}

func (rs *RuntimeSupervisor) notifyChange() {
	if rs.onChange != nil {
		rs.onChange()
	}
}

// StartAutoStarts launches all ServerProfiles and ClientProfiles configured with AutoStart: true.
func (rs *RuntimeSupervisor) StartAutoStarts() {
	rs.lifecycleMu.Lock()
	defer rs.lifecycleMu.Unlock()
	rs.scheduleAutoStarts()
}

func (rs *RuntimeSupervisor) scheduleAutoStarts() {
	generation := rs.store.activeGeneration()
	go func() {
		rs.lifecycleMu.Lock()
		defer rs.lifecycleMu.Unlock()
		if generation != rs.store.activeGeneration() {
			return
		}
		select {
		case <-rs.stopPollCh:
			return
		default:
		}
		_, doc := rs.store.Snapshot()
		for _, srv := range doc.Servers {
			if srv.AutoStart {
				rs.recordStartError(srv.ID, rs.startServer(srv))
			}
		}
		for _, cli := range doc.Clients {
			if cli.AutoStart {
				rs.recordStartError(cli.ID, rs.startClient(cli))
			}
		}
	}()
}

func (rs *RuntimeSupervisor) recordStartError(id string, err error) {
	if err != nil {
		failure := instanceError{generation: rs.store.activeGeneration(), message: err.Error()}
		rs.mu.Lock()
		rs.lastErrors[id] = failure
		rs.mu.Unlock()
		rs.notifyChange()
	}
}

// HotSwapBinaries temporarily stops running frps/frpc instances so binary files are unlocked on Windows,
// invokes swapFn to activate the staged binaries, and restores previously running instances.
func (rs *RuntimeSupervisor) HotSwapBinaries(swapFn func() error, rollback ...func() error) (int, error) {
	rs.lifecycleMu.Lock()
	defer rs.lifecycleMu.Unlock()
	rs.mu.RLock()
	activeIDs := make(map[string]bool, len(rs.procs))
	for id, p := range rs.procs {
		p.mu.Lock()
		st := p.state
		p.mu.Unlock()
		if st == "running" || st == "starting" {
			activeIDs[id] = true
		}
	}
	rs.mu.RUnlock()

	var failures []error
	stopped := make(map[string]bool)
	for id := range activeIDs {
		if err := rs.stopInstance(id); err != nil {
			failures = append(failures, fmt.Errorf("停止 %s: %w", id, err))
		} else {
			stopped[id] = true
		}
	}
	activated := false
	if len(failures) == 0 {
		if err := swapFn(); err != nil {
			failures = append(failures, err)
		} else {
			activated = true
		}
	}
	_, doc := rs.store.Snapshot()
	restarted, restoreErr := rs.restoreInstances(doc, stopped)
	if restoreErr != nil {
		failures = append(failures, restoreErr)
		if activated && len(rollback) != 0 && rollback[0] != nil {
			// The slot switch does not touch running executables. Keep healthy
			// instances alive until the old binaries can verify their configs.
			if err := rollback[0](); err != nil {
				failures = append(failures, fmt.Errorf("回退核心版本失败: %w", err))
			} else {
				restarted, restoreErr = rs.restoreInstances(doc, stopped)
				if restoreErr != nil {
					failures = append(failures, fmt.Errorf("恢复旧版本实例失败: %w", restoreErr))
				}
				failures = append(failures, errors.New("已回退到原核心版本"))
			}
		}
	}
	rs.notifyChange()
	if len(failures) > 0 {
		return restarted, fmt.Errorf("原有 %d 个实例，已恢复 %d 个: %w", len(activeIDs), restarted, errors.Join(failures...))
	}
	return restarted, nil
}

func (rs *RuntimeSupervisor) restoreInstances(doc ConfigDocument, ids map[string]bool) (int, error) {
	pending := maps.Clone(ids)
	restarted := 0
	var failures []error
	restore := func(id string, err error) {
		if err == nil {
			err = rs.waitForAdmin(id)
		}
		if err != nil {
			rs.recordStartError(id, err)
			failures = append(failures, fmt.Errorf("恢复 %s: %w", id, err))
		} else {
			restarted++
		}
		delete(pending, id)
	}
	for _, srv := range doc.Servers {
		if pending[srv.ID] {
			restore(srv.ID, rs.startServer(srv))
		}
	}
	for _, cli := range doc.Clients {
		if pending[cli.ID] {
			restore(cli.ID, rs.startClient(cli))
		}
	}
	for id := range pending {
		failures = append(failures, fmt.Errorf("无法恢复 %s，活动配置中缺少此实例", id))
	}
	return restarted, errors.Join(failures...)
}

func (rs *RuntimeSupervisor) waitForAdmin(id string) error {
	rs.mu.RLock()
	p := rs.procs[id]
	rs.mu.RUnlock()
	if p == nil {
		return errors.New("核心进程已退出")
	}
	suffix := "/api/status"
	if p.kind == "frps" {
		suffix = "/api/serverinfo"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d%s", p.adminPort, suffix), nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(p.adminUser, p.adminPass)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		resp, err := rs.httpClient.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				select {
				case <-p.done:
					return errors.New("核心进程已退出")
				default:
					return nil
				}
			}
		}
		select {
		case <-p.done:
			return errors.New("核心进程已退出")
		case <-ctx.Done():
			return errors.New("核心管理端口未就绪")
		case <-ticker.C:
		}
	}
}

// StopAll gracefully terminates all running frps and frpc child processes and internal visitor bridges.
func (rs *RuntimeSupervisor) StopAll() {
	rs.lifecycleMu.Lock()
	defer rs.lifecycleMu.Unlock()
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
			_ = rs.stopInstance(pid)
		}(id)
	}
	wg.Wait()
	rs.stopLoopbackAgentAndBridges()
}

// IsInstanceRunning reports whether an frps or frpc instance is currently active.
func (rs *RuntimeSupervisor) IsInstanceRunning(id string) bool {
	rs.mu.RLock()
	p, ok := rs.procs[id]
	rs.mu.RUnlock()
	if !ok || p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state == "running"
}

// StartServer pre-verifies new configuration on a staging file BEFORE stopping any live frps instance (F04),
// and injects pluginSecret as a clean REST path segment to avoid double-'?' query truncation (F01).
func (rs *RuntimeSupervisor) StartServer(srv ServerProfile) error {
	rs.lifecycleMu.Lock()
	defer rs.lifecycleMu.Unlock()
	return rs.startServer(srv)
}

func (rs *RuntimeSupervisor) startServer(srv ServerProfile) error {
	select {
	case <-rs.stopPollCh:
		return errors.New("服务正在关闭")
	default:
	}
	if _, err := ValidateAllowPortsSpec(srv.AllowPorts); err != nil {
		return err
	}
	bins := LocateBinaries(rs.dataDir)
	if !bins.FrpsFound {
		return errors.New("未找到内置 frps 核心程序，请确认安装包完整或将官方 frps 放入 data/bin 目录")
	}

	adminPort, err := pickFreeLoopbackPort()
	if err != nil {
		return fmt.Errorf("分配本地管理端口失败: %w", err)
	}
	adminPass := randomHex(16)
	pluginSecret := randomHex(20)
	cbAddr := rs.CallbackAddr()
	if rs.agentPort > 0 {
		cbAddr = fmt.Sprintf("127.0.0.1:%d", rs.agentPort)
	}

	rendered := RenderServerTOML(srv, ServerRuntimeInjections{
		Preview:        false,
		AdminPort:      adminPort,
		AdminUser:      "kurumi",
		AdminPass:      adminPass,
		KurumiCallback: cbAddr,
		PluginPath:     fmt.Sprintf("/internal/frps-plugin/%s/%s", srv.ID, pluginSecret),
	})

	stagingPath := filepath.Join(rs.runtimeDir, srv.ID+".staging.toml")
	if _, err := writeIfChanged(stagingPath, []byte(rendered.TOML), 0o600); err != nil {
		return fmt.Errorf("写入预检配置失败: %w", err)
	}
	if err := verifyConfigBinary(bins.FrpsPath, stagingPath, rendered.Env); err != nil {
		_ = os.Remove(stagingPath)
		return err
	}

	// Pre-verification passed: flush traffic counters & stop old instance before swapping live config
	if err := rs.stopInstance(srv.ID); err != nil {
		return err
	}

	cfgPath := filepath.Join(rs.runtimeDir, srv.ID+".toml")
	if err := replaceFileAtomic(stagingPath, cfgPath, 0o600); err != nil {
		_ = os.Remove(stagingPath)
		return fmt.Errorf("激活运行配置失败: %w", err)
	}

	return rs.spawnProcess(srv.ID, "frps", bins.FrpsPath, cfgPath, rendered, "", adminPort, "kurumi", adminPass, pluginSecret)
}

// StartClient pre-verifies new configuration on a staging file BEFORE stopping any live frpc instance.
func (rs *RuntimeSupervisor) StartClient(cli ClientProfile) error {
	rs.lifecycleMu.Lock()
	defer rs.lifecycleMu.Unlock()
	return rs.startClient(cli)
}

func (rs *RuntimeSupervisor) startClient(cli ClientProfile) error {
	select {
	case <-rs.stopPollCh:
		return errors.New("服务正在关闭")
	default:
	}
	bins := LocateBinaries(rs.dataDir)
	if !bins.FrpcFound {
		return errors.New("未找到内置 frpc 核心程序，请确认安装包完整或将官方 frpc 放入 data/bin 目录")
	}

	adminPort, err := pickFreeLoopbackPort()
	if err != nil {
		return fmt.Errorf("分配本地管理端口失败: %w", err)
	}
	adminPass := randomHex(16)

	rendered := RenderClientTOML(cli, ClientRuntimeInjections{
		Preview:    false,
		AdminPort:  adminPort,
		AdminUser:  "kurumi",
		AdminPass:  adminPass,
		AgentPort:  rs.agentPort,
		ClientID:   cli.ID,
		ClientName: cli.Name,
		CtrlSecret: cli.CtrlSecret,
		AgentToken: cli.AgentToken,
	})

	stagingPath := filepath.Join(rs.runtimeDir, cli.ID+".staging.toml")
	if _, err := writeIfChanged(stagingPath, []byte(rendered.TOML), 0o600); err != nil {
		return fmt.Errorf("写入预检配置失败: %w", err)
	}
	if err := verifyConfigBinary(bins.FrpcPath, stagingPath, rendered.Env); err != nil {
		_ = os.Remove(stagingPath)
		return err
	}

	if err := rs.stopInstance(cli.ID); err != nil {
		return err
	}

	cfgPath := filepath.Join(rs.runtimeDir, cli.ID+".toml")
	if err := replaceFileAtomic(stagingPath, cfgPath, 0o600); err != nil {
		_ = os.Remove(stagingPath)
		return fmt.Errorf("激活运行配置失败: %w", err)
	}

	return rs.spawnProcess(cli.ID, "frpc", bins.FrpcPath, cfgPath, rendered, clientCommonSignature(cli), adminPort, "kurumi", adminPass, "")
}

// ReloadOrRestartClient uses official frpc /api/reload ONLY when common non-proxy settings AND secret env slice are unchanged (F02, F03);
// otherwise it gracefully restarts the subprocess after pre-verification.
func (rs *RuntimeSupervisor) ReloadOrRestartClient(cli ClientProfile) error {
	rs.lifecycleMu.Lock()
	defer rs.lifecycleMu.Unlock()
	return rs.reloadOrRestartClient(cli)
}

func (rs *RuntimeSupervisor) reloadOrRestartClient(cli ClientProfile) error {
	rs.mu.RLock()
	p, running := rs.procs[cli.ID]
	rs.mu.RUnlock()
	if !running || p == nil {
		return nil
	}

	p.mu.Lock()
	adminPort := p.adminPort
	adminUser := p.adminUser
	adminPass := p.adminPass
	cfgPath := p.cfgPath
	oldSig := p.commonSig
	oldEnv := append([]string(nil), p.procEnv...)
	p.mu.Unlock()

	newSig := clientCommonSignature(cli)
	if oldSig != newSig {
		return rs.startClient(cli)
	}

	bins := LocateBinaries(rs.dataDir)
	if !bins.FrpcFound {
		return errors.New("未找到 frpc 核心程序")
	}

	rendered := RenderClientTOML(cli, ClientRuntimeInjections{
		Preview:    false,
		AdminPort:  adminPort,
		AdminUser:  adminUser,
		AdminPass:  adminPass,
		AgentPort:  rs.agentPort,
		ClientID:   cli.ID,
		ClientName: cli.Name,
		CtrlSecret: cli.CtrlSecret,
		AgentToken: cli.AgentToken,
	})
	if !equalStringSlices(oldEnv, rendered.Env) {
		return rs.startClient(cli)
	}

	stagingPath := cfgPath + ".staging"
	if _, err := writeIfChanged(stagingPath, []byte(rendered.TOML), 0o600); err != nil {
		return err
	}
	if err := verifyConfigBinary(bins.FrpcPath, stagingPath, rendered.Env); err != nil {
		_ = os.Remove(stagingPath)
		return err
	}
	if err := replaceFileAtomic(stagingPath, cfgPath, 0o600); err != nil {
		_ = os.Remove(stagingPath)
		return err
	}

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
	return rs.startClient(cli)
}

func clientCommonSignature(cli ClientProfile) string {
	cp := cli
	cp.Tunnels = nil
	cp.StartProxies = nil
	cp.CoverAsset = ""
	cp.Note = ""
	cp.AutoStart = false
	b, _ := json.Marshal(cp)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (rs *RuntimeSupervisor) spawnProcess(id, kind, binPath, cfgPath string, rendered RenderedConfig, commonSig string, adminPort int, adminUser, adminPass, pluginSecret string) error {
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
		generation:    rs.store.activeGeneration(),
		id:            id,
		kind:          kind,
		cfgPath:       cfgPath,
		adminPort:     adminPort,
		adminUser:     adminUser,
		adminPass:     adminPass,
		pluginSecret:  pluginSecret,
		commonSig:     commonSig,
		procEnv:       append([]string(nil), rendered.Env...),
		cmd:           cmd,
		startedAt:     time.Now(),
		done:          make(chan struct{}),
		logs:          logs,
		state:         "running",
		conns:         make(map[string]ConnLiveItem),
		rateSamples:   make(map[string]proxyRateSample),
		remoteClients: make(map[string]*remoteClientSession),
	}

	logs.Append(fmt.Sprintf("[KurumiFrp] 已启动官方 %s 核心 (PID=%d)", kind, cmd.Process.Pid))

	rs.mu.Lock()
	delete(rs.lastErrors, id)
	rs.procs[id] = proc
	rs.historyLogs[id] = logs
	rs.mu.Unlock()
	rs.notifyChange()

	go streamPipeLines(stdout, logs)
	go streamPipeLines(stderr, logs)
	go func() {
		waitErr := cmd.Wait()
		close(proc.done)
		var exitErrMsg string
		proc.mu.Lock()
		if waitErr != nil && proc.state != "stopping" {
			proc.state = "error"
			exitErrMsg = waitErr.Error()
			proc.lastErr = exitErrMsg
			logs.Append(fmt.Sprintf("[KurumiFrp] %s 异常退出: %v", kind, waitErr))
		} else {
			proc.state = "stopped"
			logs.Append(fmt.Sprintf("[KurumiFrp] %s 已停止", kind))
		}
		proc.mu.Unlock()

		rs.mu.Lock()
		if cur, exists := rs.procs[id]; exists && cur == proc {
			delete(rs.procs, id)
			if exitErrMsg != "" {
				rs.lastErrors[id] = instanceError{generation: proc.generation, message: exitErrMsg}
			}
		}
		rs.mu.Unlock()
		rs.notifyChange()
	}()

	return nil
}

// StopInstance stops a running frps or frpc instance outside the global mutex so UI requests never hang.
func (rs *RuntimeSupervisor) StopInstance(id string) error {
	rs.lifecycleMu.Lock()
	defer rs.lifecycleMu.Unlock()
	return rs.stopInstance(id)
}

func (rs *RuntimeSupervisor) stopInstance(id string) error {
	rs.flushServerTrafficSnapshot(id)
	rs.mu.Lock()
	delete(rs.lastErrors, id)
	p, ok := rs.procs[id]
	rs.mu.Unlock()
	// unlocked lookup completed above
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
			return fmt.Errorf("等待实例 %s 退出超时", id)
		}
	}

	rs.mu.Lock()
	if cur, exists := rs.procs[id]; exists && cur == p {
		delete(rs.procs, id)
	}
	rs.mu.Unlock()
	rs.terminateBridgesForServer(id)
	rs.notifyChange()
	return nil
}

// Views returns live runtime views for all ServerProfiles and ClientProfiles in doc.
func (rs *RuntimeSupervisor) Views(doc ConfigDocument) map[string]ProcessRuntimeView {
	return rs.viewsForGeneration(doc, rs.store.activeGeneration())
}

func (rs *RuntimeSupervisor) viewsForGeneration(doc ConfigDocument, generation uint64) map[string]ProcessRuntimeView {
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
		if p := rs.procs[srv.ID]; p != nil && p.generation == generation {
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
			view.RemoteClients = buildRemoteClientSummaries(srv.ID, p)
			p.mu.Unlock()
		} else if lastErr, ok := rs.lastErrors[srv.ID]; ok && lastErr.generation == generation {
			view.State = "error"
			view.LastError = lastErr.message
		}
		out[srv.ID] = view
	}

	for _, cli := range doc.Clients {
		view := ProcessRuntimeView{
			ID:    cli.ID,
			Kind:  "frpc",
			State: "stopped",
		}
		if p := rs.procs[cli.ID]; p != nil && p.generation == generation {
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
		} else if lastErr, ok := rs.lastErrors[cli.ID]; ok && lastErr.generation == generation {
			view.State = "error"
			view.LastError = lastErr.message
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
// Connection Dashboard controls AND track connected KurumiFrp client instances for remote management.
func (rs *RuntimeSupervisor) HandleFRPSPlugin(w http.ResponseWriter, r *http.Request, routeRest string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	serverID := strings.Trim(routeRest, "/")
	pathToken := ""
	if slashIdx := strings.IndexByte(serverID, '/'); slashIdx > 0 {
		pathToken = serverID[slashIdx+1:]
		serverID = serverID[:slashIdx]
	}
	rs.mu.RLock()
	proc, ok := rs.procs[serverID]
	rs.mu.RUnlock()
	if !ok || proc == nil || proc.kind != "frps" || proc.pluginSecret == "" {
		http.Error(w, "unknown server instance", http.StatusNotFound)
		return
	}
	gotToken := pathToken
	if gotToken == "" {
		gotToken = r.URL.Query().Get("token")
		// Defensive strip if legacy query got '?op=' concatenated by upstream frps
		if qIdx := strings.IndexByte(gotToken, '?'); qIdx >= 0 {
			gotToken = gotToken[:qIdx]
		}
	}
	if subtle.ConstantTimeCompare([]byte(gotToken), []byte(proc.pluginSecret)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	op := r.URL.Query().Get("op")
	var reqPayload struct {
		Version string         `json:"version"`
		Op      string         `json:"op"`
		Content map[string]any `json:"content"`
	}
	if err := decodeJSONBody(r, &reqPayload, 1<<20); err != nil || reqPayload.Content == nil ||
		(op != "" && reqPayload.Op != "" && op != reqPayload.Op) {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	if op == "" {
		op = strings.TrimSpace(reqPayload.Op)
	}
	if op == "" {
		op = stringField(reqPayload.Content, "op")
	}
	if op == "" {
		http.Error(w, "missing plugin operation", http.StatusBadRequest)
		return
	}

	rs.recordRemoteClientEvent(serverID, proc, op, reqPayload.Content)

	proxyName := stringField(reqPayload.Content, "proxy_name")
	if proxyName != "" && !isInternalCtrlProxy(proxyName) && (op == "NewProxy" || op == "NewUserConn") {
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
	rs.reapStaleBridges()
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
	proxyTypes := []string{"tcp", "udp", "http", "https", "stcp", "sudp", "xtcp", "tcpmux"}
	now := time.Now()
	discovered := make(map[string]ConnLiveItem)
	sampledTypes := make(map[string]bool)
	ctrlStatus := make(map[string]bool)
	var pendingPolicyUpdates []string

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
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			continue
		}
		var payload struct {
			Proxies []struct {
				Name            string `json:"name"`
				TodayTrafficIn  int64  `json:"todayTrafficIn"`
				TodayTrafficOut int64  `json:"todayTrafficOut"`
				CurConns        int64  `json:"curConns"`
				Status          string `json:"status"`
				ClientVersion   string `json:"clientVersion"`
			} `json:"proxies"`
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&payload)
		resp.Body.Close()
		if err != nil || payload.Proxies == nil {
			continue
		}

		sampledTypes[pt] = true
		p.mu.Lock()
		for _, item := range payload.Proxies {
			if item.Name == "" {
				continue
			}
			if isInternalCtrlProxy(item.Name) {
				ctrlStatus[item.Name] = (item.Status == "online")
				continue
			}
			pol, hasPol := srv.ConnPolicies[item.Name]
			if !hasPol {
				pol = ConnPolicy{ProxyName: item.Name, Enabled: true}
			}
			deltaIn := trafficDelta(item.TodayTrafficIn, pol.LastRawInBytes)
			deltaOut := trafficDelta(item.TodayTrafficOut, pol.LastRawOutBytes)
			totalIn := pol.RecordedInBytes + deltaIn
			totalOut := pol.RecordedOutBytes + deltaOut
			if (deltaIn+deltaOut) >= 65536 || item.TodayTrafficIn < pol.LastRawInBytes || item.TodayTrafficOut < pol.LastRawOutBytes {
				pendingPolicyUpdates = append(pendingPolicyUpdates, item.Name)
			}

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

	if len(sampledTypes) == 0 {
		return false
	}
	p.mu.Lock()
	previousCount := len(p.conns)
	for name, item := range p.conns {
		if !sampledTypes[item.ProxyType] {
			if _, refreshed := discovered[name]; !refreshed {
				discovered[name] = item
			}
		}
	}
	p.conns = discovered
	for _, sess := range p.remoteClients {
		if online, seen := ctrlStatus[sess.ctrlProxyName]; seen {
			sess.ctrlReady = online
			sess.online = online
			if online {
				sess.lastPingAt = now
			}
		}
	}
	p.mu.Unlock()
	if len(pendingPolicyUpdates) > 0 {
		rs.persistTrafficSamples(p, pendingPolicyUpdates)
	}
	return previousCount > 0 || len(discovered) > 0 || len(ctrlStatus) > 0
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

	if resp.StatusCode != http.StatusOK {
		return false
	}
	var statusMap map[string][]TunnelLiveStatus
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&statusMap); err != nil {
		return false
	}
	var flat []TunnelLiveStatus
	for _, group := range statusMap {
		for _, item := range group {
			if isInternalCtrlProxy(item.Name) {
				continue
			}
			flat = append(flat, item)
		}
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
		if isInternalCtrlProxy(name) {
			continue
		}
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
		if !isInternalCtrlProxy(name) {
			merged[name] = item
		}
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

func stripANSI(s string) string {
	var out strings.Builder
	out.Grow(len(s))
	n := len(s)
	for i := 0; i < n; i++ {
		if s[i] == 0x1b && i+1 < n && s[i+1] == '[' {
			i += 2
			for i < n && ((s[i] >= '0' && s[i] <= '9') || s[i] == ';') {
				i++
			}
			continue
		}
		if s[i] == '[' && i+2 < n && s[i+1] >= '0' && s[i+1] <= '9' {
			j := i + 1
			for j < n && ((s[j] >= '0' && s[j] <= '9') || s[j] == ';') {
				j++
			}
			if j < n && s[j] == 'm' && j-i <= 8 {
				i = j
				continue
			}
		}
		out.WriteByte(s[i])
	}
	return out.String()
}

func trafficDelta(raw, previous int64) int64 {
	if raw < previous {
		return maxInt64(0, raw) // the daily counter rolled over
	}
	return raw - previous
}

// A policy reset records the current raw counters under the store lock. Rebase
// each persistence operation on that same checkpoint, including shutdown flushes.
func (rs *RuntimeSupervisor) persistTrafficSamples(p *managedProc, names []string) error {
	rs.store.mu.Lock()
	defer rs.store.mu.Unlock()
	rs.mu.RLock()
	owner := rs.procs[p.id]
	rs.mu.RUnlock()
	if owner != p || p.generation != rs.store.generation {
		return nil
	}
	p.mu.Lock()
	if names == nil {
		for name := range p.conns {
			names = append(names, name)
		}
	}
	updates := make(map[string]proxyRateSample, len(names))
	for _, name := range names {
		if sample, ok := p.rateSamples[name]; ok {
			updates[name] = sample
		}
	}
	p.mu.Unlock()
	if len(updates) == 0 {
		return nil
	}
	return rs.store.updateLocked(func(doc *ConfigDocument) error {
		for i := range doc.Servers {
			if doc.Servers[i].ID == p.id {
				if doc.Servers[i].ConnPolicies == nil {
					doc.Servers[i].ConnPolicies = make(map[string]ConnPolicy)
				}
				for name, sample := range updates {
					pol, ok := doc.Servers[i].ConnPolicies[name]
					if !ok {
						pol = ConnPolicy{ProxyName: name, Enabled: true}
					}
					pol.RecordedInBytes += trafficDelta(sample.inBytes, pol.LastRawInBytes)
					pol.RecordedOutBytes += trafficDelta(sample.outBytes, pol.LastRawOutBytes)
					pol.LastRawInBytes = sample.inBytes
					pol.LastRawOutBytes = sample.outBytes
					doc.Servers[i].ConnPolicies[name] = pol
				}
				return nil
			}
		}
		return nil
	})
}

// CurrentProxyRawTraffic returns the latest sampled raw todayTrafficIn/Out for a proxy on a running frps instance.
func (rs *RuntimeSupervisor) CurrentProxyRawTraffic(serverID, proxyName string) (int64, int64) {
	rs.mu.RLock()
	p, ok := rs.procs[serverID]
	rs.mu.RUnlock()
	if !ok || p == nil {
		return 0, 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if s, ok := p.rateSamples[proxyName]; ok {
		return s.inBytes, s.outBytes
	}
	return 0, 0
}

func (rs *RuntimeSupervisor) flushServerTrafficSnapshot(serverID string) error {
	rs.mu.RLock()
	p, ok := rs.procs[serverID]
	rs.mu.RUnlock()
	if !ok || p == nil {
		return nil
	}
	return rs.persistTrafficSamples(p, nil)
}

// ClearInstanceError clears retained crash errors, buffered logs, and runtime TOML artifacts when an instance profile is deleted (A08).
func (rs *RuntimeSupervisor) ClearInstanceError(id string) {
	rs.mu.Lock()
	delete(rs.lastErrors, id)
	delete(rs.historyLogs, id)
	rs.mu.Unlock()
	if strings.TrimSpace(id) != "" && filepath.Base(id) == id {
		_ = os.Remove(filepath.Join(rs.runtimeDir, id+".toml"))
		_ = os.Remove(filepath.Join(rs.runtimeDir, id+".staging.toml"))
		_ = os.Remove(filepath.Join(rs.runtimeDir, id+".toml.staging"))
	}
}
