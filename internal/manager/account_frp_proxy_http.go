package manager

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// tryLockManagedProxyRequest avoids waiting indefinitely for either auth.mu
// or the account gate. The cookie is resolved again after the gate is held.
func (a *auth) tryLockManagedProxyRequest(r *http.Request) (principal, func(), bool) {
	if r == nil || r.Context().Err() != nil || !a.mu.TryLock() {
		return principal{}, nil, false
	}
	p, valid := a.identityLocked(r)
	a.mu.Unlock()
	if !valid || r.Context().Err() != nil {
		return principal{}, nil, false
	}
	if !p.Account || !p.PasswordAccount || p.gate == nil {
		return p, func() {}, true
	}
	if !p.gate.TryRLock() {
		return principal{}, nil, false
	}
	if r.Context().Err() != nil || !a.mu.TryLock() {
		p.gate.RUnlock()
		return principal{}, nil, false
	}
	current, valid := a.identityLocked(r)
	a.mu.Unlock()
	if !valid || !current.Account || !current.PasswordAccount || current.AccountID != p.AccountID ||
		current.gate != p.gate || r.Context().Err() != nil {
		p.gate.RUnlock()
		return principal{}, nil, false
	}
	return current, p.gate.RUnlock, true
}

// accountManagedProxySnapshotAPI runs with the account gate already held by
// the normal request router. It must not acquire that gate again.
func (a *App) accountManagedProxySnapshotAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	unavailable := func() { fail(w, http.StatusServiceUnavailable, errors.New("采样不可用")) }
	p := requestPrincipal(r)
	if r.Method != http.MethodGet || !routeAllowed(p, r) || p.sessionExpires.IsZero() {
		unavailable()
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query) != 1 || len(query["id"]) != 1 || !idPattern.MatchString(query["id"][0]) {
		unavailable()
		return
	}
	instanceID := query["id"][0]
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
	if err = controller.SetWriteDeadline(deadline); err != nil {
		unavailable()
		return
	}
	ctx, cancel := context.WithDeadline(r.Context(), deadline)
	defer cancel()
	r = r.WithContext(ctx)
	result, err := a.readAccountManagedProxySnapshot(r, p, instanceID)
	if err != nil {
		unavailable()
		return
	}
	// Never serialize the internal scope, credential metadata, or a flag based
	// on other controls. These names are observations during the stated window.
	body, err := json.Marshal(struct {
		Names           []string  `json:"names"`
		StartedAt       time.Time `json:"startedAt"`
		SampledAt       time.Time `json:"sampledAt"`
		MayBeIncomplete bool      `json:"mayBeIncomplete"`
	}{result.snapshot.Names, result.snapshot.StartedAt, result.snapshot.SampledAt, true})
	if err != nil {
		unavailable()
		return
	}
	scope, err := a.captureAccountManagedProxyScope(r, p, instanceID)
	if err != nil || scope != result.scope || scope.run.managedProxySnapshotServer() == nil {
		unavailable()
		return
	}
	// Recheck the same session without taking its already-held account gate.
	if !a.auth.mu.TryLock() {
		unavailable()
		return
	}
	current, valid := a.auth.identityLocked(r)
	valid = valid && a.auth.accountLoadErr == nil && !a.auth.accountsWriteUncertain &&
		current.Account && current.PasswordAccount && current.AccountID == p.AccountID && current.gate == p.gate &&
		current.AccountRevision == p.AccountRevision && current.sessionExpires.Equal(p.sessionExpires) &&
		current.accountExpires.Equal(p.accountExpires) && current.canReadConfig() && current.canView(instanceID)
	a.auth.mu.Unlock()
	if !valid || ctx.Err() != nil || !time.Now().Before(deadline) {
		unavailable()
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	// Flush headers first so an unsupported flusher cannot leave sensitive bytes
	// buffered until after the gate is released. The deadline bounds both flushes.
	if err := controller.Flush(); err != nil || ctx.Err() != nil || !time.Now().Before(deadline) {
		return
	}
	if _, err := w.Write(body); err != nil {
		return
	}
	_ = controller.Flush()
}
