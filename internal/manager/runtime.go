package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Presikid/KurumiFrp/internal/childprocess"
)

// Runtime status reports process liveness, NOT proxy registration or service health.
type RuntimeStatus struct {
	State   string `json:"state"`
	PID     int    `json:"pid"`
	Started string `json:"started"`
	RunID   string `json:"runId,omitempty"`
	Exit    string `json:"exit"`
	Applied string `json:"applied"`
	Pending bool   `json:"pending"`
}
type process struct {
	mu                      sync.Mutex
	cmd                     *exec.Cmd
	done                    chan struct{}
	status                  RuntimeStatus
	logs                    *logBuffer
	coreID                  string
	appliedConfig           string
	appliedSecretDigest     string
	privateStatus           *privateStatus
	monitorReason           string
	appliedTunnels          map[string]Tunnel
	appliedTunnelSignatures map[string][32]byte
}
type logBuffer struct {
	mu       sync.Mutex
	pending  []byte
	dropping bool
	lines    []string
	secrets  []string // immutable after the first Write; retained for literal fixtures
	redactor *strings.Replacer
}

var ansi = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

func newLogRedactor(secrets []string) *strings.Replacer {
	values := make(map[string]struct{})
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
	for _, secret := range secrets {
		if secret == "" {
			continue // an empty replacement pattern would match between every byte
		}
		add(secret)
		// A raw multiline secret is split by the line-oriented log buffer.
		if strings.Contains(secret, "\n") {
			for _, part := range strings.Split(secret, "\n") {
				add(part)
				add(strings.TrimSuffix(part, "\r"))
			}
		}
		jsonText, _ := json.Marshal(secret)
		add(templateCredential(secret))
		add("\"" + templateCredential(secret) + "\"")
		for _, quoted := range []string{quote(secret), string(jsonText), strconv.Quote(secret)} {
			add(quoted)
			add(quoted[1 : len(quoted)-1])
		}
	}
	ordered := make([]string, 0, len(values))
	for value := range values {
		ordered = append(ordered, value)
	}
	// Replacer resolves matches at the same position by argument order. A short
	// token must not consume the prefix of a longer credential and expose its tail.
	sort.Slice(ordered, func(i, j int) bool {
		if len(ordered[i]) == len(ordered[j]) {
			return ordered[i] < ordered[j]
		}
		return len(ordered[i]) > len(ordered[j])
	})
	pairs := make([]string, 0, 2*len(ordered))
	for _, value := range ordered {
		pairs = append(pairs, value, "[REDACTED]")
	}
	return strings.NewReplacer(pairs...)
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
		if len(l.lines) > 200 {
			l.lines[0] = ""
			l.lines = l.lines[len(l.lines)-200:]
		}
		l.pending = l.pending[:0]
		l.dropping = false
		b = b[newline+1:]
	}
	return written, nil
}
func (l *logBuffer) read() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string{}, l.lines...)
}
func (p *process) view() RuntimeStatus            { p.mu.Lock(); defer p.mu.Unlock(); return p.status }
func (a *App) binary(kind string) (string, error) { return a.core.Binary(kind) }
func childEnv(extra []string) []string {
	out := []string{}
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "KURUMI_") {
			out = append(out, v)
		}
	}
	return append(out, extra...)
}
func (a *App) verify(i Instance, r Rendered) (string, error) {
	bin, e := a.binary(i.Kind)
	if e != nil {
		return "", e
	}
	return a.verifyBinary(context.Background(), bin, r)
}
func (a *App) verifyBinary(parent context.Context, bin string, r Rendered) (string, error) {
	f, e := os.CreateTemp(a.store.dir, "verify-*.toml")
	if e != nil {
		return "", e
	}
	defer os.Remove(f.Name())
	if _, e = f.WriteString(r.Text); e != nil {
		f.Close()
		return "", e
	}
	if e = f.Close(); e != nil {
		return "", e
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "verify", "-c", f.Name())
	cmd.Dir = a.store.dir
	childprocess.Configure(cmd)
	cmd.Env = childEnv(r.Env)
	buf := newLogBuffer(r.Secrets)
	cmd.Stdout = buf
	cmd.Stderr = buf
	cmd.WaitDelay = 2 * time.Second
	e = cmd.Run()
	// A verifier may exit before a child that inherited its output pipe.
	_ = childprocess.Kill(cmd)
	buf.Write([]byte("\n"))
	out := strings.Join(buf.read(), "\n")
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return out, fmt.Errorf("frp 配置检查超时")
	}
	if ctx.Err() != nil {
		return out, fmt.Errorf("frp 配置检查已取消")
	}
	if e != nil {
		return out, fmt.Errorf("frp 拒绝配置：%s", out)
	}
	return out, nil
}
func (a *App) prepareRuntimeConfig(id string, r Rendered) (string, error) {
	runtimeDir := filepath.Join(a.store.dir, "runtime")
	if e := os.MkdirAll(runtimeDir, 0700); e != nil {
		return "", e
	}
	configPath := filepath.Join(runtimeDir, id+".toml")
	if _, e := writeIfChanged(configPath, []byte(r.Text)); e != nil {
		return "", e
	}
	return configPath, nil
}

// start is retained for internal callers which already hold a.mu. HTTP control
// prepares the file outside the lock and calls startPrepared after revalidation.
func (a *App) start(i Instance, r Rendered) error {
	if a.closing {
		return fmt.Errorf("工作台正在关闭")
	}
	if p := a.procs[i.ID]; p != nil && p.view().State == "running" {
		return fmt.Errorf("实例正在运行，请明确选择重启应用")
	}
	path, e := a.binary(i.Kind)
	if e != nil {
		return e
	}
	configPath, e := a.prepareRuntimeConfig(i.ID, r)
	if e != nil {
		return e
	}
	return a.startPrepared(i, r, path, configPath, a.activeCoreID(), nil)
}

// a.mu protects the final launch/publication so a concurrent shutdown, core
// switch or draft edit cannot pass between the snapshot check and cmd.Start.
func (a *App) startPrepared(i Instance, r Rendered, path, configPath, coreID string, status *privateStatus) error {
	if a.closing {
		return fmt.Errorf("工作台正在关闭")
	}
	if p := a.procs[i.ID]; p != nil && p.view().State == "running" {
		return fmt.Errorf("实例正在运行，请明确选择重启应用")
	}
	cmd := exec.Command(path, "-c", configPath)
	childprocess.Configure(cmd)
	cmd.Env = childEnv(r.Env)
	cmd.Dir = filepath.Dir(configPath)
	cmd.WaitDelay = 2 * time.Second
	p := &process{cmd: cmd, done: make(chan struct{}), logs: newLogBuffer(r.Secrets), coreID: coreID, appliedConfig: r.Text, appliedSecretDigest: r.SecretDigest, privateStatus: status, monitorReason: r.MonitorReason, appliedTunnels: map[string]Tunnel{}, appliedTunnelSignatures: map[string][32]byte{}}
	if status != nil {
		p.appliedConfig = status.baseText
	}
	for _, tunnel := range a.store.DB.State.clone().Tunnels {
		if tunnel.InstanceID == i.ID && tunnel.Enabled {
			p.appliedTunnels[tunnel.ID] = tunnel
			if signature, err := a.store.tunnelSignature(tunnel); err == nil {
				p.appliedTunnelSignatures[tunnel.ID] = signature
			}
		}
	}
	cmd.Stdout = p.logs
	cmd.Stderr = p.logs
	if e := cmd.Start(); e != nil {
		return fmt.Errorf("无法启动 frp：%w", e)
	}
	if status != nil {
		status.markStarted(cmd.Process.Pid)
	}
	p.status = RuntimeStatus{State: "running", PID: cmd.Process.Pid, Started: now(), RunID: randomHex(8), Applied: r.Digest}
	a.procs[i.ID] = p
	delete(a.startupErrors, i.ID)
	go func() {
		err := cmd.Wait()
		// Clean the owned process group even when its leader exited first. Do
		// this before publishing completion and allowing a replacement to start.
		_ = childprocess.Kill(cmd)
		status.cleanup()
		p.logs.Write([]byte("\n"))
		p.mu.Lock()
		p.status.State = "exited"
		p.status.PID = 0
		if err != nil {
			p.status.Exit = err.Error()
			if status != nil {
				p.status.Exit += "；如日志显示本机状态端口被占用，可重新启动实例以重新分配端口"
			}
		} else {
			p.status.Exit = "exit 0"
		}
		p.mu.Unlock()
		close(p.done)
	}()
	return nil
}
func (a *App) stop(id string) error {
	a.mu.Lock()
	p := a.procs[id]
	a.mu.Unlock()
	return stopProcess(p)
}
func stopProcess(p *process) error {
	if p == nil || p.view().State != "running" {
		return nil
	}
	if p.cmd == nil || p.cmd.Process == nil {
		return fmt.Errorf("无法访问托管进程")
	}
	_ = childprocess.Terminate(p.cmd)
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-p.done:
		return nil
	case <-timer.C:
	}
	_ = childprocess.Kill(p.cmd)
	timer.Reset(3 * time.Second)
	select {
	case <-p.done:
		return nil
	case <-timer.C:
		return fmt.Errorf("进程停止结果未知，未启动替代进程")
	}
}
func (a *App) statuses() map[string]RuntimeStatus {
	out := map[string]RuntimeStatus{}
	for _, i := range a.store.DB.State.Instances {
		v := RuntimeStatus{State: "stopped"}
		if p := a.procs[i.ID]; p != nil {
			v = p.view()
			r, e := a.store.render(i.ID, false)
			v.Pending = e != nil || v.Applied != r.Digest || p.coreID != a.activeCoreID()
		} else if message := a.startupErrors[i.ID]; i.AutoStart && message != "" {
			v = RuntimeStatus{State: "autostart-failed", Exit: message}
		}
		out[i.ID] = v
	}
	return out
}
func (a *App) Close() error {
	a.closeOnce.Do(func() {
		a.mu.Lock()
		a.closing = true
		if a.startupCancel != nil {
			a.startupCancel()
		}
		for _, op := range a.operations {
			op.cancel()
		}
		a.mu.Unlock()
		if a.startupDone != nil {
			<-a.startupDone
		}
		a.core.Cancel()
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
		a.core.Close()
		a.closeErr = errors.Join(failures...)
	})
	return a.closeErr
}

// Keeps the scanner-free writer above tested for secret fragments spanning writes.
var _ interface{ Write([]byte) (int, error) } = (*logBuffer)(nil)
