package manager

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	pbkdf2Iterations  = 600000
	sessionCookie     = "kurumi_session"
	sessionTTL        = 12 * time.Hour
	maxRevoked        = 128
	maxAttemptIPs     = 1000
	maxFailuresPerMin = 10
)

// adminAccountFile stores the single global administrator's username and PBKDF2-SHA256 password hash.
type adminAccountFile struct {
	Version  int    `json:"version"`
	Username string `json:"username"`
	Salt     string `json:"salt"`
	Hash     string `json:"hash"`
}

type loginAttempt struct {
	count       int
	windowStart time.Time
	lastAttempt time.Time
}

type browserTicket struct {
	expires time.Time
}

// AuthManager manages single-user username+password authentication, local CLI token, and restart-resilient HMAC sessions.
type AuthManager struct {
	mu          sync.Mutex
	accountPath string
	tokenPath   string
	localToken  string
	account     adminAccountFile
	revoked     map[string]time.Time
	attempts    map[string]loginAttempt
	tickets     map[string]browserTicket
}

// NewAuthManager initializes or migrates the single-user credential file and local control token.
func NewAuthManager(dataDir string) (*AuthManager, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	am := &AuthManager{
		accountPath: filepath.Join(dataDir, "admin-password.json"),
		tokenPath:   filepath.Join(dataDir, "access.token"),
		revoked:     make(map[string]time.Time),
		attempts:    make(map[string]loginAttempt),
		tickets:     make(map[string]browserTicket),
	}
	tok, err := loadOrCreateToken(am.tokenPath)
	if err != nil {
		return nil, err
	}
	am.localToken = tok
	if err := am.loadOrInitAccount(); err != nil {
		return nil, err
	}
	return am, nil
}

func loadOrCreateToken(path string) (string, error) {
	if raw, err := os.ReadFile(path); err == nil {
		t := strings.TrimSpace(string(raw))
		if len(t) >= 32 {
			return t, nil
		}
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(buf)
	if _, err := writeIfChanged(path, []byte(tok+"\n"), 0o600); err != nil {
		return "", err
	}
	return tok, nil
}

func (a *AuthManager) loadOrInitAccount() error {
	raw, err := os.ReadFile(a.accountPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		salt := make([]byte, 16)
		if _, err := rand.Read(salt); err != nil {
			return err
		}
		sum := pbkdf2SHA256([]byte(""), salt, pbkdf2Iterations, 32)
		a.account = adminAccountFile{
			Version:  2,
			Username: "admin",
			Salt:     base64.RawStdEncoding.EncodeToString(salt),
			Hash:     base64.RawStdEncoding.EncodeToString(sum),
		}
		return a.saveAccountLocked()
	}
	var acc adminAccountFile
	if err := json.Unmarshal(raw, &acc); err != nil {
		return fmt.Errorf("parse admin-password.json: %w", err)
	}
	acc.Username = strings.TrimSpace(acc.Username)
	if acc.Username == "" {
		acc.Username = "admin"
	}
	if acc.Version < 2 {
		acc.Version = 2
	}
	a.account = acc
	return a.saveAccountLocked()
}

func (a *AuthManager) saveAccountLocked() error {
	b, err := json.MarshalIndent(a.account, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = writeIfChanged(a.accountPath, b, 0o600)
	return err
}

// Username returns the current single administrator's username.
func (a *AuthManager) Username() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.account.Username
}

// NeedsPasswordSetup reports whether the administrator password is still empty ("").
func (a *AuthManager) NeedsPasswordSetup() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	ok, err := verifyPasswordRecord(a.account, "")
	return err == nil && ok
}

// LocalToken returns the loopback CLI maintenance token.
func (a *AuthManager) LocalToken() string {
	return a.localToken
}

// VerifyLocalToken checks Authorization: Bearer <token> in constant time.
func (a *AuthManager) VerifyLocalToken(r *http.Request) bool {
	hdr := strings.TrimSpace(r.Header.Get("Authorization"))
	const prefix = "Bearer "
	if !strings.HasPrefix(hdr, prefix) {
		return false
	}
	got := strings.TrimSpace(hdr[len(prefix):])
	if len(got) != len(a.localToken) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(a.localToken)) == 1
}

// Authenticated checks whether the HTTP request carries a valid local Bearer token or HMAC-signed session cookie.
func (a *AuthManager) Authenticated(r *http.Request) (string, bool) {
	if a.VerifyLocalToken(r) {
		return a.Username(), true
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return "", false
	}
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pruneRevokedLocked(now)
	if _, isRevoked := a.revoked[c.Value]; isRevoked {
		return "", false
	}
	parts := strings.Split(c.Value, ".")
	if len(parts) != 3 {
		return "", false
	}
	expUnix, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || now.Unix() > expUnix {
		return "", false
	}
	expectedSig := a.signSessionPayloadLocked(parts[0] + "." + parts[1])
	if len(parts[2]) != len(expectedSig) || subtle.ConstantTimeCompare([]byte(parts[2]), []byte(expectedSig)) != 1 {
		return "", false
	}
	return a.account.Username, true
}

// Login validates the single-user username and password in constant time and issues an HMAC-signed session token.
func (a *AuthManager) Login(clientIP, username, password string) (string, error) {
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.isRateLimitedLocked(clientIP, now) {
		return "", errors.New("登录尝试过于频繁，请 1 分钟后再试")
	}

	userOK := constantTimeStringEqual(strings.TrimSpace(username), a.account.Username)
	passOK, err := verifyPasswordRecord(a.account, password)
	if err != nil || !userOK || !passOK {
		a.recordFailureLocked(clientIP, now)
		return "", errors.New("用户名或密码错误")
	}

	delete(a.attempts, clientIP)
	return a.createSessionLocked(now)
}

// UpdateCredentials updates the single administrator's username and/or password after verifying currentPassword.
// Because Salt and Hash rotate, all existing browser session tokens are immediately invalidated.
func (a *AuthManager) UpdateCredentials(currentPassword, newUsername, newPassword string) error {
	newUsername = strings.TrimSpace(newUsername)
	if len(newUsername) < 1 || len(newUsername) > 64 || strings.ContainsAny(newUsername, " \t\r\n") {
		return errors.New("用户名长度需在 1~64 个字符之间且不能包含空白字符")
	}
	if len(newPassword) > 256 {
		return errors.New("密码长度不能超过 256 个字符")
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	passOK, err := verifyPasswordRecord(a.account, currentPassword)
	if err != nil || !passOK {
		return errors.New("当前密码校验失败")
	}

	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	sum := pbkdf2SHA256([]byte(newPassword), salt, pbkdf2Iterations, 32)
	a.account = adminAccountFile{
		Version:  2,
		Username: newUsername,
		Salt:     base64.RawStdEncoding.EncodeToString(salt),
		Hash:     base64.RawStdEncoding.EncodeToString(sum),
	}
	a.revoked = make(map[string]time.Time)
	return a.saveAccountLocked()
}

// Logout revokes the active session cookie immediately.
func (a *AuthManager) Logout(r *http.Request) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return
	}
	now := time.Now()
	a.mu.Lock()
	a.pruneRevokedLocked(now)
	if len(a.revoked) >= maxRevoked {
		for k := range a.revoked {
			delete(a.revoked, k)
			break
		}
	}
	a.revoked[c.Value] = now.Add(sessionTTL)
	a.mu.Unlock()
}

// IssueBrowserTicket creates a short-lived one-time ticket for --open local browser launch.
func (a *AuthManager) IssueBrowserTicket() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	ticket := hex.EncodeToString(buf)
	now := time.Now()
	a.mu.Lock()
	for k, v := range a.tickets {
		if now.After(v.expires) {
			delete(a.tickets, k)
		}
	}
	a.tickets[ticket] = browserTicket{expires: now.Add(30 * time.Second)}
	a.mu.Unlock()
	return ticket, nil
}

// RedeemBrowserTicket exchanges a valid one-time ticket for a session token.
func (a *AuthManager) RedeemBrowserTicket(ticket string) (string, bool) {
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	t, ok := a.tickets[ticket]
	delete(a.tickets, ticket)
	if !ok || now.After(t.expires) {
		return "", false
	}
	tok, err := a.createSessionLocked(now)
	if err != nil {
		return "", false
	}
	return tok, true
}

func (a *AuthManager) createSessionLocked(now time.Time) (string, error) {
	nonceBuf := make([]byte, 12)
	if _, err := rand.Read(nonceBuf); err != nil {
		return "", err
	}
	expUnix := now.Add(sessionTTL).Unix()
	payload := fmt.Sprintf("%d.%s", expUnix, hex.EncodeToString(nonceBuf))
	sig := a.signSessionPayloadLocked(payload)
	return payload + "." + sig, nil
}

func (a *AuthManager) signSessionPayloadLocked(payload string) string {
	keySeed := a.localToken + "|" + a.account.Username + "|" + a.account.Salt + "|" + a.account.Hash
	key := sha256.Sum256([]byte(keySeed))
	mac := hmac.New(sha256.New, key[:])
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func (a *AuthManager) pruneRevokedLocked(now time.Time) {
	for k, exp := range a.revoked {
		if now.After(exp) {
			delete(a.revoked, k)
		}
	}
}

func (a *AuthManager) isRateLimitedLocked(ip string, now time.Time) bool {
	rec, ok := a.attempts[ip]
	if !ok {
		return false
	}
	if now.Sub(rec.windowStart) > time.Minute {
		delete(a.attempts, ip)
		return false
	}
	return rec.count >= maxFailuresPerMin
}

func (a *AuthManager) recordFailureLocked(ip string, now time.Time) {
	if len(a.attempts) >= maxAttemptIPs {
		var oldestIP string
		var oldestAt time.Time
		for k, v := range a.attempts {
			if now.Sub(v.windowStart) > time.Minute {
				delete(a.attempts, k)
				continue
			}
			if oldestIP == "" || v.lastAttempt.Before(oldestAt) {
				oldestIP = k
				oldestAt = v.lastAttempt
			}
		}
		if len(a.attempts) >= maxAttemptIPs && oldestIP != "" {
			delete(a.attempts, oldestIP)
		}
	}
	rec, ok := a.attempts[ip]
	if !ok || now.Sub(rec.windowStart) > time.Minute {
		a.attempts[ip] = loginAttempt{count: 1, windowStart: now, lastAttempt: now}
		return
	}
	rec.count++
	rec.lastAttempt = now
	a.attempts[ip] = rec
}

func constantTimeStringEqual(a, b string) bool {
	ha := sha256.Sum256([]byte(a))
	hb := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}

func verifyPasswordRecord(rec adminAccountFile, password string) (bool, error) {
	salt, err := base64.RawStdEncoding.DecodeString(rec.Salt)
	if err != nil {
		return false, err
	}
	want, err := base64.RawStdEncoding.DecodeString(rec.Hash)
	if err != nil {
		return false, err
	}
	got := pbkdf2SHA256([]byte(password), salt, pbkdf2Iterations, len(want))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// pbkdf2SHA256 implements RFC 2898 PBKDF2-HMAC-SHA256 using only the Go standard library.
func pbkdf2SHA256(password, salt []byte, iter, keyLen int) []byte {
	prf := hmac.New(sha256.New, password)
	hashLen := prf.Size()
	numBlocks := (keyLen + hashLen - 1) / hashLen
	var dk []byte
	var blockBuf [4]byte
	u := make([]byte, hashLen)
	t := make([]byte, hashLen)

	for block := 1; block <= numBlocks; block++ {
		prf.Reset()
		prf.Write(salt)
		binary.BigEndian.PutUint32(blockBuf[:], uint32(block))
		prf.Write(blockBuf[:])
		u = prf.Sum(u[:0])
		copy(t, u)
		for i := 2; i <= iter; i++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(u[:0])
			for j := range t {
				t[j] ^= u[j]
			}
		}
		dk = append(dk, t...)
	}
	return bytes.Clone(dk[:keyLen])
}

func clientIPFromRequest(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
