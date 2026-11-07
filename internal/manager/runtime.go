package manager

import (
	"context"
	"errors"
	"fmt"
	"github.com/Presikid/KurumiFrp/internal/childprocess"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Runtime status reports process liveness, NOT proxy registration or service health.
type RuntimeStatus struct {
	State   string `json:"state"`
	PID     int    `json:"pid"`
	Started string `json:"started"`
	Exit    string `json:"exit"`
	Applied string `json:"applied"`
	Pending bool   `json:"pending"`
}
type process struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	done   chan struct{}
	status RuntimeStatus
	logs   *logBuffer
	coreID string
}
type logBuffer struct {
	mu       sync.Mutex
	pending  []byte
	dropping bool
	lines    []string
	secrets  []string
}

var ansi = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

func redact(s string, secrets []string) string {
	for _, v := range secrets {
		s = strings.ReplaceAll(s, v, "[REDACTED]")
		s = strings.ReplaceAll(s, quote(v), "[REDACTED]")
	}
	return ansi.ReplaceAllString(s, "")
}
func (l *logBuffer) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, c := range b {
		if c == '\n' {
			line := "[oversized log line omitted]"
			if !l.dropping {
				line = redact(string(l.pending), l.secrets)
			}
			l.lines = append(l.lines, line)
			if len(l.lines) > 200 {
				l.lines = l.lines[len(l.lines)-200:]
			}
			l.pending = nil
			l.dropping = false
			continue
		}
		if !l.dropping {
			l.pending = append(l.pending, c)
			if len(l.pending) > 16384 {
				l.pending = nil
				l.dropping = true
			}
		}
	}
	return len(b), nil
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
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "verify", "-c", f.Name())
	cmd.Dir = a.store.dir
	childprocess.Configure(cmd)
	cmd.Env = childEnv(r.Env)
	buf := &logBuffer{secrets: r.Secrets}
	cmd.Stdout = buf
	cmd.Stderr = buf
	cmd.WaitDelay = 2 * time.Second
	e = cmd.Run()
	buf.Write([]byte("\n"))
	out := strings.Join(buf.read(), "\n")
	if ctx.Err() != nil {
		return out, fmt.Errorf("frp 配置检查超时")
	}
	if e != nil {
		return out, fmt.Errorf("frp 拒绝配置：%s", out)
	}
	return out, nil
}
func (a *App) start(i Instance, r Rendered) error {
	if p := a.procs[i.ID]; p != nil && p.view().State == "running" {
		return fmt.Errorf("实例正在运行，请明确选择重启应用")
	}
	path, e := a.binary(i.Kind)
	if e != nil {
		return e
	}
	runtimeDir := filepath.Join(a.store.dir, "runtime")
	if e = os.MkdirAll(runtimeDir, 0700); e != nil {
		return e
	}
	configPath := filepath.Join(runtimeDir, i.ID+".toml")
	if e = atomicWrite(configPath, []byte(r.Text)); e != nil {
		return e
	}
	cmd := exec.Command(path, "-c", configPath)
	childprocess.Configure(cmd)
	cmd.Env = childEnv(r.Env)
	cmd.Dir = runtimeDir
	cmd.WaitDelay = 2 * time.Second
	p := &process{cmd: cmd, done: make(chan struct{}), logs: &logBuffer{secrets: r.Secrets}}
	if a.store.DB.ActiveCore != nil {
		p.coreID = *a.store.DB.ActiveCore
	}
	cmd.Stdout = p.logs
	cmd.Stderr = p.logs
	if e = cmd.Start(); e != nil {
		return fmt.Errorf("无法启动 frp：%w", e)
	}
	p.status = RuntimeStatus{State: "running", PID: cmd.Process.Pid, Started: now(), Applied: r.Digest}
	a.procs[i.ID] = p
	go func() {
		err := cmd.Wait()
		p.logs.Write([]byte("\n"))
		p.mu.Lock()
		p.status.State = "exited"
		p.status.PID = 0
		if err != nil {
			p.status.Exit = err.Error()
		} else {
			p.status.Exit = "exit 0"
		}
		p.mu.Unlock()
		close(p.done)
	}()
	return nil
}
func (a *App) stop(id string) error {
	p := a.procs[id]
	if p == nil || p.view().State != "running" {
		return nil
	}
	if runtime.GOOS == "windows" {
		_ = p.cmd.Process.Kill()
	} else {
		_ = p.cmd.Process.Signal(os.Interrupt)
	}
	select {
	case <-p.done:
		return nil
	case <-time.After(5 * time.Second):
	}
	_ = p.cmd.Process.Kill()
	select {
	case <-p.done:
		return nil
	case <-time.After(3 * time.Second):
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
			coreID := ""
			if a.store.DB.ActiveCore != nil {
				coreID = *a.store.DB.ActiveCore
			}
			v.Pending = e != nil || v.Applied != r.Digest || p.coreID != coreID
		}
		out[i.ID] = v
	}
	return out
}
func (a *App) Close() error {
	a.core.Close()
	a.mu.Lock()
	defer a.mu.Unlock()
	var wg sync.WaitGroup
	var mu sync.Mutex
	var failures []error
	for id := range a.procs {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if err := a.stop(id); err != nil {
				mu.Lock()
				failures = append(failures, err)
				mu.Unlock()
			}
		}(id)
	}
	wg.Wait()
	return errors.Join(failures...)
}

// Keeps the scanner-free writer above tested for secret fragments spanning writes.
var _ interface{ Write([]byte) (int, error) } = (*logBuffer)(nil)
