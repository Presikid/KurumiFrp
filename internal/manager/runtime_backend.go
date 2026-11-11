package manager

import (
	"context"
	"fmt"
	"io"
)

// RuntimeConfig is an immutable snapshot for one instance and one launch.
// Rendered.Text contains frp template references; Rendered.Env contains their
// TOML-escaped values, not a process-wide environment. Both contain sensitive
// data and must be handled per instance without changing os.Environ.
type RuntimeConfig struct {
	InstanceID string
	// RunID is assigned for Start; verification has no run yet. A backend
	// must not report observations to App until Start has returned.
	RunID    string
	Kind     string
	Rendered Rendered
	// Embedded-only control-entry policy, copied with the instance snapshot.
	ServerAccessMode string
	ServerAccessList string
	// Internal opt-in for account-token frps. The resolver must be bound to
	// this instance and incarnation, return promptly, and never log tokens.
	ManagedAccountLogin       bool
	ManagedAccountIncarnation string
	ManagedLoginResolver      func(context.Context, string) (string, error)
}

func validateManagedRuntimeConfig(cfg RuntimeConfig) error {
	if !cfg.ManagedAccountLogin {
		if cfg.ManagedLoginResolver != nil || cfg.ManagedAccountIncarnation != "" {
			return fmt.Errorf("未启用账户认证，不能提供账户认证配置")
		}
		return nil
	}
	if cfg.Kind != "frps" || !idPattern.MatchString(cfg.InstanceID) ||
		!validLocalTrafficID(cfg.ManagedAccountIncarnation) || cfg.ManagedLoginResolver == nil {
		return fmt.Errorf("账户认证需要有效的服务端实例、实例标识及令牌查询器")
	}
	return nil
}

func (a *App) checkManagedAccountBackend(i Instance, cfg RuntimeConfig) error {
	if err := validateManagedRuntimeConfig(cfg); err != nil {
		return err
	}
	if !cfg.ManagedAccountLogin {
		return nil
	}
	if cfg.InstanceID != i.ID || cfg.Kind != i.Kind {
		return fmt.Errorf("账户认证配置与目标实例不匹配")
	}
	if _, ok := a.runtime.(*embeddedRuntime); !ok {
		return fmt.Errorf("当前运行核心不支持账户认证，仅内嵌服务端可用")
	}
	return nil
}

// RuntimeBackend selects, validates and starts runtime instances. Resolve is
// called under the app lock: it must be fast and return the same
// opaque target while the backend remains unchanged. Start receives a
// verified configuration for that target. Methods must support concurrent
// calls for different instances. Verify must respect cancellation. Start must
// return promptly and must not call back into App; an error means it left no
// instance running. Implementations must not mutate the supplied configuration
// or retain it or the writer beyond the instance's lifetime. output is the
// log source for this one RunID: write only that run's output to it and finish
// all writes before Wait returns. The manager buffers and redacts complete
// lines; a backend must not route concurrent instances through a global logger.
//
// An embedded.Client/Server adapter can implement this interface without a
// binary or a temporary runtime file: resolve its supported version, expand
// the template using only Rendered.Env, validate it, then start the matching
// client/server with the supplied writer. Its handle owns cancellation and
// resource cleanup; do not use the request context for the instance lifetime.
type RuntimeBackend interface {
	Resolve(kind string) (target string, err error)
	Verify(ctx context.Context, target string, config RuntimeConfig) (output string, err error)
	Start(target string, config RuntimeConfig, output io.Writer) (RuntimeInstance, error)
}

// ControlAccessCapable opts a runtime into enforcing RuntimeConfig's server
// control-entry policy. A runtime must reject unsupported policy values.
type ControlAccessCapable interface {
	SupportsControlAccess() bool
}

// SystemCATLSCapable opts a runtime into enforcing frpc's system-CA TLS
// verification. A supporting backend must honor transport.tls.useSystemCa
// for every accepted client transport and must not silently fall back.
type SystemCATLSCapable interface {
	SupportsSystemCATLS() bool
}

// TLSFileCapable opts a backend into honoring frp's transport TLS certificate,
// key, trusted CA and server name settings without silently ignoring them.
// Every accepted transport must honor these fields. Client CAs must verify the
// server using the configured name; a server CA must require and verify client
// certificates and must not permit a plaintext bypass.
type TLSFileCapable interface {
	SupportsTLSFiles() bool
}

func (a *App) checkTLSFileBackend(i Instance) error {
	if i.TLSCertFile == "" && i.TLSKeyFile == "" && i.TLSTrustedCAFile == "" && i.ClientTLSServerName == "" {
		return nil
	}
	backend, ok := a.runtime.(TLSFileCapable)
	if !ok || !backend.SupportsTLSFiles() {
		return fmt.Errorf("当前运行核心不支持指定 frp TLS 证书、私钥、CA 或服务端名称")
	}
	return nil
}

func (a *App) checkSystemCATLSBackend(i Instance) error {
	if !i.ClientTLSUseSystemCA {
		return nil
	}
	backend, ok := a.runtime.(SystemCATLSCapable)
	if !ok || !backend.SupportsSystemCATLS() {
		return fmt.Errorf("当前运行核心不支持使用系统信任库验证服务端证书")
	}
	return nil
}

// RuntimeInstance represents exactly one generation. Wait is called once and
// returns its exit reason only after its resources can safely be replaced.
// Stop may run concurrently with Wait, must respect ctx and request shutdown;
// the manager still waits for Wait before allowing a replacement. An instance
// that cannot be stopped remains running from the manager's point of view. A
// client instance may also implement ProxyRegistrationReader for its own run.
// A backend with RuntimeReadiness should return nil or context.Canceled for an
// ordinary stop, and must preserve independent startup or cleanup failures in
// a Wait error which is not classified as context.Canceled.
type RuntimeInstance interface {
	Wait() error
	Stop(ctx context.Context) error
}

// RuntimeReadiness is optional. Ready closes after the engine enters its run
// loop; Done closes when that loop exits, before cleanup may have completed.
// ReadyNow must be concurrent-safe, return promptly without calling App, and
// recheck that the engine has not exited.
// Done may close without Ready after startup failure. Wait remains the cleanup
// boundary. These observations do not establish authentication or reachability.
type RuntimeReadiness interface {
	Ready() <-chan struct{}
	Done() <-chan struct{}
	ReadyNow() bool
}

// runtimeExitObserver can inspect the engine's own exit signal before its
// Done notification has been forwarded. Like ReadyNow, it must be concurrent-
// safe, return promptly, and never call back into App.
type runtimeExitObserver interface {
	ExitedNow() bool
}

// runtimeCleanupObserver exposes a non-sensitive uncertainty signal while
// Wait continues to hold the run. It must return promptly and never call App.
type runtimeCleanupObserver interface {
	CleanupUncertain() bool
}

// RuntimePID is optional and is intended for the legacy process adapter.
// In-process instances should omit it; their reported PID is zero.
type RuntimePID interface {
	PID() int
}

// RuntimeTrafficReader reports observations for one generation. It must return
// promptly; unavailable measurements must not be represented as zero.
type RuntimeTrafficReader interface {
	ReadTraffic() TrafficSnapshot
}

// RuntimeClientRuleTrafficReader provides an independent snapshot of the
// applied client rules for a single run; the returned slice is caller-owned.
type RuntimeClientRuleTrafficReader interface {
	ReadClientRuleTraffic() ClientRuleTrafficSnapshot
}

// RuntimeServerTrafficReader reports a server's own forwarding counters for
// one run. It must be concurrent-safe, and each returned slice is caller-owned.
// Its byte scope differs from RuntimeTrafficReader's client payload counters.
type RuntimeServerTrafficReader interface {
	ReadServerTraffic() ServerTrafficSnapshot
}
