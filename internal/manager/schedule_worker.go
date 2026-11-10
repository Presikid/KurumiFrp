package manager

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

func scheduledAction(plan instanceSchedule, minute int64) string {
	local := time.Unix(minute*60, 0).UTC().Add(time.Duration(plan.OffsetMinutes) * time.Minute)
	for _, e := range plan.Events {
		if e.Day == int(local.Weekday()) && e.Minute == local.Hour()*60+local.Minute() {
			return e.Action
		}
	}
	return ""
}

func (a *App) scheduleLoop(ctx context.Context) {
	defer close(a.scheduleDone)
	select {
	case <-ctx.Done():
		return
	case <-a.startupDone:
	}
	// Do not replay an event that happened while the workbench was offline,
	// including an event at the start of the current minute.
	startedMinute := time.Now().Unix() / 60
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case tick := <-ticker.C:
			minute := tick.Unix() / 60
			if minute > startedMinute {
				a.scheduleTick(ctx, minute)
			}
		}
	}
}

func (a *App) scheduleTick(ctx context.Context, minute int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closing || ctx.Err() != nil || time.Now().Unix()/60 != minute {
		return
	}
	for id, plan := range a.store.DB.Schedules {
		if !plan.Enabled || plan.Paused || a.scheduleBlocks[id] != "" {
			continue
		}
		if plan.LastStatus == "active" && !a.procs[id].active() {
			_ = a.pauseExitedScheduleLocked(id, plan)
			continue
		}
		if minute <= plan.LastMinute {
			continue
		}
		action := scheduledAction(plan, minute)
		if action == "" {
			continue
		}
		if plan.LastStatus == "pending" {
			plan = pauseSchedule(plan, "上次定时操作尚未结束，本次未执行；请核对后重新启用")
			if err := a.writeScheduleLocked(id, &plan, false); err != nil {
				if a.scheduleBlocks == nil {
					a.scheduleBlocks = make(map[string]string)
				}
				a.scheduleBlocks[id] = "无法保存冲突状态，计划在本次运行期间已暂停"
				a.stateCache = nil
			}
			continue
		}
		wasActive := plan.LastStatus == "active"
		plan.LastMinute, plan.LastStatus = minute, "pending"
		if err := a.writeScheduleLocked(id, &plan, false); err != nil {
			if a.scheduleBlocks == nil {
				a.scheduleBlocks = make(map[string]string)
			}
			a.scheduleBlocks[id] = "无法保存执行记录，计划在本次运行期间已暂停"
			a.stateCache = nil
			continue
		}
		p := a.procs[id]
		if wasActive && action == "start" {
			if p.active() && p.view().State == "running" {
				a.finishScheduleLocked(id, plan, false, true)
			} else {
				a.finishScheduleLocked(id, plan, true, false)
			}
			continue
		}
		if p.active() && p.view().State != "running" {
			a.finishScheduleLocked(id, plan, true, false)
			continue
		}
		if (action == "start" && p.active()) || (action == "stop" && !p.active()) {
			a.finishScheduleLocked(id, plan, false, action == "start")
			continue
		}
		req := runtimeRequest{Revision: a.store.DB.Revision, ID: id, Action: action}
		snapshot, op, _, err := a.beginRuntimeOperationSourceLocked(ctx, req, false, false)
		if err != nil {
			a.finishScheduleLocked(id, plan, true, false)
			continue
		}
		op.scheduled = true
		snapshot.scheduleVersion, snapshot.scheduleMinute = plan.Version, minute
		go a.runScheduleAction(snapshot, op, action)
	}
}

func (a *App) finishScheduleLocked(id string, plan instanceSchedule, failed, active bool) {
	current, ok := a.store.DB.Schedules[id]
	if !ok || a.scheduleBlocks[id] != "" || current.Version != plan.Version || current.LastMinute != plan.LastMinute || current.LastStatus != "pending" {
		return
	}
	if failed {
		current = pauseSchedule(current, "计划操作失败或结果未知，请核对实例后重新启用")
		current.LastStatus = "failed"
	} else if active {
		current.LastStatus = "active"
	} else {
		current.LastStatus = "done"
	}
	if err := a.writeScheduleLocked(id, &current, false); err != nil {
		if a.scheduleBlocks == nil {
			a.scheduleBlocks = make(map[string]string)
		}
		a.scheduleBlocks[id] = "无法保存执行结果，请核对实例；重启后计划仍会暂停"
		a.stateCache = nil
	}
}

func (a *App) runScheduleAction(snapshot runtimeSnapshot, op *runtimeOperation, action string) {
	id := snapshot.instance.ID
	defer a.finishRuntimeOperation(id, op)
	var err error
	if action == "start" {
		err = a.restoreOne(snapshot, op)
		if err != nil && op.ctx.Err() == nil && a.webhooks != nil {
			a.webhooks.emitInstance("instance.start_failed", snapshot.instance.ID, snapshot.instance.Kind)
		}
		if err == nil {
			a.mu.Lock()
			p := a.procs[id]
			a.mu.Unlock()
			if p == nil {
				err = fmt.Errorf("未发现启动的实例")
			} else {
				go a.monitorScheduledProcess(id, p, snapshot.scheduleVersion)
			}
			if p != nil && p.readiness != nil {
				timer := time.NewTimer(30 * time.Second)
				select {
				case <-p.readiness.Ready():
					if !p.readiness.ReadyNow() || !p.active() {
						err = fmt.Errorf("实例未能完成启动")
					}
				case <-p.readiness.Done():
					err = fmt.Errorf("实例未能完成启动")
				case <-p.done:
					err = fmt.Errorf("实例在启动时退出")
				case <-timer.C:
					err = fmt.Errorf("实例启动状态未知")
				case <-op.ctx.Done():
					err = fmt.Errorf("实例启动状态未知")
				}
				timer.Stop()
			}
		}
	} else {
		a.mu.Lock()
		_, err = a.checkScheduledSnapshot(op.ctx, snapshot)
		a.mu.Unlock()
		if err == nil {
			err = stopProcess(snapshot.process)
		}
	}
	a.mu.Lock()
	plan, ok := a.store.DB.Schedules[id]
	if ok && plan.Version == snapshot.scheduleVersion {
		a.finishScheduleLocked(id, plan, err != nil, action == "start")
	}
	a.mu.Unlock()
}

// Called only after all managed processes stopped successfully. A persisted
// active marker otherwise makes the next startup pause after an unclean exit.
func (a *App) finishSchedulesAfterShutdown() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	db := a.store.DB
	db.Schedules = cloneSchedules(db.Schedules)
	changed := false
	for id, plan := range db.Schedules {
		if plan.LastStatus == "active" && a.scheduleBlocks[id] == "" {
			plan.LastStatus = "done"
			db.Schedules[id] = plan
			changed = true
		}
	}
	if !changed {
		return nil
	}
	if err := a.store.write(db); err != nil {
		return fmt.Errorf("无法保存计划停止状态：%w", err)
	}
	a.store.DB = db
	return nil
}

func (a *App) monitorScheduledProcess(id string, p *process, version string) {
	<-p.done
	p.mu.Lock()
	requested := p.stopRequested
	p.mu.Unlock()
	if requested {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	plan, ok := a.store.DB.Schedules[id]
	if a.closing || !ok || !plan.Enabled || plan.Paused || plan.Version != version || a.procs[id] != p {
		return
	}
	_ = a.pauseExitedScheduleLocked(id, plan)
}

// Requires a.mu. A delayed verifier must not launch outside its event minute.
func (a *App) checkScheduledSnapshot(ctx context.Context, snapshot runtimeSnapshot) (int, error) {
	plan, ok := a.store.DB.Schedules[snapshot.instance.ID]
	if a.closing || ctx.Err() != nil || !ok || !plan.Enabled || plan.Paused || plan.Version != snapshot.scheduleVersion || plan.LastStatus != "pending" || plan.LastMinute != snapshot.scheduleMinute || time.Now().Unix()/60 != snapshot.scheduleMinute || a.store.DB.Revision != snapshot.revision || a.procs[snapshot.instance.ID] != snapshot.process {
		return http.StatusConflict, fmt.Errorf("计划、配置、进程或执行时间已变化")
	}
	return http.StatusOK, nil
}
