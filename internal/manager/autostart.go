package manager

import "context"

// restoreAutoStart uses the same lifecycle reservations and stale-config checks
// as an explicit start. Shutdown cancels and joins this worker before waiting
// for runtime operations, preventing Add/Wait races.
func (a *App) restoreAutoStart(ctx context.Context, instances []Instance) {
	defer close(a.startupDone)
	for _, i := range instances {
		if !i.AutoStart || ctx.Err() != nil {
			continue
		}
		a.mu.Lock()
		current, ok := a.store.DB.State.Instance(i.ID)
		revision := a.store.DB.Revision
		a.mu.Unlock()
		if !ok || !current.AutoStart {
			continue
		}
		req := runtimeRequest{Revision: revision, ID: i.ID, Action: "start"}
		snapshot, op, _, err := a.beginRuntimeOperation(ctx, req, false)
		if err == nil {
			err = a.restoreOne(snapshot, op)
			a.finishRuntimeOperation(i.ID, op)
		}
		if err != nil && ctx.Err() == nil {
			a.mu.Lock()
			latest, exists := a.store.DB.State.Instance(i.ID)
			p := a.procs[i.ID]
			if !a.closing && exists && latest.AutoStart && (p == nil || p.view().State != "running") {
				a.startupErrors[i.ID] = err.Error()
			}
			a.mu.Unlock()
		}
	}
}

func (a *App) restoreOne(snapshot runtimeSnapshot, op *runtimeOperation) error {
	if _, err := a.verifyBinary(op.ctx, snapshot.binary, snapshot.rendered); err != nil {
		return err
	}
	a.mu.Lock()
	_, err := a.checkRuntimeSnapshot(op.ctx, snapshot)
	a.mu.Unlock()
	if err != nil {
		return err
	}
	runtimeRendered, private, err := a.preparePrivateStatus(op.ctx, snapshot.binary, snapshot.instance, snapshot.rendered)
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
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	_, err = a.checkRuntimeSnapshot(op.ctx, snapshot)
	if err != nil {
		return err
	}
	err = a.startPrepared(snapshot.instance, runtimeRendered, snapshot.binary, configPath, snapshot.coreID, private)
	started = err == nil
	return err
}
