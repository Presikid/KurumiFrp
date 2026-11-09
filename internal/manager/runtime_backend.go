package manager

import (
	"context"
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

	// Only the compatibility backend uses a file to launch an external core.
	configPath string
}

// RuntimeBackend selects, validates and starts runtime instances. Resolve is
// called under the app lock: it must be fast and return the same
// opaque target while that kind/core selection is unchanged. Start receives a
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
	Resolve(kind, coreID string) (target string, err error)
	Verify(ctx context.Context, target string, config RuntimeConfig) (output string, err error)
	Start(target string, config RuntimeConfig, output io.Writer) (RuntimeInstance, error)
}

// RuntimeInstance represents exactly one generation. Wait is called once and
// returns its exit reason only after its resources can safely be replaced.
// Stop may run concurrently with Wait, must respect ctx and request shutdown;
// the manager still waits for Wait before allowing a replacement. An instance
// that cannot be stopped remains running from the manager's point of view. A
// client instance may also implement ProxyRegistrationReader for its own run.
type RuntimeInstance interface {
	Wait() error
	Stop(ctx context.Context) error
}

// RuntimePID is optional and is intended for the legacy process adapter.
// In-process instances should omit it; their reported PID is zero.
type RuntimePID interface {
	PID() int
}
