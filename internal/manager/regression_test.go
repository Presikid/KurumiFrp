package manager

import (
	"encoding/json"
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
	if s.pruned != 1 || len(s.DB.History) != 3 || !strings.Contains(s.DB.Events[len(s.DB.Events)-1].Message, "移除 1") {
		t.Fatalf("history pruning not reported: %d", s.pruned)
	}
	if _, err := newStore(dir); err != nil {
		t.Fatal("saved state did not reload:", err)
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
