package manager

import (
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type attempt struct {
	count int
	since time.Time
}

const maxLoginSources = 1000
const loginWindow = time.Minute

type auth struct {
	token       string
	mu          sync.Mutex
	attempts    map[string]attempt
	sessions    map[string]time.Time
	tickets     map[string]time.Time
	nextCleanup time.Time
}

func (a *auth) valid(r *http.Request) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if t := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "); t != "" && subtle.ConstantTimeCompare([]byte(t), []byte(a.token)) == 1 {
		return true
	}
	c, e := r.Cookie("kurumi_session")
	if e != nil {
		return false
	}
	expiry, ok := a.sessions[c.Value]
	if ok && !time.Now().Before(expiry) {
		delete(a.sessions, c.Value)
		return false
	}
	return ok
}
func (a *auth) login(r *http.Request, token string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	now := time.Now()
	// Sweep at most once per second; an expired entry for this source is
	// handled immediately even between sweeps. Never evict a live counter:
	// rotating addresses must not reset an existing source's rate limit.
	a.prune(now)
	at, known := a.attempts[ip]
	if known && !now.Before(at.since.Add(loginWindow)) {
		delete(a.attempts, ip)
		at, known = attempt{}, false
	}
	if at.count >= 10 || (!known && len(a.attempts) >= maxLoginSources) {
		return "", fmt.Errorf("尝试过于频繁，请稍后再试")
	}
	if at.count == 0 {
		at.since = now
	}
	at.count++
	a.attempts[ip] = at
	if subtle.ConstantTimeCompare([]byte(token), []byte(a.token)) != 1 {
		return "", fmt.Errorf("访问令牌不正确")
	}
	if len(a.sessions) >= 64 {
		return "", fmt.Errorf("会话数量达到上限")
	}
	sid := randomHex(32)
	a.sessions[sid] = now.Add(12 * time.Hour)
	return sid, nil
}

// prune is called with a.mu held. Both tables have hard capacity limits.
func (a *auth) prune(now time.Time) {
	if now.Before(a.nextCleanup) {
		return
	}
	for k, v := range a.attempts {
		if !now.Before(v.since.Add(loginWindow)) {
			delete(a.attempts, k)
		}
	}
	for k, expiry := range a.sessions {
		if !now.Before(expiry) {
			delete(a.sessions, k)
		}
	}
	a.nextCleanup = now.Add(time.Second)
}

func (a *auth) logout(r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if c, e := r.Cookie("kurumi_session"); e == nil {
		delete(a.sessions, c.Value)
	}
}
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return r.Header.Get("Sec-Fetch-Site") != "cross-site"
	}
	u, e := url.Parse(origin)
	return e == nil && (u.Scheme == "http" || u.Scheme == "https") && strings.EqualFold(u.Host, r.Host) && u.Path == ""
}
func loopbackListen(addr string) error {
	h, p, e := net.SplitHostPort(addr)
	if e != nil {
		return e
	}
	ip := net.ParseIP(h)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("management listener must be an explicit loopback IP")
	}
	n, e := strconv.Atoi(p)
	if e != nil || n < 1024 || n > 65535 {
		return fmt.Errorf("management port must be 1024..65535")
	}
	return nil
}
