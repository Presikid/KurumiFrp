package manager

import (
	"context"
	"fmt"
	"strings"
)

// restoreAutoStart uses the same lifecycle reservations and stale-config checks
// as an explicit start. Shutdown cancels and joins this worker before waiting
// for runtime operations, preventing Add/Wait races.
func (a *App) restoreAutoStart(ctx context.Context, instances []Instance) {
	defer func() {
		a.mu.Lock()
		a.autoStartSuppressed = nil
		close(a.startupDone)
		a.mu.Unlock()
	}()
	for _, i := range instances {
		if !i.AutoStart || ctx.Err() != nil {
			continue
		}
		a.mu.Lock()
		current, ok := a.store.DB.State.Instance(i.ID)
		if !ok || !current.AutoStart || a.autoStartSuppressed[i.ID] || a.closing || ctx.Err() != nil {
			a.mu.Unlock()
			continue
		}
		req := runtimeRequest{Revision: a.store.DB.Revision, ID: i.ID, Action: "start"}
		snapshot, op, _, err := a.beginRuntimeOperationLocked(ctx, req, false)
		if err != nil {
			a.recordAutoStartFailureLocked(ctx, req, snapshot, err)
			a.mu.Unlock()
			continue
		}
		a.mu.Unlock()

		if err = a.restoreOne(snapshot, op); err != nil {
			// Keep the reservation until the result is recorded. Otherwise a
			// later successful manual start could be overwritten by this error.
			a.mu.Lock()
			a.recordAutoStartFailureLocked(ctx, req, snapshot, err)
			a.mu.Unlock()
		}
		a.finishRuntimeOperation(i.ID, op)
	}
}

// recordAutoStartFailureLocked requires a.mu. Do not attribute an old
// verification error to a configuration that changed while the operation ran.
func (a *App) recordAutoStartFailureLocked(ctx context.Context, req runtimeRequest, snapshot runtimeSnapshot, err error) {
	latest, exists := a.store.DB.State.Instance(req.ID)
	p := a.procs[req.ID]
	if a.closing || ctx.Err() != nil || !exists || !latest.AutoStart || (p != nil && p.view().State == "running") {
		return
	}
	message := "请手动校验配置后重试"
	if a.store.DB.Revision != req.Revision {
		message = "自动恢复期间配置或核心已变化，请检查后手动启动"
	} else if len(err.Error()) > 4096 {
		message = "失败信息过长，已隐藏；请手动校验配置后重试"
	} else if safe := safeStatusText(err.Error(), snapshot.rendered.Secrets); safe != "" {
		message = strings.ToValidUTF8(safe, "")
	}
	if a.startupErrors == nil {
		a.startupErrors = make(map[string]string)
	}
	a.startupErrors[req.ID] = message
}

func (a *App) restoreOne(snapshot runtimeSnapshot, op *runtimeOperation) error {
	if _, err := a.verifyRuntime(op.ctx, snapshot.target, snapshot.instance, snapshot.rendered); err != nil {
		return fmt.Errorf("配置校验失败：%w", err)
	}
	a.mu.Lock()
	_, err := a.checkRuntimeSnapshot(op.ctx, snapshot)
	a.mu.Unlock()
	if err != nil {
		return err
	}
	runtimeRendered, private, err := a.preparePrivateStatus(op.ctx, snapshot.target, snapshot.instance, snapshot.rendered)
	if err != nil {
		return err
	}
	started := false
	defer func() {
		if !started {
			private.cleanup()
		}
	}()
	configPath, err := a.prepareRuntimeConfig(snapshot.instance.ID, runtimeRendered)
	if err != nil {
		return fmt.Errorf("无法写入运行配置：%w", err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	_, err = a.checkRuntimeSnapshot(op.ctx, snapshot)
	if err != nil {
		return err
	}
	err = a.startPrepared(snapshot.instance, runtimeRendered, snapshot.target, configPath, snapshot.coreID, private)
	started = err == nil
	return err
}
