package manager

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const maxLoginHistory = 300

type loginHistoryEntry struct {
	At      string `json:"at"`
	Account string `json:"account"`
	Peer    string `json:"peer"`
	Success bool   `json:"success"`
}

type loginHistoryFile struct {
	Version int                 `json:"version"`
	Entries []loginHistoryEntry `json:"entries"`
}

// Login history is local security data, not part of configuration or backups.
// A corrupt or unreadable file is never overwritten automatically.
type loginHistory struct {
	mu          sync.Mutex
	path        string
	entries     []loginHistoryEntry
	loadFailed  bool
	writeFailed bool
	lastFailure time.Time
	suppressed  uint64
}

func loadLoginHistory(dir string) *loginHistory {
	h := &loginHistory{path: filepath.Join(dir, "login-history.json")}
	data, err := privateFileLimit(h.path, 256<<10)
	if os.IsNotExist(err) {
		return h
	}
	if err != nil {
		h.loadFailed = true
		return h
	}
	var file loginHistoryFile
	if decodeStrictJSON(data, &file) != nil || file.Version != 1 || len(file.Entries) > maxLoginHistory {
		h.loadFailed = true
		return h
	}
	for _, entry := range file.Entries {
		_, err := time.Parse(time.RFC3339, entry.At)
		if err != nil || (entry.Account != "本机令牌" && entry.Account != "启动凭据" && entry.Account != "未知账户" && !accountNamePattern.MatchString(entry.Account)) {
			h.loadFailed = true
			return h
		}
		if entry.Peer != "未知" {
			ip, err := netip.ParseAddr(entry.Peer)
			if err != nil || ip.Zone() != "" || ip.Unmap().String() != entry.Peer {
				h.loadFailed = true
				return h
			}
		}
	}
	h.entries = file.Entries
	return h
}

func loginPeer(r *http.Request) string {
	peer := loginPeerIP(r.RemoteAddr)
	if peer == "" {
		return "未知"
	}
	return peer
}

// Never persist arbitrary user-supplied text from the username field: it may
// contain a mistakenly entered credential. Only registered names are recorded.
func (a *App) loginAccountLabel(name string) string {
	if name == "" {
		return "本机令牌"
	}
	if !accountNamePattern.MatchString(name) {
		return "未知账户"
	}
	a.auth.mu.Lock()
	defer a.auth.mu.Unlock()
	for _, account := range a.auth.accounts {
		if strings.EqualFold(account.Username, name) {
			return account.Username
		}
	}
	return "未知账户"
}

func (h *loginHistory) record(r *http.Request, account string, success bool) {
	if h == nil {
		return
	}
	if account == "" {
		account = "本机令牌"
	} else if account != "启动凭据" && account != "本机令牌" && account != "未知账户" && !accountNamePattern.MatchString(account) {
		account = "未知账户"
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.loadFailed {
		return
	}
	now := time.Now()
	// Avoid unbounded disk writes during repeated failed attempts. Successes are
	// always recorded; skipped failures are counted for this workbench run.
	if !success && !h.lastFailure.IsZero() && now.Sub(h.lastFailure) < time.Second && !now.Before(h.lastFailure) {
		h.suppressed++
		return
	}
	if !success {
		h.lastFailure = now
	}
	h.entries = append(h.entries, loginHistoryEntry{At: now.UTC().Format(time.RFC3339), Account: account, Peer: loginPeer(r), Success: success})
	if len(h.entries) > maxLoginHistory {
		h.entries = append([]loginHistoryEntry(nil), h.entries[len(h.entries)-maxLoginHistory:]...)
	}
	h.writeFailed = h.persistLocked() != nil
}

func (h *loginHistory) persistLocked() error {
	data, err := json.MarshalIndent(loginHistoryFile{Version: 1, Entries: h.entries}, "", "  ")
	if err != nil {
		return err
	}
	_, err = writeIfChanged(h.path, data)
	return err
}

func (h *loginHistory) snapshot() ([]loginHistoryEntry, bool, uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	entries := make([]loginHistoryEntry, len(h.entries))
	for i := range h.entries {
		entries[i] = h.entries[len(h.entries)-1-i]
	}
	return entries, !h.loadFailed && !h.writeFailed, h.suppressed
}

func (h *loginHistory) clear() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.loadFailed {
		return fmt.Errorf("登录记录文件无法读取，原文件已保留")
	}
	previous := h.entries
	h.entries = nil
	if err := h.persistLocked(); err != nil {
		h.entries = previous
		h.writeFailed = true
		return fmt.Errorf("无法保存登录记录")
	}
	h.writeFailed, h.suppressed = false, 0
	return nil
}

func (a *App) loginHistoryAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		entries, saved, suppressed := a.loginHistory.snapshot()
		reply(w, http.StatusOK, map[string]any{"entries": entries, "saved": saved, "suppressed": suppressed})
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Action string `json:"action"`
	}
	if err := decode(w, r, &req); err != nil || req.Action != "clear" {
		fail(w, http.StatusBadRequest, fmt.Errorf("登录记录操作无效"))
		return
	}
	if err := a.loginHistory.clear(); err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	reply(w, http.StatusOK, map[string]bool{"ok": true})
}
