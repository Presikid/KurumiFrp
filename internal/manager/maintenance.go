package manager

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

// The setting is local to the workbench, outside portable configuration and
// backups. Its lock orders a completed update with later control admissions.
type maintenanceMode struct {
	mu      sync.RWMutex
	path    string
	enabled bool
	unknown bool
}

type maintenanceFile struct {
	Enabled *bool `json:"enabled"`
}

type maintenanceView struct {
	Enabled   bool `json:"enabled"`
	Available bool `json:"available"`
}

func loadMaintenanceMode(dir string) *maintenanceMode {
	m := &maintenanceMode{path: filepath.Join(dir, "maintenance.json")}
	m.readLocked()
	return m
}

// The caller holds mu or has not published m yet. Invalid files are retained.
func (m *maintenanceMode) readLocked() {
	data, err := privateFileLimit(m.path, 4096)
	if os.IsNotExist(err) {
		m.enabled, m.unknown = false, false
		return
	}
	var file maintenanceFile
	if err != nil || decodeStrictJSON(data, &file) != nil || file.Enabled == nil {
		m.unknown = true
		return
	}
	m.enabled, m.unknown = *file.Enabled, false
}

func (m *maintenanceMode) restrictsControls() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.enabled || m.unknown
}

func (m *maintenanceMode) view() maintenanceView {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.unknown {
		m.readLocked()
	}
	return maintenanceView{Enabled: m.enabled, Available: !m.unknown}
}

// Only admission is checked. Requests admitted before a change may finish.
// Disk updates happen before publication, and a stale administrator view cannot
// silently reverse a change made in another window.
func (m *maintenanceMode) set(expected, enabled bool) (maintenanceView, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.unknown {
		m.readLocked()
	}
	view := maintenanceView{Enabled: m.enabled, Available: !m.unknown}
	if m.unknown {
		return view, http.StatusServiceUnavailable, fmt.Errorf("维护设置无法读取，原文件已保留")
	}
	if expected != m.enabled {
		return view, http.StatusConflict, fmt.Errorf("维护状态已变化，请重新读取后操作")
	}
	if enabled == m.enabled {
		return view, http.StatusOK, nil
	}
	data := []byte(`{"enabled":false}`)
	if enabled {
		data = []byte(`{"enabled":true}`)
	}
	if _, err := writeIfChanged(m.path, data); err != nil {
		// A failed write can leave the on-disk result uncertain. Re-read it
		// without replacing a damaged file; later attempts can retry a read.
		m.readLocked()
		return maintenanceView{Enabled: m.enabled, Available: !m.unknown}, http.StatusInternalServerError, fmt.Errorf("维护设置保存失败，请重新读取并核对")
	}
	m.enabled = enabled
	return maintenanceView{Enabled: m.enabled, Available: true}, http.StatusOK, nil
}

func (a *App) maintenanceAPI(w http.ResponseWriter, r *http.Request) {
	if a.maintenance == nil {
		fail(w, http.StatusServiceUnavailable, fmt.Errorf("维护设置不可用"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		reply(w, http.StatusOK, a.maintenance.view())
	case http.MethodPost:
		var req struct {
			Expected *bool `json:"expected"`
			Enabled  *bool `json:"enabled"`
		}
		if err := decode(w, r, &req); err != nil || req.Expected == nil || req.Enabled == nil {
			fail(w, http.StatusBadRequest, fmt.Errorf("维护设置请求无效"))
			return
		}
		view, status, err := a.maintenance.set(*req.Expected, *req.Enabled)
		if err != nil {
			fail(w, status, err)
			return
		}
		reply(w, status, view)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
