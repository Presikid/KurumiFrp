package server

import (
	"fmt"
	"sync"
	"time"

	"github.com/fatedier/frp/pkg/util/lifetime"
)

// Retain state for the service lifetime: late close results must still affect
// later controls using the same identity. Bound this by authenticated identity.
const maxManagedIdentityStates = 4096

// addManaged publishes only while the same account gate used by mutations is
// held. The lifecycle locks are acquired before the nonblocking gate lookup.
func (cm *ControlManager) addManaged(ctl *Control) error {
	for {
		cm.mu.RLock()
		old := cm.ctlsByRunID[ctl.runID]
		cm.mu.RUnlock()
		if old != nil {
			old.runMu.Lock()
		}
		var oldCtl *Control
		var expiresAt time.Time
		retry, err := func() (bool, error) {
			if old != nil {
				defer old.runMu.Unlock()
			}
			cm.mu.Lock()
			defer cm.mu.Unlock()
			if cm.closed {
				return false, fmt.Errorf("control manager is closed")
			}
			if cm.ctlsByRunID[ctl.runID] != old {
				return true, nil
			}
			if old != nil {
				if old.ctl == ctl || old.ctl.sessionCtx.managedIdentity != ctl.sessionCtx.managedIdentity {
					return false, fmt.Errorf("run id belongs to another credential")
				}
				old.ctl.lifecycleMu.Lock()
				defer old.ctl.lifecycleMu.Unlock()
			}
			ctl.lifecycleMu.Lock()
			defer ctl.lifecycleMu.Unlock()
			if ctl.state != controlStateCreated || !validManagedIdentity(ctl.sessionCtx.managedIdentity) {
				return false, fmt.Errorf("managed control is unavailable")
			}
			return false, func() error {
				release, expiry, err := ctl.sessionCtx.managedProvider.TryAuthorize(ctl.sessionCtx.managedIdentity)
				if release != nil {
					defer release()
				}
				if err != nil || release == nil {
					return fmt.Errorf("account admission denied or busy")
				}
				// UTC removes any monotonic component, so the expiry watcher
				// reevaluates the actual wall clock after time adjustments.
				expiry = expiry.UTC()
				if !expiry.IsZero() && !expiry.After(time.Now().UTC()) {
					return fmt.Errorf("account has expired")
				}
				identity := ctl.sessionCtx.managedIdentity
				state := cm.managedStates[identity]
				// A failure racing after this load schedules a scan that waits for
				// cm.mu and therefore also sees the control published below.
				if state != nil && state.failed.Load() {
					return ErrManagedCleanupUncertain
				}
				if state == nil && len(cm.managedStates) >= maxManagedIdentityStates {
					return fmt.Errorf("managed identity limit reached; restart the server to admit a new identity")
				}
				id := ControlID(nextControlID.Add(1))
				if err := ctl.admitLocked(cm, id); err != nil {
					return err
				}
				if state == nil {
					state = &managedIdentityState{cm: cm, identity: identity, ctx: ctl.ctx}
					cm.managedStates[identity] = state
				}
				ctl.managedState = state
				ctl.managedExpiresAt = expiry
				runMu := &sync.Mutex{}
				if old != nil {
					runMu = old.runMu
				}
				entry := &controlEntry{ctl: ctl, id: id, runMu: runMu}
				if old != nil {
					oldCtl = old.ctl
					ctl.handoffBarrier = oldCtl.markReplacedLocked()
					entry.registryOnline = old.registryOnline
					entry.registryControlID = old.registryControlID
				}
				cm.ctlsByRunID[ctl.runID] = entry
				cm.liveControls[ctl] = struct{}{}
				expiresAt = expiry
				return nil
			}()
		}()
		if retry {
			continue
		}
		if err != nil {
			return err
		}
		if !expiresAt.IsZero() {
			go func(expiry time.Time) {
				defer lifetime.Recover(ctl.ctx)
				defer func() {
					if value := recover(); value != nil {
						ctl.noteManagedClose(fmt.Errorf("managed expiry watcher panicked"))
						panic(value)
					}
				}()
				ctl.watchManagedExpiry(cm, expiry)
			}(expiresAt)
		}
		go func() {
			defer lifetime.Recover(ctl.ctx)
			cm.trackControl(ctl)
		}()
		if oldCtl != nil {
			oldCtl.Replaced(ctl)
		}
		return nil
	}
}

// A running or pending control is stopped at its checked account expiry. The
// short recheck interval detects wall-clock jumps without a service-wide timer.
func (ctl *Control) watchManagedExpiry(cm *ControlManager, expiry time.Time) {
	for {
		select {
		case <-ctl.doneCh:
			return
		default:
		}
		remaining := expiry.Sub(time.Now().UTC())
		if remaining <= 0 {
			ctl.requestManagedStop(cm)
			return
		}
		if remaining > 10*time.Second {
			remaining = 10 * time.Second
		}
		timer := time.NewTimer(remaining)
		select {
		case <-ctl.doneCh:
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
