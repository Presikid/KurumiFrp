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

// ManagedLoginResolver looks up an independent machine token for an untrusted
// account ID. It must be concurrency-safe, return promptly without I/O, and
// reject missing, disabled or expired accounts. Tokens must be cryptographically
// random, unique to each account grant, and 32 bytes encoded as lowercase hex.
// This hook governs new login attempts only: already authenticated controls,
// their work connections, and forwarded traffic are not revoked by an update.
// An in-flight lookup may still complete after its account policy changes.
type ManagedLoginResolver func(ctx context.Context, accountID string) (string, error)

type managedLogin struct {
	accountID string
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
	if cfg.KCPBindPort != 0 || cfg.QUICBindPort != 0 || cfg.SSHTunnelGateway.BindPort != 0 {
		return errors.New("account login supports direct TCP/TLS only")
	}
	if cfg.VhostHTTPPort != 0 || cfg.VhostHTTPSPort != 0 || cfg.TCPMuxHTTPConnectPort != 0 ||
		cfg.WebServer.Port != 0 || cfg.MetricsServer.Port != 0 || len(cfg.HTTPPlugins) != 0 {
		return errors.New("account login does not support auxiliary listeners or server plugins")
	}
	return nil
}

func resolveManagedLogin(ctx context.Context, resolver ManagedLoginResolver, login *msg.Login, scopes []v1.AuthScope) (*managedLogin, error) {
	invalid := errors.New("account credentials are invalid")
	if ctx == nil || ctx.Err() != nil || resolver == nil || login == nil ||
		!validManagedAccountID(login.User) || !managedTimestampOK(login.Timestamp) {
		return nil, invalid
	}
	token, err := resolver(ctx, login.User)
	if err != nil {
		return nil, invalid
	}
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
	return &managedLogin{accountID: login.User, verifier: verifier, key: []byte(token)}, nil
}
