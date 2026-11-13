package manager

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
)

func stateWriteOutcomeError(operation string, err error) error {
	return fmt.Errorf("%s未完成或配置保存状态未确认；请核对配置：%w", operation, err)
}

// Bound and consume slow uploads before waiting on the app lock.
func bufferMutationBody(w http.ResponseWriter, r *http.Request, limit int64) (*http.Request, error) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		return nil, fmt.Errorf("无法读取请求或请求超过大小限制")
	}
	clone := r.Clone(r.Context())
	clone.Body = io.NopCloser(bytes.NewReader(data))
	clone.ContentLength = int64(len(data))
	return clone, nil
}

func (a *App) importConfigAPI(w http.ResponseWriter, r *http.Request) {
	// Reserve the shared transfer slot and buffer before taking the app lock,
	// so a slow client cannot block other operations.
	if !a.reserveBackup() {
		fail(w, http.StatusConflict, fmt.Errorf("备份或导入操作正在进行或工作台正在关闭"))
		return
	}
	defer a.releaseBackup()
	buffered, err := bufferMutationBody(w, r, maxStateSize)
	if err != nil {
		fail(w, 400, err)
		return
	}
	release, err := a.lockStateReplacement()
	if err != nil {
		fail(w, 409, err)
		return
	}
	defer release()
	a.importConfig(w, buffered)
}

// Portable files require an explicit core selection. A nil local value can
// still mean a legacy external selection has yet to be migrated, so normalize
// only the exported copy and leave the local database untouched.
func normalizeExportedCore(db *Database) {
	if db.ActiveCore == nil {
		id := ""
		db.ActiveCore = &id
	}
}

// Exported files contain the data key and are therefore sensitive credentials.
func (a *App) export(w http.ResponseWriter, r *http.Request) {
	scope := r.URL.Query().Get("scope")
	db := a.store.DB
	normalizeExportedCore(&db)
	db.Webhook = nil // Local notification credentials are not exported.
	db.Webhooks = nil
	db.Nodes = nil   // Node grants and credentials stay on this device.
	db.LocalTrafficIDs = nil // Traffic history identities stay on this device.
	db.Format = currentDatabaseFormat
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
	db.Schedules = schedulesForState(db.Schedules, db.State)
	reply(w, 200, db)
}

// Select the data for a password-encrypted export. The caller holds a.mu and
// clones the selected data before releasing the lock.
func selectExportScope(db Database, scope string, target uint64) (Database, error) {
	db.LocalTrafficIDs = nil
	db.History = append([]Snapshot{}, db.History...)
	switch scope {
	case "", "all":
	case "config":
		db.History = []Snapshot{}
	case "snapshots", "snapshot":
		if len(db.History) == 0 {
			return Database{}, fmt.Errorf("没有可导出的快照")
		}
		if scope == "snapshot" {
			matches := []Snapshot{}
			for _, h := range db.History {
				if h.Revision == target {
					matches = append(matches, h)
				}
			}
			if len(matches) == 0 {
				return Database{}, fmt.Errorf("快照不存在")
			}
			db.History = matches
		}
		last := db.History[len(db.History)-1]
		db.State = last.State.clone()
		db.History = db.History[:len(db.History)-1]
	default:
		return Database{}, fmt.Errorf("无效的导出范围")
	}
	db.Schedules = schedulesForState(db.Schedules, db.State)
	return db, nil
}

// importConfig is called with a.mu held for both plain and encrypted imports.
func (a *App) importConfig(w http.ResponseWriter, r *http.Request) {
	revision, err := strconv.ParseUint(r.URL.Query().Get("revision"), 10, 64)
	if err != nil || revision != a.store.DB.Revision {
		fail(w, 409, fmt.Errorf("配置已更新，请刷新后重试"))
		return
	}
	if err = a.canReplaceStateLocked(); err != nil {
		fail(w, http.StatusConflict, err)
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxStateSize))
	if err != nil {
		fail(w, 400, fmt.Errorf("无法读取配置文件或文件超过大小限制"))
		return
	}
	db, err := singleFileDatabase(data)
	if err != nil {
		fail(w, 400, fmt.Errorf("无效或不受支持的单文件配置：%w", err))
		return
	}
	key, err := base64.RawStdEncoding.DecodeString(db.Key)
	if err != nil || len(key) != 32 || db.HistoryLimit < 0 || db.HistoryLimit > maxHistoryLimit || len(db.History) > db.HistoryLimit || db.Revision == 0 {
		fail(w, 400, fmt.Errorf("配置文件格式无效"))
		return
	}
	// The in-process runtime does not use the old external core selection.
	db.ActiveCore = a.store.DB.ActiveCore
	check := &Store{key: key}
	revisions := map[uint64]bool{}
	highestHistoryRevision := uint64(0)
	for _, h := range db.History {
		if h.Revision == 0 || h.Revision >= db.Revision || h.Revision > maxImportedHistoryRevision || revisions[h.Revision] {
			fail(w, 400, fmt.Errorf("快照版本无效"))
			return
		}
		revisions[h.Revision] = true
		if h.Revision > highestHistoryRevision {
			highestHistoryRevision = h.Revision
		}
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
			if err = check.validateStoredHTTPHeaders(t); err != nil {
				fail(w, 400, err)
				return
			}
			if err = check.validatePluginSecret(t); err != nil {
				fail(w, 400, err)
				return
			}
			if t.Secret != "" {
				if _, err = check.decrypt(t.Secret); err != nil {
					fail(w, 400, err)
					return
				}
			}
			if t.HTTPPassword != "" {
				if _, err = check.decrypt(t.HTTPPassword); err != nil {
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
	if err = validateSchedules(db.State, db.Schedules); err != nil {
		fail(w, 400, err)
		return
	}
	// Legacy account credentials may depend on the current data key. Their
	// file is not included in portable backups and must remain untouched.
	if !bytes.Equal(a.store.key, key) {
		_, accountErr := os.Lstat(filepath.Join(a.store.dir, "accounts.json"))
		if accountErr == nil || !os.IsNotExist(accountErr) {
			fail(w, http.StatusConflict, fmt.Errorf("本机有旧版账户资料或无法确认其状态；为保留旧资料，已拒绝使用不同数据密钥的导入。请备份完整数据目录，工作台配置备份不包含旧版账户资料"))
			return
		}
	}
	db.Schedules = cloneSchedules(db.Schedules)
	for id, plan := range db.Schedules {
		plan = pauseSchedule(plan, "导入或恢复后请检查并重新启用计划")
		plan.LastMinute, plan.LastStatus = 0, ""
		db.Schedules[id] = plan
	}
	// Ignore imported notification settings. The imported key replaces the local
	// key, so re-encrypt all local notification secrets before committing.
	db.Webhook = nil
	if a.store.DB.Webhook != nil {
		local, err := rewrapWebhookConfig(a.store, check, *a.store.DB.Webhook)
		if err != nil {
			fail(w, 500, fmt.Errorf("无法迁移本机通知设置"))
			return
		}
		db.Webhook = &local
	}
	db.Webhooks, err = rewrapWebhookTargets(a.store, check, a.store.DB.Webhook, a.store.DB.Webhooks)
	if err != nil {
		fail(w, 500, fmt.Errorf("无法迁移本机通知目标，未导入配置"))
		return
	}
	// Imported node authority is ignored. Keep this device’s node settings and
	// re-encrypt outbound credentials under the replacement data key.
	db.Nodes, err = rewrapNodeSettings(a.store, check, a.store.DB.Nodes)
	if err != nil {
		fail(w, 500, fmt.Errorf("无法迁移本机节点设置，未导入配置"))
		return
	}
	// The imported current revision is an untrusted concurrency token. Keep
	// snapshot labels intact, but allocate a fresh local revision above them.
	db.Revision = a.store.DB.Revision
	if db.Revision < highestHistoryRevision {
		db.Revision = highestHistoryRevision
	}
	if err = incrementRevision(&db); err != nil {
		fail(w, 400, err)
		return
	}
	db.Format = currentDatabaseFormat
	// Imported IDs must never inherit this device's previous observations.
	db.LocalTrafficIDs = freshLocalTrafficIDs(db.State)
	previousKey := a.store.key
	a.store.key = key
	if err = a.store.write(db); err != nil {
		a.store.key = previousKey
		fail(w, 500, stateWriteOutcomeError("导入", err))
		return
	}
	a.store.DB = db
	a.discardStoppedRuntimeLocked()
	if a.webhooks != nil {
		a.configureWebhooks()
		a.webhooks.emitConfig(db.Revision)
	}
	reply(w, 200, map[string]any{"ok": true, "revision": db.Revision})
}
