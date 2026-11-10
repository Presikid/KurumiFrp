package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RuntimeStatus reports instance liveness, not proxy registration or service
// health. Generation counts admitted runs of an ID, including startup failures;
// RunID also distinguishes generations across App lifetimes.
type RuntimeStatus struct {
	State      string `json:"state"`
	PID        int    `json:"pid"`
	Started    string `json:"started"`
	RunID      string `json:"runId,omitempty"`
	Generation uint64 `json:"generation,omitempty"`
	Exit       string `json:"exit"`
	Applied    string `json:"applied"`
	Pending    bool   `json:"pending"`
}
type process struct {
	mu                  sync.Mutex
	instance            RuntimeInstance
	done                chan struct{}
	status              RuntimeStatus
	logs                *logBuffer
	appliedConfig       string
	appliedDigest       string
	appliedSecretDigest string
	// Set before publication for an in-process client and kept for this run.
	registrationReader      ProxyRegistrationReader
	readiness               RuntimeReadiness
	monitorReason           string
	stopRequested           bool
	upEmitted               bool
	downEmitted             bool
	failedBeforeReady       bool
	appliedTunnels          map[string]Tunnel
	appliedTunnelSignatures map[string][32]byte
}

// An instance remains owned until its Wait and final bookkeeping finish,
// including while it is starting or stopping.
func (p *process) active() bool {
	if p == nil {
		return false
	}
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}
type logBuffer struct {
	mu       sync.Mutex
	pending  []byte
	dropping bool
	lines    []string
	secrets  []string // immutable after the first Write; retained for literal fixtures
	redactor *logRedactor
	sequence uint64
	changed  chan struct{}
}

var ansi = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

type logRedactor struct {
	values []string
	short  []string
}

func newLogRedactor(secrets []string) *logRedactor {
	values := make(map[string]struct{})
	short := make(map[string]struct{})
	add := func(s string) {
		if s != "" {
			values[s] = struct{}{}
		}
		// Logs are stripped of terminal formatting before masking. Also include
		// this form when the credential itself contains an ANSI sequence.
		if clean := ansi.ReplaceAllString(s, ""); clean != "" {
			values[clean] = struct{}{}
		}
	}
	addSecret := func(secret string) {
		if secret == "" {
			return // an empty replacement pattern would match between every byte
		}
		addVariant := func(value string) {
			add(value)
			if len(secret) < 8 && value != "" {
				short[value] = struct{}{}
			}
		}
		addVariant(secret)
		// A raw multiline secret is split by the line-oriented log buffer.
		if strings.Contains(secret, "\n") {
			for _, part := range strings.Split(secret, "\n") {
				addVariant(part)
				addVariant(strings.TrimSuffix(part, "\r"))
			}
		}
		jsonText, _ := json.Marshal(secret)
		addVariant(templateCredential(secret))
		addVariant("\"" + templateCredential(secret) + "\"")
		for _, quoted := range []string{quote(secret), string(jsonText), strconv.Quote(secret)} {
			addVariant(quoted)
			addVariant(quoted[1 : len(quoted)-1])
		}
	}
	for _, secret := range secrets {
		addSecret(secret)
		// HTTP transports can strip surrounding spaces or tabs before a value
		// appears in an error. Treat the remaining nonempty value as sensitive.
		trimmed := strings.Trim(secret, " \t")
		if trimmed != "" && trimmed != secret {
			addSecret(trimmed)
		}
	}
	ordered := make([]string, 0, len(values))
	for value := range values {
		ordered = append(ordered, value)
	}
	shortValues := make([]string, 0, len(short))
	for value := range short {
		shortValues = append(shortValues, value)
	}
	return &logRedactor{values: ordered, short: shortValues}
}

// Replace masks the union of all matching spans. Matching against the original
// line matters: replacing one credential first could hide the start of another
// overlapping credential and leave the latter's tail visible.
func (r *logRedactor) Replace(s string) string {
	// A short value can match ordinary text constantly. Do not silently replace
	// individual characters and make the result look like the original line.
	for _, value := range r.short {
		if strings.Contains(s, value) {
			return "[内容已隐藏：可能包含短密钥]"
		}
	}
	var boundaries []int
	const maxMatches = 32768
	matches := 0
	for _, value := range r.values {
		for start := 0; start < len(s); {
			i := strings.Index(s[start:], value)
			if i < 0 {
				break
			}
			i += start
			matches++
			if matches > maxMatches {
				// Do not expose any fragment when excessive overlaps exhaust the budget.
				return "[REDACTED]"
			}
			if boundaries == nil {
				boundaries = make([]int, len(s)+1)
			}
			boundaries[i]++
			boundaries[i+len(value)]--
			start = i + 1 // include overlapping occurrences of the same value
		}
	}
	if boundaries == nil {
		return s
	}
	var out strings.Builder
	active := 0
	masked := false
	for i := 0; i < len(s); i++ {
		active += boundaries[i]
		if active > 0 {
			if !masked {
				out.WriteString("[REDACTED]")
				masked = true
			}
		} else {
			masked = false
			out.WriteByte(s[i])
		}
	}
	return out.String()
}

func redact(s string, secrets []string) string {
	return newLogRedactor(secrets).Replace(ansi.ReplaceAllString(s, ""))
}
func newLogBuffer(secrets []string) *logBuffer {
	return &logBuffer{secrets: append([]string(nil), secrets...), redactor: newLogRedactor(secrets)}
}
func (l *logBuffer) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.redactor == nil {
		l.redactor = newLogRedactor(l.secrets)
	}

	written := len(b)
	added := false
	for len(b) > 0 {
		newline := bytes.IndexByte(b, '\n')
		chunk := b
		if newline >= 0 {
			chunk = b[:newline]
		}
		if !l.dropping && len(chunk) > 0 {
			if len(chunk) > 16384-len(l.pending) {
				l.pending = nil
				l.dropping = true
			} else {
				l.pending = append(l.pending, chunk...)
			}
		}
		if newline < 0 {
			break
		}

		line := "[oversized log line omitted]"
		if !l.dropping {
			line = l.redactor.Replace(ansi.ReplaceAllString(string(l.pending), ""))
		}
		l.lines = append(l.lines, line)
		l.sequence++
		added = true
		if len(l.lines) > 200 {
			l.lines[0] = ""
			l.lines = l.lines[len(l.lines)-200:]
		}
		l.pending = l.pending[:0]
		l.dropping = false
		b = b[newline+1:]
	}
	// Broadcast once per write without waiting for any network reader.
	if added && l.changed != nil {
		close(l.changed)
		l.changed = nil
	}
	return written, nil
}
func (l *logBuffer) read() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string{}, l.lines...)
}

// updates captures redacted lines and a wakeup atomically. Readers that fall
// behind the retained window must replace their snapshot instead of appending.
func (l *logBuffer) updates(cursor uint64, initial bool) ([]string, uint64, bool, <-chan struct{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.changed == nil {
		l.changed = make(chan struct{})
	}
	oldest := l.sequence - uint64(len(l.lines))
	reset := initial || cursor < oldest || cursor > l.sequence
	start := 0
	if !reset {
		start = int(cursor - oldest)
	}
	return append([]string{}, l.lines[start:]...), l.sequence, reset, l.changed
}
func (p *process) view() RuntimeStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.viewLocked()
}

// viewLocked uses the same readiness observation for every state consumer.
// The caller must hold p.mu.
func (p *process) viewLocked() RuntimeStatus {
	status := p.status
	// The observer goroutine may not have run yet after the engine has exited.
	if status.State == "running" && p.readiness != nil && !p.readiness.ReadyNow() {
		status.State = "stopping"
	}
	// An exited generation is still owned until Wait's final bookkeeping ends.
	// In particular, do not issue a remote start preview before p.done closes.
	if status.State == "exited" && p.active() {
		status.State = "stopping"
	}
	if observer, ok := p.readiness.(runtimeCleanupObserver); ok && p.active() && observer.CleanupUncertain() {
		status.State = "stopping"
		status.Exit = "内嵌核心异常，资源清理状态未知；需退出工作台并核对残留锁文件"
	}
	return status
}

func (a *App) resolveRuntime(kind string) (string, error) {
	return a.runtime.Resolve(kind)
}

func runtimeConfig(i Instance, r Rendered) RuntimeConfig {
	r.Env = append([]string(nil), r.Env...)
	r.Secrets = append([]string(nil), r.Secrets...)
	return RuntimeConfig{InstanceID: i.ID, Kind: i.Kind, Rendered: r, ServerAccessMode: i.ServerAccessMode, ServerAccessList: i.ServerAccessList}
}

func (a *App) verify(i Instance, r Rendered) (string, error) {
	target, e := a.resolveRuntime(i.Kind)
	if e != nil {
		return "", e
	}
	return a.verifyRuntime(context.Background(), target, i, r)
}
func (a *App) verifyRuntime(parent context.Context, target string, i Instance, r Rendered) (string, error) {
	if err := a.checkControlAccessBackend(i); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	output, err := a.runtime.Verify(ctx, target, runtimeConfig(i, r))
	output = redact(output, r.Secrets)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return output, fmt.Errorf("frp 配置检查超时")
	}
	if ctx.Err() != nil {
		return output, fmt.Errorf("frp 配置检查已取消")
	}
	if err != nil {
		return output, errors.New(redact(err.Error(), r.Secrets))
	}
	return output, nil
}
// start is retained for internal callers which already hold a.mu.
func (a *App) start(i Instance, r Rendered) error {
	if a.closing {
		return fmt.Errorf("工作台正在关闭")
	}
	if p := a.procs[i.ID]; p.active() {
		return fmt.Errorf("实例正在运行，请明确选择重启应用")
	}
	target, e := a.resolveRuntime(i.Kind)
	if e != nil {
		return e
	}
	return a.startPrepared(i, r, target)
}

// a.mu protects final publication from concurrent shutdown or draft changes.
func (a *App) startPrepared(i Instance, r Rendered, target string) error {
	return a.startPreparedWithSource(i, r, target, false)
}

// startPreparedWithSource is called with a.mu held. Automatic restoration
// records later asynchronous startup failures against this generation.
func (a *App) startPreparedWithSource(i Instance, r Rendered, target string, autoStart bool) error {
	if a.closing {
		return fmt.Errorf("工作台正在关闭")
	}
	if err := a.checkControlAccessBackend(i); err != nil {
		return err
	}
	if p := a.procs[i.ID]; p.active() {
		return fmt.Errorf("实例正在运行，请明确选择重启应用")
	}
	p := &process{done: make(chan struct{}), logs: newLogBuffer(r.Secrets), appliedConfig: r.Text, appliedDigest: r.Digest, appliedSecretDigest: r.SecretDigest, appliedTunnels: map[string]Tunnel{}, appliedTunnelSignatures: map[string][32]byte{}}
	for _, tunnel := range a.store.DB.State.clone().Tunnels {
		if tunnel.InstanceID == i.ID && tunnel.Enabled {
			p.appliedTunnels[tunnel.ID] = tunnel
			if signature, err := a.store.tunnelSignature(tunnel); err == nil {
				p.appliedTunnelSignatures[tunnel.ID] = signature
			}
		}
	}
	config := runtimeConfig(i, r)
	config.RunID = randomHex(8)
	instance, err := a.runtime.Start(target, config, p.logs)
	if err != nil {
		return fmt.Errorf("无法启动 frp：%s", redact(err.Error(), r.Secrets))
	}
	if instance == nil {
		return fmt.Errorf("无法启动 frp：运行后端未返回实例")
	}
	p.instance = instance
	if reader, ok := instance.(ProxyRegistrationReader); ok && i.Kind == "frpc" {
		p.registrationReader = reader
	} else if i.Kind == "frpc" {
		p.monitorReason = "当前运行后端尚未提供代理注册状态"
	}
	pid := 0
	if info, ok := instance.(RuntimePID); ok {
		pid = info.PID()
	}
	if a.generations == nil {
		a.generations = make(map[string]uint64)
	}
	a.generations[i.ID]++
	state, startedAt, applied := "running", now(), r.Digest
	readiness, hasReadiness := instance.(RuntimeReadiness)
	if hasReadiness {
		state, startedAt, applied = "starting", "", ""
		p.readiness = readiness
	}
	p.status = RuntimeStatus{State: state, PID: pid, Started: startedAt, RunID: config.RunID, Generation: a.generations[i.ID], Applied: applied}
	a.procs[i.ID] = p
	delete(a.startupErrors, i.ID)
	launchRevision := a.store.DB.Revision
	if !hasReadiness && a.webhooks != nil {
		a.webhooks.emitInstance("instance.up", i.ID, i.Kind)
	}
	if !hasReadiness {
		p.upEmitted = true
	}
	if hasReadiness {
		go a.publishRuntimeReady(i, p, readiness)
	}
	if source, ok := instance.(RuntimeTrafficReader); ok {
		go a.captureRuntimeTraffic(i.ID, config.RunID, p, source)
	}
	if source, ok := instance.(RuntimeServerTrafficReader); ok && i.Kind == "frps" {
		go a.captureServerDailyTraffic(i.ID, config.RunID, p, source)
	}
	go func() {
		err := instance.Wait()
		if source, ok := instance.(RuntimeTrafficReader); ok {
			if snapshot := source.ReadTraffic(); snapshot.Available {
				_ = a.RecordTrafficSnapshot(i.ID, config.RunID, snapshot)
			}
		}
		if source, ok := instance.(RuntimeServerTrafficReader); ok && i.Kind == "frps" {
			a.recordServerDailyTraffic(i.ID, config.RunID, p, source.ReadServerTraffic())
		}
		p.logs.Write([]byte("\n"))
		p.mu.Lock()
		wasRunning := p.upEmitted
		// An independent startup or cleanup error remains a failure even if a
		// stop was requested while the engine was exiting or rolling back.
		startupFailed := hasReadiness && !p.upEmitted && (p.failedBeforeReady || !p.stopRequested || (err != nil && !errors.Is(err, context.Canceled)))
		p.status.State = "exited"
		p.status.PID = 0
		if err != nil {
			p.status.Exit = safeStatusText(err.Error(), r.Secrets)
		} else {
			p.status.Exit = "exit 0"
		}
		exitMessage := p.status.Exit
		if a.webhooks != nil && wasRunning && !p.downEmitted {
			a.webhooks.emitInstance("instance.down", i.ID, i.Kind)
			p.downEmitted = true
		} else if a.webhooks != nil && startupFailed {
			a.webhooks.emitInstance("instance.start_failed", i.ID, i.Kind)
		}
		p.mu.Unlock()
		if autoStart && startupFailed {
			a.mu.Lock()
			latest, exists := a.store.DB.State.Instance(i.ID)
			if !a.closing && exists && latest.AutoStart && a.procs[i.ID] == p {
				message := exitMessage
				if err == nil {
					message = "实例在启动完成前退出"
				}
				if a.store.DB.Revision != launchRevision {
					message = "自动恢复期间配置已变化，请检查后手动启动"
				}
				if a.startupErrors == nil {
					a.startupErrors = make(map[string]string)
				}
				a.startupErrors[i.ID] = message
			}
			a.mu.Unlock()
		}
		close(p.done)
	}()
	return nil
}

// Readiness publication races with stop and completion. Keep the generation
// reserved until Wait returns, and never publish readiness after stop starts.
func (a *App) publishRuntimeReady(i Instance, p *process, ready RuntimeReadiness) {
	select {
	case <-ready.Ready():
	case <-ready.Done():
	case <-p.done:
		return
	}
	a.mu.Lock()
	if a.closing || a.procs[i.ID] != p {
		a.mu.Unlock()
		return
	}
	select {
	case <-a.quit:
		a.mu.Unlock()
		return
	default:
	}
	p.mu.Lock()
	if p.status.State == "starting" && !p.stopRequested && ready.ReadyNow() {
		p.status.State = "running"
		p.status.Started = now()
		p.status.Applied = p.appliedDigest
		p.upEmitted = true
		if a.webhooks != nil {
			a.webhooks.emitInstance("instance.up", i.ID, i.Kind)
		}
	}
	p.mu.Unlock()
	a.mu.Unlock()

	select {
	case <-ready.Done():
	case <-p.done:
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.procs[i.ID] != p {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.status.State != "exited" {
		if !p.upEmitted && !p.stopRequested {
			p.failedBeforeReady = true
		}
		p.status.State = "stopping"
		if p.upEmitted && !p.downEmitted && a.webhooks != nil {
			a.webhooks.emitInstance("instance.down", i.ID, i.Kind)
			p.downEmitted = true
		}
	}
}

func (a *App) stop(id string) error {
	a.mu.Lock()
	p := a.procs[id]
	a.mu.Unlock()
	return stopProcess(p)
}
func stopProcess(p *process) error {
	if !p.active() {
		return nil
	}
	p.mu.Lock()
	if !p.stopRequested && !p.upEmitted && p.readiness != nil {
		// Observe failure before recording the first stop request. A second
		// stop must not reinterpret the first stop's exit as startup failure.
		if observer, ok := p.readiness.(runtimeExitObserver); ok {
			p.failedBeforeReady = p.failedBeforeReady || observer.ExitedNow()
		} else {
			select {
			case <-p.readiness.Done():
				p.failedBeforeReady = true
			default:
			}
		}
	}
	p.stopRequested = true
	instance := p.instance
	exited := p.status.State == "exited"
	if _, hasReadiness := instance.(RuntimeReadiness); !exited && hasReadiness {
		p.status.State = "stopping"
	}
	p.mu.Unlock()
	if instance == nil {
		return fmt.Errorf("无法访问托管实例")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if !exited {
		if err := instance.Stop(ctx); err != nil {
			select {
			case <-p.done:
				return nil
			default:
			}
			return fmt.Errorf("实例停止结果未知，未启动替代实例：%s", safeStatusText(err.Error(), p.logs.secrets))
		}
	}
	// A successful stop request is not proof that Wait has returned.
	select {
	case <-p.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("实例停止结果未知，未启动替代实例")
	}
}
func (a *App) statuses() map[string]RuntimeStatus {
	out := map[string]RuntimeStatus{}
	for _, i := range a.store.DB.State.Instances {
		out[i.ID] = a.observedRuntimeStatusLocked(i, a.observeRuntimeLocked(i))
	}
	return out
}
func (a *App) Close() error {
	a.closeOnce.Do(func() {
		a.RequestShutdown()
		a.mu.Lock()
		a.closing = true
		if a.webhooks != nil {
			a.webhooks.close()
		}
		if a.startupCancel != nil {
			a.startupCancel()
		}
		if a.scheduleCancel != nil {
			a.scheduleCancel()
		}
		for _, op := range a.operations {
			op.cancel()
		}
		a.mu.Unlock()
		if a.startupDone != nil {
			<-a.startupDone
		}
		if a.scheduleDone != nil {
			<-a.scheduleDone
		}
		// In-flight control operations must finish before the final process
		// snapshot; no app mutex is held while waiting on them or on children.
		a.operationWG.Wait()
		a.mu.Lock()
		processes := make([]*process, 0, len(a.procs))
		for _, p := range a.procs {
			processes = append(processes, p)
		}
		a.mu.Unlock()
		var wg sync.WaitGroup
		var mu sync.Mutex
		var failures []error
		for _, p := range processes {
			wg.Add(1)
			go func(p *process) {
				defer wg.Done()
				if err := stopProcess(p); err != nil {
					mu.Lock()
					failures = append(failures, err)
					mu.Unlock()
				}
			}(p)
		}
		wg.Wait()
		if a.trafficDaily != nil {
			close(a.trafficDaily.stop)
			<-a.trafficDaily.done
		}
		if len(failures) == 0 {
			if err := a.finishSchedulesAfterShutdown(); err != nil {
				failures = append(failures, err)
			}
		}
		a.closeErr = errors.Join(failures...)
	})
	return a.closeErr
}

// Keeps the scanner-free writer above tested for secret fragments spanning writes.
var _ interface{ Write([]byte) (int, error) } = (*logBuffer)(nil)
