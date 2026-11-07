package manager

import (
	"fmt"
	"net/http"
	"time"
)

// Launch tickets are short-lived, single-use credentials placed only in a URL
// fragment. The persistent access token is never put in a URL or browser history.
func (a *App) BrowserURL() string {
	a.auth.mu.Lock()
	defer a.auth.mu.Unlock()
	if a.auth.tickets == nil {
		a.auth.tickets = map[string]time.Time{}
	}
	for k, t := range a.auth.tickets {
		if time.Now().After(t) {
			delete(a.auth.tickets, k)
		}
	}
	if len(a.auth.tickets) >= 8 {
		return "http://" + a.addr + "/"
	}
	ticket := randomHex(32)
	a.auth.tickets[ticket] = time.Now().Add(time.Minute)
	return "http://" + a.addr + "/#launch=" + ticket
}
func (a *App) launchLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Ticket string `json:"ticket"`
	}
	if err := decode(w, r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	a.auth.mu.Lock()
	expires, ok := a.auth.tickets[req.Ticket]
	delete(a.auth.tickets, req.Ticket)
	for k, t := range a.auth.sessions {
		if time.Now().After(t) {
			delete(a.auth.sessions, k)
		}
	}
	if !ok || time.Now().After(expires) || len(a.auth.sessions) >= 64 {
		a.auth.mu.Unlock()
		fail(w, 401, fmt.Errorf("启动凭据无效或已过期，请重新打开桌面快捷方式"))
		return
	}
	sid := randomHex(32)
	a.auth.sessions[sid] = time.Now().Add(12 * time.Hour)
	a.auth.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "kurumi_session", Value: sid, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 43200})
	reply(w, 200, map[string]bool{"ok": true})
}
