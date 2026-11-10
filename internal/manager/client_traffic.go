package manager

import (
	"fmt"
	"net/http"
	"time"
)

// Names refer to applied frp rules, not the current, possibly changed draft.
// Counters use the client's payload boundary and are scoped to one run.
type ClientRuleTraffic struct {
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	ReceivedBytes uint64 `json:"receivedBytes,string"`
	SentBytes     uint64 `json:"sentBytes,string"`
}

type ClientRuleTrafficSnapshot struct {
	SampledAt time.Time
	Available bool
	Rules     []ClientRuleTraffic
}

func (a *App) clientRuleTrafficAPI(w http.ResponseWriter, r *http.Request) {
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
	if instance.Kind != "frpc" {
		a.mu.Unlock()
		fail(w, http.StatusBadRequest, fmt.Errorf("此接口仅适用于客户端实例"))
		return
	}
	proc := a.procs[id]
	var source RuntimeClientRuleTrafficReader
	if proc != nil {
		source, _ = proc.instance.(RuntimeClientRuleTrafficReader)
	}
	a.mu.Unlock()

	var snapshot ClientRuleTrafficSnapshot
	if source != nil {
		snapshot = source.ReadClientRuleTraffic()
	}
	a.mu.Lock()
	instance, exists = a.store.DB.State.Instance(id)
	if !exists || instance.Kind != "frpc" || a.procs[id] != proc {
		a.mu.Unlock()
		fail(w, http.StatusConflict, fmt.Errorf("实例或运行代次已变化，请刷新后重试"))
		return
	}
	status := RuntimeStatus{State: "stopped"}
	if proc != nil {
		status = proc.view()
	}
	a.mu.Unlock()

	view := map[string]any{
		"instanceId": id, "runId": status.RunID, "state": status.State,
		"available": false, "sampledAt": nil, "rules": []ClientRuleTraffic{},
	}
	if status.State == "running" && snapshot.Available {
		view["available"], view["sampledAt"], view["rules"] = true, snapshot.SampledAt, snapshot.Rules
	}
	reply(w, http.StatusOK, view)
}
