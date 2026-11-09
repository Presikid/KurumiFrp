package manager

import (
	"context"
	"fmt"
	"net/http"
)

type runtimeOperation struct {
	ctx    context.Context
	cancel context.CancelFunc
}

type runtimeRequest struct {
	Revision uint64 `json:"revision"`
	ID       string `json:"id"`
	Action   string `json:"action"`
}

type runtimeSnapshot struct {
	instance Instance
	rendered Rendered
	binary   string
	coreID   string
	revision uint64
	process  *process
}

// Reserve one operation per instance. A duplicate request fails promptly instead
// of queuing an old start/restart behind a slow operation. Unrelated instances,
// state polling and draft edits remain available during verification and stop.
func (a *App) beginRuntimeOperation(parent context.Context, req runtimeRequest, validate bool) (runtimeSnapshot, *runtimeOperation, int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
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
	if a.operations[req.ID] != nil {
		return snapshot, nil, http.StatusConflict, fmt.Errorf("此实例正在执行其他操作，请稍后重试")
	}
	p := a.procs[req.ID]
	if !validate && req.Action == "start" && p != nil && p.view().State == "running" {
		return snapshot, nil, http.StatusBadRequest, fmt.Errorf("实例正在运行，请明确选择重启应用")
	}
	snapshot = runtimeSnapshot{instance: i, revision: req.Revision, coreID: a.activeCoreID(), process: p}
	if validate || req.Action != "stop" {
		var err error
		snapshot.rendered, err = a.store.render(req.ID, false)
		if err != nil {
			return snapshot, nil, http.StatusBadRequest, err
		}
		snapshot.binary, err = a.binary(i.Kind)
		if err != nil {
			return snapshot, nil, http.StatusBadRequest, err
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

// activeCoreID and checkRuntimeSnapshot are called with a.mu held.
func (a *App) activeCoreID() string {
	if a.store.DB.ActiveCore != nil {
		return *a.store.DB.ActiveCore
	}
	return ""
}

func (a *App) checkRuntimeSnapshot(ctx context.Context, snapshot runtimeSnapshot) (int, error) {
	if a.closing {
		return http.StatusServiceUnavailable, fmt.Errorf("工作台正在关闭，未启动新进程")
	}
	if ctx.Err() != nil {
		return http.StatusBadRequest, fmt.Errorf("操作已取消，未启动新进程")
	}
	if a.store.DB.Revision != snapshot.revision || a.activeCoreID() != snapshot.coreID || a.procs[snapshot.instance.ID] != snapshot.process {
		return http.StatusConflict, fmt.Errorf("操作期间配置、核心或进程已变化，请刷新后重试")
	}
	rendered, err := a.store.render(snapshot.instance.ID, false)
	if err != nil || rendered.Digest != snapshot.rendered.Digest {
		return http.StatusConflict, fmt.Errorf("操作期间实例配置已变化，请重新验证后应用")
	}
	binary, err := a.binary(snapshot.instance.Kind)
	if err != nil || binary != snapshot.binary {
		return http.StatusConflict, fmt.Errorf("操作期间核心已变化，请重新验证后应用")
	}
	return http.StatusOK, nil
}

func (a *App) runtimeAPI(w http.ResponseWriter, r *http.Request) {
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
	snapshot, op, status, err := a.beginRuntimeOperation(r.Context(), req, validate)
	if err != nil {
		fail(w, status, err)
		return
	}
	defer a.finishRuntimeOperation(req.ID, op)
	if op.ctx.Err() != nil {
		fail(w, http.StatusBadRequest, fmt.Errorf("操作已取消"))
		return
	}
	if !validate && req.Action == "stop" {
		if err = stopProcess(snapshot.process); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		a.mu.Lock()
		revision := a.store.DB.Revision
		a.mu.Unlock()
		reply(w, http.StatusOK, map[string]any{"ok": true, "revision": revision})
		return
	}

	output, err := a.verifyBinary(op.ctx, snapshot.binary, snapshot.rendered)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	// Do not stop a healthy process when its replacement became stale while the
	// external verifier was running.
	a.mu.Lock()
	status, err = a.checkRuntimeSnapshot(op.ctx, snapshot)
	a.mu.Unlock()
	if err != nil {
		fail(w, status, err)
		return
	}
	if validate {
		reply(w, http.StatusOK, map[string]any{"ok": true, "output": output, "note": "仅验证配置，不证明网络连通"})
		return
	}
	runtimeRendered, private, err := a.preparePrivateStatus(op.ctx, snapshot.binary, snapshot.instance, snapshot.rendered)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	started := false
	defer func() {
		if !started {
			private.cleanup()
		}
	}()
	// Status setup invokes a verifier outside the lock too. Recheck before
	// stopping the existing process if the draft/core changed during it.
	a.mu.Lock()
	status, err = a.checkRuntimeSnapshot(op.ctx, snapshot)
	a.mu.Unlock()
	if err != nil {
		fail(w, status, err)
		return
	}
	if req.Action == "restart" {
		if err = stopProcess(snapshot.process); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
	}
	// Verification deliberately uses an isolated staging file. Do not overwrite
	// the file of a running process before validation and an explicit restart.
	configPath, err := a.prepareRuntimeConfig(snapshot.instance.ID, runtimeRendered)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	// A draft/core switch or shutdown can also happen during the stop wait or
	// file preparation. Publish a process only after a final, locked recheck.
	a.mu.Lock()
	status, err = a.checkRuntimeSnapshot(op.ctx, snapshot)
	if err == nil {
		err = a.startPrepared(snapshot.instance, runtimeRendered, snapshot.binary, configPath, snapshot.coreID, private)
		if err != nil {
			status = http.StatusBadRequest
		} else {
			started = true
		}
	}
	revision := a.store.DB.Revision
	a.mu.Unlock()
	if err != nil {
		fail(w, status, err)
		return
	}
	reply(w, http.StatusOK, map[string]any{"ok": true, "revision": revision})
}
