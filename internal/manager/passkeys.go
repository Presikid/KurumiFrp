package manager

import (
	"bytes"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

const passkeyFlowLifetime = 2 * time.Minute

type passkeyUser struct {
	id string
	name string
	credentials []webauthn.Credential
}

func (u passkeyUser) WebAuthnID() []byte { return []byte(u.id) }
func (u passkeyUser) WebAuthnName() string { return u.name }
func (u passkeyUser) WebAuthnDisplayName() string { return u.name }
func (u passkeyUser) WebAuthnCredentials() []webauthn.Credential { return u.credentials }

type passkeyFlow struct {
	operation string
	user passkeyUser
	session webauthn.SessionData
	sessionCookie string
	accountRevision uint64
	storeRevision uint64
	name string
	origin string
	expires time.Time
}

// localhost is a trustworthy origin in browsers. The exact scheme and port
// are bound to each ceremony; 127.0.0.1 and remote hosts are not accepted.
func passkeyWebAuthn(r *http.Request) (*webauthn.WebAuthn, string, error) {
	host, port, err := net.SplitHostPort(r.Host)
	if err != nil || !strings.EqualFold(host, passkeyRPID) {
		return nil, "", fmt.Errorf("Passkey 需要使用 localhost 地址访问工作台")
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	origin := scheme + "://" + net.JoinHostPort(passkeyRPID, port)
	w, err := webauthn.New(&webauthn.Config{RPID: passkeyRPID, RPDisplayName: "KurumiFRP", RPOrigins: []string{origin}, AuthenticatorSelection: protocol.AuthenticatorSelection{UserVerification: protocol.VerificationRequired}})
	return w, origin, err
}

// Caller holds auth.mu; credential records are replaced, never mutated in place.
func (a *auth) passkeyUserLocked(account localAccount) passkeyUser {
	u := passkeyUser{id: account.ID, name: account.Username, credentials: []webauthn.Credential{}}
	for _, record := range a.passkeys.data.Records {
		if record.AccountID == account.ID && record.RPID == passkeyRPID {
			u.credentials = append(u.credentials, record.Credential)
		}
	}
	return u
}

// Registration and deletion require the current local password, even for an
// existing browser session. Caller must recheck identity after this work.
func (a *auth) confirmAccountPassword(r *http.Request, p principal, password string) (localAccount, error) {
	key := accountAttemptKey(p.Name)
	if err := a.beginAttempt(key); err != nil {
		return localAccount{}, err
	}
	failed := false
	defer func() { a.finishAttempt(key, failed) }()
	if len(password) > 1024 {
		return localAccount{}, fmt.Errorf("账户密码不正确")
	}
	a.mu.Lock()
	var account localAccount
	for _, candidate := range a.accounts {
		if candidate.ID == p.AccountID && candidate.activeAt(time.Now()) {
			account = candidate
			break
		}
	}
	a.mu.Unlock()
	if account.ID == "" || !a.acquireHash() {
		return localAccount{}, fmt.Errorf("账户不可用或操作繁忙")
	}
	salt, _ := base64.RawStdEncoding.DecodeString(account.Salt)
	want, _ := base64.RawStdEncoding.DecodeString(account.Hash)
	got, err := passwordHash(password, salt)
	a.releaseHash()
	if err != nil || subtle.ConstantTimeCompare(got, want) != 1 {
		failed = true
		return localAccount{}, fmt.Errorf("账户密码不正确")
	}
	return account, nil
}

// A short-lived browser-bound cookie names a one-use in-memory ceremony.
// Caller holds auth.mu; old flows are pruned before enforcing the cap.
func (a *auth) savePasskeyFlow(w http.ResponseWriter, r *http.Request, flow passkeyFlow) error {
	if a.passkeyFlows == nil {
		a.passkeyFlows = make(map[string]passkeyFlow)
	}
	now := time.Now()
	for id, old := range a.passkeyFlows {
		if !now.Before(old.expires) {
			delete(a.passkeyFlows, id)
		}
	}
	if len(a.passkeyFlows) >= 64 {
		return fmt.Errorf("Passkey 请求过多，请稍后重试")
	}
	id := randomHex(32)
	flow.expires = now.Add(passkeyFlowLifetime)
	a.passkeyFlows[id] = flow
	http.SetCookie(w, &http.Cookie{Name: "kurumi_passkey_flow", Value: id, Path: "/api/passkeys/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: int(passkeyFlowLifetime.Seconds())})
	return nil
}

// Caller holds auth.mu. Each flow is consumed even if verification fails.
func (a *auth) takePasskeyFlow(w http.ResponseWriter, r *http.Request, operation, origin string) (passkeyFlow, bool) {
	cookie, err := r.Cookie("kurumi_passkey_flow")
	if err != nil || !nodeTokenPattern.MatchString(cookie.Value) {
		return passkeyFlow{}, false
	}
	http.SetCookie(w, &http.Cookie{Name: "kurumi_passkey_flow", Path: "/api/passkeys/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	flow, ok := a.passkeyFlows[cookie.Value]
	delete(a.passkeyFlows, cookie.Value)
	return flow, ok && flow.operation == operation && flow.origin == origin && time.Now().Before(flow.expires) && a.passkeys.loadErr == nil && a.accountLoadErr == nil && a.accountsRevision == flow.accountRevision && a.passkeys.data.Revision == flow.storeRevision
}

func (a *App) passkeysAPI(w http.ResponseWriter, r *http.Request) {
	p := requestPrincipal(r)
	if r.URL.Path == "/api/passkeys" && r.Method == http.MethodGet {
		a.auth.mu.Lock()
		if a.auth.passkeys.loadErr != nil {
			a.auth.mu.Unlock()
			fail(w, 503, fmt.Errorf("Passkey 文件不可读取，原文件已保留"))
			return
		}
		items := []map[string]string{}
		for _, record := range a.auth.passkeys.data.Records {
			if record.AccountID == p.AccountID {
				items = append(items, map[string]string{"id": base64.RawURLEncoding.EncodeToString(record.Credential.ID), "name": record.Name, "createdAt": record.CreatedAt})
			}
		}
		a.auth.mu.Unlock()
		reply(w, 200, map[string]any{"passkeys": items})
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	switch r.URL.Path {
	case "/api/passkeys/register/start":
		a.passkeyRegisterStart(w, r, p)
	case "/api/passkeys/register/finish":
		a.passkeyRegisterFinish(w, r, p)
	case "/api/passkeys/delete":
		a.passkeyDelete(w, r, p)
	default:
		http.NotFound(w, r)
	}
}

func (a *App) passkeyRegisterStart(w http.ResponseWriter, r *http.Request, p principal) {
	var req struct { Name string `json:"name"`; Password string `json:"password"` }
	if err := decode(w, r, &req); err != nil || !oidcTextOK(req.Name, 80) {
		fail(w, 400, fmt.Errorf("Passkey 名称或请求无效"))
		return
	}
	wa, origin, err := passkeyWebAuthn(r)
	if err != nil { fail(w, 400, err); return }
	account, err := a.auth.confirmAccountPassword(r, p, req.Password)
	if err != nil { fail(w, 400, err); return }
	browserSession, err := r.Cookie("kurumi_session")
	if err != nil { fail(w, 401, fmt.Errorf("请先登录")); return }
	a.auth.mu.Lock()
	defer a.auth.mu.Unlock()
	current, valid := a.auth.identityLocked(r)
	if !valid || current.AccountID != account.ID || a.auth.passkeys.loadErr != nil || r.Context().Err() != nil {
		fail(w, 409, fmt.Errorf("账户或 Passkey 配置已变化，请重试")); return
	}
	u := a.auth.passkeyUserLocked(account)
	if len(u.credentials) >= maxPasskeysPerAccount {
		fail(w, 400, fmt.Errorf("账户的 Passkey 数量已达到上限")); return
	}
	options, session, err := wa.BeginRegistration(u, webauthn.WithExclusions(webauthn.Credentials(u.credentials).CredentialDescriptors()), webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementPreferred))
	if err != nil { fail(w, 500, fmt.Errorf("无法创建 Passkey 注册请求")); return }
	flow := passkeyFlow{operation: "register", user: u, session: *session, sessionCookie: browserSession.Value, accountRevision: a.auth.accountsRevision, storeRevision: a.auth.passkeys.data.Revision, name: req.Name, origin: origin}
	if err := a.auth.savePasskeyFlow(w, r, flow); err != nil { fail(w, 429, err); return }
	reply(w, 200, map[string]any{"options": options})
}

func (a *App) passkeyRegisterFinish(w http.ResponseWriter, r *http.Request, p principal) {
	wa, origin, err := passkeyWebAuthn(r)
	if err != nil { fail(w, 400, err); return }
	a.auth.mu.Lock()
	flow, ok := a.auth.takePasskeyFlow(w, r, "register", origin)
	browserSession, cookieErr := r.Cookie("kurumi_session")
	if !ok || cookieErr != nil || subtle.ConstantTimeCompare([]byte(browserSession.Value), []byte(flow.sessionCookie)) != 1 || p.AccountID != flow.user.id {
		a.auth.mu.Unlock(); fail(w, 409, fmt.Errorf("注册请求已失效，请重新开始")); return
	}
	a.auth.mu.Unlock()
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" { fail(w, 400, fmt.Errorf("需要 application/json")); return }
	r.Body = http.MaxBytesReader(w, r.Body, 128<<10)
	credential, err := wa.FinishRegistration(flow.user, flow.session, r)
	if err != nil || credential == nil { fail(w, 400, fmt.Errorf("Passkey 注册未完成")); return }
	a.auth.mu.Lock()
	defer a.auth.mu.Unlock()
	current, valid := a.auth.identityLocked(r)
	if !valid || current.AccountID != flow.user.id || a.auth.accountsRevision != flow.accountRevision || a.auth.passkeys.data.Revision != flow.storeRevision || r.Context().Err() != nil {
		fail(w, 409, fmt.Errorf("账户或 Passkey 配置已变化，请重新注册")); return
	}
	for _, record := range a.auth.passkeys.data.Records {
		if bytes.Equal(record.Credential.ID, credential.ID) { fail(w, 409, fmt.Errorf("此 Passkey 已注册")); return }
	}
	records := append([]passkeyRecord{}, a.auth.passkeys.data.Records...)
	records = append(records, passkeyRecord{AccountID: flow.user.id, Name: flow.name, RPID: passkeyRPID, CreatedAt: time.Now().UTC().Format(time.RFC3339), Flags: uint8(credential.Flags.ProtocolValue()), Credential: *credential})
	if err := a.auth.passkeys.save(records); err != nil { fail(w, 500, err); return }
	reply(w, 200, map[string]bool{"ok": true})
}

func (a *App) passkeyDelete(w http.ResponseWriter, r *http.Request, p principal) {
	var req struct { ID string `json:"id"`; Password string `json:"password"` }
	if err := decode(w, r, &req); err != nil || len(req.ID) > 1400 { fail(w, 400, fmt.Errorf("请求无效")); return }
	id, err := base64.RawURLEncoding.DecodeString(req.ID)
	if err != nil || len(id) == 0 || len(id) > 1024 { fail(w, 400, fmt.Errorf("Passkey 标识无效")); return }
	account, err := a.auth.confirmAccountPassword(r, p, req.Password)
	if err != nil { fail(w, 400, err); return }
	a.auth.mu.Lock()
	defer a.auth.mu.Unlock()
	current, valid := a.auth.identityLocked(r)
	if !valid || current.AccountID != account.ID || a.auth.passkeys.loadErr != nil || r.Context().Err() != nil { fail(w, 409, fmt.Errorf("账户或 Passkey 配置已变化")); return }
	records, found := make([]passkeyRecord, 0, len(a.auth.passkeys.data.Records)), false
	for _, record := range a.auth.passkeys.data.Records {
		if record.AccountID == account.ID && bytes.Equal(record.Credential.ID, id) { found = true; continue }
		records = append(records, record)
	}
	if !found { fail(w, 404, fmt.Errorf("Passkey 不存在")); return }
	if err := a.auth.passkeys.save(records); err != nil { fail(w, 500, err); return }
	reply(w, 200, map[string]bool{"ok": true})
}

func (a *App) passkeyStatus(w http.ResponseWriter, r *http.Request) {
	_, _, err := passkeyWebAuthn(r)
	a.auth.mu.Lock()
	available := err == nil && a.auth.passkeys.loadErr == nil && a.auth.accountLoadErr == nil
	a.auth.mu.Unlock()
	reply(w, 200, map[string]bool{"available": available})
}

func (a *App) passkeyLoginStart(w http.ResponseWriter, r *http.Request) {
	var req struct { Username string `json:"username"` }
	if err := decode(w, r, &req); err != nil { fail(w, 400, fmt.Errorf("登录请求无效")); return }
	wa, origin, err := passkeyWebAuthn(r)
	if err != nil { fail(w, 400, err); return }
	key := accountAttemptKey(req.Username)
	if err := a.auth.beginAttemptWithPeer(key, true, r.RemoteAddr); err != nil { fail(w, 429, err); return }
	failed := false
	defer func() { a.auth.finishAttemptWithPeer(key, failed, true, r.RemoteAddr) }()
	a.auth.mu.Lock()
	defer a.auth.mu.Unlock()
	if a.auth.passkeys.loadErr != nil || a.auth.accountLoadErr != nil { fail(w, 503, fmt.Errorf("Passkey 暂不可用")); return }
	var account localAccount
	for _, candidate := range a.auth.accounts {
		if strings.EqualFold(candidate.Username, req.Username) && candidate.activeAt(time.Now()) { account = candidate; break }
	}
	if account.ID == "" { failed = true; fail(w, 400, fmt.Errorf("账户或 Passkey 不可用")); return }
	u := a.auth.passkeyUserLocked(account)
	if len(u.credentials) == 0 { failed = true; fail(w, 400, fmt.Errorf("账户或 Passkey 不可用")); return }
	options, session, err := wa.BeginLogin(u, webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil { fail(w, 500, fmt.Errorf("无法创建 Passkey 登录请求")); return }
	flow := passkeyFlow{operation: "login", user: u, session: *session, accountRevision: a.auth.accountsRevision, storeRevision: a.auth.passkeys.data.Revision, origin: origin}
	if err := a.auth.savePasskeyFlow(w, r, flow); err != nil { fail(w, 429, err); return }
	reply(w, 200, map[string]any{"options": options})
}

func (a *App) passkeyLoginFinish(w http.ResponseWriter, r *http.Request) {
	wa, origin, err := passkeyWebAuthn(r)
	if err != nil { fail(w, 400, err); return }
	a.auth.mu.Lock()
	flow, ok := a.auth.takePasskeyFlow(w, r, "login", origin)
	a.auth.mu.Unlock()
	if !ok { fail(w, 409, fmt.Errorf("登录请求已失效，请重新开始")); return }
	key := accountAttemptKey(flow.user.name)
	if err := a.auth.beginAttemptWithPeer(key, true, r.RemoteAddr); err != nil { fail(w, 429, err); return }
	failed, success := false, false
	defer func() {
		a.auth.finishAttemptWithPeer(key, failed, true, r.RemoteAddr)
		a.loginHistory.record(r, flow.user.name, success)
	}()
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" { fail(w, 400, fmt.Errorf("需要 application/json")); return }
	r.Body = http.MaxBytesReader(w, r.Body, 128<<10)
	credential, err := wa.FinishLogin(flow.user, flow.session, r)
	if err != nil || credential == nil || credential.Authenticator.CloneWarning {
		failed = true; fail(w, 401, fmt.Errorf("Passkey 登录未完成")); return
	}
	a.auth.mu.Lock()
	defer a.auth.mu.Unlock()
	if a.auth.accountLoadErr != nil || a.auth.passkeys.loadErr != nil || a.auth.accountsRevision != flow.accountRevision || a.auth.passkeys.data.Revision != flow.storeRevision || r.Context().Err() != nil {
		fail(w, 409, fmt.Errorf("账户或 Passkey 配置已变化，请重新登录")); return
	}
	active := false
	for _, account := range a.auth.accounts {
		if account.ID == flow.user.id && account.activeAt(time.Now()) { active = true; break }
	}
	if !active { fail(w, 401, fmt.Errorf("账户不可用")); return }
	records, found := append([]passkeyRecord{}, a.auth.passkeys.data.Records...), false
	for i, record := range records {
		if record.AccountID == flow.user.id && bytes.Equal(record.Credential.ID, credential.ID) {
			records[i].Credential = *credential
			records[i].Flags = uint8(credential.Flags.ProtocolValue())
			found = true
			break
		}
	}
	if !found { fail(w, 409, fmt.Errorf("Passkey 已变化，请重新登录")); return }
	if err := a.auth.passkeys.save(records); err != nil { fail(w, 500, err); return }
	sid, err := a.auth.newSessionLocked(r, flow.user.id, time.Now())
	if err != nil { fail(w, 503, fmt.Errorf("无法创建登录会话")); return }
	http.SetCookie(w, &http.Cookie{Name: "kurumi_session", Value: sid, Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: 43200})
	success = true
	reply(w, 200, map[string]bool{"ok": true})
}
