package manager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Presikid/KurumiFrp/internal/childprocess"
	"github.com/Presikid/KurumiFrp/internal/core"
)

// externalRuntime preserves the existing core while the in-process adapter is
// developed separately. No subprocess details are needed by RuntimeInstance's
// manager or by an injected backend.
type externalRuntime struct {
	core *core.Manager
	dir  string
}

func (b *externalRuntime) Resolve(kind, _ string) (string, error) {
	return b.core.Binary(kind)
}

func childEnv(extra []string) []string {
	out := []string{}
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "KURUMI_") {
			out = append(out, v)
		}
	}
	return append(out, extra...)
}

func (b *externalRuntime) Verify(parent context.Context, target string, config RuntimeConfig) (string, error) {
	if config.ServerAccessMode != "" || strings.TrimSpace(config.ServerAccessList) != "" {
		return "", fmt.Errorf("控制入口来源限制仅支持内嵌服务端")
	}
	f, err := os.CreateTemp(b.dir, "verify-*.toml")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	if _, err = f.WriteString(config.Rendered.Text); err != nil {
		f.Close()
		return "", err
	}
	if err = f.Close(); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, target, "verify", "-c", f.Name())
	cmd.Dir = b.dir
	childprocess.Configure(cmd)
	cmd.Env = childEnv(config.Rendered.Env)
	buf := newLogBuffer(config.Rendered.Secrets)
	cmd.Stdout = buf
	cmd.Stderr = buf
	cmd.WaitDelay = 2 * time.Second
	err = cmd.Run()
	// A verifier may exit before a child that inherited its output pipe.
	_ = childprocess.Kill(cmd)
	_, _ = buf.Write([]byte("\n"))
	out := strings.Join(buf.read(), "\n")
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return out, fmt.Errorf("frp 配置检查超时")
	}
	if ctx.Err() != nil {
		return out, fmt.Errorf("frp 配置检查已取消")
	}
	if err != nil {
		if strings.TrimSpace(out) == "" {
			return out, fmt.Errorf("frp 配置检查失败（%v），核心未提供详细信息", err)
		}
		return out, fmt.Errorf("frp 拒绝配置：%s", out)
	}
	return out, nil
}

func (b *externalRuntime) Start(target string, config RuntimeConfig, output io.Writer) (RuntimeInstance, error) {
	if config.ServerAccessMode != "" || strings.TrimSpace(config.ServerAccessList) != "" {
		return nil, fmt.Errorf("控制入口来源限制仅支持内嵌服务端")
	}
	cmd := exec.Command(target, "-c", config.configPath)
	childprocess.Configure(cmd)
	cmd.Env = childEnv(config.Rendered.Env)
	cmd.Dir = filepath.Dir(config.configPath)
	cmd.WaitDelay = 2 * time.Second
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &externalInstance{cmd: cmd, done: make(chan struct{})}, nil
}

type externalInstance struct {
	cmd  *exec.Cmd
	done chan struct{}
	mu   sync.Mutex
}

func (p *externalInstance) PID() int { return p.cmd.Process.Pid }

func (p *externalInstance) Wait() error {
	err := p.cmd.Wait()
	// Clean the owned process group before allowing a replacement to start.
	p.mu.Lock()
	_ = childprocess.Kill(p.cmd)
	close(p.done)
	p.mu.Unlock()
	return err
}

// Serialize signal attempts with the completion notification. Wait may already
// have reaped the leader before acquiring this lock; it does not pin the PGID.
func (p *externalInstance) signal(signal func(*exec.Cmd) error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	select {
	case <-p.done:
		return
	default:
		_ = signal(p.cmd)
	}
}

func (p *externalInstance) Stop(ctx context.Context) error {
	p.signal(childprocess.Terminate)
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-p.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	p.signal(childprocess.Kill)
	select {
	case <-p.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
