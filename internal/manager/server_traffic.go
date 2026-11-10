package manager

import (
	"fmt"
	"net/http"
	"time"
)

// ServerTrafficProxy identifies counters collected for a proxy name and type.
// JSON uses decimal strings for bytes so JavaScript can preserve every uint64.
type ServerTrafficProxy struct {
	Name          string `json:"name"`
	Type          string `json:"type"`
	ReceivedBytes uint64 `json:"receivedBytes,string"`
	SentBytes     uint64 `json:"sentBytes,string"`
}

// ServerTrafficSnapshot is independent of the client's payload-only snapshot.
// Received means user towards proxy; sent means proxy towards user. The counts
// are taken at forwarding boundaries, may include framing or heartbeat bytes,
// and do not include peer-to-peer traffic that bypasses this server. HTTP sent
// bytes are read from a proxy work connection and may not reach the user. UDP
// counts recorded datagram contents, not every packet received by the server.
// The same observation supplies the totals and caller-owned proxy slice.
type ServerTrafficSnapshot struct {
	SampledAt        time.Time
	Available        bool
	ReceivedBytes    uint64
	SentBytes        uint64
	// A reset epoch identifies counter discontinuities even when totals grow.
	ResetEpoch       uint64
	EpochKnown       bool
	Proxies          []ServerTrafficProxy
	ProxiesTruncated bool
}

type serverTrafficView struct {
	InstanceID       string               `json:"instanceId"`
	RunID            string               `json:"runId"`
	State            string               `json:"state"`
	Available        bool                 `json:"available"`
	Reason           string               `json:"reason,omitempty"`
	SampledAt        *time.Time           `json:"sampledAt"`
	ReceivedBytes    *uint64              `json:"receivedBytes,string"`
	SentBytes        *uint64              `json:"sentBytes,string"`
	Proxies          []ServerTrafficProxy `json:"proxies"`
	ProxiesTruncated bool                 `json:"proxiesTruncated"`
	DetailsAllowed   bool                 `json:"detailsAllowed"`
}

// GET /api/server-traffic?id=<instance ID> is authenticated and requires the
// caller's view grant for that server. The retained runtime may continue to
// collect late bytes after stopping; a new generation replaces its counters.
func (a *App) serverTrafficAPI(w http.ResponseWriter, r *http.Request) {
	ids := r.URL.Query()["id"]
	if len(ids) != 1 || ids[0] == "" {
		fail(w, http.StatusBadRequest, fmt.Errorf("请提供一个实例 ID"))
		return
	}
	id := ids[0]
	principal := requestPrincipal(r)
	if !principal.canView(id) {
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
	if instance.Kind != "frps" {
		a.mu.Unlock()
		fail(w, http.StatusBadRequest, fmt.Errorf("此接口仅适用于服务端实例"))
		return
	}
	proc := a.procs[id]
	var source RuntimeServerTrafficReader
	if proc != nil {
		source, _ = proc.instance.(RuntimeServerTrafficReader)
	}
	a.mu.Unlock()

	// Do not hold App.mu while copying and sorting the service's proxy data.
	var snapshot ServerTrafficSnapshot
	if source != nil {
		snapshot = source.ReadServerTraffic()
	}

	// A start, delete, or state replacement may have occurred during the read.
	// Never attach an old generation's counters to a newly published runtime.
	a.mu.Lock()
	instance, exists = a.store.DB.State.Instance(id)
	if !exists || instance.Kind != "frps" || a.procs[id] != proc {
		a.mu.Unlock()
		fail(w, http.StatusConflict, fmt.Errorf("实例或运行代次已变化，请刷新后重试"))
		return
	}
	status := RuntimeStatus{State: "stopped"}
	if proc != nil {
		status = proc.view()
	}
	a.mu.Unlock()

	view := serverTrafficView{
		InstanceID: id, RunID: status.RunID, State: status.State,
		Reason: "source_unavailable", Proxies: []ServerTrafficProxy{}, DetailsAllowed: principal.canReadConfig(),
	}
	if proc == nil {
		view.Reason = "not_started"
	} else if snapshot.Available {
		view.Available, view.Reason = true, ""
		at, received, sent := snapshot.SampledAt, snapshot.ReceivedBytes, snapshot.SentBytes
		view.SampledAt, view.ReceivedBytes, view.SentBytes = &at, &received, &sent
		if view.DetailsAllowed {
			view.Proxies, view.ProxiesTruncated = snapshot.Proxies, snapshot.ProxiesTruncated
			if view.Proxies == nil {
				view.Proxies = []ServerTrafficProxy{}
			}
		}
	}
	reply(w, http.StatusOK, view)
}
