// Copyright 2023 The frp Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
// Modified by KurumiFRP to integrate client traffic accounting.
// Modified by KurumiFRP: track restricted client proxy workers.
// Modified by KurumiFRP: make proxy worker startup and shutdown repeatable.

package proxy

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fatedier/frp/client/event"
	"github.com/fatedier/frp/client/health"
	"github.com/fatedier/frp/client/traffic"
	v1 "github.com/fatedier/frp/pkg/config/v1"
	"github.com/fatedier/frp/pkg/msg"
	"github.com/fatedier/frp/pkg/naming"
	plugin "github.com/fatedier/frp/pkg/plugin/client"
	"github.com/fatedier/frp/pkg/transport"
	"github.com/fatedier/frp/pkg/util/lifetime"
	"github.com/fatedier/frp/pkg/util/xlog"
	"github.com/fatedier/frp/pkg/vnet"
)

const (
	ProxyPhaseNew         = "new"
	ProxyPhaseWaitStart   = "wait start"
	ProxyPhaseStartErr    = "start error"
	ProxyPhaseRunning     = "running"
	ProxyPhaseCheckFailed = "check failed"
	ProxyPhaseClosed      = "closed"
)

const (
	healthOK uint32 = iota
	healthFailed
	healthPending
)

var (
	statusCheckInterval = 3 * time.Second
	waitResponseTimeout = 20 * time.Second
	startErrTimeout     = 30 * time.Second
)

type WorkingStatus struct {
	Name  string             `json:"name"`
	Type  string             `json:"type"`
	Phase string             `json:"status"`
	Err   string             `json:"err"`
	Cfg   v1.ProxyConfigurer `json:"cfg"`

	// Got from server.
	RemoteAddr string `json:"remote_addr"`
}

type Wrapper struct {
	WorkingStatus

	// underlying proxy
	pxy Proxy

	// if ProxyConf has healcheck config
	// monitor will watch if it is alive
	monitor *health.Monitor

	// event handler
	handler event.Handler

	msgTransporter transport.MessageTransporter
	// vnet controller
	vnetController *vnet.Controller

	health           uint32
	lastSendStartMsg time.Time
	lastStartErr     time.Time
	closeCh          chan struct{}
	healthNotifyCh   chan struct{}
	started          bool
	mu               sync.RWMutex

	xl  *xlog.Logger
	ctx context.Context

	wireName       string
	trafficCounter *traffic.Rule
	socks5Rule     *isolatedSocks5Rule
}

const maxIsolatedSocks5RuleWork = 512

// Track each isolated SOCKS5 rule's work from admission through plugin return,
// including work that has not reached the plugin yet. Stop cancels its dials,
// closes its streams, and waits for the work before releasing the rule.
type isolatedSocks5Rule struct {
	ctx     context.Context
	cancel  context.CancelFunc
	cleanup *pluginCleanupState

	mu       sync.Mutex
	stopping bool
	streams  map[*isolatedSocks5Work]struct{}
	work     sync.WaitGroup
}

type isolatedSocks5Work struct {
	rule       *isolatedSocks5Rule
	conn       net.Conn
	closeOnce  sync.Once
	finishOnce sync.Once
}

func (r *isolatedSocks5Rule) admit(conn net.Conn) *isolatedSocks5Work {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopping || r.ctx.Err() != nil || len(r.streams) >= maxIsolatedSocks5RuleWork || !r.cleanup.admitSocks5Work() {
		return nil
	}
	var item *isolatedSocks5Work
	counted, committed := false, false
	defer func() {
		if !committed {
			delete(r.streams, item)
			if counted {
				r.work.Done()
			}
			r.cleanup.finishSocks5Work()
		}
	}()
	item = &isolatedSocks5Work{rule: r, conn: conn}
	r.work.Add(1)
	counted = true
	r.streams[item] = struct{}{}
	committed = true
	return item
}

func (item *isolatedSocks5Work) close() {
	item.closeOnce.Do(func() {
		completed := false
		defer func() {
			if !completed {
				item.rule.cleanup.fail()
			}
		}()
		// Interrupt work still resolving addresses or setting up wrappers as
		// well as sessions already admitted by the plugin.
		// The isolated service rejects custom connectors. Its default connector
		// wraps each work stream in trackedConnectorConn, whose Close is shared
		// and caches its result when the plugin also closes the same stream.
		_ = item.conn.SetDeadline(time.Now())
		err := item.conn.Close()
		if isolatedSocks5CloseFailed(err) {
			item.rule.cleanup.fail()
		}
		completed = true
	})
}

func (item *isolatedSocks5Work) finish() {
	item.finishOnce.Do(func() {
		r := item.rule
		defer func() {
			r.mu.Lock()
			delete(r.streams, item)
			r.mu.Unlock()
			r.cleanup.finishSocks5Work()
			r.work.Done()
		}()
		item.close()
	})
}

func (r *isolatedSocks5Rule) reject(conn net.Conn) {
	// A rejected stream never owns a slot, but still needs deadline-assisted
	// teardown and the same failure reporting as an admitted stream.
	(&isolatedSocks5Work{rule: r, conn: conn}).close()
}

func (r *isolatedSocks5Rule) stop() {
	r.mu.Lock()
	r.stopping = true
	r.cancel()
	streams := make([]*isolatedSocks5Work, 0, len(r.streams))
	for item := range r.streams {
		streams = append(streams, item)
	}
	r.mu.Unlock()
	for _, item := range streams {
		item.close()
	}
	r.work.Wait()
}

func NewWrapper(
	ctx context.Context,
	cfg v1.ProxyConfigurer,
	clientCfg *v1.ClientCommonConfig,
	encryptionKey []byte,
	eventHandler event.Handler,
	msgTransporter transport.MessageTransporter,
	vnetController *vnet.Controller,
	udpPacketCodec string,
) *Wrapper {
	return newWrapper(ctx, cfg, clientCfg, encryptionKey, eventHandler, msgTransporter, vnetController, udpPacketCodec, nil)
}

func newWrapper(
	ctx context.Context,
	cfg v1.ProxyConfigurer,
	clientCfg *v1.ClientCommonConfig,
	encryptionKey []byte,
	eventHandler event.Handler,
	msgTransporter transport.MessageTransporter,
	vnetController *vnet.Controller,
	udpPacketCodec string,
	counter *traffic.Rule,
) *Wrapper {
	baseInfo := cfg.GetBaseConfig()
	var socks5Rule *isolatedSocks5Rule
	if cleanup := pluginCleanupFromContext(ctx); cleanup != nil && baseInfo.Plugin.Type == v1.PluginSocks5 {
		ruleCtx, cancel := context.WithCancel(ctx)
		ctx = ruleCtx
		socks5Rule = &isolatedSocks5Rule{
			ctx: ruleCtx, cancel: cancel, cleanup: cleanup,
			streams: make(map[*isolatedSocks5Work]struct{}),
		}
	}
	xl := xlog.FromContextSafe(ctx).Spawn().AppendPrefix(baseInfo.Name)
	pw := &Wrapper{
		WorkingStatus: WorkingStatus{
			Name:  baseInfo.Name,
			Type:  baseInfo.Type,
			Phase: ProxyPhaseNew,
			Cfg:   cfg,
		},
		closeCh:        make(chan struct{}),
		healthNotifyCh: make(chan struct{}),
		handler:        eventHandler,
		msgTransporter: msgTransporter,
		vnetController: vnetController,
		xl:             xl,
		ctx:            xlog.NewContext(ctx, xl),
		wireName:       naming.AddUserPrefix(clientCfg.User, baseInfo.Name),
		trafficCounter: counter,
		socks5Rule:     socks5Rule,
	}

	if baseInfo.HealthCheck.Type != "" && baseInfo.LocalPort > 0 {
		// Do not register the proxy until the first successful check, and do
		// not report a failure until the configured threshold has been reached.
		pw.health = healthPending
		addr := net.JoinHostPort(baseInfo.LocalIP, strconv.Itoa(baseInfo.LocalPort))
		pw.monitor = health.NewMonitor(pw.ctx, baseInfo.HealthCheck, addr,
			pw.statusNormalCallback, pw.statusFailedCallback)
		xl.Tracef("enable health check monitor")
	}

	pw.pxy = newProxy(pw.ctx, pw.Cfg, clientCfg, encryptionKey, pw.msgTransporter, pw.vnetController, udpPacketCodec, pw.trafficCounter)
	return pw
}

func (pw *Wrapper) SetInWorkConnCallback(cb func(*v1.ProxyBaseConfig, net.Conn, *msg.StartWorkConn) bool) {
	pw.pxy.SetInWorkConnCallback(cb)
}

func (pw *Wrapper) SetRunningStatus(remoteAddr string, respErr string) error {
	pw.mu.Lock()
	defer pw.mu.Unlock()
	if pw.Phase != ProxyPhaseWaitStart {
		return fmt.Errorf("status not wait start, ignore start message")
	}

	pw.RemoteAddr = remoteAddr
	if respErr != "" {
		pw.Phase = ProxyPhaseStartErr
		pw.Err = respErr
		pw.lastStartErr = time.Now()
		return fmt.Errorf("%s", pw.Err)
	}

	if PluginCleanupFailed(pw.ctx) {
		return ErrPluginCleanupFailed
	}
	// Run may resolve DNS or construct a plugin; do not hold the shared
	// cleanup state lock while it performs those operations.
	err := pw.pxy.Run()
	if err != nil {
		if PluginCleanupFailed(pw.ctx) {
			pw.pxy.Close()
			return ErrPluginCleanupFailed
		}
		pw.close()
		pw.Phase = ProxyPhaseStartErr
		pw.Err = err.Error()
		pw.lastStartErr = time.Now()
		return err
	}

	if !startWithCleanPlugins(pw.ctx, func() {
		pw.Phase = ProxyPhaseRunning
		pw.Err = ""
	}) {
		// Run was admitted before failure but must not become visible after it.
		pw.pxy.Close()
		return ErrPluginCleanupFailed
	}
	return nil
}

func (pw *Wrapper) Start() {
	pw.mu.Lock()
	if pw.started || pw.Phase == ProxyPhaseClosed {
		pw.mu.Unlock()
		return
	}
	pw.started = true
	pw.mu.Unlock()
	lifetime.Go(pw.ctx, pw.checkWorker)
	if pw.monitor != nil {
		pw.monitor.Start()
	}
}

func (pw *Wrapper) Stop() {
	pw.mu.Lock()
	defer pw.mu.Unlock()
	if pw.Phase == ProxyPhaseClosed {
		return
	}
	select {
	case <-pw.closeCh:
	default:
		close(pw.closeCh)
	}
	if pw.socks5Rule != nil {
		pw.socks5Rule.stop()
	}
	pw.pxy.Close()
	if pw.monitor != nil {
		pw.monitor.Stop()
	}
	pw.Phase = ProxyPhaseClosed
	pw.close()
}

// discardUnstarted releases a wrapper that was never published or started.
// No CloseProxy event is sent for a rule that was not admitted.
func (pw *Wrapper) discardUnstarted() {
	if pw.socks5Rule != nil {
		pw.socks5Rule.stop()
	}
	pw.pxy.Close()
	if pw.monitor != nil {
		pw.monitor.Stop()
	}
}

func (pw *Wrapper) close() {
	_ = pw.handler(&event.CloseProxyPayload{
		CloseProxyMsg: &msg.CloseProxy{
			ProxyName: pw.wireName,
		},
	})
}

func (pw *Wrapper) checkWorker() {
	xl := pw.xl
	if pw.monitor != nil {
		// let monitor do check request first
		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-pw.closeCh:
			timer.Stop()
			return
		case <-pw.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
	for {
		select {
		case <-pw.closeCh:
			return
		case <-pw.ctx.Done():
			return
		default:
		}
		// check proxy status
		now := time.Now()
		switch atomic.LoadUint32(&pw.health) {
		case healthOK:
			pw.mu.Lock()
			if pw.Phase == ProxyPhaseNew ||
				pw.Phase == ProxyPhaseCheckFailed ||
				(pw.Phase == ProxyPhaseWaitStart && now.After(pw.lastSendStartMsg.Add(waitResponseTimeout))) ||
				(pw.Phase == ProxyPhaseStartErr && now.After(pw.lastStartErr.Add(startErrTimeout))) {

				xl.Tracef("change status from [%s] to [%s]", pw.Phase, ProxyPhaseWaitStart)
				pw.Phase = ProxyPhaseWaitStart

				var newProxyMsg msg.NewProxy
				pw.Cfg.MarshalToMsg(&newProxyMsg)
				newProxyMsg.ProxyName = pw.wireName
				pw.lastSendStartMsg = now
				_ = pw.handler(&event.StartProxyPayload{
					NewProxyMsg: &newProxyMsg,
				})
			}
			pw.mu.Unlock()
		case healthFailed:
			pw.mu.Lock()
			if pw.Phase == ProxyPhaseNew || pw.Phase == ProxyPhaseRunning || pw.Phase == ProxyPhaseWaitStart || pw.Phase == ProxyPhaseStartErr {
				if pw.Phase == ProxyPhaseRunning || pw.Phase == ProxyPhaseWaitStart {
					pw.close()
				}
				xl.Tracef("change status from [%s] to [%s]", pw.Phase, ProxyPhaseCheckFailed)
				pw.Phase = ProxyPhaseCheckFailed
				pw.Err = ""
			}
			pw.mu.Unlock()
		}

		select {
		case <-pw.closeCh:
			return
		case <-pw.ctx.Done():
			return
		case <-time.After(statusCheckInterval):
		case <-pw.healthNotifyCh:
		}
	}
}

func (pw *Wrapper) statusNormalCallback() {
	xl := pw.xl
	atomic.StoreUint32(&pw.health, healthOK)
	select {
	case pw.healthNotifyCh <- struct{}{}:
	default:
	}
	xl.Infof("health check success")
}

func (pw *Wrapper) statusFailedCallback() {
	xl := pw.xl
	atomic.StoreUint32(&pw.health, healthFailed)
	select {
	case pw.healthNotifyCh <- struct{}{}:
	default:
	}
	xl.Infof("health check failed")
}

func (pw *Wrapper) InWorkConn(workConn net.Conn, m *msg.StartWorkConn) {
	xl := pw.xl
	var pxy Proxy
	var running bool
	var rule *isolatedSocks5Rule
	var work *isolatedSocks5Work
	admissionComplete := false
	defer func() {
		// Keep ownership and release any reserved slot if admission panics.
		if !admissionComplete && rule != nil {
			if work != nil {
				work.finish()
			} else {
				rule.reject(workConn)
			}
		}
	}()
	func() {
		pw.mu.RLock()
		defer pw.mu.RUnlock()
		pxy = pw.pxy
		running = pw.Phase == ProxyPhaseRunning
		rule = pw.socks5Rule
		if pxy != nil && running && rule != nil {
			work = rule.admit(workConn)
		}
	}()
	admissionComplete = true
	if rule != nil && work == nil {
		rule.reject(workConn)
		return
	}
	scheduled := false
	if work != nil {
		// If logging or worker admission panics, keep the admitted stream
		// owned until it has been closed and removed from the rule.
		defer func() {
			if !scheduled {
				work.finish()
			}
		}()
	}
	if pxy != nil && running {
		xl.Debugf("start a new work connection, localAddr: %s remoteAddr: %s", workConn.LocalAddr().String(), workConn.RemoteAddr().String())
		if !lifetime.Go(pw.ctx, func() {
			if work != nil {
				defer work.finish()
				if pw.ctx.Err() != nil {
					return
				}
			}
			pxy.InWorkConn(workConn, m)
		}) {
			if work == nil {
				_ = workConn.Close()
			}
		} else {
			scheduled = true
		}
	} else {
		workConn.Close()
	}
}

// reloadablePlugin owns its reload serialization and error snapshot. ReloadError
// must not block on I/O. Reload must be safe during Close, and retain usable
// resources on failure. Existing plugins need not implement this capability.
type reloadablePlugin interface {
	plugin.Plugin
	Reload() error
	ReloadError() error
}

// Caller holds pw.mu. Concrete reloadable plugins are pointer instances.
func (pw *Wrapper) reloadablePluginLocked() reloadablePlugin {
	owner, ok := pw.pxy.(interface{ getPlugin() plugin.Plugin })
	if !ok {
		return nil
	}
	p, _ := owner.getPlugin().(reloadablePlugin)
	return p
}

func (pw *Wrapper) GetStatus() *WorkingStatus {
	pw.mu.RLock()
	defer pw.mu.RUnlock()
	ps := &WorkingStatus{
		Name:       pw.Name,
		Type:       pw.Type,
		Phase:      pw.Phase,
		Err:        pw.Err,
		Cfg:        pw.Cfg,
		RemoteAddr: pw.RemoteAddr,
	}
	if pw.Phase == ProxyPhaseRunning {
		if p := pw.reloadablePluginLocked(); p != nil {
			if err := p.ReloadError(); err != nil {
				if ps.Err != "" {
					ps.Err += "; "
				}
				ps.Err += fmt.Sprintf("reload plugin %s: %v", p.Name(), err)
			}
		}
	}
	return ps
}
