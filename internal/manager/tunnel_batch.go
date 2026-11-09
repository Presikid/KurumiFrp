package manager

import (
	"fmt"
	"net/http"
)

// batchTunnelEnabledAPI runs under the application lock. It changes only the
// requested draft flags in one validated commit and never controls a process.
func (a *App) batchTunnelEnabledAPI(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Revision uint64   `json:"revision"`
		IDs      []string `json:"ids"`
		Enabled  *bool    `json:"enabled"`
	}
	if err := decode(w, r, &req); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if req.Revision != a.store.DB.Revision {
		fail(w, http.StatusConflict, fmt.Errorf("配置已在其他窗口更新，请刷新后重新选择"))
		return
	}
	if req.Enabled == nil || len(req.IDs) == 0 || len(req.IDs) > 512 {
		fail(w, http.StatusBadRequest, fmt.Errorf("请指定启停意图和 1–512 条规则"))
		return
	}
	wanted := make(map[string]bool, len(req.IDs))
	for _, id := range req.IDs {
		if !idPattern.MatchString(id) || wanted[id] {
			fail(w, http.StatusBadRequest, fmt.Errorf("规则 ID 无效或重复"))
			return
		}
		wanted[id] = true
	}
	st := a.store.DB.State.clone()
	found, changed := 0, 0
	for i := range st.Tunnels {
		if !wanted[st.Tunnels[i].ID] {
			continue
		}
		found++
		if st.Tunnels[i].Enabled != *req.Enabled {
			st.Tunnels[i].Enabled = *req.Enabled
			changed++
		}
	}
	if found != len(wanted) {
		fail(w, http.StatusBadRequest, fmt.Errorf("部分规则已不存在，请刷新后重新选择"))
		return
	}
	if changed > 0 {
		action := "停用"
		if *req.Enabled {
			action = "启用"
		}
		if err := a.store.commit(st, fmt.Sprintf("批量%s %d 条规则草稿", action, changed)); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
	}
	reply(w, http.StatusOK, map[string]any{"ok": true, "revision": a.store.DB.Revision, "changed": changed})
}
