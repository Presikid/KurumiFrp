package server

import (
	"fmt"

	"github.com/fatedier/frp/pkg/msg"
	"github.com/fatedier/frp/pkg/util/lifetime"
	"github.com/fatedier/frp/server/proxy"
)

// A handler may panic after creating a resource but before publishing it.
// Mark cleanup uncertain before the dispatcher unwinds and reports the panic.
func (ctl *Control) managedHandler(handler func(msg.Message)) func(msg.Message) {
	if ctl.managedScope == nil {
		return handler
	}
	return func(m msg.Message) {
		defer func() {
			if value := recover(); value != nil {
				ctl.noteManagedClose(fmt.Errorf("managed control handler panicked"))
				panic(value)
			}
		}()
		handler(m)
	}
}

// managedWorker owns the final cleanup even if its startup or wait panics.
func (ctl *Control) managedWorker() {
	dispatcherInvoked := false
	metricOpen := false
	defer func() {
		panicValue := recover()
		if panicValue != nil {
			ctl.noteManagedClose(fmt.Errorf("managed control worker panicked"))
			if lifetime.ReportPanic(ctl.ctx) {
				panicValue = nil
			}
		}
		ctl.cleanupManagedWorker(dispatcherInvoked, metricOpen)
		if panicValue != nil {
			panic(panicValue)
		}
	}()

	ctl.serverMetrics.NewClient()
	metricOpen = true
	lifetime.Go(ctl.ctx, ctl.heartbeatWorker)
	// If Run panics after launching its read loop, cleanup must still wait for
	// Done. If it panics before launching one, cleanup remains unconfirmed.
	dispatcherInvoked = true
	ctl.msgDispatcher.Run()
	lifetime.Go(ctl.ctx, func() {
		for i := 0; i < ctl.poolCount; i++ {
			_ = ctl.msgDispatcher.Send(&msg.ReqWorkConn{})
		}
	})
	<-ctl.msgDispatcher.Done()
}

// managedCleanupStep records a panic without skipping the other resources. A
// failed step prevents doneCh from closing even if later workers finish.
func (ctl *Control) managedCleanupStep(fn func()) (ok bool) {
	defer func() {
		if recover() != nil {
			ctl.noteManagedClose(fmt.Errorf("managed control cleanup panicked"))
		}
	}()
	fn()
	return true
}

// Catch a scope panic inside the once callback, before another caller can
// observe Stop as complete and finish the control.
func (ctl *Control) stopManagedScope() bool {
	ctl.managedScopeStopOnce.Do(func() {
		defer func() {
			if recover() != nil {
				ctl.managedScopeStopFailed = true
				ctl.noteManagedClose(fmt.Errorf("managed forwarding stop panicked"))
			}
		}()
		ctl.managedScope.Stop()
	})
	return !ctl.managedScopeStopFailed
}

func (ctl *Control) takeManagedProxies() map[string]proxy.Proxy {
	ctl.mu.Lock()
	defer ctl.mu.Unlock()
	proxies := ctl.proxies
	ctl.proxies = make(map[string]proxy.Proxy)
	return proxies
}

func (ctl *Control) cleanupManagedWorker(dispatcherInvoked, metricOpen bool) {
	defer func() {
		if recover() != nil {
			ctl.noteManagedClose(fmt.Errorf("managed control cleanup panicked"))
		}
	}()
	clean := true
	ctl.lifecycleMu.Lock()
	if ctl.state == controlStateRunning {
		ctl.state = controlStateClosing
	}
	ctl.lifecycleMu.Unlock()
	if !ctl.managedCleanupStep(func() { _ = ctl.interruptReadAndClose() }) {
		clean = false
	}

	// Stop forwarding concurrently with listener closure; neither operation
	// holds the control, account, or manager locks while closing sockets.
	stopDone := make(chan bool, 1)
	go func() { stopDone <- ctl.stopManagedScope() }()
	closeProxies := func() {
		var proxies map[string]proxy.Proxy
		if !ctl.managedCleanupStep(func() { proxies = ctl.takeManagedProxies() }) {
			clean = false
			return
		}
		for _, pxy := range proxies {
			if !ctl.managedCleanupStep(func() { ctl.closeProxy(pxy) }) {
				clean = false
			}
		}
	}
	closeProxies()

	var pooled []*proxy.WorkConn
	if !ctl.managedCleanupStep(func() {
		ctl.mu.Lock()
		defer ctl.mu.Unlock()
		close(ctl.workConnCh)
		for workConn := range ctl.workConnCh {
			pooled = append(pooled, workConn)
		}
	}) {
		clean = false
	}
	for _, workConn := range pooled {
		if !ctl.managedCleanupStep(func() { ctl.noteManagedClose(workConn.Close()) }) {
			clean = false
		}
	}

	if dispatcherInvoked {
		// NewProxy handlers execute synchronously on the read loop. A handler
		// already in flight can publish a listener after the first snapshot.
		<-ctl.msgDispatcher.Done()
	}
	closeProxies()
	if !<-stopDone {
		clean = false
	} else if !ctl.managedCleanupStep(ctl.managedScope.Wait) {
		clean = false
	}
	if metricOpen && !ctl.managedCleanupStep(ctl.serverMetrics.CloseClient) {
		clean = false
	}
	if !clean {
		// Keep this control live: callers return timeout/uncertain, never a
		// false completion after an interrupted cleanup sequence.
		return
	}
	if ctl.manager != nil && !ctl.managedCleanupStep(func() { ctl.manager.Remove(ctl) }) {
		return
	}
	if !ctl.managedCleanupStep(func() { ctl.xl.Infof("client exit success") }) {
		return
	}
	ctl.managedCleanupStep(func() {
		ctl.lifecycleMu.Lock()
		defer ctl.lifecycleMu.Unlock()
		ctl.finishLocked()
	})
}
