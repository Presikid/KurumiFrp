package manager

import (
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

type attempt struct {
	count         int
	inFlight      int
	since         time.Time
	limitNotified bool
}

const (
	maxLoginKeys           = 1000
	maxLoginFailures       = 10
	maxGlobalLoginFailures = 200
	loginWindow            = time.Minute
)

type auth struct {
	token             string
	mu                sync.Mutex
	attempts          map[string]attempt
	globalAttempts    attempt
	keysLimitNotified bool
	nextLimitNotice   time.Time
	onLoginLimit      func(string)
	sessions          map[string]authSession
	tickets           map[string]time.Time
	nextCleanup       time.Time
	accountPath       string
	accounts          []localAccount
	accountsRevision  uint64
	accountLoadErr    error
	hashSlots         chan struct{}
}

type authSession struct {
	expires   time.Time
	accountID string        // empty for the local access token or launch ticket
	gate      *sync.RWMutex // root browser session; account sessions use the account gate
}

type principal struct {
	Name              string   `json:"name"`
	Role              string   `json:"role"`
	Account           bool     `json:"account"`
	AllowLogs         bool     `json:"allowLogs,omitempty"`
	OperableInstances []string `json:"operableInstances,omitempty"`
	LogInstances      []string `json:"logInstances,omitempty"`
	Instances         []string `json:"-"`
	AccountID         string   `json:"-"`
	gate              *sync.RWMutex
}

func (p principal) admin() bool { return p.Role == "admin" }
func (p principal) canOperate(id string) bool {
	if p.admin() {
		return true
	}
	if p.Role != "operator" {
		return false
	}
	if !p.canView(id) {
		return false
	}
	for _, allowed := range p.OperableInstances {
		if allowed == id {
			return true
		}
	}
	return false
}
func (p principal) canReadLogs(id string) bool {
	if p.admin() {
		return true
	}
	if !p.AllowLogs || !p.canView(id) {
		return false
	}
	for _, allowed := range p.LogInstances {
		if allowed == id {
			return true
		}
	}
	return false
}
func (p principal) canView(id string) bool {
	if p.admin() {
		return true
	}
	if id == "" {
		return false
	}
	for _, allowed := range p.Instances {
		if allowed == id {
			return true
		}
	}
	return false
}

func (a *auth) localToken(r *http.Request) bool {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return false
	}
	t := strings.TrimPrefix(header, "Bearer ")
	return t != "" && subtle.ConstantTimeCompare([]byte(t), []byte(a.token)) == 1
}

func (a *auth) identity(r *http.Request) (principal, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.identityLocked(r)
}

// identityLocked checks the current session and account while a.mu is held.
func (a *auth) identityLocked(r *http.Request) (principal, bool) {
	if a.localToken(r) {
		return principal{Name: "本机管理令牌", Role: "admin"}, true
	}
	c, e := r.Cookie("kurumi_session")
	if e != nil {
		return principal{}, false
	}
	session, ok := a.sessions[c.Value]
	if ok && !time.Now().Before(session.expires) {
		delete(a.sessions, c.Value)
		return principal{}, false
	}
	if !ok {
		return principal{}, false
	}
	if session.accountID == "" {
		return principal{Name: "本机管理令牌", Role: "admin", gate: session.gate}, true
	}
	for _, account := range a.accounts {
		if account.ID == session.accountID && account.Enabled {
			logs := effectiveLogInstances(account)
			return principal{Name: account.Username, Role: account.Role, Account: true, AllowLogs: account.AllowLogs && len(logs) != 0, Instances: append([]string(nil), account.Instances...), OperableInstances: effectiveControlInstances(account), LogInstances: logs, AccountID: account.ID, gate: account.gate}, true
		}
	}
	delete(a.sessions, c.Value)
	return principal{}, false
}

// Both account login and password changes use the same credential budget.
// Fold aliases without consulting the account list, including for unknown names.
func accountAttemptKey(username string) string {
	if len(username) > 32 {
		return "invalid-account"
	}
	name := strings.Map(func(r rune) rune {
		folded := r
		for {
			if folded >= 'A' && folded <= 'Z' {
				return folded + ('a' - 'A')
			}
			if folded >= 'a' && folded <= 'z' {
				return folded
			}
			folded = unicode.SimpleFold(folded)
			if folded == r {
				return r
			}
		}
	}, username)
	if accountNamePattern.MatchString(name) {
		return "account:" + name
	}
	return "invalid-account"
}

// Reservations include in-flight checks so concurrent guesses cannot all
// pass the limit before their failures are recorded.
func (a *auth) beginAttempt(key string) error {
	return a.beginAttemptWithPeer(key, false, "")
}

func (a *auth) beginAttemptWithPeer(key string, login bool, peer string) error {
	a.mu.Lock()
	now := time.Now()
	a.prune(now)
	at, known := a.attempts[key]
	storedAttempt := at
	if !known || !now.Before(at.since.Add(loginWindow)) {
		at.count, at.since = 0, now
		if at.inFlight < maxLoginFailures {
			at.limitNotified = false
		}
	}
	if a.globalAttempts.since.IsZero() || !now.Before(a.globalAttempts.since.Add(loginWindow)) {
		a.globalAttempts.count, a.globalAttempts.since, a.globalAttempts.limitNotified = 0, now, false
	}
	keyLimited := at.count+at.inFlight >= maxLoginFailures
	globalLimited := key != "token" && a.globalAttempts.count+a.globalAttempts.inFlight >= maxGlobalLoginFailures
	keysLimited := !known && key != "token" && len(a.attempts) >= maxLoginKeys
	if !keyLimited {
		at.limitNotified = false
	}
	if a.globalAttempts.count+a.globalAttempts.inFlight < maxGlobalLoginFailures {
		a.globalAttempts.limitNotified = false
	}
	if len(a.attempts) < maxLoginKeys {
		a.keysLimitNotified = false
	}
	if keyLimited || globalLimited || keysLimited {
		changed := false
		if login {
			if keyLimited && !at.limitNotified {
				at.limitNotified, changed = true, true
			}
			if globalLimited && !a.globalAttempts.limitNotified {
				a.globalAttempts.limitNotified, changed = true, true
			}
			if keysLimited && !a.keysLimitNotified {
				a.keysLimitNotified, changed = true, true
			}
		}
		if known {
			// A rejected attempt must not change the existing credential budget.
			storedAttempt.limitNotified = at.limitNotified
			a.attempts[key] = storedAttempt
		}
		callback, notify := a.reserveLimitNotice(now, changed)
		a.mu.Unlock()
		if notify && callback != nil {
			callback(loginPeerIP(peer))
		}
		return fmt.Errorf("尝试过于频繁，请稍后再试")
	}
	at.inFlight++
	if key != "token" {
		a.globalAttempts.inFlight++
	}
	a.attempts[key] = at
	a.mu.Unlock()
	return nil
}

// Called with a.mu held, limiting all login notices to one per minute.
func (a *auth) reserveLimitNotice(now time.Time, changed bool) (func(string), bool) {
	notify := changed && !now.Before(a.nextLimitNotice)
	if notify {
		a.nextLimitNotice = now.Add(time.Minute)
	}
	return a.onLoginLimit, notify
}

// Only the network peer is used; forwarding headers are never trusted.
func loginPeerIP(peer string) string {
	host, _, err := net.SplitHostPort(peer)
	if err != nil {
		return ""
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return ""
	}
	return ip.WithZone("").Unmap().String()
}

func (a *auth) finishAttempt(key string, failed bool) {
	a.finishAttemptWithPeer(key, failed, false, "")
}

func (a *auth) finishAttemptWithPeer(key string, failed, login bool, peer string) {
	a.mu.Lock()
	now := time.Now()
	at := a.attempts[key]
	if !now.Before(at.since.Add(loginWindow)) {
		at.count, at.since, at.limitNotified = 0, now, false
	}
	if !now.Before(a.globalAttempts.since.Add(loginWindow)) {
		a.globalAttempts.count, a.globalAttempts.since, a.globalAttempts.limitNotified = 0, now, false
	}
	at.inFlight--
	if key != "token" {
		a.globalAttempts.inFlight--
	}
	changed := false
	if failed {
		at.count++
		if key != "token" {
			a.globalAttempts.count++
		}
		if login && at.count >= maxLoginFailures && !at.limitNotified {
			at.limitNotified, changed = true, true
		}
		if login && key != "token" && a.globalAttempts.count >= maxGlobalLoginFailures && !a.globalAttempts.limitNotified {
			a.globalAttempts.limitNotified, changed = true, true
		}
	}
	if at.count == 0 && at.inFlight == 0 {
		delete(a.attempts, key)
	} else {
		a.attempts[key] = at
	}
	callback, notify := a.reserveLimitNotice(now, changed)
	a.mu.Unlock()
	if notify && callback != nil {
		callback(loginPeerIP(peer))
	}
}

func (a *auth) login(r *http.Request, token, username, password string) (string, error) {
	if username != "" && token != "" {
		return "", fmt.Errorf("登录信息无效")
	}
	key := "token"
	if username != "" {
		key = accountAttemptKey(username)
	}
	if err := a.beginAttemptWithPeer(key, true, r.RemoteAddr); err != nil {
		return "", err
	}
	failed := false
	defer func() { a.finishAttemptWithPeer(key, failed, true, r.RemoteAddr) }()
	if username != "" {
		var sid string
		var err error
		sid, failed, err = a.loginAccount(r, username, password)
		return sid, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if subtle.ConstantTimeCompare([]byte(token), []byte(a.token)) != 1 {
		failed = true
		return "", fmt.Errorf("访问令牌不正确")
	}
	return a.newSessionLocked(r, "", time.Now())
}

// A newly authenticated browser replaces its old cookie. Each account can
// hold at most four sessions, and full capacity evicts an account session so
// that the local administrator retains a recovery path.
func (a *auth) newSessionLocked(r *http.Request, accountID string, now time.Time) (string, error) {
	for sid, session := range a.sessions {
		if !now.Before(session.expires) {
			delete(a.sessions, sid)
		}
	}
	// Plan all evictions first. If another session is busy, a failed login must
	// not log out the browser whose cookie it was trying to replace.
	removed := make(map[string]bool)
	held := make(map[*sync.RWMutex]bool)
	defer func() {
		for gate := range held {
			gate.Unlock()
		}
	}()
	reserve := func(sid string) bool {
		session, exists := a.sessions[sid]
		if !exists || removed[sid] {
			return true
		}
		gate := session.gate
		if session.accountID != "" {
			for _, account := range a.accounts {
				if account.ID == session.accountID {
					gate = account.gate
					break
				}
			}
		}
		if gate != nil && !held[gate] {
			// auth.mu is held: never wait for a gate, whose owner may need mu.
			if !gate.TryLock() {
				return false
			}
			held[gate] = true
		}
		removed[sid] = true
		return true
	}
	if cookie, err := r.Cookie("kurumi_session"); err == nil {
		if !reserve(cookie.Value) {
			return "", fmt.Errorf("当前会话仍有操作进行，请稍后再试")
		}
	}
	if accountID != "" {
		count := 0
		oldest, oldestTime := "", time.Time{}
		for sid, session := range a.sessions {
			if session.accountID == accountID && !removed[sid] {
				count++
				if oldest == "" || session.expires.Before(oldestTime) {
					oldest, oldestTime = sid, session.expires
				}
			}
		}
		if count >= 4 {
			if !reserve(oldest) {
				return "", fmt.Errorf("当前账户的操作尚未结束，请稍后再试")
			}
		}
	}
	if len(a.sessions)-len(removed) >= 64 {
		type candidate struct {
			id      string
			expires time.Time
			root    bool
		}
		var candidates []candidate
		for sid, session := range a.sessions {
			if !removed[sid] && (session.accountID != "" || accountID == "") {
				candidates = append(candidates, candidate{sid, session.expires, session.accountID == ""})
			}
		}
		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].root != candidates[j].root {
				return !candidates[i].root
			}
			return candidates[i].expires.Before(candidates[j].expires)
		})
		evicted := false
		for _, candidate := range candidates {
			if reserve(candidate.id) {
				evicted = true
				break
			}
		}
		if !evicted {
			return "", fmt.Errorf("会话繁忙，请稍后再试")
		}
	}
	for sid := range removed {
		delete(a.sessions, sid)
	}
	sid := randomHex(32)
	session := authSession{expires: now.Add(12 * time.Hour), accountID: accountID}
	if accountID == "" {
		session.gate = &sync.RWMutex{}
	}
	a.sessions[sid] = session
	return sid, nil
}

// prune is called with a.mu held. Both tables have hard capacity limits.
func (a *auth) prune(now time.Time) {
	if now.Before(a.nextCleanup) {
		return
	}
	for k, v := range a.attempts {
		if v.inFlight == 0 && !now.Before(v.since.Add(loginWindow)) {
			delete(a.attempts, k)
		}
	}
	for k, session := range a.sessions {
		if !now.Before(session.expires) {
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
