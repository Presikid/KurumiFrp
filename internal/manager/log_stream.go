package manager

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const (
	maxLogStreams            = 32
	maxLogStreamsPerInstance = 8
	maxLogEventContent       = 64 << 10
	logStreamHeartbeat       = 10 * time.Second
	logStreamWriteTimeout    = 5 * time.Second
)

// Raw backend output can contain arbitrary values. Only administrators may
// read logs. Call with a.mu held, before the authentication lock.
func (a *App) canReadInstanceLogs(r *http.Request, id string) bool {
	p, ok := a.auth.identity(r)
	return ok && p.admin() && p.canView(id)
}

func (a *App) pollLogs(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if !idPattern.MatchString(id) {
		fail(w, http.StatusBadRequest, fmt.Errorf("需要有效的实例"))
		return
	}
	a.mu.Lock()
	if !a.canReadInstanceLogs(r, id) {
		a.mu.Unlock()
		fail(w, http.StatusForbidden, fmt.Errorf("没有查看实例日志的权限"))
		return
	}
	lines := []string{}
	runID := ""
	if _, exists := a.store.DB.State.Instance(id); exists {
		if p := a.procs[id]; p != nil {
			status := p.view()
			runID = status.RunID
			lines = p.logs.read()
		}
	}
	a.mu.Unlock()
	// As with the stream, a slow response must not hold the application lock.
	reply(w, http.StatusOK, map[string]any{"lines": lines, "runId": runID})
}

type logStreamLines struct {
	InstanceID string   `json:"instanceId"`
	RunID      string   `json:"runId"`
	Lines      []string `json:"lines"`
}

type logStreamEnd struct {
	InstanceID string `json:"instanceId"`
	RunID      string `json:"runId"`
}

// No app or buffer lock is held during network I/O. All event names and
// lifecycle messages are fixed; the only log content is the redacted buffer.
func writeLogEvent(w http.ResponseWriter, controller *http.ResponseController, event string, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	frame := append([]byte("event: "+event+"\ndata: "), body...)
	frame = append(frame, '\n', '\n')
	if err = controller.SetWriteDeadline(time.Now().Add(logStreamWriteTimeout)); err != nil {
		return err
	}
	n, err := w.Write(frame)
	if err != nil {
		return err
	}
	if n != len(frame) {
		return fmt.Errorf("incomplete log stream write")
	}
	if err = controller.Flush(); err != nil {
		return err
	}
	return controller.SetWriteDeadline(time.Time{})
}

// Re-evaluate the current session and instance before each frame, not just
// when the request is opened. A stream is tied to one process object/run.
func (a *App) logStreamCurrent(r *http.Request, id string, p *process) bool {
	if r.Context().Err() != nil {
		return false
	}
	select {
	case <-a.quit:
		return false
	default:
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closing || !a.canReadInstanceLogs(r, id) || a.procs[id] != p {
		return false
	}
	_, exists := a.store.DB.State.Instance(id)
	return exists
}

// Keep the account/session gate only while checking and writing one bounded
// frame; a long-lived stream must never hold a gate for its whole lifetime.
func (a *App) lockLogFrame(r *http.Request, id string, proc *process) (func(), bool) {
	p, unlock, ok := a.auth.lockRequest(r)
	if !ok {
		return nil, false
	}
	if !p.admin() || !p.canView(id) || !a.logStreamCurrent(r, id, proc) {
		unlock()
		return nil, false
	}
	return unlock, true
}

// Bound each JSON allocation and network write. A large snapshot starts with
// one snapshot event followed by line events, in the same captured order.
func (a *App) writeLogLines(w http.ResponseWriter, controller *http.ResponseController, r *http.Request, id, runID string, p *process, lines []string, reset bool) bool {
	if len(lines) == 0 && !reset {
		return true
	}
	for start := 0; ; {
		end, size := start, 0
		for end < len(lines) && (end == start || size+len(lines[end]) <= maxLogEventContent) {
			size += len(lines[end])
			end++
		}
		unlock, ok := a.lockLogFrame(r, id, p)
		if !ok {
			return false
		}
		event := "lines"
		if reset && start == 0 {
			event = "snapshot"
		}
		err := writeLogEvent(w, controller, event, logStreamLines{id, runID, lines[start:end]})
		unlock()
		if err != nil {
			return false
		}
		if end == len(lines) {
			return true
		}
		start = end
	}
}

func (a *App) streamLogs(w http.ResponseWriter, r *http.Request) {
	id, runID := r.URL.Query().Get("id"), r.URL.Query().Get("runId")
	if !idPattern.MatchString(id) || runID == "" || len(runID) > 128 {
		fail(w, http.StatusBadRequest, fmt.Errorf("需要有效的实例及运行代次"))
		return
	}
	select {
	case <-a.quit:
		fail(w, http.StatusServiceUnavailable, fmt.Errorf("工作台正在关闭"))
		return
	default:
	}

	a.mu.Lock()
	if !a.canReadInstanceLogs(r, id) {
		a.mu.Unlock()
		fail(w, http.StatusForbidden, fmt.Errorf("没有查看实例日志的权限"))
		return
	}
	_, exists := a.store.DB.State.Instance(id)
	p := a.procs[id]
	status := RuntimeStatus{}
	if p != nil {
		status = p.view()
	}
	if !exists || p == nil || status.RunID != runID || a.closing {
		a.mu.Unlock()
		fail(w, http.StatusConflict, fmt.Errorf("实例或运行代次已变化"))
		return
	}
	if a.logStreams >= maxLogStreams || a.logStreamsByID[id] >= maxLogStreamsPerInstance {
		a.mu.Unlock()
		fail(w, http.StatusTooManyRequests, fmt.Errorf("实时日志连接已满"))
		return
	}
	if a.logStreamsByID == nil {
		a.logStreamsByID = make(map[string]int)
	}
	a.logStreams++
	a.logStreamsByID[id]++
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.logStreams--
		a.logStreamsByID[id]--
		if a.logStreamsByID[id] == 0 {
			delete(a.logStreamsByID, id)
		}
		a.mu.Unlock()
	}()

	controller := http.NewResponseController(w)
	// A deadline is mandatory: an unread connection must not retain a handler
	// indefinitely. The server's ordinary 45-second timeout is reset per write.
	if err := controller.SetWriteDeadline(time.Now().Add(logStreamWriteTimeout)); err != nil {
		fail(w, http.StatusNotImplemented, fmt.Errorf("当前连接不支持实时日志"))
		return
	}
	defer controller.SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")

	ticker := time.NewTicker(logStreamHeartbeat)
	defer ticker.Stop()
	cursor := uint64(0)
	initial := true
	for {
		lines, next, reset, changed := p.logs.updates(cursor, initial)
		if !a.writeLogLines(w, controller, r, id, runID, p, lines, reset) {
			return
		}
		cursor, initial = next, false

		select {
		case <-r.Context().Done():
			return
		case <-a.quit:
			return
		case <-changed:
			// Read the next batch from the bounded buffer on the next iteration.
		case <-p.done:
			// The process publishes its final line before closing done.
			lines, _, reset, _ := p.logs.updates(cursor, false)
			if !a.writeLogLines(w, controller, r, id, runID, p, lines, reset) {
				return
			}
			if unlock, ok := a.lockLogFrame(r, id, p); ok {
				_ = writeLogEvent(w, controller, "end", logStreamEnd{id, runID})
				unlock()
			}
			return
		case <-ticker.C:
			unlock, ok := a.lockLogFrame(r, id, p)
			if !ok {
				return
			}
			err := writeLogEvent(w, controller, "heartbeat", struct{}{})
			unlock()
			if err != nil {
				return
			}
		}
	}
}
