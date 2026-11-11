package manager

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const trafficBudgetMaxBytes = 128 << 10

// A budget is a local sampling reminder and optional start check, not a quota.
// Limits are keyed by the sampled instance identity and never exported as config.
// All fields are protected by App.mu after initialization.
type trafficBudgetStore struct {
	path       string
	version    string
	limits     map[string]string
	blockStart map[string]bool
	loadFailed bool
}

type trafficBudgetFile struct {
	Format  int               `json:"format"`
	Version string            `json:"version"`
	Limits  map[string]string `json:"limits"`
	BlockStart map[string]bool `json:"blockStart,omitempty"`
}

func loadTrafficBudgets(dir string) *trafficBudgetStore {
	b := &trafficBudgetStore{
		path:   filepath.Join(dir, "traffic-budgets.json"),
		limits: make(map[string]string),
		blockStart: make(map[string]bool),
	}
	data, err := privateFileLimit(b.path, trafficBudgetMaxBytes)
	if os.IsNotExist(err) {
		return b
	}
	if err != nil {
		b.loadFailed = true
		return b
	}
	var file trafficBudgetFile
	if decodeStrictJSON(data, &file) != nil || file.Format != 1 ||
		!validLocalTrafficID(file.Version) || file.Limits == nil || len(file.Limits) > 64 {
		b.loadFailed = true
		return b
	}
	for identity, limit := range file.Limits {
		if !validLocalTrafficID(identity) || !validTrafficBudget(limit) || limit == "0" {
			b.loadFailed = true
			return b
		}
	}
	if len(file.BlockStart) > 64 {
		b.loadFailed = true
		return b
	}
	for identity, enabled := range file.BlockStart {
		if !validLocalTrafficID(identity) || !enabled || file.Limits[identity] == "" {
			b.loadFailed = true
			return b
		}
	}
	b.version, b.limits, b.blockStart = file.Version, file.Limits, file.BlockStart
	return b
}

// The API uses decimal strings so a browser does not round large byte values.
// Zero disables the reminder; values are unsigned, canonical decimal uint64s.
func validTrafficBudget(value string) bool {
	n, err := strconv.ParseUint(value, 10, 64)
	return err == nil && strconv.FormatUint(n, 10) == value
}

// save is called with App.mu held. Settings are small and only written when an
// administrator changes one; traffic sampling never writes this file.
func (b *trafficBudgetStore) save(identity, limit string, blockStart bool, liveIDs map[string]string) error {
	if b == nil || b.loadFailed || !validLocalTrafficID(identity) || !validTrafficBudget(limit) {
		return fmt.Errorf("流量提醒设置不可用")
	}
	live := make(map[string]bool, len(liveIDs))
	for _, id := range liveIDs {
		live[id] = true
	}
	if !live[identity] {
		return fmt.Errorf("实例已变化，请刷新")
	}
	limits := make(map[string]string, len(b.limits)+1)
	blocked := make(map[string]bool, len(b.blockStart)+1)
	for id, value := range b.limits {
		if live[id] {
			limits[id] = value
			if b.blockStart[id] {
				blocked[id] = true
			}
		}
	}
	if limit != "0" {
		limits[identity] = limit
		if blockStart {
			blocked[identity] = true
		} else {
			delete(blocked, identity)
		}
	} else {
		delete(limits, identity)
		delete(blocked, identity)
	}
	file := trafficBudgetFile{Format: 1, Version: randomHex(16), Limits: limits, BlockStart: blocked}
	data, err := json.Marshal(file)
	if err == nil && len(data) > trafficBudgetMaxBytes {
		err = fmt.Errorf("流量提醒设置过大")
	}
	if err == nil {
		_, err = writeIfChanged(b.path, data)
	}
	if err != nil {
		// A failed replacement may have an unknown result. Do not serve stale
		// settings or overwrite the existing file until the next startup.
		b.loadFailed = true
		return err
	}
	b.version, b.limits, b.blockStart = file.Version, file.Limits, file.BlockStart
	return nil
}

// trafficBudgetAPI is wired by the App after loadTrafficBudgets. GET requires
// instance-view and traffic-read access; POST is reserved for administrators.
// POST uses both the state revision and the settings version to reject stale
// edits. The settings version is empty until the first successful save.
func (a *App) trafficBudgetAPI(b *trafficBudgetStore, w http.ResponseWriter, r *http.Request) {
	principal := requestPrincipal(r)
	switch r.Method {
	case http.MethodGet:
		ids := r.URL.Query()["id"]
		if len(ids) != 1 || !idPattern.MatchString(ids[0]) {
			fail(w, http.StatusBadRequest, fmt.Errorf("请提供一个有效实例 ID"))
			return
		}
		if !principal.canReadTraffic() || !principal.canView(ids[0]) {
			fail(w, http.StatusForbidden, fmt.Errorf("没有此实例的流量查看权限"))
			return
		}
		a.mu.Lock()
		instance, exists := a.store.DB.State.Instance(ids[0])
		if !exists {
			a.mu.Unlock()
			fail(w, http.StatusNotFound, fmt.Errorf("实例不存在"))
			return
		}
		if b == nil || b.loadFailed {
			a.mu.Unlock()
			fail(w, http.StatusServiceUnavailable, fmt.Errorf("无法读取流量提醒设置，启动检查不会执行；请检查本机存储"))
			return
		}
		identity := a.store.DB.LocalTrafficIDs[instance.ID]
		if !validLocalTrafficID(identity) {
			a.mu.Unlock()
			fail(w, http.StatusServiceUnavailable, fmt.Errorf("实例流量身份不可用"))
			return
		}
		view := a.trafficBudgetView(b, instance, identity, time.Now().UTC())
		a.mu.Unlock()
		reply(w, http.StatusOK, view)
	case http.MethodPost:
		if !principal.admin() {
			fail(w, http.StatusForbidden, fmt.Errorf("仅管理员可修改流量提醒"))
			return
		}
		var req struct {
			ID         string `json:"id"`
			Revision   uint64 `json:"revision"`
			Version    string `json:"version"`
			LimitBytes string `json:"limitBytes"`
			BlockStart *bool  `json:"blockStart"`
		}
		if err := decode(w, r, &req); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		if !idPattern.MatchString(req.ID) || !validTrafficBudget(req.LimitBytes) || (req.BlockStart != nil && *req.BlockStart && req.LimitBytes == "0") {
			fail(w, http.StatusBadRequest, fmt.Errorf("请提供有效实例 ID 和十进制字节数"))
			return
		}
		a.mu.Lock()
		if a.closing || req.Revision != a.store.DB.Revision {
			a.mu.Unlock()
			fail(w, http.StatusConflict, fmt.Errorf("工作台关闭中或配置已变化，请刷新"))
			return
		}
		if b == nil || b.loadFailed {
			a.mu.Unlock()
			fail(w, http.StatusServiceUnavailable, fmt.Errorf("无法读取流量提醒设置，请检查本机存储"))
			return
		}
		if req.Version != b.version {
			a.mu.Unlock()
			fail(w, http.StatusConflict, fmt.Errorf("流量提醒设置已变化，请刷新"))
			return
		}
		instance, exists := a.store.DB.State.Instance(req.ID)
		if !exists {
			a.mu.Unlock()
			fail(w, http.StatusNotFound, fmt.Errorf("实例不存在"))
			return
		}
		if a.operations[instance.ID] != nil {
			a.mu.Unlock()
			fail(w, http.StatusConflict, fmt.Errorf("此实例正在执行操作，请稍后修改流量提醒"))
			return
		}
		identity := a.store.DB.LocalTrafficIDs[instance.ID]
		blockStart := b.blockStart[identity]
		if req.BlockStart != nil {
			blockStart = *req.BlockStart
		}
		if req.LimitBytes == "0" {
			blockStart = false
		}
		if err := b.save(identity, req.LimitBytes, blockStart, a.store.DB.LocalTrafficIDs); err != nil {
			a.mu.Unlock()
			fail(w, http.StatusInternalServerError, fmt.Errorf("无法保存流量提醒设置，请检查本机存储"))
			return
		}
		view := a.trafficBudgetView(b, instance, identity, time.Now().UTC())
		a.mu.Unlock()
		reply(w, http.StatusOK, view)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// trafficBudgetView is called under App.mu. Reached means the confirmed
// sampling deltas already reach the reminder. Not reached does not prove the
// real traffic is below it. Missing data is unknown, never an implicit zero.
func (a *App) trafficBudgetView(b *trafficBudgetStore, instance Instance, identity string, at time.Time) map[string]any {
	limit := b.limits[identity]
	if limit == "" {
		limit = "0"
	}
	month := at.UTC().Format("2006-01")
	var recorded any
	recordedDays := 0
	historySaved, historyStorageError := false, true
	h := a.trafficDaily
	if h != nil {
		if !h.loadFailed {
			h.prune(a.store.DB.LocalTrafficIDs, at)
			if series := h.series[identity]; series != nil && series.Kind == instance.Kind {
				for _, row := range trafficMonths(series.Days) {
					if row.Month == month {
						received, okIn := new(big.Int).SetString(row.ReceivedBytes, 10)
						sent, okOut := new(big.Int).SetString(row.SentBytes, 10)
						if okIn && okOut {
							recorded = new(big.Int).Add(received, sent).String()
							recordedDays = row.RecordedDays
						}
						break
					}
				}
			}
		}
		historySaved = !h.loadFailed && !h.writeFailed && h.version == h.persisted
		historyStorageError = h.loadFailed || h.writeFailed
	}
	status := "disabled"
	if limit != "0" {
		status = "unknown"
		if value, ok := recorded.(string); ok {
			amount, okAmount := new(big.Int).SetString(value, 10)
			threshold, okLimit := new(big.Int).SetString(limit, 10)
			if okAmount && okLimit {
				status = "not_reached"
				if amount.Cmp(threshold) >= 0 {
					status = "reached"
				}
			}
		}
	}
	return map[string]any{
		"instanceId": instance.ID, "kind": instance.Kind,
		"revision": a.store.DB.Revision, "version": b.version,
		"month": month, "limitBytes": limit, "blockStart": b.blockStart[identity], "recordedBytes": recorded,
		"recordedDays": recordedDays, "status": status, "partial": true,
		"historySaved": historySaved, "historyStorageError": historyStorageError,
	}
}

// The caller holds App.mu. Missing samples allow a start but cannot establish
// that actual traffic is below the threshold. An unreadable settings file
// cannot identify which instances opted in, so the check is skipped.
type trafficBudgetStartError string

func (e trafficBudgetStartError) Error() string { return string(e) }

func (a *App) checkTrafficBudgetStartLocked(instance Instance) error {
	b := a.trafficBudget
	if b == nil || b.loadFailed {
		return nil
	}
	identity := a.store.DB.LocalTrafficIDs[instance.ID]
	if !b.blockStart[identity] {
		return nil
	}
	h := a.trafficDaily
	if h == nil || h.loadFailed || h.writeFailed {
		return trafficBudgetStartError("已启用月度采样启动检查，但流量历史读取或写入失败；请检查存储或关闭此检查")
	}
	view := a.trafficBudgetView(b, instance, identity, time.Now().UTC())
	if view["status"] == "reached" {
		return trafficBudgetStartError("本月已记录的流量采样达到提醒值，启动检查阻止了本次启动或重启")
	}
	return nil
}
