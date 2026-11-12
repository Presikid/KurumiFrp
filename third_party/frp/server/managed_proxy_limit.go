package server

import (
	"fmt"
	"sync"

	"github.com/fatedier/frp/server/proxy"
)

// managedCountedProxy transfers the slot to the actual listener owner. A
// failed or panicking close retains the slot until the process exits.
type managedCountedProxy struct {
	proxy.Proxy
	ctl     *Control
	release func()
	once    sync.Once
}

func (p *managedCountedProxy) Close() {
	p.once.Do(func() {
		defer func() {
			if value := recover(); value != nil {
				p.ctl.noteManagedClose(fmt.Errorf("managed proxy close panicked"))
				panic(value)
			}
		}()
		p.Proxy.Close()
		if !p.ctl.managedUncertain.Load() && !p.ctl.managedFailedLocked() {
			p.release()
		}
	})
}

func (ctl *Control) reserveManagedProxy() (func(), error) {
	ctl.lifecycleMu.Lock()
	defer ctl.lifecycleMu.Unlock()
	if ctl.state != controlStateRunning || ctl.managedExpiredLocked() || ctl.managedFailedLocked() {
		return nil, fmt.Errorf("account session has expired or is closing")
	}
	return ctl.sessionCtx.managedProvider.TryReserveProxy(ctl.sessionCtx.managedIdentity)
}
