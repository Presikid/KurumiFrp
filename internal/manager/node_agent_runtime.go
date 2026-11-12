package manager

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Presikid/KurumiFrp/internal/version"
)

// Outbound authority is local to this workbench and is not part of exports.
// Credential is encrypted with the same local key as other node credentials.
type outboundAgentConfig struct {
	URL string `json:"url"`
	NodeID string `json:"nodeId"`
	Credential string `json:"credential"`
	Addresses []string `json:"addresses,omitempty"`
	Enabled bool `json:"enabled"`
	ShareTraffic bool `json:"shareTraffic"`
	Stamp string `json:"stamp"`
}

func cloneOutboundAgentConfig(cfg *outboundAgentConfig) *outboundAgentConfig {
	if cfg == nil {
		return nil
	}
	copy := *cfg
	copy.Addresses = append([]string(nil), cfg.Addresses...)
	return &copy
}

func validateStoredOutboundAgent(s *Store, cfg *outboundAgentConfig) error {
	if cfg == nil {
		return nil
	}
	origin, err := normalizeNodeURL(cfg.URL)
	if err != nil || origin != cfg.URL || !idPattern.MatchString(cfg.NodeID) || !nodeTokenPattern.MatchString(cfg.Stamp) || !validPinnedAddresses(cfg.Addresses, true) || (cfg.Enabled && len(cfg.Addresses) == 0) {
		return fmt.Errorf("invalid outbound node reporting settings")
	}
	credential, err := s.decrypt(cfg.Credential)
	if err != nil || !nodeTokenPattern.MatchString(credential) {
		return fmt.Errorf("invalid outbound node reporting credential")
	}
	return nil
}

type outboundAgentStatus struct {
	State string `json:"state"`
	Message string `json:"message,omitempty"`
	LastSuccessAt string `json:"lastSuccessAt,omitempty"`
}

// gate serializes setting changes and worker replacement. The worker never
// takes gate; it may take App.mu, so callers must not hold App.mu while waiting.
type outboundAgentManager struct {
	gate sync.Mutex
	statusMu sync.Mutex
	cancel context.CancelFunc
	done chan struct{}
	stamp string
	status outboundAgentStatus
}

func (m *outboundAgentManager) setStatus(stamp, state, message string, success bool) {
	m.statusMu.Lock()
	defer m.statusMu.Unlock()
	if m.stamp != stamp {
		return
	}
	m.status.State, m.status.Message = state, message
	if success {
		m.status.LastSuccessAt = now()
	}
}

func (m *outboundAgentManager) resetStatus(stamp, state, message string) {
	m.statusMu.Lock()
	m.stamp = stamp
	m.status = outboundAgentStatus{State: state, Message: message}
	m.statusMu.Unlock()
}

func (m *outboundAgentManager) view(stamp string, enabled bool) outboundAgentStatus {
	m.statusMu.Lock()
	defer m.statusMu.Unlock()
	if !enabled {
		return outboundAgentStatus{State: "disabled"}
	}
	if m.stamp != stamp || m.status.State == "" {
		return outboundAgentStatus{State: "connecting"}
	}
	return m.status
}

// The caller holds gate, but never App.mu.
func (m *outboundAgentManager) stopLocked() {
	if m.cancel != nil {
		m.cancel()
		<-m.done
		m.cancel, m.done = nil, nil
	}
}

func (m *outboundAgentManager) stop() {
	m.gate.Lock()
	defer m.gate.Unlock()
	m.stopLocked()
	m.resetStatus("", "disabled", "")
}

// The caller holds gate, but never App.mu. An imported configuration may
// re-encrypt the credential without changing its reporting settings or stamp.
func (m *outboundAgentManager) restartLocked(a *App) {
	m.stopLocked()
	a.mu.Lock()
	var cfg *outboundAgentConfig
	if a.store.DB.Nodes != nil {
		cfg = cloneOutboundAgentConfig(a.store.DB.Nodes.Reporter)
	}
	if a.closing || cfg == nil || !cfg.Enabled {
		a.mu.Unlock()
		m.resetStatus("", "disabled", "")
		return
	}
	credential, err := a.store.decrypt(cfg.Credential)
	a.mu.Unlock()
	if err != nil {
		m.resetStatus(cfg.Stamp, "blocked", "无法读取上报授权，请重新配置。")
		return
	}
	sender, err := newOutboundAgentSender(cfg.URL, cfg.Addresses, credential, cfg.NodeID, cfg.ShareTraffic)
	if err != nil {
		m.resetStatus(cfg.Stamp, "blocked", "上报设置无效，请重新配置。")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel, m.done = cancel, make(chan struct{})
	done := m.done
	m.resetStatus(cfg.Stamp, "connecting", "")
	go func() {
		defer close(done)
		m.run(ctx, a, sender, cfg.Stamp)
	}()
}

func outboundAgentWait(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func outboundAgentBackoff(failures int) time.Duration {
	delay := 2 * time.Second
	for n := 1; n < failures && delay < time.Minute; n++ {
		delay *= 2
	}
	if delay > time.Minute {
		return time.Minute
	}
	return delay
}

func outboundAgentBlockingError(err error) bool {
	return errors.Is(err, errOutboundAgentAuthorization) || errors.Is(err, errOutboundAgentAddressChanged) || errors.Is(err, errOutboundAgentIdentity) || errors.Is(err, errOutboundAgentProtocol)
}

func (m *outboundAgentManager) run(ctx context.Context, a *App, sender *outboundAgentSender, stamp string) {
	failures := 0
	for ctx.Err() == nil {
		// Do not let an uncertain old snapshot arrive arbitrarily late and
		// appear current. A new session invalidates the old one at the receiver.
		pendingAge := sender.interval
		if pendingAge > outboundAgentMaxPendingAge {
			pendingAge = outboundAgentMaxPendingAge
		}
		if len(sender.pending) != 0 && time.Since(sender.pendingSince) >= pendingAge {
			sender.session, sender.nextSequence, sender.pending, sender.pendingSince = "", 0, nil, time.Time{}
		}
		var err error
		if sender.session == "" {
			m.setStatus(stamp, "connecting", "", false)
			err = sender.openSession(ctx)
		}
		if err == nil && sender.pending == nil {
			var summary nodeSummary
			summary, err = a.captureOutboundAgentSummaryForPeer(ctx, stamp, sender.allowTraffic, sender.allowTransitions)
			if errors.Is(err, errOutboundAgentSnapshotUnavailable) {
				m.setStatus(stamp, "waiting", "实例状态正在变化，本轮未发送。", false)
				if !outboundAgentWait(ctx, sender.interval) {
					return
				}
				continue
			}
			if err == nil {
				err = sender.prepareReport(summary)
			}
			if errors.Is(err, errOutboundAgentSessionRenew) {
				sender.session, sender.nextSequence = "", 0
				continue
			}
		}
		if err == nil {
			m.setStatus(stamp, "sending", "", false)
			remaining := pendingAge - time.Since(sender.pendingSince)
			if remaining <= 0 {
				continue
			}
			sendCtx, cancel := context.WithTimeout(ctx, remaining)
			err = sender.sendPending(sendCtx)
			cancel()
		}
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			failures = 0
			m.setStatus(stamp, "waiting", "最近一次报告已获确认。", true)
			if !outboundAgentWait(ctx, sender.interval) {
				return
			}
			continue
		}
		if outboundAgentBlockingError(err) {
			m.setStatus(stamp, "blocked", err.Error(), false)
			return
		}
		if errors.Is(err, errOutboundAgentSnapshotChanged) {
			return
		}
		if !errors.Is(err, errOutboundAgentTemporary) && !errors.Is(err, errOutboundAgentConflict) && !errors.Is(err, context.DeadlineExceeded) {
			m.setStatus(stamp, "blocked", "节点摘要或上报配置无效，请检查设置。", false)
			return
		}
		if failures < 16 {
			failures++
		}
		m.setStatus(stamp, "retrying", "上报未获确认，将按退避间隔重试。", false)
		delay := outboundAgentBackoff(failures)
		if len(sender.pending) != 0 {
			remaining := pendingAge - time.Since(sender.pendingSince)
			if remaining < delay {
				delay = remaining
			}
			if delay < 0 {
				delay = 0
			}
		}
		if !outboundAgentWait(ctx, delay) {
			return
		}
	}
}

var (
	errOutboundAgentSnapshotUnavailable = errors.New("node summary is temporarily unavailable")
	errOutboundAgentSnapshotChanged = errors.New("node reporting settings changed")
)

// captureOutboundAgentSummary is a snapshot, not a probe or a count of clients
// or proxies. Runtime readers run without App.mu; all instance generations and
// the configuration revision are rechecked before the result is returned.
func (a *App) captureOutboundAgentSummary(ctx context.Context, stamp string, shareTraffic bool) (nodeSummary, error) {
	return a.captureOutboundAgentSummaryForPeer(ctx, stamp, shareTraffic, false)
}

func (a *App) captureOutboundAgentSummaryForPeer(ctx context.Context, stamp string, shareTraffic, allowTransitions bool) (nodeSummary, error) {
	type source struct {
		proc *process
		state string
		runID string
	}
	a.mu.Lock()
	if a.closing || ctx.Err() != nil || a.store.DB.Nodes == nil || a.store.DB.Nodes.Reporter == nil || !a.store.DB.Nodes.Reporter.Enabled || a.store.DB.Nodes.Reporter.Stamp != stamp || a.store.DB.Nodes.Reporter.ShareTraffic != shareTraffic {
		a.mu.Unlock()
		return nodeSummary{}, errOutboundAgentSnapshotChanged
	}
	revision := a.store.DB.Revision
	instances := make([]nodeInstanceSummary, 0, len(a.store.DB.State.Instances))
	sources := make([]source, 0, len(a.store.DB.State.Instances))
	for _, instance := range a.store.DB.State.Instances {
		proc := a.procs[instance.ID]
		status := RuntimeStatus{State: "stopped"}
		if proc != nil {
			status = proc.view()
		}
		if !allowTransitions && (status.State == "starting" || status.State == "stopping") {
			a.mu.Unlock()
			return nodeSummary{}, errOutboundAgentSnapshotUnavailable
		}
		state := status.State
		if instance.AutoStart && a.startupErrors[instance.ID] != "" && state != "running" && state != "starting" && state != "stopping" {
			state = "autostart-failed"
		}
		instances = append(instances, nodeInstanceSummary{ID: instance.ID, Name: instance.Name, Kind: instance.Kind, State: state, Binding: nodeInstanceBinding(a.store.DB.LocalTrafficIDs[instance.ID])})
		sources = append(sources, source{proc: proc, state: state, runID: status.RunID})
	}
	a.mu.Unlock()
	if shareTraffic {
		for i, source := range sources {
			if ctx.Err() != nil {
				return nodeSummary{}, errOutboundAgentSnapshotChanged
			}
			if source.proc == nil || source.state != "running" || source.runID == "" {
				continue
			}
			var at time.Time
			var received, sent uint64
			available := false
			if instances[i].Kind == "frpc" {
				if reader, ok := source.proc.instance.(RuntimeTrafficReader); ok {
					snapshot := reader.ReadTraffic()
					at, received, sent, available = snapshot.SampledAt, snapshot.ReceivedBytes, snapshot.SentBytes, snapshot.Available
				}
			} else if reader, ok := source.proc.instance.(RuntimeServerTrafficReader); ok {
				snapshot := reader.ReadServerTraffic()
				at, received, sent, available = snapshot.SampledAt, snapshot.ReceivedBytes, snapshot.SentBytes, snapshot.Available
			}
			observed := time.Now()
			if available && received != ^uint64(0) && sent != ^uint64(0) && !at.IsZero() && !at.After(observed.Add(trafficFutureSkew)) && observed.Sub(at) <= trafficFreshness {
				instances[i].Traffic = &nodeTraffic{SampledAt: at.UTC(), ReceivedBytes: &received, SentBytes: &sent}
			}
		}
	}
	a.mu.Lock()
	valid := ctx.Err() == nil && !a.closing && revision == a.store.DB.Revision && a.store.DB.Nodes != nil && a.store.DB.Nodes.Reporter != nil && a.store.DB.Nodes.Reporter.Enabled && a.store.DB.Nodes.Reporter.Stamp == stamp
	if valid {
		for i, source := range sources {
			instance, exists := a.store.DB.State.Instance(instances[i].ID)
			if !exists || a.procs[instances[i].ID] != source.proc {
				valid = false
				break
			}
			status := RuntimeStatus{State: "stopped"}
			if source.proc != nil {
				status = source.proc.view()
			}
			state := status.State
			if instance.AutoStart && a.startupErrors[instance.ID] != "" && state != "running" && state != "starting" && state != "stopping" {
				state = "autostart-failed"
			}
			if state != source.state || status.RunID != source.runID {
				valid = false
				break
			}
		}
	}
	a.mu.Unlock()
	if !valid {
		return nodeSummary{}, errOutboundAgentSnapshotUnavailable
	}
	summary := nodeSummary{Protocol: 1, Version: version.Current, Instances: instances}
	if !validNodeSummaryWithTransitions(summary, allowTransitions) {
		return nodeSummary{}, fmt.Errorf("node summary does not fit the reporting protocol")
	}
	return summary, nil
}
