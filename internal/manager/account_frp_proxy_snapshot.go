package manager

import (
	"context"
	"net/http"
	"time"

	"github.com/Presikid/KurumiFrp/internal/embedded"
	frpserver "github.com/fatedier/frp/server"
)

// accountManagedProxyScope contains identity metadata only, never a token or
// password. A process pointer and run ID distinguish a restarted frps.
type accountManagedProxyScope struct {
	identity        frpserver.ManagedIdentity
	incarnation     string
	runID           string
	accountRevision uint64
	process         *process
	run             *embeddedRun
}

// Keep the authorization scope alongside the data for one final comparison
// after encoding and immediately before a future bounded HTTP response.
type accountManagedProxyRead struct {
	snapshot frpserver.ManagedProxySnapshot
	scope    accountManagedProxyScope
}

// readAccountManagedProxySnapshot is an internal building block, not an HTTP
// endpoint. Its caller must have verified that p is a local-password account
// session and must keep p.gate.RLock held through its final authorization check
// and bounded response write. The caller must also bound the write by both
// session and account expiry.
func (a *App) readAccountManagedProxySnapshot(r *http.Request, p principal, instanceID string) (accountManagedProxyRead, error) {
	if r == nil {
		return accountManagedProxyRead{}, frpserver.ErrManagedProxySnapshotUnavailable
	}
	ctx := r.Context()
	before, err := a.captureAccountManagedProxyScope(r, p, instanceID)
	if err != nil {
		return accountManagedProxyRead{}, err
	}
	result, err := before.run.readManagedProxyNames(ctx, before.identity)
	if err != nil {
		return accountManagedProxyRead{}, err
	}
	after, err := a.captureAccountManagedProxyScope(r, p, instanceID)
	if err != nil {
		return accountManagedProxyRead{}, err
	}
	if before != after || before.run.managedProxySnapshotServer() == nil {
		return accountManagedProxyRead{}, frpserver.ErrManagedProxySnapshotUnavailable
	}
	if err := ctx.Err(); err != nil {
		return accountManagedProxyRead{}, err
	}
	return accountManagedProxyRead{snapshot: result, scope: before}, nil
}

// The caller holds the target account's read gate. App.mu and auth.mu protect
// the brief scope capture only; neither is held across the frp snapshot.
func (a *App) captureAccountManagedProxyScope(r *http.Request, p principal, instanceID string) (accountManagedProxyScope, error) {
	invalid := func() (accountManagedProxyScope, error) {
		return accountManagedProxyScope{}, frpserver.ErrManagedProxySnapshotUnavailable
	}
	if a == nil || r == nil || !idPattern.MatchString(instanceID) || !p.Account || !p.PasswordAccount || p.gate == nil ||
		(p.Role != "admin" && p.Role != "operator" && p.Role != "viewer") || !p.canReadConfig() || !p.canView(instanceID) ||
		!accountIDPattern.MatchString(p.AccountID) {
		return invalid()
	}
	if err := r.Context().Err(); err != nil {
		return accountManagedProxyScope{}, err
	}
	if !a.mu.TryLock() {
		return invalid()
	}
	defer a.mu.Unlock()
	if !a.auth.mu.TryLock() {
		return invalid()
	}
	defer a.auth.mu.Unlock()
	if a.closing || a.store == nil || a.auth.accountLoadErr != nil || a.auth.accountsWriteUncertain ||
		len(a.auth.accounts) > maxAccounts || len(a.store.DB.State.Instances) > 64 || a.operations[instanceID] != nil {
		return invalid()
	}
	current, ok := a.auth.identityLocked(r)
	if !ok || !current.Account || !current.PasswordAccount || current.AccountID != p.AccountID || current.gate != p.gate ||
		current.AccountRevision != p.AccountRevision || (current.Role != "admin" && current.Role != "operator" && current.Role != "viewer") ||
		!current.canReadConfig() || !current.canView(instanceID) {
		return invalid()
	}
	instance, ok := a.store.DB.State.Instance(instanceID)
	incarnation := a.store.DB.LocalTrafficIDs[instanceID]
	if !ok || instance.Kind != "frps" || !validLocalTrafficID(incarnation) ||
		a.accountFRPRevocations[accountFRPRevocationKey{current.AccountID, instanceID}] != nil {
		return invalid()
	}
	var account *localAccount
	for i := range a.auth.accounts {
		if a.auth.accounts[i].ID != current.AccountID {
			continue
		}
		if account != nil {
			return invalid()
		}
		account = &a.auth.accounts[i]
	}
	if account == nil || account.gate != p.gate || !account.activeAt(time.Now()) || len(account.FRPTokens) > maxAccountFRPTokens {
		return invalid()
	}
	identity := frpserver.ManagedIdentity{AccountID: current.AccountID}
	credentialMatches := 0
	for i := range account.FRPTokens {
		entry := &account.FRPTokens[i]
		if entry.InstanceID != instanceID {
			continue
		}
		credentialMatches++
		if credentialMatches != 1 || entry.Incarnation != incarnation || !accountIDPattern.MatchString(entry.CredentialID) ||
			entry.Version == 0 || entry.Version > maxJSONRevision {
			return invalid()
		}
		identity.CredentialID, identity.Version = entry.CredentialID, entry.Version
	}
	if credentialMatches != 1 {
		return invalid()
	}
	proc := a.procs[instanceID]
	if proc == nil || !proc.mu.TryLock() {
		return invalid()
	}
	defer proc.mu.Unlock()
	run, ok := proc.instance.(*embeddedRun)
	if !ok || run == nil || !run.managedAccountLogin || run.cleanupUncertain.Load() ||
		proc.managedAccountIncarnation != incarnation || !proc.active() || proc.stopRequested ||
		proc.status.State != "running" || proc.status.RunID == "" {
		return invalid()
	}
	return accountManagedProxyScope{
		identity:        identity,
		incarnation:     incarnation,
		runID:           proc.status.RunID,
		accountRevision: current.AccountRevision,
		process:         proc,
		run:             run,
	}, nil
}

func (r *embeddedRun) readManagedProxyNames(ctx context.Context, identity frpserver.ManagedIdentity) (frpserver.ManagedProxySnapshot, error) {
	if ctx == nil {
		return frpserver.ManagedProxySnapshot{}, frpserver.ErrManagedProxySnapshotUnavailable
	}
	if err := ctx.Err(); err != nil {
		return frpserver.ManagedProxySnapshot{}, err
	}
	service := r.managedProxySnapshotServer()
	if service == nil {
		return frpserver.ManagedProxySnapshot{}, frpserver.ErrManagedProxySnapshotUnavailable
	}
	result, err := service.ReadManagedProxyNames(ctx, identity)
	if err != nil {
		return frpserver.ManagedProxySnapshot{}, err
	}
	if err = ctx.Err(); err != nil {
		return frpserver.ManagedProxySnapshot{}, err
	}
	if r.managedProxySnapshotServer() != service {
		return frpserver.ManagedProxySnapshot{}, frpserver.ErrManagedProxySnapshotUnavailable
	}
	return result, nil
}

func (r *embeddedRun) managedProxySnapshotServer() *embedded.Server {
	if r == nil || !r.managedAccountLogin || r.cleanupUncertain.Load() || embeddedCleanupUnknown.Load() {
		return nil
	}
	select {
	case <-r.ready:
	default:
		return nil
	}
	if !r.mu.TryRLock() {
		return nil
	}
	service, exiting := r.server, r.engineExit
	r.mu.RUnlock()
	if service == nil || exiting == nil {
		return nil
	}
	select {
	case <-exiting:
		return nil
	case <-r.exiting:
		return nil
	case <-r.done:
		return nil
	default:
		return service
	}
}
