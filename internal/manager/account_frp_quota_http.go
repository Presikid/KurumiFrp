package manager

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"
)

type accountFRPQuotaSnapshot struct {
	used      int
	limit     *int
	sampledAt time.Time
	revision  uint64
}

// accountFRPProxyQuotaAPI runs while the router holds the account read gate.
// Counts describe this account's reservations across this workbench, not
// online clients, individual servers, bandwidth, or traffic.
func (a *App) accountFRPProxyQuotaAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	unavailable := func() { fail(w, http.StatusServiceUnavailable, errors.New("配额暂不可用")) }
	p := requestPrincipal(r)
	if r.Method != http.MethodGet || r.URL.RawQuery != "" || !routeAllowed(p, r) || p.sessionExpires.IsZero() {
		unavailable()
		return
	}
	deadline := time.Now().Add(2 * time.Second)
	if p.sessionExpires.Before(deadline) {
		deadline = p.sessionExpires
	}
	if !p.accountExpires.IsZero() && p.accountExpires.Before(deadline) {
		deadline = p.accountExpires
	}
	if !time.Now().Before(deadline) {
		unavailable()
		return
	}
	controller := http.NewResponseController(w)
	if err := controller.SetWriteDeadline(deadline); err != nil {
		unavailable()
		return
	}
	ctx, cancel := context.WithDeadline(r.Context(), deadline)
	defer cancel()
	r = r.WithContext(ctx)
	snapshot, ok := a.captureAccountFRPQuota(r, p)
	if !ok {
		unavailable()
		return
	}
	body, err := json.Marshal(struct {
		Used      int       `json:"used"`
		Limit     *int      `json:"limit"`
		SampledAt time.Time `json:"sampledAt"`
	}{snapshot.used, snapshot.limit, snapshot.sampledAt})
	if err != nil || !a.currentAccountFRPQuotaSession(r, p, snapshot.revision) || ctx.Err() != nil || !time.Now().Before(deadline) {
		unavailable()
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	// Flush while the account gate is held; never leave quota bytes buffered
	// for a later response after session revocation has completed.
	if err := controller.Flush(); err != nil || ctx.Err() != nil || !time.Now().Before(deadline) {
		return
	}
	if _, err := w.Write(body); err != nil {
		return
	}
	_ = controller.Flush()
}

// The caller holds p.gate.RLock. Take auth.mu before the slot lock, matching
// reservation and administrative reads; never wait for either lock.
func (a *App) captureAccountFRPQuota(r *http.Request, p principal) (accountFRPQuotaSnapshot, bool) {
	if r.Context().Err() != nil || embeddedCleanupUnknown.Load() || !a.auth.mu.TryLock() {
		return accountFRPQuotaSnapshot{}, false
	}
	defer a.auth.mu.Unlock()
	current, valid := a.auth.identityLocked(r)
	if !valid || !sameAccountFRPQuotaSession(p, current) || a.auth.accountLoadErr != nil || a.auth.accountsWriteUncertain ||
		len(a.auth.accounts) > maxAccounts || embeddedCleanupUnknown.Load() {
		return accountFRPQuotaSnapshot{}, false
	}
	var account *localAccount
	for i := range a.auth.accounts {
		if a.auth.accounts[i].ID == current.AccountID {
			if account != nil {
				return accountFRPQuotaSnapshot{}, false
			}
			account = &a.auth.accounts[i]
		}
	}
	if account == nil || account.gate != p.gate || !account.activeAt(time.Now()) {
		return accountFRPQuotaSnapshot{}, false
	}
	var limit *int
	if account.FRPProxyLimit != nil {
		value := *account.FRPProxyLimit
		if value < 0 || value > maxAccountFRPProxies {
			return accountFRPQuotaSnapshot{}, false
		}
		limit = &value
	}
	if !a.auth.frpProxySlotsMu.TryLock() {
		return accountFRPQuotaSnapshot{}, false
	}
	used := a.auth.frpProxySlots[current.AccountID]
	sampledAt := time.Now().UTC()
	a.auth.frpProxySlotsMu.Unlock()
	if used < 0 || uint64(used) > maxJSONRevision || embeddedCleanupUnknown.Load() || r.Context().Err() != nil {
		return accountFRPQuotaSnapshot{}, false
	}
	return accountFRPQuotaSnapshot{used: used, limit: limit, sampledAt: sampledAt, revision: current.AccountRevision}, true
}

func (a *App) currentAccountFRPQuotaSession(r *http.Request, p principal, revision uint64) bool {
	if r.Context().Err() != nil || embeddedCleanupUnknown.Load() || !a.auth.mu.TryLock() {
		return false
	}
	defer a.auth.mu.Unlock()
	current, valid := a.auth.identityLocked(r)
	return valid && sameAccountFRPQuotaSession(p, current) && current.AccountRevision == revision &&
		a.auth.accountLoadErr == nil && !a.auth.accountsWriteUncertain && !embeddedCleanupUnknown.Load() && r.Context().Err() == nil
}

func sameAccountFRPQuotaSession(expected, current principal) bool {
	return expected.Account && expected.PasswordAccount && accountIDPattern.MatchString(expected.AccountID) && expected.gate != nil &&
		current.Account && current.PasswordAccount && current.AccountID == expected.AccountID && current.gate == expected.gate &&
		current.sessionExpires.Equal(expected.sessionExpires) && current.accountExpires.Equal(expected.accountExpires) &&
		(current.Role == "admin" || current.Role == "operator" || current.Role == "viewer")
}
