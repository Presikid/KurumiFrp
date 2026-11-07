package manager

import (
	"fmt"
	"net/http"
	"strconv"
)

func (s *Store) exportState(state State, secrets bool) (State, error) {
	if !secrets { return state.public(), nil }
	state = state.clone()
	for i := range state.Instances {
		value, err := s.decrypt(state.Instances[i].Token)
		if err != nil { return State{}, err }
		state.Instances[i].Token = value
	}
	for i := range state.Tunnels {
		if state.Tunnels[i].Secret != "" {
			value, err := s.decrypt(state.Tunnels[i].Secret)
			if err != nil { return State{}, err }
			state.Tunnels[i].Secret = value
		}
	}
	return state, nil
}

func (a *App) export(w http.ResponseWriter, r *http.Request) {
	scope := r.URL.Query().Get("scope")
	secrets := r.URL.Query().Get("secrets") == "1"
	if scope != "all" && scope != "config" && scope != "snapshots" && scope != "snapshot" {
		fail(w, 400, fmt.Errorf("无效的导出范围")); return
	}
	result := map[string]any{"format": "kurumi-export-v1", "scope": scope, "revision": a.store.DB.Revision, "includesSecrets": secrets, "historyLimit": a.store.DB.HistoryLimit}
	if scope == "all" || scope == "config" {
		state, err := a.store.exportState(a.store.DB.State, secrets)
		if err != nil { fail(w, 500, err); return }
		result["config"] = state
	}
	if scope != "config" {
		var target uint64
		if scope == "snapshot" {
			var err error
			target, err = strconv.ParseUint(r.URL.Query().Get("revision"), 10, 64)
			if err != nil || target == 0 { fail(w, 400, fmt.Errorf("无效的快照版本")); return }
		}
		snapshots := []Snapshot{}
		for _, snap := range a.store.DB.History {
			if scope == "snapshot" && snap.Revision != target { continue }
			state, err := a.store.exportState(snap.State, secrets)
			if err != nil { fail(w, 500, err); return }
			snap.State = state
			snapshots = append(snapshots, snap)
		}
		if scope == "snapshot" && len(snapshots) == 0 { fail(w, 404, fmt.Errorf("快照不存在")); return }
		result["snapshots"] = snapshots
	}
	reply(w, 200, result)
}
