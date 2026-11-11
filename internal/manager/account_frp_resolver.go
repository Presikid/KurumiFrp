package manager

import (
	"context"
	"crypto/subtle"
	"errors"
	"time"
)

var errAccountFRPLogin = errors.New("account credentials are unavailable")

// Current encrypted 64-character tokens are shorter than this limit. The
// explicit bound keeps corrupt account records from causing large allocations.
const maxAccountFRPEncryptedTokenSize = 256

// accountFRPLoginResolver binds a resolver to one local frps incarnation.
// Callers must not hold App.mu or auth.mu while invoking it. The resolver uses
// only TryLock and in-memory operations, and fails closed when state is busy.
// It observes committed state at lookup time; a change still waiting to commit,
// an already returned lookup, or an existing control is not revoked by it.
// This helper is not connected to the runtime or enabled by its creation.
func (a *App) accountFRPLoginResolver(instanceID, incarnation string) func(context.Context, string) (string, error) {
	return func(ctx context.Context, accountID string) (token string, err error) {
		// Fail closed without leaking a token even if an unexpected in-memory
		// state or callback context causes a panic. Deferred unlocks run first.
		defer func() {
			if recover() != nil {
				token, err = "", errAccountFRPLogin
			}
		}()
		if a == nil || ctx == nil || ctx.Err() != nil || len(instanceID) != 16 || !idPattern.MatchString(instanceID) ||
			len(incarnation) != 32 || !validLocalTrafficID(incarnation) || len(accountID) != 32 || !accountIDPattern.MatchString(accountID) {
			return "", errAccountFRPLogin
		}
		if !a.mu.TryLock() {
			return "", errAccountFRPLogin
		}
		defer a.mu.Unlock()
		if !a.auth.mu.TryLock() {
			return "", errAccountFRPLogin
		}
		defer a.auth.mu.Unlock()
		if ctx.Err() != nil || a.closing || a.store == nil || a.auth.accountLoadErr != nil ||
			len(a.store.DB.State.Instances) > 64 || len(a.auth.accounts) > maxAccounts {
			return "", errAccountFRPLogin
		}
		instance, exists := a.store.DB.State.Instance(instanceID)
		if !exists || instance.Kind != "frps" || a.store.DB.LocalTrafficIDs[instanceID] != incarnation {
			return "", errAccountFRPLogin
		}

		// A list beyond the storage limit cannot be fully checked within the
		// resolver's bound. An administrator must remove the excess records.
		accounts := a.auth.accounts
		target := -1
		targetCount := 0
		for i := range accounts {
			user := &accounts[i]
			if ctx.Err() != nil || len(user.FRPTokens) > maxAccountFRPTokens {
				return "", errAccountFRPLogin
			}
			if user.ID == accountID {
				target = i
				targetCount++
			}
		}
		if targetCount != 1 || len(accounts[target].ExpiresAt) > 64 || !accounts[target].activeAt(time.Now()) {
			return "", errAccountFRPLogin
		}

		// The target must have one well-formed record for this instance, bound
		// to this incarnation, before any token can be returned.
		matches := 0
		selected := ""
		for _, entry := range accounts[target].FRPTokens {
			if entry.InstanceID != instanceID {
				continue
			}
			matches++
			if matches != 1 || entry.Incarnation != incarnation || len(entry.CredentialID) != 32 ||
				!accountIDPattern.MatchString(entry.CredentialID) || entry.Version == 0 || entry.Version > maxJSONRevision ||
				len(entry.Encrypted) == 0 || len(entry.Encrypted) > maxAccountFRPEncryptedTokenSize {
				return "", errAccountFRPLogin
			}
			plain, decryptErr := a.store.decrypt(entry.Encrypted)
			if decryptErr != nil || len(plain) != 64 || !nodeTokenPattern.MatchString(plain) {
				return "", errAccountFRPLogin
			}
			selected = plain
		}
		if matches != 1 {
			return "", errAccountFRPLogin
		}

		// A proof does not bind the account ID. Compare every decryptable token
		// for this incarnation, including disabled accounts and records with
		// bad metadata. Unknown plaintext cannot be ruled out as a collision;
		// the administrator must disable or replace that record first.
		for i := range accounts {
			if ctx.Err() != nil {
				return "", errAccountFRPLogin
			}
			if i == target {
				continue
			}
			for _, entry := range accounts[i].FRPTokens {
				if entry.InstanceID != instanceID || entry.Incarnation != incarnation {
					continue
				}
				if len(entry.Encrypted) == 0 || len(entry.Encrypted) > maxAccountFRPEncryptedTokenSize {
					return "", errAccountFRPLogin
				}
				plain, decryptErr := a.store.decrypt(entry.Encrypted)
				if decryptErr != nil {
					return "", errAccountFRPLogin
				}
				if len(plain) == 64 && subtle.ConstantTimeCompare([]byte(selected), []byte(plain)) == 1 {
					return "", errAccountFRPLogin
				}
			}
		}
		if ctx.Err() != nil || !accounts[target].activeAt(time.Now()) {
			return "", errAccountFRPLogin
		}
		return selected, nil
	}
}
