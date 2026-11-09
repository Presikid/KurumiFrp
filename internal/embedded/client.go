// Package embedded provides lifecycle management for an in-process frp client.
package embedded

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"time"
)

var (
	ErrInvalidService = errors.New("embedded client service is nil")
	ErrClientStarted  = errors.New("embedded client has already been started or closed")
	ErrCleanupFailed  = errors.New("embedded client cleanup failed")
)

// ClientService is the small part of an embedded client engine used by Client.
// Implementations must permit Close and ProxyPhase to run concurrently with Run.
// Close must also release resources if it is called before Run begins.
type ClientService interface {
	Run(context.Context) error
	Close()
	ProxyPhase(name string) (phase string, known bool)
}

type ClientState string

const (
	ClientNew      ClientState = "new"
	ClientRunning  ClientState = "running"
	ClientStopping ClientState = "stopping"
	ClientExited   ClientState = "exited"
)

type ExitReason string

const (
	ExitNone          ExitReason = ""
	ExitStopped       ExitReason = "stopped"
	ExitFailed        ExitReason = "failed"
	ExitUnexpected    ExitReason = "unexpected"
	ExitCleanupFailed ExitReason = "cleanup_failed"
)

// ClientStatus describes the engine lifecycle, not server connectivity.
// ExitReason is populated only after both Run and cleanup have finished.
type ClientStatus struct {
	State     ClientState
	StartedAt time.Time
	Exit      ExitReason
}

// Registration describes the latest proxy phase reported by frp. Known is
// false when the engine has no current status (including during reconnect).
// Even a running proxy phase is not an end-to-end health or traffic check.
type Registration struct {
	Known bool
	Phase string
}

// Client owns one service for one instance and is single-use. Create a new
// Client to restart an instance. Its Run context is independent of the HTTP
// request that starts it; the application must call Shutdown on app shutdown.
type Client struct {
	mu          sync.Mutex
	service     ClientService
	state       ClientState
	startedAt   time.Time
	exit        ExitReason
	cancel      context.CancelFunc
	closeOnce   sync.Once
	closeDone   chan struct{}
	done        chan struct{}
	closeFailed bool
}

// NewClient takes ownership of service, including any resources it already
// allocated. The caller must call Shutdown even if it decides not to Start.
func NewClient(service ClientService) (*Client, error) {
	if nilInterface(service) {
		return nil, ErrInvalidService
	}
	return &Client{
		service:   service,
		state:     ClientNew,
		closeDone: make(chan struct{}),
		done:      make(chan struct{}),
	}, nil
}

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

// Start launches the owned service once. Success means only that the engine
// was launched; it does not mean that login or proxy registration succeeded.
func (c *Client) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != ClientNew {
		return ErrClientStarted
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.startedAt = time.Now()
	c.state = ClientRunning
	go c.run(ctx)
	return nil
}

func (c *Client) run(ctx context.Context) {
	var runErr error
	defer func() {
		// Never expose panic values or upstream errors: either can contain secrets.
		panicked := recover() != nil
		c.mu.Lock()
		reason := ExitStopped
		switch {
		case panicked:
			reason = ExitFailed
		case runErr != nil && !(runErr == context.Canceled && c.state == ClientStopping && ctx.Err() == context.Canceled):
			// A concurrent stop must not hide a real failure from Run.
			reason = ExitFailed
		case c.state != ClientStopping:
			reason = ExitUnexpected
		}
		c.state = ClientStopping
		c.cancel()
		c.mu.Unlock()
		c.closeService()
		<-c.closeDone
		c.finish(reason)
	}()
	runErr = c.service.Run(ctx)
}

func (c *Client) closeService() {
	c.closeOnce.Do(func() {
		go func() {
			defer func() {
				if recover() != nil {
					c.mu.Lock()
					c.closeFailed = true
					c.mu.Unlock()
				}
				close(c.closeDone)
			}()
			c.service.Close()
		}()
	})
}

func (c *Client) finish(reason ExitReason) {
	c.mu.Lock()
	if c.closeFailed {
		reason = ExitCleanupFailed
	}
	c.exit = reason
	c.state = ClientExited
	c.mu.Unlock()
	close(c.done)
}

// Shutdown requests cancellation and cleanup exactly once. It waits only until
// ctx expires; if it returns a context error, cleanup may still be in progress.
// Subsequent calls can wait again. A nil context is not accepted.
func (c *Client) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return errors.New("embedded client shutdown context is nil")
	}
	c.mu.Lock()
	if c.state == ClientNew {
		c.state = ClientStopping
		go func() {
			c.closeService()
			<-c.closeDone
			c.finish(ExitStopped)
		}()
	} else if c.state == ClientRunning {
		c.state = ClientStopping
		c.cancel()
		c.closeService()
	}
	c.mu.Unlock()
	_, err := c.Wait(ctx)
	return err
}

// Done closes after the service has exited and cleanup has completed.
func (c *Client) Done() <-chan struct{} { return c.done }

// Wait waits for completion without initiating shutdown. It never returns raw
// upstream errors or configuration values, which may contain credentials.
func (c *Client) Wait(ctx context.Context) (ClientStatus, error) {
	if ctx == nil {
		return c.Status(), errors.New("embedded client wait context is nil")
	}
	select {
	case <-c.done:
		status := c.Status()
		if status.Exit == ExitCleanupFailed {
			return status, ErrCleanupFailed
		}
		return status, nil
	case <-ctx.Done():
		return c.Status(), ctx.Err()
	}
}

func (c *Client) Status() ClientStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return ClientStatus{State: c.state, StartedAt: c.startedAt, Exit: c.exit}
}

// ProxyRegistration returns a sanitized phase without the upstream status
// object, which contains the proxy configuration and server-provided errors.
func (c *Client) ProxyRegistration(name string) Registration {
	if c.Status().State != ClientRunning {
		return Registration{}
	}
	phase, known := c.service.ProxyPhase(name)
	if !known || c.Status().State != ClientRunning {
		return Registration{}
	}
	switch phase {
	case "new", "wait start", "start error", "running", "check failed", "closed":
		return Registration{Known: true, Phase: phase}
	default:
		return Registration{Known: true, Phase: "unknown"}
	}
}
