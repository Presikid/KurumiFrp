package manager

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// Bound expensive backup work and the buffered plain-import allocation per app.
func (a *App) reserveBackup() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.activeBackup || a.closing {
		return false
	}
	a.activeBackup = true
	return true
}

func (a *App) releaseBackup() {
	a.mu.Lock()
	a.activeBackup = false
	a.mu.Unlock()
}

func cloneBackupDatabase(db Database) Database {
	db.State = db.State.clone()
	db.History = append([]Snapshot(nil), db.History...)
	for i := range db.History {
		db.History[i].State = db.History[i].State.clone()
	}
	db.Events = append([]Event(nil), db.Events...)
	if db.ActiveCore != nil {
		id := *db.ActiveCore
		db.ActiveCore = &id
	}
	return db
}

func (a *App) backupExport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Revision uint64 `json:"revision"`
		Password string `json:"password"`
		Scope    string `json:"scope"`
		Snapshot uint64 `json:"snapshot"`
	}
	if err := decode(w, r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	if !a.reserveBackup() {
		fail(w, 409, fmt.Errorf("备份操作正在进行或工作台正在关闭"))
		return
	}
	defer a.releaseBackup()
	a.mu.Lock()
	if req.Revision != a.store.DB.Revision {
		a.mu.Unlock()
		fail(w, 409, fmt.Errorf("configuration changed; refresh and retry"))
		return
	}
	db, err := selectExportScope(a.store.DB, req.Scope, req.Snapshot)
	if err != nil {
		a.mu.Unlock()
		fail(w, 400, err)
		return
	}
	db = cloneBackupDatabase(db)
	normalizeExportedCore(&db)
	// Webhook destinations are local credentials, not portable backup data.
	db.Webhook = nil
	db.Nodes = nil // Remote node authority remains on this device.
	key := append([]byte(nil), a.store.key...)
	a.mu.Unlock()
	backup, err := encryptBackup(db, key, req.Password)
	if err != nil {
		fail(w, 400, err)
		return
	}
	reply(w, 200, backup)
}

func (a *App) backupImport(w http.ResponseWriter, r *http.Request) {
	if !a.reserveBackup() {
		fail(w, 409, fmt.Errorf("备份操作正在进行或工作台正在关闭"))
		return
	}
	defer a.releaseBackup()
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		fail(w, 400, fmt.Errorf("application/json is required"))
		return
	}
	var req struct {
		Revision uint64          `json:"revision"`
		Password string          `json:"password"`
		Backup   EncryptedBackup `json:"backup"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBackupSize))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		fail(w, 400, fmt.Errorf("invalid backup request or file exceeds the size limit"))
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		fail(w, 400, fmt.Errorf("request must contain exactly one JSON object"))
		return
	}
	a.mu.Lock()
	validRevision := req.Revision == a.store.DB.Revision && !a.closing
	a.mu.Unlock()
	if !validRevision {
		fail(w, 409, fmt.Errorf("configuration changed; refresh and retry"))
		return
	}
	db, err := decryptBackup(req.Backup, req.Password)
	if err != nil {
		fail(w, 400, err)
		return
	}
	plain, err := json.Marshal(db)
	if err != nil || len(plain) > maxStateSize {
		fail(w, 400, fmt.Errorf("invalid backup contents"))
		return
	}
	clone := r.Clone(r.Context())
	u := *r.URL
	query := u.Query()
	query.Set("revision", strconv.FormatUint(req.Revision, 10))
	u.RawQuery = query.Encode()
	clone.URL = &u
	clone.Body = io.NopCloser(bytes.NewReader(plain))
	clone.ContentLength = int64(len(plain))
	release, err := a.lockStateReplacement()
	if err != nil {
		fail(w, 409, err)
		return
	}
	defer release()
	if a.closing {
		fail(w, 409, fmt.Errorf("工作台正在关闭"))
		return
	}
	a.importConfig(w, clone)
}
