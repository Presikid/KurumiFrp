package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	logHistoryFileLimit   = 1 << 20
	logHistoryLineLimit   = 32 << 10
	logHistoryRecordLimit = 256 << 10
	logHistoryQueueLimit  = 256
)

type logHistoryConfig struct {
	Version int  `json:"version"`
	Enabled bool `json:"enabled"`
}

type logHistoryEntry struct {
	Version    int    `json:"version"`
	InstanceID string `json:"instanceId"`
	Binding    string `json:"binding"`
	RunID      string `json:"runId"`
	At         string `json:"at"`
	Line       string `json:"line"`
	epoch      uint64
}

type logHistoryStatus struct {
	Enabled       bool   `json:"enabled"`
	Available     bool   `json:"available"`
	SettingsError bool   `json:"settingsError"`
	Dropped       string `json:"dropped"`
}

type logHistoryCommand struct {
	action     string
	instanceID string
	binding    string
	response   chan logHistoryResult
}

type logHistoryResult struct {
	status logHistoryStatus
	lines  []string
	err    error
}

// One worker owns the files and serializes settings, reads, rotation and clear.
// Runtime writers only make a nonblocking enqueue of already-redacted lines.
type logHistory struct {
	settingsPath string
	currentPath  string
	oldPath      string
	entries      chan logHistoryEntry
	commands     chan logHistoryCommand
	stop         chan struct{}
	done         chan struct{}

	admitMu  sync.RWMutex
	stopping bool
	enabled  atomic.Bool
	epoch    atomic.Uint64
	dropped  atomic.Uint64

	// Only the worker accesses these fields after construction.
	settingsError      bool
	settingsWriteError bool
	writeError         bool
	readError          bool
	clearError         bool
	file               *os.File
	size               int64

	// Written by the worker before done is closed; read only after it closes.
	stopErr error
}

func newLogHistory(dir string) *logHistory {
	h := &logHistory{
		settingsPath: filepath.Join(dir, "runtime-log-history-settings.json"),
		currentPath:  filepath.Join(dir, "runtime-log-history.jsonl"),
		oldPath:      filepath.Join(dir, "runtime-log-history.old.jsonl"),
		entries:      make(chan logHistoryEntry, logHistoryQueueLimit),
		commands:     make(chan logHistoryCommand),
		stop:         make(chan struct{}),
		done:         make(chan struct{}),
	}
	h.epoch.Store(1)
	data, err := privateFileLimit(h.settingsPath, 1024)
	if err == nil {
		var cfg struct {
			Version int   `json:"version"`
			Enabled *bool `json:"enabled"`
		}
		if decodeStrictJSON(data, &cfg) != nil || cfg.Version != 1 || cfg.Enabled == nil {
			h.settingsError = true
		} else {
			h.enabled.Store(*cfg.Enabled)
		}
	} else if !os.IsNotExist(err) {
		h.settingsError = true
	}
	go h.run()
	return h
}

func (h *logHistory) enqueue(instanceID, binding, runID, line string) {
	if h == nil || !h.enabled.Load() {
		return
	}
	line = strings.ToValidUTF8(line, "\uFFFD")
	if len(line) > logHistoryLineLimit {
		line = "[oversized log line omitted]"
	}
	h.admitMu.RLock()
	defer h.admitMu.RUnlock()
	if h.stopping || !h.enabled.Load() {
		return
	}
	entry := logHistoryEntry{
		Version: 1, InstanceID: instanceID, Binding: binding, RunID: runID,
		At: time.Now().UTC().Format(time.RFC3339Nano), Line: line, epoch: h.epoch.Load(),
	}
	select {
	case h.entries <- entry:
	default:
		h.dropped.Add(1)
	}
}

func (h *logHistory) status() logHistoryStatus {
	return logHistoryStatus{
		Enabled: h.enabled.Load(), Available: !h.settingsError && !h.settingsWriteError && !h.writeError && !h.readError && !h.clearError,
		SettingsError: h.settingsError, Dropped: strconv.FormatUint(h.dropped.Load(), 10),
	}
}

func (h *logHistory) request(ctx context.Context, action, instanceID, binding string) (logHistoryResult, error) {
	if h == nil {
		return logHistoryResult{}, fmt.Errorf("日志历史不可用")
	}
	response := make(chan logHistoryResult, 1)
	cmd := logHistoryCommand{action: action, instanceID: instanceID, binding: binding, response: response}
	select {
	case h.commands <- cmd:
	case <-ctx.Done():
		return logHistoryResult{}, ctx.Err()
	case <-h.done:
		return logHistoryResult{}, fmt.Errorf("日志历史已关闭")
	}
	select {
	case result := <-response:
		return result, result.err
	case <-ctx.Done():
		return logHistoryResult{}, ctx.Err()
	case <-h.done:
		select {
		case result := <-response:
			return result, result.err
		default:
			return logHistoryResult{}, fmt.Errorf("日志历史已关闭")
		}
	}
}

func (h *logHistory) shutdown(ctx context.Context) error {
	if h == nil {
		return nil
	}
	h.admitMu.Lock()
	if !h.stopping {
		h.stopping = true
		close(h.stop)
	}
	h.admitMu.Unlock()
	select {
	case <-h.done:
		return h.stopErr
	case <-ctx.Done():
		select {
		case <-h.done:
			return h.stopErr
		default:
			return ctx.Err()
		}
	}
}

func (h *logHistory) run() {
	defer close(h.done)
	for {
		// The persistent signal survives a timed-out shutdown caller and takes
		// priority over later requests once the worker returns from file I/O.
		select {
		case <-h.stop:
			h.finish()
			return
		default:
		}
		// Give management and reads a chance even during continuous output.
		select {
		case <-h.stop:
			h.finish()
			return
		case cmd := <-h.commands:
			h.handleCommand(cmd)
			continue
		default:
		}
		select {
		case <-h.stop:
			h.finish()
			return
		case cmd := <-h.commands:
			h.handleCommand(cmd)
		case entry := <-h.entries:
			h.writeEntry(entry)
		}
	}
}

func (h *logHistory) finish() {
	// Shutdown stops admission before signalling, so this drain is bounded.
	for {
		select {
		case entry := <-h.entries:
			h.writeEntry(entry)
		default:
			h.stopErr = h.closeFile()
			return
		}
	}
}

func (h *logHistory) handleCommand(cmd logHistoryCommand) {
	result := logHistoryResult{}
	switch cmd.action {
	case "status":
	case "enable", "disable":
		result.err = h.setEnabled(cmd.action == "enable")
	case "clear":
		result.err = h.clear()
	case "read":
		result.lines, result.err = h.read(cmd.instanceID, cmd.binding)
	default:
		result.err = fmt.Errorf("日志历史操作无效")
	}
	result.status = h.status()
	cmd.response <- result
}

func (h *logHistory) setEnabled(enabled bool) error {
	if h.settingsError {
		return fmt.Errorf("日志历史设置文件无法读取，原文件已保留")
	}
	wasEnabled := h.enabled.Load()
	if wasEnabled == enabled && !h.settingsWriteError {
		return nil
	}
	data, err := json.Marshal(logHistoryConfig{Version: 1, Enabled: enabled})
	if err == nil {
		_, err = writeIfChanged(h.settingsPath, data)
	}
	if err != nil {
		h.settingsWriteError = true
		return fmt.Errorf("无法保存日志历史设置")
	}
	h.settingsWriteError = false
	if wasEnabled == enabled {
		return nil
	}
	if enabled {
		h.admitMu.Lock()
		h.epoch.Add(1)
		h.enabled.Store(true)
		h.admitMu.Unlock()
		return nil
	}
	// Prevent new admissions, then finish entries already accepted. Neither
	// disabling nor a failed write removes previously saved history.
	h.admitMu.Lock()
	h.enabled.Store(false)
	h.admitMu.Unlock()
	for {
		select {
		case entry := <-h.entries:
			h.writeEntry(entry)
		default:
			h.epoch.Add(1)
			return nil
		}
	}
}

func (h *logHistory) closeFile() error {
	var err error
	if h.file != nil {
		if err = h.file.Close(); err != nil {
			h.writeError = true
		}
		h.file = nil
	}
	h.size = 0
	return err
}

// Files are fixed names inside the private data directory. Refuse symlinks,
// non-regular files and files exceeding the configured limit.
func logHistoryReadFile(path string) ([]byte, error) {
	data, err := privateFileLimit(path, logHistoryFileLimit)
	if os.IsNotExist(err) {
		return nil, nil
	}
	return data, err
}

func logHistoryCompletePrefix(data []byte) ([]byte, bool) {
	if len(data) == 0 || data[len(data)-1] == '\n' {
		return data, false
	}
	last := bytes.LastIndexByte(data, '\n')
	return data[:last+1], true
}

func logHistoryEntries(data []byte, instanceID, binding string) ([]string, error) {
	lines := []string{}
	for _, raw := range bytes.Split(data, []byte{'\n'}) {
		if len(raw) == 0 {
			continue
		}
		var entry logHistoryEntry
		if len(raw) > logHistoryRecordLimit || decodeStrictJSON(raw, &entry) != nil ||
			entry.Version != 1 || !idPattern.MatchString(entry.InstanceID) ||
			!nodeTokenPattern.MatchString(entry.Binding) || !idPattern.MatchString(entry.RunID) ||
			len(entry.Line) > logHistoryLineLimit {
			return nil, fmt.Errorf("日志历史格式无效")
		}
		if _, err := time.Parse(time.RFC3339Nano, entry.At); err != nil {
			return nil, fmt.Errorf("日志历史格式无效")
		}
		if entry.InstanceID == instanceID && entry.Binding == binding {
			lines = append(lines, "["+entry.At+" · "+entry.RunID+"] "+entry.Line)
		}
	}
	return lines, nil
}

func (h *logHistory) openCurrent() error {
	if h.file != nil {
		return nil
	}
	data, err := logHistoryReadFile(h.currentPath)
	if err != nil {
		return err
	}
	complete, partial := logHistoryCompletePrefix(data)
	if _, err = logHistoryEntries(complete, "", ""); err != nil {
		return err
	}
	f, err := os.OpenFile(h.currentPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	if err = f.Chmod(0600); err == nil && partial {
		err = f.Truncate(int64(len(complete)))
	}
	if err == nil {
		var end int64
		end, err = f.Seek(0, io.SeekEnd)
		if err == nil && end != int64(len(complete)) {
			err = fmt.Errorf("日志历史文件已变化")
		}
	}
	if err != nil {
		_ = f.Close()
		return err
	}
	h.file, h.size = f, int64(len(complete))
	return nil
}

func logHistoryRemoveFile(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("日志历史路径不是普通文件")
	}
	return os.Remove(path)
}

func (h *logHistory) rotate() error {
	if err := h.closeFile(); err != nil {
		return err
	}
	if err := logHistoryRemoveFile(h.oldPath); err != nil {
		return err
	}
	return os.Rename(h.currentPath, h.oldPath)
}

func (h *logHistory) writeEntry(entry logHistoryEntry) {
	if entry.epoch != h.epoch.Load() {
		return
	}
	data, err := json.Marshal(entry)
	if err == nil {
		data = append(data, '\n')
	}
	if err != nil || len(data) > logHistoryRecordLimit {
		h.dropped.Add(1)
		h.writeError = true
		return
	}
	if err = h.openCurrent(); err == nil && h.size+int64(len(data)) > logHistoryFileLimit {
		err = h.rotate()
		if err == nil {
			err = h.openCurrent()
		}
	}
	if err == nil {
		var n int
		n, err = h.file.Write(data)
		h.size += int64(n)
		if err == nil && n != len(data) {
			err = fmt.Errorf("日志历史写入不完整")
		}
	}
	if err != nil {
		h.dropped.Add(1)
		h.writeError = true
		h.closeFile()
		return
	}
	h.writeError = false
}

func (h *logHistory) read(instanceID, binding string) ([]string, error) {
	lines := []string{}
	partialFile := false
	for _, path := range []string{h.oldPath, h.currentPath} {
		data, err := logHistoryReadFile(path)
		if err != nil {
			h.readError = true
			return nil, fmt.Errorf("日志历史无法读取，原文件已保留")
		}
		complete, partial := logHistoryCompletePrefix(data)
		if partial {
			partialFile = true
		}
		part, err := logHistoryEntries(complete, instanceID, binding)
		if err != nil {
			h.readError = true
			return nil, fmt.Errorf("日志历史无法读取，原文件已保留")
		}
		lines = append(lines, part...)
	}
	h.readError = partialFile
	return lines, nil
}

func (h *logHistory) clear() error {
	// Entries admitted before this point may not reappear after clear.
	h.admitMu.Lock()
	h.epoch.Add(1)
	h.admitMu.Unlock()
	if err := h.closeFile(); err != nil {
		h.clearError = true
		return fmt.Errorf("日志历史可能未清除，请核对文件后重试")
	}
	oldErr := logHistoryRemoveFile(h.oldPath)
	currentErr := logHistoryRemoveFile(h.currentPath)
	if oldErr != nil || currentErr != nil {
		h.writeError = true
		h.clearError = true
		return fmt.Errorf("日志历史可能只清除了一部分，请核对文件后重试")
	}
	h.writeError = false
	h.readError = false
	h.clearError = false
	return nil
}

// History settings and clearing affect all local instances and are admin-only.
func (a *App) logHistoryAPI(w http.ResponseWriter, r *http.Request) {
	if !requestPrincipal(r).admin() {
		fail(w, http.StatusForbidden, fmt.Errorf("仅管理员可管理日志历史"))
		return
	}
	action := "status"
	if r.Method == http.MethodPost {
		var req struct {
			Action string `json:"action"`
		}
		if err := decode(w, r, &req); err != nil || (req.Action != "enable" && req.Action != "disable" && req.Action != "clear") {
			fail(w, http.StatusBadRequest, fmt.Errorf("日志历史操作无效"))
			return
		}
		action = req.Action
	} else if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	result, err := a.logHistory.request(ctx, action, "", "")
	if err != nil {
		fail(w, http.StatusServiceUnavailable, fmt.Errorf("日志历史操作未完成或结果未确认，请刷新后核对"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	reply(w, http.StatusOK, map[string]any{"ok": true, "status": result.status})
}
