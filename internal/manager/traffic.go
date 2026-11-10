package manager

import (
	"fmt"
	"net/http"
	"time"
)

const (
	trafficHistoryLimit  = 120
	trafficHistoryWindow = 10 * time.Minute
	trafficFreshness     = 30 * time.Second
	trafficFutureSkew    = time.Second
)

// TrafficSnapshot is supplied by the runtime's counter adapter. When Available
// is true, the counters are the source's cumulative forwarded payload bytes
// associated with this run: ReceivedBytes enters the instance from its FRP
// peer and SentBytes leaves towards that peer. Count each forwarded byte once,
// without protocol overhead. After a counter reset these are the new raw values,
// not reconstructed run totals. Zero is meaningful only in an available
// snapshot, whose two counters must come from the same observation.
//
// SampledAt is the time of this observation, including a failed observation.
// Mark Discontinuity when the source changes or its counters may have reset,
// even if the new counter values happen to be greater than the previous ones.
// Submit Available=false when the source cannot produce a measurement. A
// source should sample more frequently than trafficFreshness while running.
type TrafficSnapshot struct {
	SampledAt     time.Time
	Available     bool
	ReceivedBytes uint64
	SentBytes     uint64
	Discontinuity bool
}

// TrafficPoint is one observation in a single run. Null counters mean the
// observation was unavailable. Null rates mean there is no continuous baseline;
// they must not be interpreted as zero. Byte totals are decimal strings in JSON
// to preserve the full uint64 range in JavaScript clients.
type TrafficPoint struct {
	SampledAt              time.Time `json:"sampledAt"`
	Available              bool      `json:"available"`
	ReceivedBytes          *uint64   `json:"receivedBytes,string"`
	SentBytes              *uint64   `json:"sentBytes,string"`
	ReceivedBytesPerSecond *float64  `json:"receivedBytesPerSecond"`
	SentBytesPerSecond     *float64  `json:"sentBytesPerSecond"`
	Discontinuity          bool      `json:"discontinuity"`
	DiscontinuityReason    string    `json:"discontinuityReason,omitempty"`
}

// instanceTraffic is a current observation plus recent observations from the
// same run. SampledAt is the last attempted observation, even when it failed,
// has gone stale, or the run has stopped. Curve never crosses a run boundary.
// Unavailable reasons are not_running, not_sampled, source_unavailable, stale
// and clock_changed. When unavailable, current counters and rates are null;
// historical observations may still be present in Curve.
type instanceTraffic struct {
	RunID                  string         `json:"runId"`
	Available              bool           `json:"available"`
	Reason                 string         `json:"reason,omitempty"`
	SampledAt              *time.Time     `json:"sampledAt"`
	ReceivedBytes          *uint64        `json:"receivedBytes,string"`
	SentBytes              *uint64        `json:"sentBytes,string"`
	ReceivedBytesPerSecond *float64       `json:"receivedBytesPerSecond"`
	SentBytesPerSecond     *float64       `json:"sentBytesPerSecond"`
	Discontinuity          bool           `json:"discontinuity"`
	DiscontinuityReason    string         `json:"discontinuityReason,omitempty"`
	Curve                  []TrafficPoint `json:"curve"`
}

// captureRuntimeTraffic samples only the runtime instance for this run.
// RecordTrafficSnapshot rejects late observations after replacement or stop.
func (a *App) captureRuntimeTraffic(instanceID, runID string, p *process, source RuntimeTrafficReader) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		_ = a.RecordTrafficSnapshot(instanceID, runID, source.ReadTraffic())
		select {
		case <-p.done:
			return
		case <-a.quit:
			return
		case <-ticker.C:
		}
	}
}

// trafficSamples is protected by App.mu and is valid only for its runID.
type trafficSamples struct {
	runID   string
	last    TrafficPoint
	hasLast bool
	points  []TrafficPoint
}

// RecordTrafficSnapshot injects a counter observation for the current running
// generation. The caller must retain that generation's runID; late samples from
// an earlier run, stopped instances, and unknown instances are rejected. There
// is deliberately no HTTP endpoint for submitting observations.
func (a *App) RecordTrafficSnapshot(instanceID, runID string, snapshot TrafficSnapshot) error {
	if instanceID == "" || runID == "" {
		return fmt.Errorf("实例 ID 和运行代次不能为空")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	instance, ok := a.store.DB.State.Instance(instanceID)
	if !ok {
		return fmt.Errorf("实例不存在")
	}
	p := a.procs[instanceID]
	if p == nil {
		return fmt.Errorf("实例未运行")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if (p.status.State != "running" && p.status.State != "stopping") || p.status.RunID != runID {
		return fmt.Errorf("运行代次已变化或实例未运行")
	}
	samples := a.traffic[instanceID]
	if samples == nil || samples.runID != runID {
		samples = &trafficSamples{runID: runID}
	}
	if err := samples.record(snapshot, time.Now().UTC()); err != nil {
		return err
	}
	if a.traffic == nil {
		a.traffic = make(map[string]*trafficSamples)
	}
	if a.traffic[instanceID] != samples {
		a.pruneTrafficLocked()
		a.traffic[instanceID] = samples
	}
	if instance.Kind == "frpc" && a.trafficDaily != nil {
		a.trafficDaily.observe(a.store.DB.LocalTrafficIDs[instanceID], instance.Kind, trafficDailySample{
			At: snapshot.SampledAt, RunID: runID, Available: snapshot.Available,
			Received: snapshot.ReceivedBytes, Sent: snapshot.SentBytes,
			Discontinuity: snapshot.Discontinuity, EpochKnown: true,
		}, a.store.DB.LocalTrafficIDs, time.Now().UTC())
	}
	return nil
}

// Prune on insertion so deleted instances cannot accumulate an unbounded
// number of histories even when process objects outlive their drafts.
func (a *App) pruneTrafficLocked() {
	if len(a.traffic) == 0 {
		return
	}
	instances := make(map[string]struct{}, len(a.store.DB.State.Instances))
	for _, instance := range a.store.DB.State.Instances {
		instances[instance.ID] = struct{}{}
	}
	for id := range a.traffic {
		if _, ok := instances[id]; !ok {
			delete(a.traffic, id)
		}
	}
}

func (t *trafficSamples) record(snapshot TrafficSnapshot, now time.Time) error {
	if snapshot.SampledAt.IsZero() {
		return fmt.Errorf("流量采样时间不能为空")
	}
	at := snapshot.SampledAt.UTC()
	if at.After(now.Add(trafficFutureSkew)) || at.Before(now.Add(-trafficHistoryWindow)) {
		return fmt.Errorf("流量采样时间超出允许范围")
	}
	// If the system clock moved backwards, the old timeline is no longer a
	// valid baseline. Start a new segment instead of rejecting samples until
	// the clock catches up or presenting the old sample as perpetually fresh.
	clockChanged := t.hasLast && t.last.SampledAt.After(now.Add(trafficFutureSkew))
	if t.hasLast && !at.After(t.last.SampledAt) && !clockChanged {
		return fmt.Errorf("流量采样时间必须严格递增")
	}

	point := TrafficPoint{SampledAt: at, Available: snapshot.Available, Discontinuity: true}
	if !snapshot.Available {
		point.DiscontinuityReason = "source_unavailable"
	} else {
		received, sent := snapshot.ReceivedBytes, snapshot.SentBytes
		point.ReceivedBytes, point.SentBytes = &received, &sent
		switch {
		case clockChanged:
			point.DiscontinuityReason = "clock_changed"
		case !t.hasLast:
			point.DiscontinuityReason = "run_start"
		case snapshot.Discontinuity:
			point.DiscontinuityReason = "source_discontinuity"
		case !t.last.Available:
			point.DiscontinuityReason = "sampling_resumed"
		case received < *t.last.ReceivedBytes || sent < *t.last.SentBytes:
			point.DiscontinuityReason = "counter_reset"
		case at.Sub(t.last.SampledAt) > trafficFreshness:
			point.DiscontinuityReason = "sample_gap"
		default:
			seconds := at.Sub(t.last.SampledAt).Seconds()
			receivedRate := float64(received-*t.last.ReceivedBytes) / seconds
			sentRate := float64(sent-*t.last.SentBytes) / seconds
			point.ReceivedBytesPerSecond, point.SentBytesPerSecond = &receivedRate, &sentRate
			point.Discontinuity = false
		}
	}

	if clockChanged {
		t.points = nil
	}
	if len(t.points) == trafficHistoryLimit {
		copy(t.points, t.points[1:])
		t.points[len(t.points)-1] = point
	} else {
		t.points = append(t.points, point)
	}
	t.last, t.hasLast = point, true
	return nil
}

// trafficView requires App.mu and holds p.mu while copying immutable points.
// The result can be encoded without either mutex.
func trafficView(p *process, samples *trafficSamples, now time.Time) instanceTraffic {
	view := instanceTraffic{Reason: "not_running", Discontinuity: true, Curve: []TrafficPoint{}}
	if p == nil {
		return view
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	status := p.viewLocked()
	view.RunID = status.RunID
	if samples != nil && samples.runID != view.RunID {
		samples = nil
	}
	if samples != nil && samples.hasLast {
		at := samples.last.SampledAt
		view.SampledAt = &at
		if !at.After(now.Add(trafficFutureSkew)) {
			cutoff := now.Add(-trafficHistoryWindow)
			for _, point := range samples.points {
				if !point.SampledAt.Before(cutoff) {
					view.Curve = append(view.Curve, point)
				}
			}
		}
	}
	if status.State != "running" {
		return view
	}
	if samples == nil || !samples.hasLast {
		view.Reason = "not_sampled"
		return view
	}
	last := samples.last
	if last.SampledAt.After(now.Add(trafficFutureSkew)) {
		view.Reason = "clock_changed"
		return view
	}
	if !last.Available {
		view.Reason = "source_unavailable"
		return view
	}
	if now.Sub(last.SampledAt) > trafficFreshness {
		view.Reason = "stale"
		return view
	}
	view.Available = true
	view.Reason = ""
	view.ReceivedBytes, view.SentBytes = last.ReceivedBytes, last.SentBytes
	view.ReceivedBytesPerSecond, view.SentBytesPerSecond = last.ReceivedBytesPerSecond, last.SentBytesPerSecond
	view.Discontinuity, view.DiscontinuityReason = last.Discontinuity, last.DiscontinuityReason
	return view
}

// GET /api/traffic[?id=<instance ID>] is routed after authentication. Without
// id it returns all configured instances; an unknown id returns 404. This data
// is deliberately independent of the /api/state cache and its ETag.
func (a *App) trafficAPI(w http.ResponseWriter, r *http.Request) {
	ids, filtered := r.URL.Query()["id"]
	if filtered && (len(ids) != 1 || ids[0] == "") {
		fail(w, http.StatusBadRequest, fmt.Errorf("请提供一个实例 ID"))
		return
	}
	a.mu.Lock()
	now := time.Now().UTC()
	items := make(map[string]instanceTraffic)
	p := requestPrincipal(r)
	if filtered {
		if !p.canView(ids[0]) {
			a.mu.Unlock()
			fail(w, http.StatusForbidden, fmt.Errorf("没有此实例的查看权限"))
			return
		}
		if _, ok := a.store.DB.State.Instance(ids[0]); !ok {
			a.mu.Unlock()
			fail(w, http.StatusNotFound, fmt.Errorf("实例不存在"))
			return
		}
		items[ids[0]] = trafficView(a.procs[ids[0]], a.traffic[ids[0]], now)
	} else {
		for _, instance := range a.store.DB.State.Instances {
			if p.canView(instance.ID) {
				items[instance.ID] = trafficView(a.procs[instance.ID], a.traffic[instance.ID], now)
			}
		}
	}
	a.mu.Unlock()
	reply(w, http.StatusOK, map[string]any{
		"generatedAt":          now,
		"staleAfterSeconds":    int(trafficFreshness.Seconds()),
		"historyWindowSeconds": int(trafficHistoryWindow.Seconds()),
		"historyLimit":         trafficHistoryLimit,
		"instances":            items,
	})
}
