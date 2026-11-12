package server

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// ErrManagedProxySnapshotUnavailable deliberately does not identify the
// connection or resource whose state could not be safely sampled.
var ErrManagedProxySnapshotUnavailable = errors.New("managed proxy snapshot unavailable")

const (
	maxManagedSnapshotControls  = 4096
	maxManagedSnapshotNames     = 256
	maxManagedSnapshotNameBytes = 1024
	maxManagedSnapshotBytes     = 64 << 10
	maxManagedSnapshotVisits    = 4096
)

// ManagedProxySnapshot contains only name suffixes registered by the requested
// credential generation, with its internal account prefix removed. It samples
// the controls currently published in the manager's run-ID table. Different
// controls are sampled at different instants between StartedAt and SampledAt;
// the result may be incomplete and is not a reachability check or an inventory
// of listeners being created or closed.
type ManagedProxySnapshot struct {
	Names     []string
	StartedAt time.Time
	SampledAt time.Time
	// MayBeIncomplete is always true because the result is a per-control sample.
	MayBeIncomplete bool
	// Truncated means a limit was reached or a busy, unidentified control was skipped.
	Truncated bool
}

type managedSnapshotCandidate struct {
	runID   string
	entry   *controlEntry
	control *Control
	id      ControlID
	runMu   *sync.Mutex
}

// ReadManagedProxyNames samples the current controls for one already
// authenticated managed identity. Its caller must independently check that the
// identity is still authorized for this service generation and keep the
// corresponding account's admission gate held. The identity and any other
// connection details are never included in the result.
func (svr *Service) ReadManagedProxyNames(ctx context.Context, identity ManagedIdentity) (ManagedProxySnapshot, error) {
	if ctx == nil {
		return ManagedProxySnapshot{}, ErrManagedProxySnapshotUnavailable
	}
	if err := ctx.Err(); err != nil {
		return ManagedProxySnapshot{}, err
	}
	if !validManagedIdentity(identity) || !svr.managedProxySnapshotReady() {
		return ManagedProxySnapshot{}, ErrManagedProxySnapshotUnavailable
	}
	started := time.Now().UTC()
	result, err := svr.ctlManager.readManagedProxyNames(ctx, identity)
	if err != nil {
		return ManagedProxySnapshot{}, err
	}
	if err = ctx.Err(); err != nil {
		return ManagedProxySnapshot{}, err
	}
	if !svr.managedProxySnapshotReady() {
		return ManagedProxySnapshot{}, ErrManagedProxySnapshotUnavailable
	}
	result.StartedAt = started
	result.SampledAt = time.Now().UTC()
	return result, nil
}

func (svr *Service) managedProxySnapshotReady() bool {
	if svr == nil || !svr.lifecycleMu.TryLock() {
		return false
	}
	usable := svr.running && !svr.closed && svr.managedLogin != nil && svr.ctlManager != nil && svr.ctx != nil && svr.ctx.Err() == nil
	svr.lifecycleMu.Unlock()
	if !usable || svr.WorkerPanicked() {
		return false
	}
	select {
	case <-svr.ready:
	default:
		return false
	}
	select {
	case <-svr.exiting:
		return false
	default:
		return true
	}
}

func (cm *ControlManager) readManagedProxyNames(ctx context.Context, identity ManagedIdentity) (ManagedProxySnapshot, error) {
	if !cm.mu.TryRLock() {
		return ManagedProxySnapshot{}, ErrManagedProxySnapshotUnavailable
	}
	if cm.closed {
		cm.mu.RUnlock()
		return ManagedProxySnapshot{}, ErrManagedProxySnapshotUnavailable
	}
	truncated := len(cm.ctlsByRunID) > maxManagedSnapshotControls
	capacity := len(cm.ctlsByRunID)
	if capacity > maxManagedSnapshotControls {
		capacity = maxManagedSnapshotControls
	}
	candidates := make([]managedSnapshotCandidate, 0, capacity)
	for runID, entry := range cm.ctlsByRunID {
		if ctx.Err() != nil {
			cm.mu.RUnlock()
			return ManagedProxySnapshot{}, ctx.Err()
		}
		if len(candidates) == maxManagedSnapshotControls {
			break
		}
		if entry == nil || entry.ctl == nil || entry.runMu == nil {
			cm.mu.RUnlock()
			return ManagedProxySnapshot{}, ErrManagedProxySnapshotUnavailable
		}
		candidates = append(candidates, managedSnapshotCandidate{runID, entry, entry.ctl, entry.id, entry.runMu})
	}
	cm.mu.RUnlock()

	names := make(map[string]struct{})
	nameBytes, visits := 0, 0
	observed := make([]managedSnapshotCandidate, 0)
	for _, candidate := range candidates {
		matched, incomplete, stop, err := cm.readManagedControlNames(ctx, identity, candidate, names, &nameBytes, &visits)
		if err != nil {
			return ManagedProxySnapshot{}, err
		}
		if matched {
			observed = append(observed, candidate)
		}
		truncated = truncated || incomplete
		if stop {
			break
		}
	}
	// Detect a replacement or shutdown of a sampled control before returning.
	// Names are still observations from different instants, not an atomic list.
	for _, candidate := range observed {
		if err := cm.recheckManagedSnapshotControl(ctx, identity, candidate); err != nil {
			return ManagedProxySnapshot{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return ManagedProxySnapshot{}, err
	}
	if !cm.mu.TryRLock() {
		return ManagedProxySnapshot{}, ErrManagedProxySnapshotUnavailable
	}
	if cm.closed {
		cm.mu.RUnlock()
		return ManagedProxySnapshot{}, ErrManagedProxySnapshotUnavailable
	}
	cm.mu.RUnlock()
	result := ManagedProxySnapshot{Names: make([]string, 0, len(names)), MayBeIncomplete: true, Truncated: truncated}
	for name := range names {
		result.Names = append(result.Names, name)
	}
	sort.Strings(result.Names)
	return result, nil
}

func (cm *ControlManager) readManagedControlNames(ctx context.Context, identity ManagedIdentity, candidate managedSnapshotCandidate, names map[string]struct{}, nameBytes, visits *int) (matched, incomplete, stop bool, err error) {
	if err := ctx.Err(); err != nil {
		return false, false, false, err
	}
	// Follow the existing runMu -> manager -> lifecycle order. Never wait for
	// a run gate while holding the manager lock, or wait for any of these locks.
	// If an unknown control is busy, skip it without guessing its identity.
	if !candidate.runMu.TryLock() {
		return false, true, false, nil
	}
	defer candidate.runMu.Unlock()
	if !cm.mu.TryRLock() {
		return false, true, false, nil
	}
	ctl := candidate.control
	if cm.closed {
		cm.mu.RUnlock()
		return false, false, false, ErrManagedProxySnapshotUnavailable
	}
	if cm.ctlsByRunID[candidate.runID] != candidate.entry || candidate.entry.ctl != ctl || candidate.entry.id != candidate.id {
		cm.mu.RUnlock()
		return false, true, false, nil
	}
	if !ctl.lifecycleMu.TryLock() {
		cm.mu.RUnlock()
		return false, true, false, nil
	}
	// Keep the table entry stable while its names are copied, including when a
	// managed handoff is racing this sample. No lock acquisition here blocks.
	defer cm.mu.RUnlock()
	defer ctl.lifecycleMu.Unlock()
	if ctl.controlID != candidate.id || ctl.runID != candidate.runID {
		return false, true, false, nil
	}
	if ctl.sessionCtx == nil {
		return false, false, false, ErrManagedProxySnapshotUnavailable
	}
	if ctl.sessionCtx.managedIdentity != identity {
		return false, false, false, nil
	}
	if ctl.state != controlStateRunning || ctl.sessionCtx.managedProvider == nil ||
		ctl.managedScope == nil || ctl.managedState == nil || ctl.managedUncertain.Load() || ctl.managedFailedLocked() || ctl.managedExpiredLocked() {
		return false, false, false, ErrManagedProxySnapshotUnavailable
	}
	if !ctl.mu.TryRLock() {
		return false, false, false, ErrManagedProxySnapshotUnavailable
	}
	defer ctl.mu.RUnlock()
	prefix := identity.AccountID + "."
	for name := range ctl.proxies {
		if err := ctx.Err(); err != nil {
			return false, false, false, err
		}
		(*visits)++
		if *visits > maxManagedSnapshotVisits {
			return true, true, true, nil
		}
		if !strings.HasPrefix(name, prefix) || len(name) == len(prefix) {
			return false, false, false, ErrManagedProxySnapshotUnavailable
		}
		if len(name) > maxManagedSnapshotNameBytes {
			return true, true, true, nil
		}
		if !utf8.ValidString(name) {
			return false, false, false, ErrManagedProxySnapshotUnavailable
		}
		name = name[len(prefix):]
		if _, exists := names[name]; exists {
			continue
		}
		if len(names) >= maxManagedSnapshotNames || *nameBytes+len(name) > maxManagedSnapshotBytes {
			return true, true, true, nil
		}
		names[name] = struct{}{}
		*nameBytes += len(name)
		if len(names) == maxManagedSnapshotNames || *nameBytes == maxManagedSnapshotBytes {
			return true, true, true, nil
		}
	}
	return true, false, false, nil
}

func (cm *ControlManager) recheckManagedSnapshotControl(ctx context.Context, identity ManagedIdentity, candidate managedSnapshotCandidate) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !candidate.runMu.TryLock() {
		return ErrManagedProxySnapshotUnavailable
	}
	defer candidate.runMu.Unlock()
	if !cm.mu.TryRLock() {
		return ErrManagedProxySnapshotUnavailable
	}
	defer cm.mu.RUnlock()
	ctl := candidate.control
	if cm.closed || cm.ctlsByRunID[candidate.runID] != candidate.entry || candidate.entry.ctl != ctl || candidate.entry.id != candidate.id {
		return ErrManagedProxySnapshotUnavailable
	}
	if !ctl.lifecycleMu.TryLock() {
		return ErrManagedProxySnapshotUnavailable
	}
	defer ctl.lifecycleMu.Unlock()
	if ctl.controlID != candidate.id || ctl.runID != candidate.runID || ctl.state != controlStateRunning ||
		ctl.sessionCtx == nil || ctl.sessionCtx.managedProvider == nil || ctl.sessionCtx.managedIdentity != identity ||
		ctl.managedScope == nil || ctl.managedState == nil || ctl.managedUncertain.Load() || ctl.managedFailedLocked() || ctl.managedExpiredLocked() {
		return ErrManagedProxySnapshotUnavailable
	}
	return nil
}
