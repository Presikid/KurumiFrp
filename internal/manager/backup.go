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

func (a *App) backupExport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Revision uint64 `json:"revision"`
		Password string `json:"password"`
	}
	if err := decode(w, r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	if req.Revision != a.store.DB.Revision {
		fail(w, 409, fmt.Errorf("configuration changed; refresh and retry"))
		return
	}
	backup, err := encryptBackup(a.store.DB, a.store.key, req.Password)
	if err != nil {
		fail(w, 400, err)
		return
	}
	reply(w, 200, backup)
}

func (a *App) backupImport(w http.ResponseWriter, r *http.Request) {
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
	if req.Revision != a.store.DB.Revision {
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
	a.importConfig(w, clone)
}
