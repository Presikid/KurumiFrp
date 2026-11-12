package embedded

import (
	"context"

	"github.com/fatedier/frp/server"
)

// ReadManagedProxyNames samples only this running Server's upstream Service.
// The caller is responsible for authorizing identity and checking the owning
// manager instance and credential generations before and after the sample.
func (s *Server) ReadManagedProxyNames(ctx context.Context, identity server.ManagedIdentity) (server.ManagedProxySnapshot, error) {
	if ctx == nil {
		return server.ManagedProxySnapshot{}, server.ErrManagedProxySnapshotUnavailable
	}
	if err := ctx.Err(); err != nil {
		return server.ManagedProxySnapshot{}, err
	}
	svc := s.managedProxySnapshotService()
	if svc == nil {
		return server.ManagedProxySnapshot{}, server.ErrManagedProxySnapshotUnavailable
	}
	result, err := svc.ReadManagedProxyNames(ctx, identity)
	if err != nil {
		return server.ManagedProxySnapshot{}, err
	}
	if err = ctx.Err(); err != nil {
		return server.ManagedProxySnapshot{}, err
	}
	if s.managedProxySnapshotService() != svc {
		return server.ManagedProxySnapshot{}, server.ErrManagedProxySnapshotUnavailable
	}
	return result, nil
}

func (s *Server) managedProxySnapshotService() *server.Service {
	if s == nil || !s.mu.TryLock() {
		return nil
	}
	defer s.mu.Unlock()
	if s.state != serverRunning || s.service == nil || s.options.ManagedLogin == nil {
		return nil
	}
	select {
	case <-s.done:
		return nil
	default:
		return s.service
	}
}
