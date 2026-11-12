package embedded

import (
	"context"
	"errors"

	"github.com/fatedier/frp/server"
)

// RevokeManagedCredential waits for the matching credential generation to
// finish its server-side controls and tracked forwarding work. Before calling,
// the owner must make that generation invalid in the account provider.
func (s *Server) RevokeManagedCredential(ctx context.Context, identity server.ManagedIdentity) error {
	if s == nil || ctx == nil {
		return errors.New("embedded account revocation requires a server and context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	managed := s.options.ManagedLogin != nil
	svc := s.service
	s.mu.Unlock()
	if !managed {
		return errors.New("embedded server does not use account authentication")
	}
	if svc == nil {
		// Run starts only after the service is published. The provider must
		// reject this generation before it can be admitted by a later Run.
		return ctx.Err()
	}
	return svc.RevokeManagedCredential(ctx, identity)
}
