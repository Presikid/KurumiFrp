// Copyright 2026 KurumiFRP contributors
// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

const maxIsolatedSocks5InstanceWork int32 = 2048

// ErrPluginCleanupFailed omits plugin errors that could contain credentials.
var ErrPluginCleanupFailed = errors.New("isolated client plugin cleanup failed")

type pluginCleanupKey struct{}

type pluginCleanupState struct {
	mu         sync.Mutex
	failed     bool
	onFailure  func()
	socks5Work atomic.Int32
}

// The state is shared by every control generation of one isolated service.
// Atomic admission avoids taking mu while a proxy wrapper or rule is locked.
func (state *pluginCleanupState) admitSocks5Work() bool {
	for {
		active := state.socks5Work.Load()
		if active >= maxIsolatedSocks5InstanceWork {
			return false
		}
		if state.socks5Work.CompareAndSwap(active, active+1) {
			return true
		}
	}
}

func (state *pluginCleanupState) finishSocks5Work() {
	state.socks5Work.Add(-1)
}

// WithPluginCleanupFailureHandler is used only by the isolated service. The
// handler must return promptly and must not panic.
func WithPluginCleanupFailureHandler(ctx context.Context, onFailure func()) context.Context {
	return context.WithValue(ctx, pluginCleanupKey{}, &pluginCleanupState{onFailure: onFailure})
}

func pluginCleanupFromContext(ctx context.Context) *pluginCleanupState {
	if ctx == nil {
		return nil
	}
	state, _ := ctx.Value(pluginCleanupKey{}).(*pluginCleanupState)
	return state
}

// PluginCleanupFailed reports the result shared by this service's controls.
func PluginCleanupFailed(ctx context.Context) bool {
	state := pluginCleanupFromContext(ctx)
	if state == nil {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.failed
}

func (state *pluginCleanupState) fail() {
	state.mu.Lock()
	first := !state.failed
	state.failed = true
	state.mu.Unlock()
	if first && state.onFailure != nil {
		state.onFailure()
	}
}

// startWithCleanPlugins makes a short admission or status publication mutually
// ordered with cleanup failure. start must not perform blocking work.
func startWithCleanPlugins(ctx context.Context, start func()) bool {
	state := pluginCleanupFromContext(ctx)
	if state == nil {
		start()
		return true
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.failed {
		return false
	}
	start()
	return true
}
