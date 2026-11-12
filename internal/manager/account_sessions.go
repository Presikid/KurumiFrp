package manager

import (
	"errors"
	"net/http"
	"time"
)

// revokeOtherSessionsAPI is routed without the ordinary read gate because the
// mutation needs the current account's exclusive gate instead.
func (a *App) revokeOtherSessionsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.RawQuery != "" {
		fail(w, http.StatusBadRequest, errors.New("会话请求无效"))
		return
	}
	var request struct{}
	if err := decode(w, r, &request); err != nil {
		fail(w, http.StatusBadRequest, errors.New("会话请求无效"))
		return
	}
	p := requestPrincipal(r)
	if !routeAllowed(p, r) {
		fail(w, http.StatusForbidden, errors.New("没有此操作的权限"))
		return
	}
	status, err := a.revokeOtherAccountSessions(r, p)
	if err != nil {
		fail(w, status, err)
		return
	}
	reply(w, http.StatusOK, map[string]bool{"ok": true})
}

// Revocation is an in-memory mutation: do not hold either lock while writing
// the HTTP response. Logins that complete after this mutation may create a new
// session; already admitted background work and frp connections are unaffected.
func (a *App) revokeOtherAccountSessions(r *http.Request, p principal) (int, error) {
	if r.Context().Err() != nil {
		return http.StatusRequestTimeout, errors.New("请求已取消，会话未更改")
	}
	if !p.Account || !p.PasswordAccount || p.AccountID == "" || p.gate == nil {
		return http.StatusForbidden, errors.New("没有此操作的权限")
	}
	if !p.gate.TryLock() {
		return http.StatusConflict, errors.New("账户操作繁忙，会话未更改，请稍后重试")
	}
	defer p.gate.Unlock()
	if r.Context().Err() != nil {
		return http.StatusRequestTimeout, errors.New("请求已取消，会话未更改")
	}
	if !a.auth.mu.TryLock() {
		return http.StatusServiceUnavailable, errors.New("会话操作繁忙，请稍后重试")
	}
	defer a.auth.mu.Unlock()
	current, valid := a.auth.identityLocked(r)
	if !valid || !current.Account || !current.PasswordAccount || current.AccountID != p.AccountID || current.gate != p.gate {
		return http.StatusUnauthorized, errors.New("请重新登录")
	}
	cookie, err := r.Cookie("kurumi_session")
	if err != nil {
		return http.StatusUnauthorized, errors.New("请重新登录")
	}
	session, exists := a.auth.sessions[cookie.Value]
	now := time.Now()
	if !exists || session.accountID != current.AccountID || !session.passwordAccount || session.oidcRevision != 0 ||
		!now.Before(session.expires) || (!current.accountExpires.IsZero() && !now.Before(current.accountExpires)) {
		return http.StatusUnauthorized, errors.New("请重新登录")
	}
	if r.Context().Err() != nil {
		return http.StatusRequestTimeout, errors.New("请求已取消，会话未更改")
	}
	for sid, session := range a.auth.sessions {
		if sid != cookie.Value && session.accountID == current.AccountID {
			delete(a.auth.sessions, sid)
		}
	}
	return http.StatusOK, nil
}
