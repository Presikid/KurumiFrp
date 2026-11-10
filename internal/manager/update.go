package manager

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/Presikid/KurumiFrp/internal/update"
)

// Update checks already come from authenticated UI requests. These bounded,
// in-memory observations avoid duplicate notifications and stale responses.
type updateNoticeState struct {
	mu               sync.Mutex
	sequence         uint64
	latestObserved   uint64
	current          string
	seen             map[string]bool
	seenOrder        []string
	lastNotification time.Time
}

func (s *updateNoticeState) begin() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sequence++
	return s.sequence
}

func (s *updateNoticeState) observe(sequence uint64, versions []update.Version, dispatcher *webhookDispatcher) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sequence < s.latestObserved {
		return
	}
	s.latestObserved = sequence
	tag := ""
	for _, version := range versions {
		if version.Newer && version.Available {
			tag = version.Tag
			break // Versions is sorted newest first.
		}
	}
	if tag == s.current {
		return
	}
	s.current = tag
	if tag == "" || s.seen[tag] || (!s.lastNotification.IsZero() && time.Since(s.lastNotification) < 5*time.Minute) {
		return
	}
	if !dispatcher.emitUpdateAvailable(tag) {
		return
	}
	if s.seen == nil {
		s.seen = make(map[string]bool)
	}
	s.seen[tag] = true
	s.seenOrder = append(s.seenOrder, tag)
	if len(s.seenOrder) > 64 {
		delete(s.seen, s.seenOrder[0])
		s.seenOrder = s.seenOrder[1:]
	}
	s.lastNotification = time.Now()
}

func (a *App) updateAPI(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == "GET" && r.URL.Path == "/api/update/releases":
		sequence := a.updateNotices.begin()
		releases, e := a.updater.Versions(r.Context())
		if e != nil {
			fail(w, 502, e)
			return
		}
		a.updateNotices.observe(sequence, releases, a.webhooks)
		reply(w, 200, map[string]any{"versions": releases, "status": a.updater.Status()})
	case r.Method == "GET" && r.URL.Path == "/api/update/status":
		reply(w, 200, map[string]string{"status": a.updater.Status()})
	case r.Method == "POST" && r.URL.Path == "/api/update/install":
		var req struct {
			Tag string `json:"tag"`
		}
		if e := decode(w, r, &req); e != nil {
			fail(w, 400, e)
			return
		}
		if e := a.updater.Start(req.Tag); e != nil {
			fail(w, 400, e)
			return
		}
		reply(w, 202, map[string]bool{"ok": true})
	default:
		fail(w, 404, fmt.Errorf("更新接口不存在"))
	}
}
