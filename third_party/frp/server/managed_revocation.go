package server

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/fatedier/frp/pkg/util/lifetime"
)

// ErrManagedCleanupUncertain means a managed close or worker cleanup failed.
var ErrManagedCleanupUncertain = errors.New("managed connection cleanup could not be confirmed")

// One state is shared by all live controls of the same credential version.
// The failed flag is the admission boundary and remains set once observed.
type managedIdentityState struct {
	cm       *ControlManager
	identity ManagedIdentity
	ctx      context.Context
	failed   atomic.Bool
}

func (state *managedIdentityState) fail() {
	if state.failed.CompareAndSwap(false, true) {
		state.notifyFailure()
	}
}

func (state *managedIdentityState) notifyFailure() {
	go func() {
		state.cm.mu.RLock()
		var controls []*Control
		for ctl := range state.cm.liveControls {
			if ctl.managedState == state {
				controls = append(controls, ctl)
			}
		}
		state.cm.mu.RUnlock()
		for _, ctl := range controls {
			ctl.requestManagedStop(state.cm)
		}
	}()
	if notify := state.cm.onManagedCleanupFailure; notify != nil {
		go func() {
			defer lifetime.Recover(state.ctx)
			notify(state.identity)
		}()
	}
}

func (cm *ControlManager) managedCleanupError() error {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	for _, state := range cm.managedStates {
		if state.failed.Load() {
			return ErrManagedCleanupUncertain
		}
	}
	return nil
}

// Record a failed stop only while the control is still visible to snapshots.
// trackControl records actual close failures before removing the same entry.
func (cm *ControlManager) recordManagedFailureIfLive(ctl *Control) {
	cm.mu.Lock()
	_, live := cm.liveControls[ctl]
	state := ctl.managedState
	firstFailure := live && state != nil && state.failed.CompareAndSwap(false, true)
	cm.mu.Unlock()
	if firstFailure {
		state.notifyFailure()
	}
}

func (ctl *Control) requestManagedStop(cm *ControlManager) <-chan struct{} {
	ctl.managedStopOnce.Do(func() {
		go func() {
			defer close(ctl.managedStopDone)
			defer func() {
				if recover() != nil {
					ctl.managedUncertain.Store(true)
					cm.recordManagedFailureIfLive(ctl)
					ctl.managedStopErr = ErrManagedCleanupUncertain
				}
			}()
			// Freeze the lifecycle before deciding whether a worker can own
			// forwarding. Pending controls have no proxy scope work to stop.
			running := false
			func() {
				ctl.lifecycleMu.Lock()
				defer ctl.lifecycleMu.Unlock()
				switch ctl.state {
				case controlStateCreated, controlStatePending:
					ctl.state = controlStateClosing
					ctl.finishLocked()
				case controlStateRunning:
					ctl.state = controlStateClosing
					running = true
				case controlStateClosing:
					select {
					case <-ctl.doneCh:
					default:
						running = true
					}
				}
			}()
			// A failed or blocked control-socket close must not leave tracked
			// forwarding alive while the dispatcher is still waiting to exit.
			scopeStopped := make(chan struct{})
			if running {
				go func() {
					defer close(scopeStopped)
					ctl.stopManagedScope()
				}()
			} else {
				close(scopeStopped)
			}
			err := ctl.interruptReadAndClose()
			<-ctl.doneCh
			<-scopeStopped
			if err != nil || ctl.managedUncertain.Load() {
				cm.recordManagedFailureIfLive(ctl)
				ctl.managedStopErr = ErrManagedCleanupUncertain
			}
		}()
	})
	return ctl.managedStopDone
}

// RevokeManagedCredential closes the registered controls and tracked TCP
// forwarding for one exact credential generation in this service. Before the
// call, the owner must persist invalidation and make TryAuthorize reject this
// identity while holding the same account gate; it must release all account
// and manager locks before waiting here. Unregistered handshakes and work
// connections not yet attached to a control are excluded from the wait.
// A context error means the wait is incomplete and may be retried.
// Close failures remain sticky for this service generation; the owner must
// prevent unsafe reauthorization after any incomplete or uncertain cleanup.
func (svr *Service) RevokeManagedCredential(ctx context.Context, identity ManagedIdentity) error {
	if ctx == nil || svr.managedLogin == nil || !validManagedIdentity(identity) {
		return errors.New("invalid managed revocation request")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("managed revocation incomplete: %w", err)
	}
	cm := svr.ctlManager
	cm.mu.RLock()
	ctls := make([]*Control, 0)
	for ctl := range cm.liveControls {
		if ctl.sessionCtx.managedIdentity == identity {
			ctls = append(ctls, ctl)
		}
	}
	state := cm.managedStates[identity]
	failed := state != nil && state.failed.Load()
	cm.mu.RUnlock()

	done := make([]<-chan struct{}, len(ctls))
	for i, ctl := range ctls {
		done[i] = ctl.requestManagedStop(cm)
	}
	for i, ch := range done {
		select {
		case <-ch:
			if ctls[i].managedStopErr != nil {
				failed = true
			}
		case <-ctx.Done():
			waitErr := fmt.Errorf("managed revocation incomplete: %w", ctx.Err())
			cm.mu.RLock()
			state := cm.managedStates[identity]
			sticky := state != nil && state.failed.Load()
			cm.mu.RUnlock()
			if failed || sticky {
				return errors.Join(waitErr, ErrManagedCleanupUncertain)
			}
			return waitErr
		}
	}
	cm.mu.RLock()
	state = cm.managedStates[identity]
	sticky := state != nil && state.failed.Load()
	cm.mu.RUnlock()
	if failed || sticky {
		return ErrManagedCleanupUncertain
	}
	return nil
}
