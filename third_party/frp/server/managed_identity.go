package server

import (
	"context"
	"encoding/hex"
	"errors"
	"time"

	"github.com/fatedier/frp/pkg/auth"
	v1 "github.com/fatedier/frp/pkg/config/v1"
	"github.com/fatedier/frp/pkg/msg"
)

// ManagedIdentity names one credential generation within a service instance.
type ManagedIdentity struct {
	AccountID    string
	CredentialID string
	Version      uint64
}

// ManagedCredential contains a secret and must not be logged or persisted by frp.
// Token must be a unique cryptographically random 32-byte value encoded in
// lowercase hex. A rotated token must always receive a new identity version.
type ManagedCredential struct {
	Identity ManagedIdentity
	Token    string
}

// ManagedLoginProvider resolves an untrusted account ID and provides an
// admission gate shared with account disable, expiry and credential rotation.
// Both methods must be concurrency-safe, prompt, and must not perform I/O.
// The provider must never reuse an identity version for a different token.
type ManagedLoginProvider interface {
	ResolveLogin(ctx context.Context, accountID string) (ManagedCredential, error)
	// TryAuthorize must not wait for locks. It rechecks the exact identity,
	// account status, expiry, and service binding while holding the account's
	// admission read gate. On success it returns a non-nil prompt release and
	// the checked absolute expiry; a zero time means the account has no expiry.
	// It may be called with frp lifecycle locks held and must not call into frp.
	TryAuthorize(identity ManagedIdentity) (release func(), expiresAt time.Time, err error)
}

type managedLogin struct {
	identity  ManagedIdentity
	verifier  auth.Verifier
	key       []byte
}

func validManagedAccountID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, c := range id {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func validManagedIdentity(id ManagedIdentity) bool {
	return validManagedAccountID(id.AccountID) && id.CredentialID != "" && len(id.CredentialID) <= 128 && id.Version != 0
}

// Upstream token proofs have no nonce and do not bind User or RunID. A time
// window limits stale replay but does not provide full replay protection.
func managedTimestampOK(timestamp int64) bool {
	now := time.Now().Unix()
	return timestamp >= now-300 && timestamp <= now+300
}

// ValidateManagedLoginConfig checks the structural requirements for account
// login. It does not open listeners, read certificate files, or call a resolver.
// Callers must complete the server configuration before invoking it.
func ValidateManagedLoginConfig(cfg *v1.ServerConfig) error {
	if cfg == nil || !cfg.Transport.TLS.Force || cfg.Transport.TLS.CertFile == "" || cfg.Transport.TLS.KeyFile == "" {
		return errors.New("account login requires forced TLS and a server certificate")
	}
	if cfg.Auth.Method != v1.AuthMethodToken || cfg.Auth.TokenSource != nil {
		return errors.New("account login requires token authentication without a token source")
	}
	if cfg.Transport.TCPMux == nil || *cfg.Transport.TCPMux {
		return errors.New("account login requires tcpMux to be explicitly disabled")
	}
	if cfg.KCPBindPort != 0 || cfg.QUICBindPort != 0 || cfg.SSHTunnelGateway.BindPort != 0 {
		return errors.New("account login supports direct TCP/TLS only")
	}
	if cfg.VhostHTTPPort != 0 || cfg.VhostHTTPSPort != 0 || cfg.TCPMuxHTTPConnectPort != 0 ||
		cfg.WebServer.Port != 0 || cfg.MetricsServer.Port != 0 || len(cfg.HTTPPlugins) != 0 {
		return errors.New("account login does not support auxiliary listeners or server plugins")
	}
	return nil
}

func resolveManagedLogin(ctx context.Context, provider ManagedLoginProvider, login *msg.Login, scopes []v1.AuthScope) (*managedLogin, error) {
	invalid := errors.New("account credentials are invalid")
	if ctx == nil || ctx.Err() != nil || provider == nil || login == nil ||
		!validManagedAccountID(login.User) || !managedTimestampOK(login.Timestamp) {
		return nil, invalid
	}
	credential, err := provider.ResolveLogin(ctx, login.User)
	if err != nil || !validManagedIdentity(credential.Identity) || credential.Identity.AccountID != login.User {
		return nil, invalid
	}
	token := credential.Token
	decoded, err := hex.DecodeString(token)
	if err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != token {
		return nil, invalid
	}
	// Work connections must prove possession of the same account token, even
	// when the ordinary server configuration omits this additional scope.
	configuredScopes := append([]v1.AuthScope(nil), scopes...)
	configuredScopes = append(configuredScopes, v1.AuthScopeNewWorkConns)
	verifier := auth.NewTokenAuth(configuredScopes, token)
	if err := verifier.VerifyLogin(login); err != nil {
		return nil, invalid
	}
	return &managedLogin{identity: credential.Identity, verifier: verifier, key: []byte(token)}, nil
}
