package manager

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	trafficDailyDays       = 90
	trafficDailyMaxBytes   = 2 << 20
	trafficDailyFlushEvery = 30 * time.Second
)

// Values are only confirmed deltas. Partial means at least one interval in
// this UTC day could not be attributed; absent days do not mean zero traffic.
type trafficDay struct {
	Date          string `json:"date"`
	ReceivedBytes uint64 `json:"receivedBytes,string"`
	SentBytes     uint64 `json:"sentBytes,string"`
	Partial       bool   `json:"partial"`
}

type trafficDailySeries struct {
	Identity string       `json:"identity"`
	Kind     string       `json:"kind"`
	Days     []trafficDay `json:"days"`
}

type trafficDailyFile struct {
	Version int                  `json:"version"`
	SavedAt string               `json:"savedAt"`
	Series  []trafficDailySeries `json:"series"`
}

type trafficDailySample struct {
	At            time.Time
	RunID         string
	Available     bool
	Discontinuity bool
	Received      uint64
	Sent          uint64
	Epoch         uint64
	EpochKnown    bool
}

// All fields except path and channels are protected by App.mu. Disk writes
// are made by one worker after copying the data and releasing that mutex.
type trafficDailyHistory struct {
	path        string
	series      map[string]*trafficDailySeries
	last        map[string]trafficDailySample
	version     uint64
	persisted   uint64
	savedAt     string
	loadFailed  bool
	writeFailed bool
	stop        chan struct{}
	done        chan struct{}
}

func loadTrafficDaily(dir string, st State, ids map[string]string) *trafficDailyHistory {
	h := &trafficDailyHistory{
		path: filepath.Join(dir, "traffic-history.json"),
		series: make(map[string]*trafficDailySeries), last: make(map[string]trafficDailySample),
		stop: make(chan struct{}), done: make(chan struct{}),
	}
	data, err := privateFileLimit(h.path, trafficDailyMaxBytes)
	if os.IsNotExist(err) {
		return h
	}
	if err != nil {
		h.loadFailed = true
		return h
	}
	var file trafficDailyFile
	if decodeStrictJSON(data, &file) != nil || file.Version != 1 || len(file.Series) > 64 {
		h.loadFailed = true
		return h
	}
	if _, err := time.Parse(time.RFC3339, file.SavedAt); err != nil {
		h.loadFailed = true
		return h
	}
	allowed := make(map[string]string, len(st.Instances))
	for _, instance := range st.Instances {
		allowed[ids[instance.ID]] = instance.Kind
	}
	seen := make(map[string]bool, len(file.Series))
	loaded := make(map[string]*trafficDailySeries)
	for _, entry := range file.Series {
		if !validLocalTrafficID(entry.Identity) || seen[entry.Identity] ||
			(entry.Kind != "frpc" && entry.Kind != "frps") || len(entry.Days) > trafficDailyDays {
			h.loadFailed = true
			return h
		}
		seen[entry.Identity] = true
		last := ""
		for _, day := range entry.Days {
			parsed, err := time.Parse("2006-01-02", day.Date)
			if err != nil || parsed.Format("2006-01-02") != day.Date || day.Date <= last {
				h.loadFailed = true
				return h
			}
			last = day.Date
		}
		if allowed[entry.Identity] == entry.Kind {
			copy := entry
			copy.Days = append([]trafficDay(nil), entry.Days...)
			loaded[entry.Identity] = &copy
		}
	}
	h.series = loaded
	h.savedAt = file.SavedAt
	return h
}

func (h *trafficDailyHistory) prune(ids map[string]string, now time.Time) {
	allowed := make(map[string]bool, len(ids))
	for _, id := range ids {
		allowed[id] = true
	}
	cutoff := now.UTC().AddDate(0, 0, -(trafficDailyDays - 1)).Format("2006-01-02")
	for id, series := range h.series {
		if !allowed[id] {
			delete(h.series, id)
			delete(h.last, id)
			h.version++
			continue
		}
		first := 0
		for first < len(series.Days) && series.Days[first].Date < cutoff {
			first++
		}
		if excess := len(series.Days) - trafficDailyDays; excess > first {
			first = excess
		}
		if first > 0 {
			series.Days = append([]trafficDay(nil), series.Days[first:]...)
			h.version++
		}
	}
}

func (s *trafficDailySeries) day(date string) *trafficDay {
	index := sort.Search(len(s.Days), func(i int) bool { return s.Days[i].Date >= date })
	if index == len(s.Days) || s.Days[index].Date != date {
		s.Days = append(s.Days, trafficDay{})
		copy(s.Days[index+1:], s.Days[index:])
		s.Days[index] = trafficDay{Date: date, Partial: true}
	}
	return &s.Days[index]
}

func addConfirmed(a, b uint64) (uint64, bool) {
	if math.MaxUint64-a < b {
		return math.MaxUint64, false
	}
	return a + b, true
}

// observe admits only same-run, same-counter-epoch samples whose endpoints are
// available and no more than trafficFreshness apart. Cross-midnight deltas are
// not divided or attributed. The baseline itself is never persisted.
func (h *trafficDailyHistory) observe(identity, kind string, sample trafficDailySample, ids map[string]string, now time.Time) {
	if h == nil || !validLocalTrafficID(identity) || sample.At.IsZero() || sample.RunID == "" {
		return
	}
	at := sample.At.UTC()
	if at.After(now.Add(trafficFutureSkew)) || at.Before(now.Add(-trafficHistoryWindow)) {
		return
	}
	h.prune(ids, now)
	previous, hasPrevious := h.last[identity]
	if hasPrevious && !at.After(previous.At) && !previous.At.After(now.Add(trafficFutureSkew)) {
		// Concurrent final and periodic samples may arrive out of order. Do
		// not move the baseline backwards and count the same bytes twice.
		return
	}
	series := h.series[identity]
	if series == nil {
		series = &trafficDailySeries{Identity: identity, Kind: kind}
		h.series[identity] = series
	}
	date := at.Format("2006-01-02")
	current := series.day(date)
	valid := sample.Available && sample.EpochKnown && sample.Received != math.MaxUint64 && sample.Sent != math.MaxUint64
	continuous := valid && hasPrevious && previous.Available && previous.EpochKnown &&
		previous.RunID == sample.RunID && previous.Epoch == sample.Epoch && !sample.Discontinuity &&
		at.After(previous.At) && at.Sub(previous.At) <= trafficFreshness &&
		sample.Received >= previous.Received && sample.Sent >= previous.Sent
	if continuous && previous.At.UTC().Format("2006-01-02") == date {
		var okIn, okOut bool
		current.ReceivedBytes, okIn = addConfirmed(current.ReceivedBytes, sample.Received-previous.Received)
		current.SentBytes, okOut = addConfirmed(current.SentBytes, sample.Sent-previous.Sent)
		if !okIn || !okOut {
			current.Partial = true
		}
	} else {
		current.Partial = true
		if hasPrevious {
			series.day(previous.At.UTC().Format("2006-01-02")).Partial = true
		}
	}
	sample.At, sample.Available = at, valid
	h.last[identity] = sample
	h.version++
}

func (h *trafficDailyHistory) snapshot(ids map[string]string, now time.Time) trafficDailyFile {
	h.prune(ids, now)
	file := trafficDailyFile{Version: 1, SavedAt: now.UTC().Format(time.RFC3339), Series: []trafficDailySeries{}}
	for _, series := range h.series {
		entry := *series
		entry.Days = append([]trafficDay(nil), series.Days...)
		file.Series = append(file.Series, entry)
	}
	sort.Slice(file.Series, func(i, j int) bool { return file.Series[i].Identity < file.Series[j].Identity })
	return file
}

// The sole flusher performs JSON encoding and atomic disk replacement without
// App.mu. Failed writes retain dirty data for the next regular flush.
func (a *App) flushTrafficDaily() {
	a.mu.Lock()
	h := a.trafficDaily
	if h == nil || h.loadFailed || h.version == h.persisted {
		a.mu.Unlock()
		return
	}
	file := h.snapshot(a.store.DB.LocalTrafficIDs, time.Now())
	version := h.version
	a.mu.Unlock()
	data, err := json.Marshal(file)
	if err == nil && len(data) > trafficDailyMaxBytes {
		err = fmt.Errorf("traffic history exceeds its size limit")
	}
	if err == nil {
		_, err = writeIfChanged(h.path, data)
	}
	a.mu.Lock()
	h.writeFailed = err != nil
	if err == nil {
		h.persisted = version
		h.savedAt = file.SavedAt
	}
	a.mu.Unlock()
}

func (a *App) runTrafficDaily() {
	h := a.trafficDaily
	defer close(h.done)
	ticker := time.NewTicker(trafficDailyFlushEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			a.flushTrafficDaily()
		case <-h.stop:
			a.flushTrafficDaily()
			return
		}
	}
}

// Server snapshots have a different byte scope from client payload counters.
// Sample outside App.mu; a replaced or stopped generation is rejected below.
func (a *App) captureServerDailyTraffic(id, runID string, p *process, source RuntimeServerTrafficReader) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		a.recordServerDailyTraffic(id, runID, p, source.ReadServerTraffic())
		select {
		case <-p.done:
			return
		case <-ticker.C:
		}
	}
}

func (a *App) recordServerDailyTraffic(id, runID string, p *process, snapshot ServerTrafficSnapshot) {
	a.mu.Lock()
	defer a.mu.Unlock()
	instance, ok := a.store.DB.State.Instance(id)
	if !ok || instance.Kind != "frps" || a.procs[id] != p || a.trafficDaily == nil {
		return
	}
	p.mu.Lock()
	current := p.status.RunID == runID && (p.status.State == "running" || p.status.State == "stopping")
	p.mu.Unlock()
	if !current {
		return
	}
	at := snapshot.SampledAt
	if at.IsZero() {
		at = time.Now().UTC()
	}
	a.trafficDaily.observe(a.store.DB.LocalTrafficIDs[id], instance.Kind, trafficDailySample{
		At: at, RunID: runID, Available: snapshot.Available,
		Received: snapshot.ReceivedBytes, Sent: snapshot.SentBytes,
		Epoch: snapshot.ResetEpoch, EpochKnown: snapshot.EpochKnown,
	}, a.store.DB.LocalTrafficIDs, time.Now().UTC())
}

func (a *App) trafficDailyAPI(w http.ResponseWriter, r *http.Request) {
	ids := r.URL.Query()["id"]
	if len(ids) != 1 || !idPattern.MatchString(ids[0]) {
		fail(w, http.StatusBadRequest, fmt.Errorf("请提供一个有效实例 ID"))
		return
	}
	id := ids[0]
	if !requestPrincipal(r).canView(id) {
		fail(w, http.StatusForbidden, fmt.Errorf("没有此实例的查看权限"))
		return
	}
	a.mu.Lock()
	instance, exists := a.store.DB.State.Instance(id)
	if !exists {
		a.mu.Unlock()
		fail(w, http.StatusNotFound, fmt.Errorf("实例不存在"))
		return
	}
	h := a.trafficDaily
	identity := a.store.DB.LocalTrafficIDs[id]
	days := []trafficDay{}
	saved, savedAt, storageError := false, "", false
	if h != nil {
		h.prune(a.store.DB.LocalTrafficIDs, time.Now())
		if series := h.series[identity]; series != nil && series.Kind == instance.Kind {
			days = append(days, series.Days...)
		}
		saved, savedAt = !h.loadFailed && !h.writeFailed && h.version == h.persisted, h.savedAt
		storageError = h.loadFailed || h.writeFailed
	}
	a.mu.Unlock()
	reply(w, http.StatusOK, map[string]any{
		"instanceId": id, "kind": instance.Kind, "days": days,
		"retentionDays": trafficDailyDays, "saved": saved, "savedAt": savedAt,
		"storageError": storageError, "timeZone": "UTC", "flushSeconds": int(trafficDailyFlushEvery.Seconds()),
	})
}
