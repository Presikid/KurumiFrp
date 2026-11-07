package manager

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	if err != nil || len(b) <= maxStateSize {
		t.Fatal("fixture is not oversized", err)
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
	if err != nil || len(current) > maxStateSize || strings.Contains(string(current), strings.Repeat("x", 100)) {
		t.Fatal("snapshots are still stored in current configuration", err)
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
	entries, err := os.ReadDir(filepath.Join(a.store.dir, "snapshots"))
	if err != nil || len(entries) != 5 {
		t.Fatal("snapshot files", len(entries), err)
	}
	if err := a.store.setHistoryLimit(2); err != nil {
		t.Fatal(err)
	}
	entries, err = os.ReadDir(filepath.Join(a.store.dir, "snapshots"))
	if err != nil || len(entries) != 2 {
		t.Fatal("old files not removed", len(entries), err)
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

func TestExportScopesAndSecrets(t *testing.T) {
	a := testApp(t)
	seed(t, a)
	for _, scope := range []string{"all", "config", "snapshots", "snapshot"} {
		path := "/api/export?scope=" + scope
		if scope == "snapshot" {
			path += "&revision=1"
		}
		w := request(a, "GET", path, nil, a.AccessToken(), "")
		if w.Code != 200 || strings.Contains(w.Body.String(), "test-secret-1234") {
			t.Fatal(scope, w.Code, w.Body.String())
		}
		var result map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		_, hasConfig := result["config"]
		_, hasSnapshots := result["snapshots"]
		if hasConfig != (scope == "all" || scope == "config") || hasSnapshots != (scope != "config") {
			t.Fatal(scope, result)
		}
	}
	w := request(a, "GET", "/api/export?scope=config&secrets=1", nil, a.AccessToken(), "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "test-secret-1234") {
		t.Fatal("explicit secret export failed")
	}
	if request(a, "GET", "/api/export?scope=all", nil, "", "").Code != 401 {
		t.Fatal("unauthenticated export")
	}
	if request(a, "GET", "/api/export?scope=snapshot&revision=999", nil, a.AccessToken(), "").Code != 404 {
		t.Fatal("missing snapshot accepted")
	}
}

func TestOversizedCurrentStateDoesNotReplaceStoredData(t *testing.T) {
	a := testApp(t)
	i := seed(t, a)
	before, err := os.ReadFile(filepath.Join(a.store.dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	st := a.store.DB.State.clone()
	st.Tunnels = append(st.Tunnels, Tunnel{ID: randomHex(8), InstanceID: i.ID, Name: "large", Type: "tcp", LocalHost: "127.0.0.1", LocalPort: 80, RemotePort: 9001, ServerName: strings.Repeat("x", maxStateSize)})
	if err := a.store.commit(st, "too large"); err == nil {
		t.Fatal("oversized state accepted")
	}
	after, err := os.ReadFile(filepath.Join(a.store.dir, "state.json"))
	if err != nil || string(before) != string(after) {
		t.Fatal("failed save modified data", err)
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
