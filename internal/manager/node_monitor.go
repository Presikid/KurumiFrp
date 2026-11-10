package manager

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// Monitor observations are per process and contain no remote configuration.
// A changed node stamp invalidates both the result and its failure count.
type nodeMonitorObservation struct {
	stamp     string
	state     string
	reason    string
	checkedAt string
	failures  int
}

type nodeMonitorView struct {
	State     string `json:"state"`
	Reason    string `json:"reason,omitempty"`
	CheckedAt string `json:"checkedAt"`
}

func (a *App) nodeMonitorLoop(ctx context.Context) {
	defer close(a.nodeMonitorDone)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		a.checkMonitoredNodes(ctx)
		select {
		case <-ctx.Done():
			return
		case <-a.quit:
			return
		case <-ticker.C:
		}
	}
}

func (a *App) checkMonitoredNodes(ctx context.Context) {
	a.mu.Lock()
	if a.closing || ctx.Err() != nil {
		a.mu.Unlock()
		return
	}
	cfg := cloneNodeSettings(a.store.DB.Nodes)
	stamps := make(map[string]string)
	for _, node := range cfg.Nodes {
		if node.MonitorEnabled && len(node.Addresses) != 0 {
			stamps[node.ID] = nodeStamp(node)
		}
	}
	for id, observation := range a.nodeMonitors {
		if stamps[id] != observation.stamp {
			delete(a.nodeMonitors, id)
		}
	}
	a.mu.Unlock()

	limit := make(chan struct{}, maxNodeChecks)
	var wg sync.WaitGroup
	for id, stamp := range stamps {
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case <-a.quit:
			wg.Wait()
			return
		case limit <- struct{}{}:
		}
		wg.Add(1)
		go func(id, stamp string) {
			defer wg.Done()
			defer func() { <-limit }()
			_, _, code, err := a.checkRemoteNode(ctx, id, stamp)
			if ctx.Err() != nil || code == http.StatusServiceUnavailable {
				return
			}
			a.recordNodeMonitor(id, stamp, code, err)
		}(id, stamp)
	}
	wg.Wait()
}

func (a *App) recordNodeMonitor(id, stamp string, code int, checkErr error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	select {
	case <-a.quit:
		return
	default:
	}
	node, current := a.nodeWithStampLocked(id, stamp)
	if !current || !node.MonitorEnabled || a.closing {
		return
	}
	if a.nodeMonitors == nil {
		a.nodeMonitors = make(map[string]nodeMonitorObservation)
	}
	observation := a.nodeMonitors[id]
	if observation.stamp != stamp {
		observation = nodeMonitorObservation{stamp: stamp}
	}
	observation.checkedAt = now()
	if checkErr == nil {
		observation.state, observation.reason, observation.failures = "available", "", 0
	} else {
		if observation.failures < 2 {
			observation.failures++
		}
		observation.state, observation.reason = "pending", "unreachable"
		if code == http.StatusConflict {
			observation.reason = "address_changed"
		}
		if observation.failures >= 2 {
			observation.state = "unavailable"
		}
	}
	a.nodeMonitors[id] = observation
}
