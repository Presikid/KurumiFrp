package manager

import (
	"fmt"
	"net/http"
	"reflect"
	"time"
)

// A schedule is workbench authority, not authority delegated by its creator.
// A fixed UTC offset avoids silently changing behavior when local DST changes.
type scheduleEvent struct {
	Day    int    `json:"day"` // Sunday is zero.
	Minute int    `json:"minute"`
	Action string `json:"action"`
}

type instanceSchedule struct {
	Enabled       bool            `json:"enabled"`
	Paused        bool            `json:"paused,omitempty"`
	Reason        string          `json:"reason,omitempty"`
	OffsetMinutes int             `json:"offsetMinutes"`
	Events        []scheduleEvent `json:"events"`
	Version       string          `json:"version"`
	LastMinute    int64           `json:"lastMinute,omitempty"`
	LastStatus    string          `json:"lastStatus,omitempty"`
}

func cloneSchedules(src map[string]instanceSchedule) map[string]instanceSchedule {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]instanceSchedule, len(src))
	for id, plan := range src {
		plan.Events = append([]scheduleEvent(nil), plan.Events...)
		dst[id] = plan
	}
	return dst
}

func schedulesForState(plans map[string]instanceSchedule, st State) map[string]instanceSchedule {
	result := cloneSchedules(plans)
	for id := range result {
		if _, ok := st.Instance(id); !ok {
			delete(result, id)
		}
	}
	return result
}

func validateSchedule(s instanceSchedule) error {
	if s.OffsetMinutes < -12*60 || s.OffsetMinutes > 14*60 || len(s.Events) > 64 || (s.Enabled && len(s.Events) == 0) {
		return fmt.Errorf("定时计划的时区偏移或事件数量无效")
	}
	seen := make(map[int]bool, len(s.Events))
	for _, e := range s.Events {
		key := e.Day*1440 + e.Minute
		if e.Day < 0 || e.Day > 6 || e.Minute < 0 || e.Minute >= 1440 || (e.Action != "start" && e.Action != "stop") || seen[key] {
			return fmt.Errorf("定时事件无效，或者同一时间有多个事件")
		}
		seen[key] = true
	}
	if len(s.Version) != 32 || s.LastMinute < 0 || s.LastMinute > int64(maxJSONRevision) || len(s.Reason) > 256 {
		return fmt.Errorf("定时计划状态无效")
	}
	for _, c := range s.Version {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return fmt.Errorf("定时计划版本无效")
		}
	}
	switch s.LastStatus {
	case "", "pending", "active", "done", "failed":
	default:
		return fmt.Errorf("定时计划执行状态无效")
	}
	if s.LastStatus == "failed" && !s.Paused {
		return fmt.Errorf("失败的定时计划必须暂停")
	}
	return nil
}

func validateSchedules(st State, plans map[string]instanceSchedule) error {
	if len(plans) > len(st.Instances) {
		return fmt.Errorf("定时计划引用了不存在的实例")
	}
	for id, plan := range plans {
		if _, ok := st.Instance(id); !ok {
			return fmt.Errorf("定时计划引用了不存在的实例")
		}
		if err := validateSchedule(plan); err != nil {
			return err
		}
	}
	return nil
}

func pauseSchedule(plan instanceSchedule, reason string) instanceSchedule {
	plan.Paused, plan.Reason, plan.Version = true, reason, randomHex(16)
	return plan
}

func tunnelsForSchedule(st State, id string) []Tunnel {
	var result []Tunnel
	for _, tunnel := range st.Tunnels {
		if tunnel.InstanceID == id {
			result = append(result, tunnel)
		}
	}
	return result
}

func scheduleDraftChanged(before, after State, id string) bool {
	old, oldOK := before.Instance(id)
	current, currentOK := after.Instance(id)
	return oldOK != currentOK || old != current || !reflect.DeepEqual(tunnelsForSchedule(before, id), tunnelsForSchedule(after, id))
}

// Keep draft changes and schedule suspension in the same atomic state write.
func schedulesAfterStateChange(plans map[string]instanceSchedule, before, after State, restoring bool) map[string]instanceSchedule {
	result := cloneSchedules(plans)
	for id, plan := range result {
		if _, ok := after.Instance(id); !ok {
			delete(result, id)
			continue
		}
		if plan.Enabled && (restoring || scheduleDraftChanged(before, after, id)) {
			result[id] = pauseSchedule(plan, "配置已变更，请检查后重新启用计划")
		}
	}
	return result
}

func pauseAllSchedules(plans map[string]instanceSchedule, reason string) map[string]instanceSchedule {
	result := cloneSchedules(plans)
	for id, plan := range result {
		if plan.Enabled {
			result[id] = pauseSchedule(plan, reason)
		}
	}
	return result
}

// Schedule execution metadata has its own version; it does not invalidate
// unrelated in-flight instance operations through the global draft revision.
func (a *App) writeScheduleLocked(id string, plan *instanceSchedule, bumpRevision bool) error {
	db := a.store.DB
	db.Schedules = cloneSchedules(db.Schedules)
	if plan == nil {
		delete(db.Schedules, id)
	} else {
		if db.Schedules == nil {
			db.Schedules = make(map[string]instanceSchedule)
		}
		db.Schedules[id] = *plan
	}
	if bumpRevision {
		if err := incrementRevision(&db); err != nil {
			return err
		}
	}
	if err := a.store.write(db); err != nil {
		return err
	}
	a.store.DB = db
	a.stateCache = nil
	return nil
}

func (a *App) pauseScheduleForManualLocked(id string) error {
	plan, ok := a.store.DB.Schedules[id]
	if !ok || !plan.Enabled || plan.Paused {
		return nil
	}
	plan = pauseSchedule(plan, "手动操作已暂停计划，请检查后重新启用")
	if err := a.writeScheduleLocked(id, &plan, false); err != nil {
		return fmt.Errorf("无法保存计划暂停状态，未执行手动操作")
	}
	return nil
}

func (a *App) pauseExitedScheduleLocked(id string, plan instanceSchedule) error {
	plan = pauseSchedule(plan, "计划实例意外退出，请核对后重新启用")
	plan.LastStatus = "failed"
	if err := a.writeScheduleLocked(id, &plan, false); err != nil {
		if a.scheduleBlocks == nil {
			a.scheduleBlocks = make(map[string]string)
		}
		a.scheduleBlocks[id] = "无法保存退出状态，计划在本次运行期间已暂停"
		a.stateCache = nil
		return err
	}
	return nil
}

// A restart after an admitted event cannot tell whether its side effect
// completed. Never replay it or advance the plan without an administrator.
func (a *App) recoverSchedules() error {
	db := a.store.DB
	db.Schedules = cloneSchedules(db.Schedules)
	changed := false
	for id, plan := range db.Schedules {
		if plan.LastStatus == "pending" || plan.LastStatus == "active" {
			plan = pauseSchedule(plan, "上次执行结果未知，请核对实例后重新启用")
			plan.LastStatus = "failed"
			db.Schedules[id] = plan
			changed = true
		}
	}
	if !changed {
		return nil
	}
	if err := a.store.write(db); err != nil {
		return fmt.Errorf("无法保存未确认的计划状态：%w", err)
	}
	a.store.DB = db
	return nil
}

func (a *App) scheduleAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Revision      uint64          `json:"revision"`
		ID            string          `json:"id"`
		Version       string          `json:"version"`
		Remove        bool            `json:"remove"`
		Enabled       bool            `json:"enabled"`
		Resume        bool            `json:"resume"`
		OffsetMinutes int             `json:"offsetMinutes"`
		Events        []scheduleEvent `json:"events"`
	}
	if err := decode(w, r, &req); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closing || req.Revision != a.store.DB.Revision {
		fail(w, http.StatusConflict, fmt.Errorf("工作台关闭中或配置已变化，请刷新"))
		return
	}
	if _, ok := a.store.DB.State.Instance(req.ID); !ok {
		fail(w, http.StatusBadRequest, fmt.Errorf("实例不存在"))
		return
	}
	old, exists := a.store.DB.Schedules[req.ID]
	if (exists && old.Version != req.Version) || (!exists && req.Version != "") || a.operations[req.ID] != nil {
		fail(w, http.StatusConflict, fmt.Errorf("计划已变化或实例操作正在进行，请刷新后重试"))
		return
	}
	if exists && old.Enabled && !old.Paused && old.LastStatus == "active" && !a.procs[req.ID].active() {
		if err := a.pauseExitedScheduleLocked(req.ID, old); err != nil {
			fail(w, http.StatusInternalServerError, fmt.Errorf("无法保存退出状态，请检查存储"))
		} else {
			fail(w, http.StatusConflict, fmt.Errorf("实例已退出，计划已暂停；请刷新并核对"))
		}
		return
	}
	if req.Remove {
		if !exists {
			fail(w, http.StatusBadRequest, fmt.Errorf("计划不存在"))
			return
		}
		if err := a.writeScheduleLocked(req.ID, nil, true); err != nil {
			fail(w, http.StatusInternalServerError, fmt.Errorf("无法删除定时计划"))
			return
		}
	} else {
		if req.Resume && !req.Enabled {
			fail(w, http.StatusBadRequest, fmt.Errorf("关闭计划时不能恢复执行"))
			return
		}
		minute := time.Now().Unix() / 60
		if old.LastMinute > minute {
			minute = old.LastMinute
		}
		plan := instanceSchedule{Enabled: req.Enabled, OffsetMinutes: req.OffsetMinutes, Events: append([]scheduleEvent(nil), req.Events...), Version: randomHex(16), LastMinute: minute}
		if req.Enabled && a.procs[req.ID].active() {
			plan.LastStatus = "active"
		}
		if req.Enabled && !req.Resume && (old.Paused || old.LastStatus == "pending" || a.scheduleBlocks[req.ID] != "") {
			plan.Paused, plan.Reason, plan.LastStatus = true, old.Reason, "failed"
			if plan.Reason == "" {
				plan.Reason = "上次执行结果未知，请核对实例后重新启用"
			}
		}
		if err := validateSchedule(plan); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		if err := a.writeScheduleLocked(req.ID, &plan, true); err != nil {
			fail(w, http.StatusInternalServerError, fmt.Errorf("无法保存定时计划"))
			return
		}
	}
	delete(a.scheduleBlocks, req.ID)
	reply(w, http.StatusOK, map[string]any{"ok": true, "revision": a.store.DB.Revision})
}

