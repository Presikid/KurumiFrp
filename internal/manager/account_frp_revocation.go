package manager

import (
	"context"
	"errors"
	"time"

	"github.com/fatedier/frp/server"
)

type accountFRPRevocationKey struct {
	accountID  string
	instanceID string
}

type accountFRPRevocation struct {
	key        accountFRPRevocationKey
	process    *process
	identities []server.ManagedIdentity
	invalid    bool
}

// Caller holds App.mu and the account's exclusive gate. The old identity is
// invalidated by the subsequent account write before this is published.
func (a *App) accountFRPRevocationLocked(account localAccount, instanceID string) *accountFRPRevocation {
	p := a.procs[instanceID]
	if !p.active() || p.managedAccountIncarnation == "" {
		return nil
	}
	r := &accountFRPRevocation{key: accountFRPRevocationKey{account.ID, instanceID}, process: p}
	seen := make(map[server.ManagedIdentity]bool)
	for _, token := range account.FRPTokens {
		if token.InstanceID != instanceID || token.Incarnation != p.managedAccountIncarnation {
			continue
		}
		identity := server.ManagedIdentity{AccountID: account.ID, CredentialID: token.CredentialID, Version: token.Version}
		if !accountIDPattern.MatchString(identity.AccountID) || !accountIDPattern.MatchString(identity.CredentialID) ||
			identity.Version == 0 || identity.Version > maxJSONRevision {
			r.invalid = true
			continue
		}
		if !seen[identity] {
			seen[identity] = true
			r.identities = append(r.identities, identity)
		}
	}
	if len(r.identities) == 0 && !r.invalid {
		return nil
	}
	return r
}

// Caller holds App.mu and the account's exclusive gate.
func (a *App) accountFRPRevocationsLocked(account localAccount) []*accountFRPRevocation {
	var result []*accountFRPRevocation
	seen := make(map[string]bool)
	for _, token := range account.FRPTokens {
		if seen[token.InstanceID] {
			continue
		}
		seen[token.InstanceID] = true
		if r := a.accountFRPRevocationLocked(account, token.InstanceID); r != nil {
			result = append(result, r)
		}
	}
	return result
}

// Caller holds App.mu. A prior unresolved revocation must not be replaced by
// a later edit of the same account.
func (a *App) accountFRPRevocationPendingLocked(accountID string) bool {
	for key := range a.accountFRPRevocations {
		if key.accountID == accountID {
			return true
		}
	}
	return false
}

// Caller holds App.mu. No later admission can pass the provider until the old
// connections are confirmed closed or the workbench is restarted.
func (a *App) publishAccountFRPRevocationLocked(r *accountFRPRevocation) {
	if r == nil {
		return
	}
	if a.accountFRPRevocations == nil {
		a.accountFRPRevocations = make(map[accountFRPRevocationKey]*accountFRPRevocation)
	}
	a.accountFRPRevocations[r.key] = r
}

// finishAccountFRPRevocation never holds manager or account locks while it
// waits for the server. A failed or timed-out close requests that this exact
// run stop and leaves the account/instance blocked until a workbench restart.
func (a *App) finishAccountFRPRevocation(ctx context.Context, r *accountFRPRevocation) bool {
	if r == nil {
		return true
	}
	revoker, ok := r.process.instance.(RuntimeManagedCredentialRevoker)
	complete := ctx != nil && ctx.Err() == nil && ok && !r.invalid
	if complete {
		for _, identity := range r.identities {
			if err := callAccountFRPRevoker(ctx, revoker, identity); err != nil {
				complete = false
				break
			}
		}
	}
	if complete && a.clearAccountFRPRevocation(ctx, r) {
		return true
	}
	stopAccountFRPRun(r.process)
	return false
}

// stopAccountFRPRuns is scheduled after an uncertain account-file write.
// Future admissions already fail on the auth flag; stop existing managed runs
// because their effective on-disk account state is no longer known.
func (a *App) stopAccountFRPRuns() {
	a.mu.Lock()
	runs := make([]*process, 0, len(a.procs))
	for _, p := range a.procs {
		if p.active() && p.managedAccountIncarnation != "" {
			runs = append(runs, p)
		}
	}
	a.mu.Unlock()
	for _, p := range runs {
		stopAccountFRPRun(p)
	}
}

func stopAccountFRPRun(p *process) {
	go func() {
		// Report an unconfirmed stop without preventing other runs from stopping.
		defer func() {
			if recover() != nil {
				reportAccountFRPStopUnconfirmed(p)
			}
		}()
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer stopCancel()
		if err := p.instance.Stop(stopCtx); err != nil {
			reportAccountFRPStopUnconfirmed(p)
			return
		}
		select {
		case <-p.done:
		case <-stopCtx.Done():
			reportAccountFRPStopUnconfirmed(p)
		}
	}()
}

func reportAccountFRPStopUnconfirmed(p *process) {
	// A failing log sink must not crash the workbench.
	defer func() { _ = recover() }()
	if p != nil && p.active() && p.logs != nil {
		_, _ = p.logs.Write([]byte("\n受管实例停止未确认；已有 FRP 转发可能仍在运行，请确认工作台完全停止后再重启核对\n"))
	}
}

func (a *App) clearAccountFRPRevocation(ctx context.Context, r *accountFRPRevocation) bool {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return false
		}
		if a.mu.TryLock() {
			valid := ctx.Err() == nil && a.accountFRPRevocations[r.key] == r
			if valid {
				delete(a.accountFRPRevocations, r.key)
			}
			a.mu.Unlock()
			return valid
		}
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}

// Some runtime locks cannot observe context cancellation. The caller must
// still stop waiting at the deadline; a late result cannot clear the block.
func callAccountFRPRevoker(ctx context.Context, revoker RuntimeManagedCredentialRevoker, identity server.ManagedIdentity) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	result := make(chan error, 1)
	go func() { result <- invokeAccountFRPRevoker(ctx, revoker, identity) }()
	select {
	case err := <-result:
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func invokeAccountFRPRevoker(ctx context.Context, revoker RuntimeManagedCredentialRevoker, identity server.ManagedIdentity) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("managed credential revocation failed")
		}
	}()
	return revoker.RevokeManagedCredential(ctx, identity)
}
