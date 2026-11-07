package manager

import (
	"fmt"
	"net/http"
)

// Network operations use the core manager's own lock and never block normal
// configuration editing for the duration of a download.
func (a *App) coreAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		switch r.URL.Path {
		case "/api/core":
			v, e := a.core.Status()
			if e != nil {
				fail(w, 500, e)
			} else {
				reply(w, 200, v)
			}
		case "/api/core/releases":
			v, e := a.core.Releases(r.Context())
			if e != nil {
				fail(w, 502, e)
			} else {
				reply(w, 200, v)
			}
		case "/api/core/release":
			v, e := a.core.Release(r.Context(), r.URL.Query().Get("tag"))
			if e != nil {
				fail(w, 502, e)
			} else {
				reply(w, 200, v)
			}
		default:
			fail(w, 404, fmt.Errorf("core endpoint not found"))
		}
		return
	}
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	var req struct {
		Tag      string `json:"tag"`
		Platform string `json:"platform"`
		ID       string `json:"id"`
	}
	if e := decode(w, r, &req); e != nil {
		fail(w, 400, e)
		return
	}
	switch r.URL.Path {
	case "/api/core/install":
		if e := a.core.Start(req.Tag, req.Platform); e != nil {
			fail(w, 409, e)
		} else {
			reply(w, 202, map[string]bool{"ok": true})
		}
	case "/api/core/cancel":
		a.core.Cancel()
		reply(w, 200, map[string]bool{"ok": true})
	case "/api/core/activate":
		a.mu.Lock()
		defer a.mu.Unlock()
		for _, p := range a.procs {
			if p.view().State == "running" {
				fail(w, 409, fmt.Errorf("先停止所有本机实例再切换核心；下载不会打断正在运行的连接"))
				return
			}
		}
		if e := a.core.Activate(r.Context(), req.ID); e != nil {
			fail(w, 400, e)
			return
		}
		reply(w, 200, map[string]bool{"ok": true})
	default:
		fail(w, 404, fmt.Errorf("core endpoint not found"))
	}
}
