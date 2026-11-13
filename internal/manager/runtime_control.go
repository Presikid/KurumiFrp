package manager

import (
	"context"
	"fmt"
	"net/http"
)

type runtimeOperation struct {
	ctx       context.Context
	cancel    context.CancelFunc
	scheduled bool
}

type runtimeRequest struct {
	Revision      uint64 `json:"revision"`
	ID            string `json:"id"`
	Action        string `json:"action"`
	ExpectedRunID string `json:"expectedRunId,omitempty"`
}

type runtimeSnapshot struct {
	instance        Instance
	config          RuntimeConfig
	incarnation     string
	target          string
	revision        uint64
	process         *process
	scheduleVersion string
	scheduleMinute  int64
}

// A state replacement must not detach a live instance or reuse its ID for a
// different configuration. Call only with a.mu held.
func (a *App) canReplaceStateLocked() error {
	if a.closing {
		return fmt.Errorf("工作台正在关闭")
	}
	if a.startupDone != nil {
		select {
		case <-a.startupDone:
		default:
			return fmt.Errorf("请等待自动恢复结束后再导入或恢复配置")
		}
	}
	if len(a.operations) != 0 {
		return fmt.Errorf("请等待实例操作结束后再导入或恢复配置")
	}
	for _, p := range a.procs {
		if p == nil {
			continue
		}
		select {
		case <-p.done:
		default:
			return fmt.Errorf("请先停止全部运行实例后再导入或恢复配置")
		}
	}
	return nil
}

// Called only after a successful state replacement and canReplaceStateLocked.
// In-flight readers may temporarily hold their own references to old runs.
func (a *App) discardStoppedRuntimeLocked() {
	clear(a.procs)
	clear(a.traffic)
	clear(a.startupErrors)
	clear(a.nodeOperationVersions)
	clear(a.scheduleBlocks)
	for id := range a.generations {
		if _, exists := a.store.DB.State.Instance(id); !exists {
			delete(a.generations, id)
		}
	}
	a.stateCache = nil
}

// Reserve one operation per instance. A duplicate request fails promptly instead
// of queuing an old start/restart behind a slow operation. Unrelated instances,
// state polling and draft edits remain available during verification and stop.
func (a *App) beginRuntimeOperation(parent context.Context, req runtimeRequest, validate bool) (runtimeSnapshot, *runtimeOperation, int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.beginRuntimeOperationLocked(parent, req, validate)
}

// beginRuntimeOperationLocked requires a.mu. Automatic restoration uses it to
// reserve an operation and publish an immediate failure without a race against
// a concurrent manual operation.
func (a *App) beginRuntimeOperationLocked(parent context.Context, req runtimeRequest, validate bool) (runtimeSnapshot, *runtimeOperation, int, error) {
	return a.beginRuntimeOperationSourceLocked(parent, req, validate, true)
}

func (a *App) beginRuntimeOperationSourceLocked(parent context.Context, req runtimeRequest, validate, manual bool) (runtimeSnapshot, *runtimeOperation, int, error) {
	var snapshot runtimeSnapshot
	if a.closing {
		return snapshot, nil, http.StatusServiceUnavailable, fmt.Errorf("工作台正在关闭")
	}
	if req.Revision != a.store.DB.Revision {
		return snapshot, nil, http.StatusConflict, fmt.Errorf("配置已在其他窗口更新，请刷新后重试")
	}
	i, ok := a.store.DB.State.Instance(req.ID)
	if !ok {
		return snapshot, nil, http.StatusBadRequest, fmt.Errorf("实例不存在")
	}
	if !validate && req.Action != "start" && req.Action != "restart" && req.Action != "stop" {
		return snapshot, nil, http.StatusBadRequest, fmt.Errorf("未知操作")
	}
	if req.ExpectedRunID != "" && (validate || (req.Action != "restart" && req.Action != "stop")) {
		return snapshot, nil, http.StatusBadRequest, fmt.Errorf("运行代次条件仅适用于停止或重启")
	}
	if a.operations[req.ID] != nil {
		return snapshot, nil, http.StatusConflict, fmt.Errorf("此实例正在执行其他操作，请稍后重试")
	}
	p := a.procs[req.ID]
	if req.ExpectedRunID != "" && !p.runningAs(req.ExpectedRunID) {
		return snapshot, nil, http.StatusConflict, fmt.Errorf("实例运行状态或代次已变化，未停止目标运行代次")
	}
	if !validate && req.Action == "start" && p.active() {
		return snapshot, nil, http.StatusBadRequest, fmt.Errorf("实例正在运行，请明确选择重启应用")
	}
	snapshot = runtimeSnapshot{instance: i, incarnation: a.store.DB.LocalTrafficIDs[i.ID], revision: req.Revision, process: p}
	if a.nodeOperationVersions == nil {
		a.nodeOperationVersions = make(map[string]string)
	}
	// Consume older control previews even if rendering or core resolution fails.
	a.nodeOperationVersions[req.ID] = randomHex(16)
	if !validate && (req.Action == "start" || req.Action == "restart") {
		if err := a.checkTrafficBudgetStartLocked(i); err != nil {
			return snapshot, nil, http.StatusConflict, err
		}
	}
	if !validate && req.Action == "stop" && a.startupDone != nil {
		select {
		case <-a.startupDone:
		default:
			if a.autoStartSuppressed == nil {
				a.autoStartSuppressed = make(map[string]bool)
			}
			a.autoStartSuppressed[req.ID] = true
		}
		delete(a.startupErrors, req.ID)
	}
	if validate || req.Action != "stop" {
		var err error
		rendered, err := a.store.render(req.ID, false)
		if err != nil {
			return snapshot, nil, http.StatusBadRequest, fmt.Errorf("无法生成实例配置：%w", err)
		}
		snapshot.config, err = a.runtimeConfigLocked(i, rendered, snapshot.incarnation)
		if err != nil {
			return snapshot, nil, http.StatusBadRequest, err
		}
		snapshot.target, err = a.resolveRuntime(i.Kind)
		if err != nil {
			return snapshot, nil, http.StatusBadRequest, fmt.Errorf("无法使用所选 frp 核心：%w", err)
		}
	}
	if manual && !validate {
		if err := a.pauseScheduleForManualLocked(req.ID); err != nil {
			return snapshot, nil, http.StatusInternalServerError, err
		}
	}
	ctx, cancel := context.WithCancel(parent)
	op := &runtimeOperation{ctx: ctx, cancel: cancel}
	if a.operations == nil {
		a.operations = make(map[string]*runtimeOperation)
	}
	a.operations[req.ID] = op
	// Close sets closing under the same mutex before waiting, so no Add can race
	// with Wait after the operation count has fallen to zero.
	a.operationWG.Add(1)
	return snapshot, op, http.StatusOK, nil
}

func (a *App) finishRuntimeOperation(id string, op *runtimeOperation) {
	op.cancel()
	a.mu.Lock()
	if a.operations[id] == op {
		delete(a.operations, id)
	}
	a.mu.Unlock()
	a.operationWG.Done()
}

// checkRuntimeSnapshot is called with a.mu held.
func (a *App) checkRuntimeSnapshot(ctx context.Context, snapshot runtimeSnapshot) (int, error) {
	if snapshot.scheduleVersion != "" {
		if status, err := a.checkScheduledSnapshot(ctx, snapshot); err != nil {
			return status, err
		}
	}
	if a.closing {
		return http.StatusServiceUnavailable, fmt.Errorf("工作台正在关闭，未启动新进程")
	}
	if ctx.Err() != nil {
		return http.StatusBadRequest, fmt.Errorf("操作已取消，未启动新进程")
	}
	if a.store.DB.Revision != snapshot.revision || a.procs[snapshot.instance.ID] != snapshot.process {
		return http.StatusConflict, fmt.Errorf("操作期间配置或进程已变化，请刷新后重试")
	}
	if err := a.checkManagedAccountBackend(snapshot.instance, snapshot.config); err != nil {
		return http.StatusConflict, err
	}
	current, ok := a.store.DB.State.Instance(snapshot.instance.ID)
	if !ok || current.Kind != snapshot.instance.Kind || current.ManagedAccountLogin != snapshot.config.ManagedAccountLogin {
		return http.StatusConflict, fmt.Errorf("操作期间账户认证配置已变化，请刷新后重试")
	}
	rendered, err := a.store.render(snapshot.instance.ID, false)
	if err != nil || rendered.Digest != snapshot.config.Rendered.Digest {
		return http.StatusConflict, fmt.Errorf("操作期间实例配置已变化，请重新验证后应用")
	}
	target, err := a.resolveRuntime(snapshot.instance.Kind)
	if err != nil || target != snapshot.target {
		return http.StatusConflict, fmt.Errorf("操作期间核心已变化，请重新验证后应用")
	}
	return http.StatusOK, nil
}

func (a *App) runtimeAPI(w http.ResponseWriter, r *http.Request) {
	p := requestPrincipal(r)
	// Backend errors and verifier output can contain arbitrary local details.
	failure := func(status int, err error) {
		if !p.admin() {
			err = fmt.Errorf("实例操作未完成，请联系管理员查看详情")
		}
		fail(w, status, err)
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req runtimeRequest
	if err := decode(w, r, &req); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	validate := r.URL.Path == "/api/validate"
	action := req.Action
	if validate {
		action = "validate"
	}
	if !p.canOperate(req.ID) || !p.canPerformAction(action) {
		fail(w, http.StatusForbidden, fmt.Errorf("没有此实例的操作权限"))
		return
	}
	snapshot, op, status, err := a.beginRuntimeOperation(r.Context(), req, validate)
	if err != nil {
		if !validate && (req.Action == "start" || req.Action == "restart") && a.webhooks != nil {
			a.webhooks.emitInstance("instance.start_failed", snapshot.instance.ID, snapshot.instance.Kind)
		}
		failure(status, err)
		return
	}
	a.runRuntimeOperation(w, req, validate, p, snapshot, op)
}

// The caller has already authenticated and reserved the operation. This is
// shared by local and narrowly authorized remote control requests.
func (a *App) runRuntimeOperation(w http.ResponseWriter, req runtimeRequest, validate bool, p principal, snapshot runtimeSnapshot, op *runtimeOperation) {
	failure := func(status int, err error) {
		if !p.admin() {
			err = fmt.Errorf("实例操作未完成，请联系管理员查看详情")
		}
		fail(w, status, err)
	}
	var err error
	var status int
	defer a.finishRuntimeOperation(req.ID, op)
	started := false
	defer func() {
		if !validate && req.Action != "stop" && !started && op.ctx.Err() == nil && a.webhooks != nil {
			a.webhooks.emitInstance("instance.start_failed", snapshot.instance.ID, snapshot.instance.Kind)
		}
	}()
	if op.ctx.Err() != nil {
		fail(w, http.StatusBadRequest, fmt.Errorf("操作已取消"))
		return
	}
	if !validate && req.Action == "stop" {
		if err = stopProcessGuarded(snapshot.process, req.ExpectedRunID); err != nil {
			failure(http.StatusBadRequest, err)
			return
		}
		a.mu.Lock()
		revision := a.store.DB.Revision
		a.mu.Unlock()
		reply(w, http.StatusOK, map[string]any{"ok": true, "revision": revision})
		return
	}

	output, err := a.verifyRuntimeConfig(op.ctx, snapshot.target, snapshot.instance, snapshot.config)
	if err != nil {
		failure(http.StatusBadRequest, err)
		return
	}
	// Do not stop a healthy instance when its replacement became stale during verification.
	a.mu.Lock()
	status, err = a.checkRuntimeSnapshot(op.ctx, snapshot)
	if err == nil && !validate && req.Action == "restart" {
		if err = a.checkTrafficBudgetStartLocked(snapshot.instance); err != nil {
			status = http.StatusConflict
		}
	}
	a.mu.Unlock()
	if err != nil {
		failure(status, err)
		return
	}
	if validate {
		if !p.admin() {
			output = ""
		}
		reply(w, http.StatusOK, map[string]any{"ok": true, "output": output, "note": "仅验证配置，不证明网络连通"})
		return
	}
	if req.Action == "restart" {
		if err = stopProcessGuarded(snapshot.process, req.ExpectedRunID); err != nil {
			failure(http.StatusBadRequest, err)
			return
		}
	}
	// A draft change or shutdown can happen during the stop wait. Publish a
	// process only after a final, locked recheck.
	a.mu.Lock()
	status, err = a.checkRuntimeSnapshot(op.ctx, snapshot)
	if err == nil {
		err = a.startPreparedConfigWithSource(snapshot.instance, snapshot.config, snapshot.target, false)
		if err != nil {
			status = http.StatusBadRequest
			if _, ok := err.(trafficBudgetStartError); ok {
				status = http.StatusConflict
			}
		} else {
			started = true
		}
	}
	revision := a.store.DB.Revision
	a.mu.Unlock()
	if err != nil {
		if req.Action == "restart" {
			err = fmt.Errorf("重启的旧实例已停止或此前未运行，替代实例未启动：%w", err)
		}
		failure(status, err)
		return
	}
	reply(w, http.StatusOK, map[string]any{"ok": true, "revision": revision})
}
