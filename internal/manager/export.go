package manager

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/Presikid/KurumiFrp/internal/core"
	"io"
	"net/http"
	"strconv"
)

// Exported files contain the data key and are therefore sensitive credentials.
func (a *App) export(w http.ResponseWriter, r *http.Request) {
	scope := r.URL.Query().Get("scope")
	db := a.store.DB
	db.Format = 3
	db.Key = base64.RawStdEncoding.EncodeToString(a.store.key)
	db.History = append([]Snapshot{}, db.History...)
	switch scope {
	case "all":
	case "config":
		db.History = []Snapshot{}
	case "snapshots", "snapshot":
		if len(db.History) == 0 {
			fail(w, 404, fmt.Errorf("没有可导出的快照"))
			return
		}
		if scope == "snapshot" {
			target, err := strconv.ParseUint(r.URL.Query().Get("revision"), 10, 64)
			if err != nil {
				fail(w, 400, fmt.Errorf("无效的快照版本"))
				return
			}
			matches := []Snapshot{}
			for _, h := range db.History {
				if h.Revision == target {
					matches = append(matches, h)
				}
			}
			if len(matches) == 0 {
				fail(w, 404, fmt.Errorf("快照不存在"))
				return
			}
			db.History = matches
		}
		last := db.History[len(db.History)-1]
		db.State = last.State.clone()
		db.History = db.History[:len(db.History)-1]
	default:
		fail(w, 400, fmt.Errorf("无效的导出范围"))
		return
	}
	reply(w, 200, db)
}

func (a *App) importConfig(w http.ResponseWriter, r *http.Request) {
	revision, err := strconv.ParseUint(r.URL.Query().Get("revision"), 10, 64)
	if err != nil || revision != a.store.DB.Revision {
		fail(w, 409, fmt.Errorf("配置已更新，请刷新后重试"))
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxStateSize))
	if err != nil {
		fail(w, 400, fmt.Errorf("无法读取配置文件或文件超过大小限制"))
		return
	}
	var db Database
	if err = json.Unmarshal(data, &db); err != nil || db.Format != 3 {
		fail(w, 400, fmt.Errorf("无效的单文件配置"))
		return
	}
	key, err := base64.RawStdEncoding.DecodeString(db.Key)
	if err != nil || len(key) != 32 || db.HistoryLimit < 0 || db.HistoryLimit > maxHistoryLimit || len(db.History) > db.HistoryLimit || db.Revision == 0 {
		fail(w, 400, fmt.Errorf("配置文件格式无效"))
		return
	}
	if db.ActiveCore == nil || !core.ValidID(*db.ActiveCore) {
		fail(w, 400, fmt.Errorf("核心配置无效"))
		return
	}
	check := &Store{key: key}
	revisions := map[uint64]bool{}
	for _, h := range db.History {
		if h.Revision == 0 || h.Revision >= db.Revision || revisions[h.Revision] {
			fail(w, 400, fmt.Errorf("快照版本无效"))
			return
		}
		revisions[h.Revision] = true
	}
	for _, state := range append([]State{db.State}, historyStates(db.History)...) {
		if err = validate(state); err != nil {
			fail(w, 400, err)
			return
		}
		for _, i := range state.Instances {
			if _, err = check.decrypt(i.Token); err != nil {
				fail(w, 400, err)
				return
			}
		}
		for _, t := range state.Tunnels {
			if t.Secret != "" {
				if _, err = check.decrypt(t.Secret); err != nil {
					fail(w, 400, err)
					return
				}
			}
		}
	}
	if err = validateVisitorListeners(a.store.DB.State, db.State); err != nil {
		fail(w, 400, err)
		return
	}
	if db.Revision < a.store.DB.Revision {
		db.Revision = a.store.DB.Revision
	}
	db.Revision++
	previousKey := a.store.key
	a.store.key = key
	if err = a.store.write(db); err != nil {
		a.store.key = previousKey
		fail(w, 500, err)
		return
	}
	a.store.DB = db
	if err = a.core.UseConfigActive(*db.ActiveCore, func(id string) error {
		next := a.store.DB
		next.ActiveCore = &id
		next.Revision++
		if err := a.store.write(next); err != nil {
			return err
		}
		a.store.DB = next
		return nil
	}); err != nil {
		fail(w, 500, err)
		return
	}
	reply(w, 200, map[string]any{"ok": true, "revision": db.Revision})
}
