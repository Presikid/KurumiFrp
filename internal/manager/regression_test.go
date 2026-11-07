package manager

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestLegacyCoreSelectionMigrates(t *testing.T) {
	coreDir := t.TempDir()
	id := "v0.61.0-linux_amd64-0000000000000000"
	if err := os.WriteFile(filepath.Join(coreDir, "active.json"), []byte(`{"id":"`+id+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
	app, err := New(t.TempDir(), coreDir, "127.0.0.1:9000", fstest.MapFS{})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if app.store.DB.ActiveCore == nil || *app.store.DB.ActiveCore != id {
		t.Fatal("core choice was not migrated")
	}
	if _, err := os.Stat(filepath.Join(coreDir, "active.json")); !os.IsNotExist(err) {
		t.Fatal("old selection file retained", err)
	}
}

func TestLegacyOversizedHistoryCanBeSavedAndReloaded(t *testing.T) {
	dir := t.TempDir()
	s, err := newStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Reproduce a file written by the old version, beyond its read limit.
	s.DB.Format = 1
	s.DB.Revision = 4
	for i := 0; i < 3; i++ {
		s.DB.History = append(s.DB.History, Snapshot{Revision: uint64(i + 1), Summary: strings.Repeat("x", 3<<20), State: s.DB.State.clone()})
	}
	b, err := json.MarshalIndent(s.DB, "", "  ")
	if err != nil || len(b) <= 8<<20 {
		t.Fatal("fixture is not oversized", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "master.key"), s.key, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	s, err = newStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.commit(s.DB.State.clone(), "edit"); err != nil {
		t.Fatal(err)
	}
	if len(s.DB.History) != 4 {
		t.Fatalf("legacy history was not preserved: %d", len(s.DB.History))
	}
	current, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil || len(current) > maxStateSize || !strings.Contains(string(current), strings.Repeat("x", 100)) {
		t.Fatal("snapshots not stored in current configuration", err)
	}
	if _, err := newStore(dir); err != nil {
		t.Fatal("saved state did not reload:", err)
	}
}

func TestHistoryLimitAndSeparateSnapshots(t *testing.T) {
	a := testApp(t)
	seed(t, a)
	for j := 0; j < 7; j++ {
		if err := a.store.commit(a.store.DB.State.clone(), fmt.Sprint(j)); err != nil {
			t.Fatal(err)
		}
	}
	if len(a.store.DB.History) != 5 {
		t.Fatal("default retention", len(a.store.DB.History))
	}
	if _, err := os.Stat(filepath.Join(a.store.dir, "snapshots")); !os.IsNotExist(err) {
		t.Fatal("unexpected snapshot directory")
	}
	if err := a.store.setHistoryLimit(2); err != nil {
		t.Fatal(err)
	}
	s, err := newStore(a.store.dir)
	if err != nil || s.DB.HistoryLimit != 2 || len(s.DB.History) != 2 {
		t.Fatal("reload", err)
	}
	if err = s.setHistoryLimit(0); err != nil {
		t.Fatal(err)
	}
	if err = s.commit(s.DB.State.clone(), "no snapshots"); err != nil {
		t.Fatal(err)
	}
	if len(s.DB.History) != 0 {
		t.Fatal("disabled history still records")
	}
	if err = s.setHistoryLimit(101); err == nil {
		t.Fatal("invalid limit accepted")
	}
}

func TestExportAndImportAcrossStores(t *testing.T) {
	source := testApp(t)
	seed(t, source)
	coreID := "v0.61.0-linux_amd64-0000000000000000"
	source.store.DB.ActiveCore = &coreID
	for _, scope := range []string{"all", "config", "snapshots", "snapshot"} {
		path := "/api/export?scope=" + scope
		if scope == "snapshot" {
			path += "&revision=1"
		}
		w := request(source, "GET", path, nil, source.AccessToken(), "")
		if w.Code != 200 {
			t.Fatal(scope, w.Body.String())
		}
		var db Database
		if err := json.Unmarshal(w.Body.Bytes(), &db); err != nil || db.Key == "" || db.Format != 3 {
			t.Fatal(scope, err)
		}
		target := testApp(t)
		dest := request(target, "POST", fmt.Sprintf("/api/import?revision=%d", target.store.DB.Revision), db, target.AccessToken(), "")
		if dest.Code != 200 {
			t.Fatal(scope, dest.Body.String())
		}
		status, err := target.core.Status()
		if err != nil || status.Active != coreID {
			t.Fatal("active core not imported", err)
		}
		if scope == "all" || scope == "config" {
			secret, err := target.store.decrypt(target.store.DB.State.Instances[0].Token)
			if err != nil || secret != "test-secret-1234" {
				t.Fatal("imported secret", err)
			}
		}
		if _, err := newStore(target.store.dir); err != nil {
			t.Fatal(scope, err)
		}
	}
	if request(source, "GET", "/api/export?scope=all", nil, "", "").Code != 401 {
		t.Fatal("unauthenticated export")
	}
	if request(source, "GET", "/api/export?scope=snapshot&revision=999", nil, source.AccessToken(), "").Code != 404 {
		t.Fatal("missing snapshot accepted")
	}
}

func TestVisitorListenerConflictsAndLegacyState(t *testing.T) {
	i := Instance{ID: "0000000000000001", Kind: "frpc"}
	v := Tunnel{ID: "0000000000000002", InstanceID: i.ID, Name: "first", Type: "stcp-visitor", LocalHost: "127.0.0.1", LocalPort: 8000, Enabled: true}
	w := v
	w.ID, w.Name = "0000000000000003", "second"
	base := State{Instances: []Instance{i}, Tunnels: []Tunnel{v}}
	next := base.clone()
	next.Tunnels = append(next.Tunnels, w)
	if validateVisitorListeners(base, next) == nil {
		t.Fatal("same listener accepted")
	}
	next.Tunnels[1].LocalHost = "127.0.0.2"
	if err := validateVisitorListeners(base, next); err != nil {
		t.Fatal("distinct addresses rejected:", err)
	}
	next.Tunnels[1].LocalHost = "::"
	if validateVisitorListeners(base, next) == nil {
		t.Fatal("wildcard listener accepted")
	}
	if err := validateVisitorListeners(next, next); err != nil {
		t.Fatal("existing conflict must remain editable:", err)
	}
	next.Tunnels[1].Enabled = false
	if err := validateVisitorListeners(base, next); err != nil {
		t.Fatal("disabled visitor rejected:", err)
	}
	server := Instance{ID: "0000000000000004", Name: "server", Kind: "frps", Host: "127.0.0.1", Port: 8000}
	next = base.clone()
	next.Instances = append(next.Instances, server)
	if validateVisitorListeners(base, next) == nil {
		t.Fatal("frps and visitor conflict accepted")
	}
}
