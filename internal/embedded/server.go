// Package embedded provides lifecycle wrappers for frp services in this process.
package embedded

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	v1 "github.com/fatedier/frp/pkg/config/v1"
	"github.com/fatedier/frp/pkg/config/v1/validation"
	"github.com/fatedier/frp/pkg/util/xlog"
	"github.com/fatedier/frp/server"
	servertraffic "github.com/fatedier/frp/server/traffic"
)

var (
	// ErrServerStarted means that this Server has already been started or stopped.
	ErrServerStarted = errors.New("embedded frp server lifecycle has already begun")
	// ErrServerExited means Run returned without a stop request or context cancellation.
	ErrServerExited = errors.New("embedded frp server exited unexpectedly")
	// ErrServerPanicked does not include the panic value, which may contain secrets.
	ErrServerPanicked = errors.New("embedded frp server panicked")
)

type serverState uint8

const (
	serverNew serverState = iota
	serverStarting
	serverRunning
	serverStopped
)

// Server owns exactly one upstream Service. Its lifecycle is single-use; create a
// new Server for a subsequent start. Do not copy a Server after first use.
//
// Some upstream and dependency logging remains process-global. This wrapper
// does not attribute every log or guarantee that all dependency workers exit.
type Server struct {
	mu      sync.Mutex
	config  *v1.ServerConfig
	options ServerOptions
	service *server.Service
	warning string
	state   serverState
	cancel  context.CancelFunc
	done    chan struct{}
	err     error
}

// ServerOptions binds optional telemetry to one server generation. The sink
// must not be shared; an observer must be concurrent-safe, return promptly,
// and not panic.
type ServerOptions struct {
	LogSink         *xlog.Sink
	TrafficRecorder server.TrafficRecorder
	// OnWorkerPanic is an instance-scoped failure notification. It must be
	// concurrent-safe, return promptly, and not panic.
	OnWorkerPanic func()
}

// NewServer copies, completes, and validates cfg without opening listeners or
// executing a command. It does not mutate cfg. Dashboard and metrics listeners
// remain disabled unless their ports are explicitly configured.
func NewServer(cfg *v1.ServerConfig) (*Server, error) {
	return NewServerWithOptions(cfg, ServerOptions{})
}

// NewServerWithOptions also supplies optional per-generation telemetry. The
// sink is claimed by Start when the upstream service is constructed.
func NewServerWithOptions(cfg *v1.ServerConfig, options ServerOptions) (*Server, error) {
	if cfg == nil {
		return nil, errors.New("embedded frp server config is nil")
	}

	// Deep-copy the configuration, including slices and pointers retained by frp.
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("copy embedded frp server config: %w", err)
	}
	var copied v1.ServerConfig
	if err := json.Unmarshal(data, &copied); err != nil {
		return nil, fmt.Errorf("copy embedded frp server config: %w", err)
	}
	if copied.Auth.TokenSource != nil &&
		(copied.Auth.TokenSource.Type == "exec" || copied.Auth.TokenSource.Exec != nil) {
		return nil, errors.New("embedded frp server does not permit executable token sources")
	}
	// Complete defaults every SSH gateway to the same file in the process's
	// working directory. Require the caller to choose a key path explicitly.
	if copied.SSHTunnelGateway.BindPort > 0 && copied.SSHTunnelGateway.PrivateKeyFile == "" &&
		copied.SSHTunnelGateway.AutoGenPrivateKeyPath == "" {
		return nil, errors.New("embedded frp SSH gateway requires an explicit private key or generated key path")
	}
	if err := copied.Complete(); err != nil {
		return nil, fmt.Errorf("complete embedded frp server config: %w", err)
	}
	validator := validation.NewConfigValidator(nil)
	warning, err := validator.ValidateServerConfig(&copied)
	if err != nil {
		return nil, fmt.Errorf("invalid embedded frp server config: %w", err)
	}
	// The upstream server validator does not check this port or allowPorts.
	// Invalid ranges can make upstream's port-manager initialization unbounded.
	if err := validation.ValidatePort(copied.SSHTunnelGateway.BindPort, "sshTunnelGateway.bindPort"); err != nil {
		return nil, fmt.Errorf("invalid embedded frp server config: %w", err)
	}
	for i, ports := range copied.AllowPorts {
		if ports.Single != 0 {
			if ports.Single < 1 || ports.Single > 65535 || ports.Start != 0 || ports.End != 0 {
				return nil, fmt.Errorf("invalid embedded frp server config: allowPorts[%d] must specify one port from 1 to 65535", i)
			}
		} else if ports.Start < 1 || ports.End > 65535 || ports.Start > ports.End {
			return nil, fmt.Errorf("invalid embedded frp server config: allowPorts[%d] must specify an ascending range from 1 to 65535", i)
		}
	}
	s := &Server{config: &copied, options: options, done: make(chan struct{})}
	if warning != nil {
		s.warning = warning.Error()
	}
	return s, nil
}

// Warning returns any non-fatal warning from upstream configuration validation.
func (s *Server) Warning() string { return s.warning }

// Start constructs and starts the upstream Service in this process. It returns
// after listeners are constructed, not after proxy registration or a health
// check. Cancellation of ctx stops the service. Start may be called only once.
// Upstream construction itself cannot be cancelled; a concurrent Stop waits for
// it to return before the constructed Service can be closed.
func (s *Server) Start(ctx context.Context) error {
	if ctx == nil {
		return errors.New("embedded frp server context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	if s.state != serverNew {
		s.mu.Unlock()
		return ErrServerStarted
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.state = serverStarting
	s.mu.Unlock()

	if err := runCtx.Err(); err != nil {
		cancel()
		s.finish(nil)
		return err
	}
	svc, err := server.NewServiceWithOptions(s.config, server.ServiceOptions{
		LogSink:         s.options.LogSink,
		TrafficRecorder: s.options.TrafficRecorder,
		OnWorkerPanic:   s.options.OnWorkerPanic,
	})
	if err != nil {
		cancel()
		err = fmt.Errorf("construct embedded frp server: %w", err)
		var pendingCleanup *server.ConstructionCleanupError
		if errors.As(err, &pendingCleanup) {
			go func() {
				s.finish(errors.Join(err, pendingCleanup.WaitCleanup(context.Background())))
			}()
		} else {
			s.finish(err)
		}
		return err
	}

	s.mu.Lock()
	s.service = svc
	if err := runCtx.Err(); err != nil {
		s.mu.Unlock()
		cancel()
		go s.finishAfterClose(svc, nil)
		return err
	}
	s.state = serverRunning
	go s.run(runCtx, svc, cancel)
	s.mu.Unlock()
	return nil
}

// TrafficSnapshot returns false until an upstream service has been created.
// The snapshot remains available after Stop, but can still change while
// previously accepted forwarding handlers are exiting.
func (s *Server) TrafficSnapshot() (servertraffic.ServerSnapshot, bool) {
	s.mu.Lock()
	svc := s.service
	s.mu.Unlock()
	if svc == nil {
		return servertraffic.ServerSnapshot{}, false
	}
	return svc.TrafficSnapshot(), true
}

func (s *Server) run(ctx context.Context, svc *server.Service, cancel context.CancelFunc) {
	var runErr error
	defer func() {
		if recover() != nil {
			runErr = ErrServerPanicked
		}
		cancel()
		s.finishAfterClose(svc, runErr)
	}()
	runErr = svc.Run(ctx)
	if ctx.Err() != nil && errors.Is(runErr, server.ErrServiceClosed) &&
		!errors.Is(runErr, context.DeadlineExceeded) && !errors.Is(runErr, context.Canceled) {
		// Stop may close the service before its Run goroutine starts.
		runErr = nil
	} else if ctx.Err() == nil && runErr == nil {
		runErr = ErrServerExited
	}
}

// Wait without a deadline in this background worker so Done only closes after
// the service has finished cleaning up, even if Stop's caller has timed out.
func (s *Server) finishAfterClose(svc *server.Service, runErr error) {
	closeErr := svc.CloseContext(context.Background())
	if svc.WorkerPanicked() {
		runErr = errors.Join(runErr, ErrServerPanicked)
	}
	s.finish(errors.Join(runErr, closeErr))
}

func (s *Server) finish(err error) {
	if errors.Is(err, server.ErrWorkerPanicked) {
		err = errors.Join(err, ErrServerPanicked)
	}
	s.mu.Lock()
	s.err = err
	s.state = serverStopped
	close(s.done)
	s.mu.Unlock()
}

// Stop requests shutdown and waits for Run to return. It is safe to call more
// than once. If ctx expires, shutdown continues; the caller must not assume it
// has completed until Done is closed. Stopping an unstarted Server is final.
func (s *Server) Stop(ctx context.Context) error {
	if ctx == nil {
		return errors.New("embedded frp server context is nil")
	}
	s.mu.Lock()
	if s.state == serverNew {
		s.state = serverStopped
		close(s.done)
	} else if s.cancel != nil {
		s.cancel()
	}
	done := s.done
	s.mu.Unlock()
	select {
	case <-done:
		return s.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Done closes after service cleanup, including rollback after a failed
// construction. Previously accepted connection handlers may still be exiting.
func (s *Server) Done() <-chan struct{} { return s.done }

// Ready returns the upstream run-loop signal after a successful Start.
// Before construction succeeds it returns nil; Done also reports failure.
func (s *Server) Ready() <-chan struct{} {
	s.mu.Lock()
	svc := s.service
	s.mu.Unlock()
	if svc == nil {
		return nil
	}
	return svc.Ready()
}

// Exiting signals that the upstream Run loop ended, before resource cleanup.
// It returns nil before a service has been constructed.
func (s *Server) Exiting() <-chan struct{} {
	s.mu.Lock()
	svc := s.service
	s.mu.Unlock()
	if svc == nil {
		return nil
	}
	return svc.Exiting()
}

// Err returns the construction, run, or cleanup error after Done is closed.
func (s *Server) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}
