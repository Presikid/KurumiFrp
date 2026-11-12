package manager

import (
	"context"
	"crypto/subtle"
	"errors"
	"sync"
	"time"

	"github.com/fatedier/frp/server"
)

var errAccountFRPLogin = errors.New("account credentials are unavailable")

// Bound corrupted records before decrypting them under the state locks.
const maxAccountFRPEncryptedTokenSize = 256

type accountFRPProvider struct {
	app         *App
	instanceID  string
	incarnation string
}

// accountFRPLoginProvider creates a provider bound to one frps incarnation.
// The factory does not take a lock and can be called while App.mu is held.
func (a *App) accountFRPLoginProvider(instanceID, incarnation string) server.ManagedLoginProvider {
	return &accountFRPProvider{app: a, instanceID: instanceID, incarnation: incarnation}
}

// ResolveLogin observes committed state without waiting for manager locks.
// The server must call TryAuthorize after checking the returned token proof.
func (p *accountFRPProvider) ResolveLogin(ctx context.Context, accountID string) (credential server.ManagedCredential, err error) {
	defer func() {
		if recover() != nil {
			credential, err = server.ManagedCredential{}, errAccountFRPLogin
		}
	}()
	credential, _, err = p.lookup(ctx, accountID)
	return credential, err
}

// TryAuthorize holds the target account's read gate only for the server's
// in-memory admission step. Writers take the same gate before changing the
// account, so a previously resolved credential cannot be admitted after its
// replacement has committed. All manager locks are acquired with TryLock.
func (p *accountFRPProvider) TryAuthorize(identity server.ManagedIdentity) (release func(), expiresAt time.Time, err error) {
	var gate *sync.RWMutex
	gateHeld, appHeld, authHeld := false, false, false
	defer func() {
		if recover() != nil {
			if authHeld {
				p.app.auth.mu.Unlock()
			}
			if appHeld {
				p.app.mu.Unlock()
			}
			if gateHeld {
				gate.RUnlock()
			}
			release, expiresAt, err = nil, time.Time{}, errAccountFRPLogin
		}
	}()
	if p == nil || p.app == nil || !accountIDPattern.MatchString(identity.AccountID) ||
		!accountIDPattern.MatchString(identity.CredentialID) || identity.Version == 0 || identity.Version > maxJSONRevision {
		return nil, time.Time{}, errAccountFRPLogin
	}
	gate = p.accountGate(identity.AccountID)
	if gate == nil || !gate.TryRLock() {
		return nil, time.Time{}, errAccountFRPLogin
	}
	gateHeld = true
	a := p.app
	if !a.mu.TryLock() {
		gate.RUnlock()
		gateHeld = false
		return nil, time.Time{}, errAccountFRPLogin
	}
	appHeld = true
	if !a.auth.mu.TryLock() {
		a.mu.Unlock()
		appHeld = false
		gate.RUnlock()
		gateHeld = false
		return nil, time.Time{}, errAccountFRPLogin
	}
	authHeld = true
	current, currentGate, lookupErr := p.lookupLocked(context.Background(), identity.AccountID)
	if lookupErr != nil || currentGate != gate || current.Identity != identity {
		a.auth.mu.Unlock()
		authHeld = false
		a.mu.Unlock()
		appHeld = false
		gate.RUnlock()
		gateHeld = false
		return nil, time.Time{}, errAccountFRPLogin
	}
	// lookupLocked has already validated the unique account and its expiry
	// while these locks were held, so the same value is safe to pass to frp.
	for _, account := range a.auth.accounts {
		if account.ID == identity.AccountID && account.ExpiresAt != "" {
			expiresAt, _ = time.Parse(time.RFC3339, account.ExpiresAt)
			break
		}
	}
	// The service releases these immediately after publishing its in-memory
	// control. Keeping both state locks makes a global write failure or an
	// instance replacement linearize on one side of that publication.
	authHeld, appHeld, gateHeld = false, false, false
	return func() {
		a.auth.mu.Unlock()
		a.mu.Unlock()
		gate.RUnlock()
	}, expiresAt, nil
}

// accountGate reads the pointer without waiting; lookup checks it again after
// the read gate has been acquired, in case the account was replaced.
func (p *accountFRPProvider) accountGate(accountID string) *sync.RWMutex {
	if !p.app.auth.mu.TryLock() {
		return nil
	}
	defer p.app.auth.mu.Unlock()
	var gate *sync.RWMutex
	count := 0
	for i := range p.app.auth.accounts {
		if p.app.auth.accounts[i].ID == accountID {
			gate = p.app.auth.accounts[i].gate
			count++
		}
	}
	if count != 1 {
		return nil
	}
	return gate
}

// lookup holds App.mu then auth.mu only for bounded, in-memory reads. It does
// not promise to revoke an existing control or a lookup already returned.
func (p *accountFRPProvider) lookup(ctx context.Context, accountID string) (server.ManagedCredential, *sync.RWMutex, error) {
	invalid := func() (server.ManagedCredential, *sync.RWMutex, error) {
		return server.ManagedCredential{}, nil, errAccountFRPLogin
	}
	if p == nil || p.app == nil {
		return invalid()
	}
	a := p.app
	if !a.mu.TryLock() {
		return invalid()
	}
	defer a.mu.Unlock()
	if !a.auth.mu.TryLock() {
		return invalid()
	}
	defer a.auth.mu.Unlock()
	return p.lookupLocked(ctx, accountID)
}

// lookupLocked requires App.mu and auth.mu. Its caller controls whether those
// locks are released after the lookup or after the service admits a control.
func (p *accountFRPProvider) lookupLocked(ctx context.Context, accountID string) (server.ManagedCredential, *sync.RWMutex, error) {
	invalid := func() (server.ManagedCredential, *sync.RWMutex, error) {
		return server.ManagedCredential{}, nil, errAccountFRPLogin
	}
	if ctx == nil || ctx.Err() != nil || !idPattern.MatchString(p.instanceID) ||
		!validLocalTrafficID(p.incarnation) || !accountIDPattern.MatchString(accountID) {
		return invalid()
	}
	a := p.app
	if ctx.Err() != nil || a.closing || a.store == nil || a.auth.accountLoadErr != nil || a.auth.accountsWriteUncertain ||
		len(a.store.DB.State.Instances) > 64 || len(a.auth.accounts) > maxAccounts {
		return invalid()
	}
	instance, exists := a.store.DB.State.Instance(p.instanceID)
	if !exists || instance.Kind != "frps" || a.store.DB.LocalTrafficIDs[p.instanceID] != p.incarnation {
		return invalid()
	}
	if a.accountFRPRevocations[accountFRPRevocationKey{accountID, p.instanceID}] != nil {
		return invalid()
	}

	accounts := a.auth.accounts
	target, targetCount := -1, 0
	for i := range accounts {
		user := &accounts[i]
		if ctx.Err() != nil || len(user.FRPTokens) > maxAccountFRPTokens {
			return invalid()
		}
		if user.ID == accountID {
			target, targetCount = i, targetCount+1
		}
	}
	if targetCount != 1 || accounts[target].gate == nil || len(accounts[target].ExpiresAt) > 64 ||
		!accounts[target].activeAt(time.Now()) {
		return invalid()
	}

	matches := 0
	var selected accountFRPToken
	var token string
	for _, entry := range accounts[target].FRPTokens {
		if entry.InstanceID != p.instanceID {
			continue
		}
		matches++
		if matches != 1 || entry.Incarnation != p.incarnation || !accountIDPattern.MatchString(entry.CredentialID) ||
			entry.Version == 0 || entry.Version > maxJSONRevision || len(entry.Encrypted) == 0 ||
			len(entry.Encrypted) > maxAccountFRPEncryptedTokenSize {
			return invalid()
		}
		plain, err := a.store.decrypt(entry.Encrypted)
		if err != nil || !nodeTokenPattern.MatchString(plain) {
			return invalid()
		}
		selected, token = entry, plain
	}
	if matches != 1 {
		return invalid()
	}

	// A proof does not bind the account or instance. Every other stored token,
	// including stale and disabled records, participates in collision detection.
	// Unknown plaintext fails closed rather than allowing a possible collision.
	for i := range accounts {
		if ctx.Err() != nil {
			return invalid()
		}
		for _, entry := range accounts[i].FRPTokens {
			// The target's instance entry was already proven unique above.
			if i == target && entry.InstanceID == p.instanceID {
				continue
			}
			if len(entry.Encrypted) == 0 || len(entry.Encrypted) > maxAccountFRPEncryptedTokenSize {
				return invalid()
			}
			plain, err := a.store.decrypt(entry.Encrypted)
			if err != nil {
				return invalid()
			}
			if len(plain) == len(token) && subtle.ConstantTimeCompare([]byte(token), []byte(plain)) == 1 {
				return invalid()
			}
		}
	}
	if ctx.Err() != nil || !accounts[target].activeAt(time.Now()) {
		return invalid()
	}
	identity := server.ManagedIdentity{AccountID: accountID, CredentialID: selected.CredentialID, Version: selected.Version}
	return server.ManagedCredential{Identity: identity, Token: token}, accounts[target].gate, nil
}
