package manager

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInvalidActiveConfigIsPreserved(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SelectOrCreateConfig("second", ""); err != nil {
		t.Fatal(err)
	}
	const broken = `{"activeFile":"sec`
	if err := os.WriteFile(s.metaPath, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(dir); err == nil {
		t.Fatal("accepted an invalid active-config index")
	}
	if raw, err := os.ReadFile(s.metaPath); err != nil || string(raw) != broken {
		t.Fatalf("original index changed: %q, %v", raw, err)
	}
}

func TestLegacyClientsKeepTheirTunnels(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(dir); err == nil {
		t.Fatal("expected unreadable legacy configuration to stop migration")
	}
	legacy := `{"state":{
		"instances":[{"id":"a","kind":"frpc"},{"id":"b","kind":"frpc"}],
		"tunnels":[{"id":"ta","instanceId":"a"},{"id":"tb","instanceId":"b"}]
	}}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(dir); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, doc := s.Snapshot()
	if len(doc.Clients) != 2 {
		t.Fatalf("got %d migrated clients", len(doc.Clients))
	}
	for _, cli := range doc.Clients {
		if len(cli.Tunnels) != 1 || cli.Tunnels[0].ID != "t"+cli.ID {
			t.Fatalf("wrong tunnels for migrated client %q: %+v", cli.ID, cli.Tunnels)
		}
	}
}

func TestSelectedConfigKeepsGeneratedCredentials(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.configsDir, "other.json"), []byte(`{"clients":[{"name":"old"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	doc, err := s.SelectOrCreateConfig("other", "")
	if err != nil {
		t.Fatal(err)
	}
	selected := doc.Clients[0]
	sanitizeDocForBrowser(doc)
	_, live := s.Snapshot()
	s, err = NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, saved := s.Snapshot()
	for _, state := range []ConfigDocument{live, saved} {
		cli := state.Clients[0]
		if cli.ID != selected.ID || cli.CtrlSecret != selected.CtrlSecret || cli.AgentToken != selected.AgentToken || cli.AgentToken == "" || cli.CtrlSecret == "" {
			t.Fatal("selected client identity changed after redaction or restart")
		}
	}
}

func TestCredentialSaveFailureKeepsSession(t *testing.T) {
	dir := t.TempDir()
	auth, err := NewAuthManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	session, err := auth.Login("127.0.0.1", "admin", "")
	if err != nil {
		t.Fatal(err)
	}
	auth.accountPath = filepath.Join(auth.accountPath, "invalid")
	if err := auth.UpdateCredentials("", "changed", "new-password"); err == nil {
		t.Fatal("expected account save error")
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: session})
	if user, ok := auth.Authenticated(req); !ok || user != "admin" || !auth.NeedsPasswordSetup() {
		t.Fatal("failed credential change modified the current account or invalidated its session")
	}
	if _, err := auth.Login("127.0.0.1", "admin", ""); err != nil {
		t.Fatal("previous credentials stopped working after failed save")
	}
}

func TestIncompleteAccountRecordFailsInitialization(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "admin-password.json"), []byte(`{"version":2,"username":"admin"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewAuthManager(dir); err == nil {
		t.Fatal("incomplete account was accepted")
	}
}

func TestPanelSaveFailureSeparatesCurrentAndSavedPort(t *testing.T) {
	pm := NewPanelManager(t.TempDir())
	if _, _, err := pm.UpdatePort(9000, "127.0.0.1", nil, nil); err != nil {
		t.Fatal(err)
	}
	pm.SetRebindHandler("127.0.0.1:9000", func(host string, port int) (string, string, int, error) {
		return "127.0.0.1:9001", host, port, nil
	})
	pm.settingsPath = filepath.Join(pm.settingsPath, "invalid")
	st, changed, err := pm.UpdatePort(9001, "127.0.0.1", nil, nil)
	if err == nil || !changed || st.CurrentPort != 9001 || st.SavedPort != 9000 {
		t.Fatalf("status after failed save: %+v, changed=%v, err=%v", st, changed, err)
	}
}

func TestHDDownloadsRejectOversizeChunkedBodies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.(http.Flusher).Flush()
		limit := maxSingleHDAssetBytes
		if r.URL.Path == "/manifest" {
			_, _ = w.Write([]byte("{}"))
			limit = 8 << 20
		}
		padding := bytes.Repeat([]byte(" "), 32<<10)
		for i := 0; i <= limit; i += len(padding) {
			if _, err := w.Write(padding); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	m := NewAssetPackManager(t.TempDir(), &UpdateManager{allowLocal: true}, nil)
	if _, err := m.fetchRemoteAssetManifest(context.Background(), server.Client(), server.URL+"/manifest"); err == nil {
		t.Fatal("accepted oversized manifest")
	}
	dest := filepath.Join(t.TempDir(), "image.jpg")
	if _, err := m.downloadAssetOnce(context.Background(), server.Client(), server.URL+"/image", dest, ""); err == nil || !strings.Contains(err.Error(), "体积上限") {
		t.Fatalf("expected streaming size limit for artwork, got %v", err)
	}
	if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected output after a rejected download: %v", err)
	}
}

func TestHDFailedRefreshKeepsInstalledArtwork(t *testing.T) {
	dir := t.TempDir()
	m := NewAssetPackManager(dir, &UpdateManager{allowLocal: true}, nil)
	display := m.displayDirLocked()
	if err := os.MkdirAll(display, 0o700); err != nil {
		t.Fatal(err)
	}
	old := []byte("original artwork")
	filename := "s_01.jpg"
	if err := os.WriteFile(filepath.Join(display, filename), old, 0o600); err != nil {
		t.Fatal(err)
	}
	m.state.Files["s_01"] = filename
	m.state.FileSHA256["s_01"] = fmt.Sprintf("%x", sha256.Sum256(old))
	m.state.PreferHD = true
	if err := m.saveStateLocked(m.state); err != nil {
		t.Fatal(err)
	}

	newArt := []byte("updated artwork")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/main/assets/material/sunburst_dal/manifest.json":
			_ = json.NewEncoder(w).Encode(AssetPackManifest{Assets: []AssetManifestEntry{{
				Key: "s_01", Original: AssetFileMeta{Path: "updated.jpg", SHA256: fmt.Sprintf("%x", sha256.Sum256(newArt))},
			}}})
		case "/main/updated.jpg":
			_, _ = w.Write(newArt)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	m.rawBase = server.URL
	m.statePath = filepath.Join(m.statePath, "invalid")
	if st, err := m.SetPreferHD(false); err == nil || st.ActiveTier != "hd" {
		t.Fatalf("failed toggle changed tier: %q, %v", st.ActiveTier, err)
	}
	if _, err := m.StartPull(nil, "main", ""); err != nil {
		t.Fatal(err)
	}
	select {
	case <-m.pullDone:
	case <-time.After(5 * time.Second):
		t.Fatal("asset sync timed out")
	}
	if m.Status().LastError == "" {
		t.Fatal("asset sync did not report the failed state save")
	}
	downloaded := filepath.Join(display, fmt.Sprintf("s_01-%x.jpg", sha256.Sum256(newArt)))
	if raw, err := os.ReadFile(downloaded); err != nil || !bytes.Equal(raw, newArt) {
		t.Fatal("verified replacement was not retained for retry")
	}
	for _, manager := range []*AssetPackManager{m, NewAssetPackManager(dir, m.updater, nil)} {
		rec := httptest.NewRecorder()
		if !manager.ServeIfHD(rec, httptest.NewRequest(http.MethodGet, "/assets/s_01.webp", nil)) || !bytes.Equal(rec.Body.Bytes(), old) {
			t.Fatal("previously installed artwork lost after failed refresh")
		}
	}
}
