// SPDX-License-Identifier: Apache-2.0
// Modified by KurumiFRP: Lifetime tracking for the KurumiFRP embedded engine.

// Package lifetime tracks the resources of one restricted client control.
package lifetime

import (
	"context"
	"io"
	"sync"
)

type scopeKey struct{}
type resource struct{ closer io.Closer }

// Scope prevents new work after Stop and waits for admitted work to finish.
// Resources registered with Track are closed when stopping begins.
type Scope struct {
	mu        sync.Mutex
	stopping  bool
	resources map[*resource]struct{}
	work      sync.WaitGroup
	stopOnce  sync.Once
	onPanic   func()
}

func New() *Scope { return NewWithPanicHandler(nil) }

// NewWithPanicHandler reports panics in workers admitted by Go. The handler
// must be instance-specific, concurrency-safe, prompt, and must not panic.
// It must trigger shutdown; recovery does not establish that cleanup succeeded.
// A nil handler retains the ordinary process-fatal panic behavior.
func NewWithPanicHandler(onPanic func()) *Scope {
	return &Scope{resources: make(map[*resource]struct{}), onPanic: onPanic}
}

func WithScope(ctx context.Context, scope *Scope) context.Context {
	return context.WithValue(ctx, scopeKey{}, scope)
}

func fromContext(ctx context.Context) *Scope {
	if ctx == nil {
		return nil
	}
	scope, _ := ctx.Value(scopeKey{}).(*Scope)
	return scope
}

// ReportPanic notifies the owning scope after an inner handler has recovered
// a panic. It returns false when no instance-specific handler is available.
// The handler has the same nonblocking and no-panic contract as for Go.
func ReportPanic(ctx context.Context) bool {
	scope := fromContext(ctx)
	if scope == nil || scope.onPanic == nil {
		return false
	}
	scope.onPanic()
	return true
}

// Recover reports a panic from an owner-managed goroutine that cannot itself
// join the scope it helps shut down. Cleanup remains the owner's responsibility.
// Without an instance handler the original panic remains process-fatal.
func Recover(ctx context.Context) {
	if value := recover(); value != nil {
		if !ReportPanic(ctx) {
			panic(value)
		}
	}
}

// Go starts fn unless this context's scope is already stopping. With no scope
// it retains normal asynchronous behavior.
func Go(ctx context.Context, fn func()) bool {
	scope := fromContext(ctx)
	if scope == nil {
		go fn()
		return true
	}
	scope.mu.Lock()
	if scope.stopping {
		scope.mu.Unlock()
		return false
	}
	scope.work.Add(1)
	scope.mu.Unlock()
	go func() {
		defer scope.work.Done()
		if scope.onPanic != nil {
			defer func() {
				if recover() != nil {
					scope.onPanic()
				}
			}()
		}
		fn()
	}()
	return true
}

// Enter tracks work already running in a caller-owned goroutine, such as an
// HTTP handler. The returned function must be called exactly once on exit.
func Enter(ctx context.Context) (func(), bool) {
	scope := fromContext(ctx)
	if scope == nil {
		return func() {}, true
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if scope.stopping {
		return func() {}, false
	}
	scope.work.Add(1)
	return scope.work.Done, true
}

// Track registers a resource for shutdown. The returned release only removes
// the registration; the owner must still close the resource normally.
func Track(ctx context.Context, closer io.Closer) (release func(), ok bool) {
	scope := fromContext(ctx)
	if scope == nil {
		return func() {}, true
	}
	scope.mu.Lock()
	if scope.stopping {
		scope.mu.Unlock()
		_ = closer.Close()
		return func() {}, false
	}
	item := &resource{closer: closer}
	scope.resources[item] = struct{}{}
	scope.mu.Unlock()
	return func() {
		scope.mu.Lock()
		delete(scope.resources, item)
		scope.mu.Unlock()
	}, true
}

func (s *Scope) Stop() {
	s.stopOnce.Do(func() {
		s.mu.Lock()
		s.stopping = true
		resources := make([]*resource, 0, len(s.resources))
		for item := range s.resources {
			resources = append(resources, item)
		}
		s.mu.Unlock()
		for _, item := range resources {
			_ = item.closer.Close()
		}
	})
}

// Wait is called after Stop, when no more tasks can be admitted.
func (s *Scope) Wait() { s.work.Wait() }
