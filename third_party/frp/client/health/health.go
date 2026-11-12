// Copyright 2018 fatedier, fatedier@gmail.com
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

// Modified by KurumiFRP: guard monitor startup in the embedded lifecycle.

package health

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/fatedier/frp/client/internal/netx"
	v1 "github.com/fatedier/frp/pkg/config/v1"
	"github.com/fatedier/frp/pkg/util/lifetime"
	"github.com/fatedier/frp/pkg/util/xlog"
)

var ErrHealthCheckType = errors.New("error health check type")

func newHealthTimer(interval time.Duration) (<-chan time.Time, func()) {
	timer := time.NewTimer(interval)
	return timer.C, func() { timer.Stop() }
}

type Monitor struct {
	checkType      string
	interval       time.Duration
	timeout        time.Duration
	maxFailedTimes int

	// For connection checks
	addr       string
	unixSocket bool

	// For http
	url            string
	header         http.Header
	failedTimes    uint64
	statusKnown    bool
	statusOK       bool
	statusNormalFn func()
	statusFailedFn func()

	ctx       context.Context
	cancel    context.CancelFunc
	doneCh    chan struct{}
	startOnce sync.Once

	timerFactory func(time.Duration) (<-chan time.Time, func())
}

func NewMonitor(ctx context.Context, cfg v1.HealthCheckConfig, addr string,
	statusNormalFn func(), statusFailedFn func(),
) *Monitor {
	if cfg.IntervalSeconds <= 0 {
		cfg.IntervalSeconds = 10
	}
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = 3
	}
	if cfg.MaxFailed <= 0 {
		cfg.MaxFailed = 1
	}
	newctx, cancel := context.WithCancel(ctx)

	var url string
	if cfg.Type == "http" && cfg.Path != "" {
		s := "http://" + addr
		if !strings.HasPrefix(cfg.Path, "/") {
			s += "/"
		}
		url = s + cfg.Path
	}
	header := make(http.Header)
	for _, h := range cfg.HTTPHeaders {
		header.Set(h.Name, h.Value)
	}

	return &Monitor{
		checkType:      cfg.Type,
		interval:       time.Duration(cfg.IntervalSeconds) * time.Second,
		timeout:        time.Duration(cfg.TimeoutSeconds) * time.Second,
		maxFailedTimes: cfg.MaxFailed,
		addr:           addr,
		url:            url,
		header:         header,
		statusOK:       false,
		statusNormalFn: statusNormalFn,
		statusFailedFn: statusFailedFn,
		ctx:            newctx,
		cancel:         cancel,
		doneCh:         make(chan struct{}),
		timerFactory:   newHealthTimer,
	}
}

// NewUnixConnectMonitor checks an absolute Unix socket by connecting or issuing
// an HTTP GET through the isolated client's tracked network.
func NewUnixConnectMonitor(ctx context.Context, cfg v1.HealthCheckConfig, addr string,
	statusNormalFn func(), statusFailedFn func(),
) *Monitor {
	monitor := NewMonitor(ctx, cfg, addr, statusNormalFn, statusFailedFn)
	monitor.unixSocket = true
	if cfg.Type == "http" && cfg.Path != "" {
		path := cfg.Path
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		// The URL supplies only the HTTP Host and request path. DialContext
		// below always connects to the configured Unix socket instead.
		monitor.url = "http://localhost" + path
	}
	return monitor
}

func (monitor *Monitor) Start() {
	monitor.startOnce.Do(func() {
		if !lifetime.Go(monitor.ctx, monitor.checkWorker) {
			close(monitor.doneCh)
		}
	})
}

func (monitor *Monitor) Stop() {
	monitor.cancel()
}

// Done is closed when the worker launched by Start has exited.
func (monitor *Monitor) Done() <-chan struct{} {
	return monitor.doneCh
}

func (monitor *Monitor) checkWorker() {
	defer close(monitor.doneCh)

	for {
		if monitor.ctx.Err() != nil {
			return
		}

		doCtx, cancel := context.WithDeadline(monitor.ctx, time.Now().Add(monitor.timeout))
		err := monitor.doCheck(doCtx)
		cancel()

		// check if this monitor has been closed
		if monitor.ctx.Err() != nil {
			return
		}
		monitor.handleCheckResult(err)

		if !monitor.waitForNextCheck() {
			return
		}
	}
}

func (monitor *Monitor) handleCheckResult(err error) {
	xl := xlog.FromContextSafe(monitor.ctx)
	if err == nil {
		xl.Tracef("do one health check success")
		monitor.failedTimes = 0
		if !monitor.statusKnown || !monitor.statusOK {
			xl.Infof("health check status change to success")
			monitor.statusKnown = true
			monitor.statusOK = true
			if monitor.statusNormalFn != nil {
				monitor.statusNormalFn()
			}
		}
		return
	}

	xl.Warnf("do one health check failed: %v", err)
	monitor.failedTimes++
	if int(monitor.failedTimes) >= monitor.maxFailedTimes && (!monitor.statusKnown || monitor.statusOK) {
		xl.Warnf("health check status change to failed")
		monitor.statusKnown = true
		monitor.statusOK = false
		if monitor.statusFailedFn != nil {
			monitor.statusFailedFn()
		}
	}
}

func (monitor *Monitor) waitForNextCheck() bool {
	timerC, stopTimer := monitor.timerFactory(monitor.interval)
	defer stopTimer()

	select {
	case <-monitor.ctx.Done():
		return false
	case <-timerC:
		return monitor.ctx.Err() == nil
	}
}

func (monitor *Monitor) doCheck(ctx context.Context) error {
	if monitor.unixSocket && monitor.checkType != "tcp" && monitor.checkType != "http" {
		return ErrHealthCheckType
	}
	switch monitor.checkType {
	case "tcp":
		return monitor.doTCPCheck(ctx)
	case "http":
		return monitor.doHTTPCheck(ctx)
	default:
		return ErrHealthCheckType
	}
}

func (monitor *Monitor) doTCPCheck(ctx context.Context) error {
	networkType := "tcp"
	if monitor.unixSocket {
		if !strings.HasPrefix(monitor.addr, "/") {
			return errors.New("unix socket health check requires an absolute path")
		}
		networkType = "unix"
	}
	// if tcp address is not specified, always return nil
	if monitor.addr == "" {
		return nil
	}

	var conn net.Conn
	var err error
	if network := netx.FromContext(ctx); network != nil {
		conn, err = network.DialContext(ctx, networkType, monitor.addr, 0, 0, false)
	} else {
		var d net.Dialer
		conn, err = d.DialContext(ctx, networkType, monitor.addr)
	}
	if err != nil {
		return err
	}
	conn.Close()
	return nil
}

func (monitor *Monitor) doHTTPCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", monitor.url, nil)
	if err != nil {
		return err
	}
	req.Header = monitor.header
	req.Host = monitor.header.Get("Host")
	client := http.DefaultClient
	network := netx.FromContext(ctx)
	if monitor.unixSocket {
		if !strings.HasPrefix(monitor.addr, "/") {
			return errors.New("unix socket health check requires an absolute path")
		}
		if network == nil {
			return errors.New("unix socket HTTP health check requires an isolated network")
		}
	}
	if network != nil {
		transport := &http.Transport{
			DisableKeepAlives: true,
			DialContext: func(ctx context.Context, networkType, address string) (net.Conn, error) {
				if monitor.unixSocket {
					return network.DialContext(ctx, "unix", monitor.addr, 0, 0, false)
				}
				return network.DialContext(ctx, networkType, address, 0, 0, false)
			},
		}
		defer transport.CloseIdleConnections()
		client = &http.Client{
			Transport: transport,
			// A redirect is not a successful check of the configured target.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("do http health check, StatusCode is [%d] not 2xx", resp.StatusCode)
	}
	return nil
}
