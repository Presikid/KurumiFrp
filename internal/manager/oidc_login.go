package manager

import (
	"context"
	"crypto/subtle"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const oidcResponseLimit = 1 << 20

type oidcFlow struct {
	provider        *oidc.Provider
	client          *http.Client
	oauth           oauth2.Config
	verifier        string
	nonce           string
	revision        uint64
	accountRevision uint64
	expires         time.Time
}

// Restrict every discovery, token and key-set request, including metadata
// supplied endpoints, to validated public HTTPS destinations and bounded bodies.
type oidcTransport struct{ base *http.Transport }

type oidcLimitedBody struct {
	io.ReadCloser
	remaining int64
}

func (b *oidcLimitedBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if b.remaining == 0 {
		var extra [1]byte
		n, err := b.ReadCloser.Read(extra[:])
		if n != 0 {
			return 0, fmt.Errorf("OIDC response is too large")
		}
		return 0, err
	}
	if int64(len(p)) > b.remaining {
		p = p[:int(b.remaining)]
	}
	n, err := b.ReadCloser.Read(p)
	b.remaining -= int64(n)
	return n, err
}

func (t *oidcTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !validPublicHTTPSURL(req.URL.String()) {
		return nil, fmt.Errorf("OIDC endpoint is not public HTTPS")
	}
	response, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if response.ContentLength > oidcResponseLimit {
		response.Body.Close()
		return nil, fmt.Errorf("OIDC response is too large")
	}
	response.Body = &oidcLimitedBody{ReadCloser: response.Body, remaining: oidcResponseLimit}
	return response, nil
}

func newOIDCHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &oidcTransport{base: &http.Transport{
			Proxy: nil, DialContext: dialPublicHTTPS, DisableKeepAlives: true,
			TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 8 * time.Second,
			MaxResponseHeaderBytes: 16 << 10,
		}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (a *App) oidcStatus(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		fail(w, http.StatusBadRequest, fmt.Errorf("请求无效"))
		return
	}
	a.auth.mu.Lock()
	enabled := a.auth.oidcLoadErr == nil && a.auth.oidcSettings.Enabled && a.auth.accountLoadErr == nil
	a.auth.mu.Unlock()
	reply(w, http.StatusOK, map[string]bool{"enabled": enabled})
}

func (a *App) oidcStart(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		fail(w, http.StatusBadRequest, fmt.Errorf("请求无效"))
		return
	}
	var request struct{}
	if err := decode(w, r, &request); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	select {
	case a.auth.oidcStartSlots <- struct{}{}:
		defer func() { <-a.auth.oidcStartSlots }()
	default:
		fail(w, http.StatusTooManyRequests, fmt.Errorf("单点登录繁忙，请稍后重试"))
		return
	}
	a.auth.mu.Lock()
	cfg, accountRevision := a.auth.oidcSettings, a.auth.accountsRevision
	enabled := a.auth.oidcLoadErr == nil && a.auth.accountLoadErr == nil && cfg.Enabled
	a.auth.mu.Unlock()
	if !enabled {
		fail(w, http.StatusForbidden, fmt.Errorf("单点登录未启用"))
		return
	}
	client := newOIDCHTTPClient()
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	provider, err := oidc.NewProvider(oidc.ClientContext(ctx, client), cfg.Issuer)
	if err != nil {
		fail(w, http.StatusBadGateway, fmt.Errorf("无法读取身份提供方配置"))
		return
	}
	endpoint := provider.Endpoint()
	var metadata struct {
		JWKSURL string `json:"jwks_uri"`
	}
	if provider.Claims(&metadata) != nil || !validPublicHTTPSURL(endpoint.AuthURL) || !validPublicHTTPSURL(endpoint.TokenURL) || !validPublicHTTPSURL(metadata.JWKSURL) {
		fail(w, http.StatusBadGateway, fmt.Errorf("身份提供方端点无效"))
		return
	}
	endpoint.AuthStyle = oauth2.AuthStyleInParams
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	scopes := []string{oidc.ScopeOpenID}
	for _, binding := range cfg.Bindings {
		if binding.Kind == "email" {
			scopes = append(scopes, "email")
			break
		}
	}
	config := oauth2.Config{ClientID: cfg.ClientID, Endpoint: endpoint, RedirectURL: scheme + "://" + r.Host + "/api/oidc/callback", Scopes: scopes}
	state, nonce, verifier := randomHex(32), randomHex(32), oauth2.GenerateVerifier()
	flow := oidcFlow{provider: provider, client: client, oauth: config, verifier: verifier, nonce: nonce, revision: cfg.Revision, accountRevision: accountRevision, expires: time.Now().Add(5 * time.Minute)}
	address := config.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oauth2.SetAuthURLParam("nonce", nonce))
	if !validPublicHTTPSURL(address) {
		fail(w, http.StatusBadGateway, fmt.Errorf("身份提供方授权地址无效"))
		return
	}
	a.auth.mu.Lock()
	select {
	case <-a.quit:
		a.auth.mu.Unlock()
		fail(w, http.StatusServiceUnavailable, fmt.Errorf("工作台正在关闭"))
		return
	default:
	}
	if a.auth.oidcLoadErr != nil || !a.auth.oidcSettings.Enabled || a.auth.oidcSettings.Revision != cfg.Revision || a.auth.accountsRevision != accountRevision || r.Context().Err() != nil {
		a.auth.mu.Unlock()
		fail(w, http.StatusConflict, fmt.Errorf("登录设置或账户已变化，请重试"))
		return
	}
	if a.auth.oidcFlows == nil {
		a.auth.oidcFlows = make(map[string]oidcFlow)
	}
	for key, old := range a.auth.oidcFlows {
		if !time.Now().Before(old.expires) {
			delete(a.auth.oidcFlows, key)
		}
	}
	if len(a.auth.oidcFlows) >= 64 {
		a.auth.mu.Unlock()
		fail(w, http.StatusTooManyRequests, fmt.Errorf("登录请求过多，请稍后重试"))
		return
	}
	a.auth.oidcFlows[state] = flow
	a.auth.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "kurumi_oidc_state", Value: state, Path: "/api/oidc/callback", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteLaxMode, MaxAge: 300})
	reply(w, http.StatusOK, map[string]string{"authorizationURL": address})
}

func (a *App) oidcCallback(w http.ResponseWriter, r *http.Request) {
	failLogin := func() { http.Redirect(w, r, "/?oidc=failed", http.StatusSeeOther) }
	states := r.URL.Query()["state"]
	cookie, err := r.Cookie("kurumi_oidc_state")
	if err != nil || len(states) != 1 || !nodeTokenPattern.MatchString(states[0]) || subtle.ConstantTimeCompare([]byte(states[0]), []byte(cookie.Value)) != 1 {
		failLogin()
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "kurumi_oidc_state", Path: "/api/oidc/callback", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	a.auth.mu.Lock()
	flow, exists := a.auth.oidcFlows[states[0]]
	delete(a.auth.oidcFlows, states[0])
	valid := exists && time.Now().Before(flow.expires) && a.auth.oidcLoadErr == nil && a.auth.accountLoadErr == nil && a.auth.oidcSettings.Enabled && a.auth.oidcSettings.Revision == flow.revision && a.auth.accountsRevision == flow.accountRevision
	a.auth.mu.Unlock()
	if !valid {
		failLogin()
		return
	}
	failRecorded := func() {
		a.loginHistory.record(r, "未知账户", false)
		failLogin()
	}
	codes := r.URL.Query()["code"]
	if len(codes) != 1 || len(codes[0]) == 0 || len(codes[0]) > 4096 || len(r.URL.Query()["error"]) != 0 {
		failRecorded()
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	ctx = oidc.ClientContext(ctx, flow.client)
	tokens, err := flow.oauth.Exchange(ctx, codes[0], oauth2.VerifierOption(flow.verifier))
	if err != nil {
		failRecorded()
		return
	}
	raw, ok := tokens.Extra("id_token").(string)
	if !ok || len(raw) == 0 || len(raw) > 128<<10 {
		failRecorded()
		return
	}
	idToken, err := flow.provider.Verifier(&oidc.Config{ClientID: flow.oauth.ClientID}).Verify(ctx, raw)
	if err != nil || idToken == nil || !oidcTextOK(idToken.Subject, 255) || subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(flow.nonce)) != 1 || idToken.IssuedAt.IsZero() || idToken.IssuedAt.After(time.Now().Add(5*time.Minute)) {
		failRecorded()
		return
	}
	var claims struct {
		AuthorizedParty string `json:"azp"`
		Email           string `json:"email"`
		EmailVerified   *bool  `json:"email_verified"`
	}
	if idToken.Claims(&claims) != nil || (len(idToken.Audience) > 1 && claims.AuthorizedParty != flow.oauth.ClientID) || (claims.AuthorizedParty != "" && claims.AuthorizedParty != flow.oauth.ClientID) {
		failRecorded()
		return
	}
	a.auth.mu.Lock()
	select {
	case <-a.quit:
		a.auth.mu.Unlock()
		failRecorded()
		return
	default:
	}
	if a.auth.oidcLoadErr != nil || a.auth.accountLoadErr != nil || !a.auth.oidcSettings.Enabled || a.auth.oidcSettings.Revision != flow.revision || a.auth.accountsRevision != flow.accountRevision || ctx.Err() != nil {
		a.auth.mu.Unlock()
		failRecorded()
		return
	}
	accountID, emailAccountID, accountName := "", "", ""
	for _, binding := range a.auth.oidcSettings.Bindings {
		if binding.Kind == "" && binding.Subject == idToken.Subject {
			accountID = binding.AccountID
		} else if binding.Kind == "email" && claims.EmailVerified != nil && *claims.EmailVerified && binding.Subject == claims.Email {
			emailAccountID = binding.AccountID
		}
	}
	// A subject binding always wins over an email binding. Email is accepted
	// only from this verified ID token, with an explicit verified claim.
	if accountID == "" {
		accountID = emailAccountID
	}
	for _, account := range a.auth.accounts {
		if account.ID == accountID && account.activeAt(time.Now()) {
			accountName = account.Username
			break
		}
	}
	if accountName == "" {
		a.auth.mu.Unlock()
		failRecorded()
		return
	}
	sid, err := a.auth.newSessionLocked(r, accountID, time.Now())
	if err == nil {
		session := a.auth.sessions[sid]
		session.oidcRevision = flow.revision
		if idToken.Expiry.Before(session.expires) {
			session.expires = idToken.Expiry
		}
		a.auth.sessions[sid] = session
	}
	a.auth.mu.Unlock()
	if err != nil {
		failRecorded()
		return
	}
	a.loginHistory.record(r, accountName, true)
	http.SetCookie(w, &http.Cookie{Name: "kurumi_session", Value: sid, Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: 43200})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
