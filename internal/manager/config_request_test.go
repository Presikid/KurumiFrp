package manager

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestLocalConfigRequestCannotCrossSameFileImport(t *testing.T) {
	app, err := NewApp(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	state := func() uint64 {
		t.Helper()
		w := httptest.NewRecorder()
		app.handleAPI(w, httptest.NewRequest(http.MethodGet, "/api/state", nil), "admin")
		var info struct {
			Generation *uint64 `json:"configGeneration"`
			Filename   string  `json:"activeConfigFile"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil || info.Generation == nil || info.Filename != "default.json" {
			t.Fatalf("invalid state snapshot: filename=%s generation=%v err=%v", info.Filename, info.Generation, err)
		}
		return *info.Generation
	}

	// The reader consumes the first byte while the original document is active.
	// The replacement document is activated before the edit is fully received.
	original := app.store.activeGeneration()
	input, writer := io.Pipe()
	defer input.Close()
	defer writer.Close()
	edit := httptest.NewRequest(http.MethodPost, "/api/servers/save", input)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		app.handleAPI(w, edit, "admin")
		done <- w
	}()
	if _, err := io.WriteString(writer, "{"); err != nil {
		t.Fatal(err)
	}
	const replace = `{"fileName":"default.json","activate":true,"content":{"name":"new plan","servers":[{"id":"shared","name":"preserved","bindPort":7100}]}}`
	w := httptest.NewRecorder()
	app.handleAPI(w, httptest.NewRequest(http.MethodPost, "/api/configs/import-json", strings.NewReader(replace)), "admin")
	if w.Code != http.StatusOK {
		t.Fatalf("activate replacement: %d %s", w.Code, w.Body.String())
	}
	const profile = `"id":"shared","name":"updated","bindPort":7200}`
	if _, err := io.WriteString(writer, profile); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	if response := <-done; response.Code != http.StatusConflict {
		t.Fatalf("edit spanning same-file replacement: %d %s", response.Code, response.Body.String())
	}
	_, doc := app.store.Snapshot()
	if len(doc.Servers) != 1 || doc.Servers[0].Name != "preserved" || doc.Servers[0].BindPort != 7100 {
		t.Fatalf("stale edit changed replacement plan: %+v", doc.Servers)
	}
	current := state()
	if current == original {
		t.Fatal("same-file replacement did not advance the state generation")
	}
	for _, tc := range []struct {
		method, generation string
		want               int
	}{
		{http.MethodGet, "", http.StatusMethodNotAllowed},
		{http.MethodPost, strconv.FormatUint(original, 10), http.StatusConflict},
		{http.MethodPost, strconv.FormatUint(current, 10), http.StatusOK},
		{http.MethodPost, "", http.StatusOK},
	} {
		req := httptest.NewRequest(tc.method, "/api/servers/save", strings.NewReader("{"+profile))
		req.Header.Set("X-Config-Generation", tc.generation)
		w := httptest.NewRecorder()
		app.handleAPI(w, req, "admin")
		if w.Code != tc.want {
			t.Errorf("%s generation=%q: got %d want %d", tc.method, tc.generation, w.Code, tc.want)
		}
	}
}

type adminTransportFunc func(*http.Request) (*http.Response, error)

func (f adminTransportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestLocalPolicyEditKeepsProcessOwnershipUntilApplied(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(doc *ConfigDocument) error {
		doc.Servers = []ServerProfile{{ID: "shared"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rt := &RuntimeSupervisor{store: store, procs: map[string]*managedProc{
		"shared": {id: "shared", kind: "frps", generation: store.activeGeneration()},
	}}
	called := false
	rt.httpClient = &http.Client{Transport: adminTransportFunc(func(r *http.Request) (*http.Response, error) {
		called = true
		_, doc := store.Snapshot()
		if policy, ok := doc.Servers[0].ConnPolicies["web"]; !ok || policy.Enabled {
			t.Error("runtime action ran before the disabled policy was persisted")
		}
		if rt.lifecycleMu.TryLock() {
			rt.lifecycleMu.Unlock()
			t.Error("activation could take ownership between persistence and the process action")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
	})}
	app := &App{store: store, runtime: rt}
	req := httptest.NewRequest(http.MethodPost, "/api/servers/conn-policy", strings.NewReader(`{"serverId":"shared","proxyName":"web","enabled":false}`))
	w := httptest.NewRecorder()
	app.handleAPI(w, req, "admin")
	if w.Code != http.StatusOK || !called {
		t.Fatalf("policy edit: http=%d, administered=%v", w.Code, called)
	}
}

func TestStateUsesOnlyRuntimeFromItsConfigGeneration(t *testing.T) {
	app, err := NewApp(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	old := app.store.activeGeneration()
	done := make(chan struct{})
	close(done)
	app.runtime.mu.Lock()
	app.runtime.procs["shared-server"] = &managedProc{id: "shared-server", kind: "frps", state: "running", generation: old, done: done}
	app.runtime.procs["shared-client"] = &managedProc{id: "shared-client", kind: "frpc", state: "running", generation: old, done: done}
	app.runtime.lastErrors["shared-error"] = instanceError{generation: old, message: "old error"}
	app.runtime.mu.Unlock()
	err = app.runtime.switchConfig(func() error {
		_, err := app.store.ImportConfigDocument("next", []byte(`{"servers":[{"id":"shared-server"}],"clients":[{"id":"shared-client"},{"id":"shared-error"}]}`), true)
		if err != nil {
			return err
		}
		// Every ID has already been reused while the old runtime still exists.
		w := httptest.NewRecorder()
		app.handleAPI(w, httptest.NewRequest(http.MethodGet, "/api/state", nil), "admin")
		var state struct {
			Runtime map[string]ProcessRuntimeView `json:"runtime"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
			return err
		}
		for _, id := range []string{"shared-server", "shared-client", "shared-error"} {
			if view := state.Runtime[id]; view.State != "stopped" || view.Running || view.LastError != "" {
				t.Errorf("new plan showed old runtime for %s: %+v", id, view)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
