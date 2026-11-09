package manager

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// A subprocess fixture exercises lifecycle management, not the frp protocol.
func TestMain(m *testing.M) {
	if os.Getenv("FRP_TEST_HELPER") == "1" {
		if len(os.Args) > 1 && os.Args[1] == "verify" {
			if marker := os.Getenv("FRP_TEST_VERIFY_MARKER"); marker != "" {
				if err := os.WriteFile(marker, []byte("entered"), 0600); err != nil {
					os.Exit(2)
				}
				for {
					if _, err := os.Stat(marker + ".release"); err == nil {
						break
					}
					time.Sleep(20 * time.Millisecond)
				}
			}
			if strings.Contains(os.Getenv("KURUMI_AUTH_TOML"), "invalid-verify") {
				fmt.Println(os.Getenv("KURUMI_AUTH_TOML"))
				os.Exit(1)
			}
			fmt.Println("configuration verified by fixture")
			os.Exit(0)
		}
		fmt.Println("fixture child started")
		for {
			time.Sleep(time.Second)
		}
	}
	os.Exit(m.Run())
}
func TestOwnedProcessLifecycle(t *testing.T) {
	a := testApp(t)
	i := seed(t, a)
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(exe)
	if e != nil {
		t.Fatal(e)
	}
	name := "frpc"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if e = os.WriteFile(filepath.Join(a.bin, name), b, 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("FRP_TEST_HELPER", "1")
	w := request(a, "POST", "/api/control", map[string]any{"revision": a.store.DB.Revision, "id": i.ID, "action": "start"}, a.AccessToken(), "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	before := a.procs[i.ID].view()
	if before.State != "running" || before.PID < 1 || before.RunID == "" {
		t.Fatal(before)
	}
	status := a.procs[i.ID].privateStatus
	if status == nil {
		t.Fatal("private status missing")
	}
	for _, endpoint := range []string{"/api/config?id=" + i.ID, "/api/export?scope=all", "/api/logs?id=" + i.ID} {
		response := request(a, "GET", endpoint, nil, a.AccessToken(), "")
		if response.Code != 200 || strings.Contains(response.Body.String(), status.password) || strings.Contains(response.Body.String(), status.dir) {
			t.Fatalf("status credential or path leaked via %s", endpoint)
		}
	}
	runtimeText, err := os.ReadFile(filepath.Join(a.store.dir, "runtime", i.ID+".toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(runtimeText), status.password) {
		t.Fatal("status password in runtime file")
	}
	if a.statuses()[i.ID].Pending {
		t.Fatal("freshly applied config pending")
	}
	pub := a.store.DB.State.public().Instances[0]
	pub.Token = "invalid-verify-123"
	w = request(a, "POST", "/api/save", map[string]any{"revision": a.store.DB.Revision, "kind": "instance", "value": pub}, a.AccessToken(), "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if !a.statuses()[i.ID].Pending {
		t.Fatal("draft change not shown")
	}
	w = request(a, "POST", "/api/control", map[string]any{"revision": a.store.DB.Revision, "id": i.ID, "action": "restart"}, a.AccessToken(), "")
	if w.Code != 400 || strings.Contains(w.Body.String(), pub.Token) {
		t.Fatal("failed verify leaked or accepted", w.Body.String())
	}
	if a.procs[i.ID].view().PID != before.PID {
		t.Fatal("failed verification killed running process")
	}
	w = request(a, "POST", "/api/restore", map[string]any{"revision": a.store.DB.Revision, "target": 1}, a.AccessToken(), "")
	if w.Code != 400 {
		t.Fatal("restore allowed with running process")
	}
	w = request(a, "POST", "/api/control", map[string]any{"revision": a.store.DB.Revision, "id": i.ID, "action": "stop"}, a.AccessToken(), "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if a.procs[i.ID].view().State == "running" {
		t.Fatal("child not stopped")
	}
	if _, err := os.Stat(status.dir); !os.IsNotExist(err) {
		t.Fatalf("status certificate directory retained: %v", err)
	}
}

func autoStartFixture(t *testing.T, installBinary bool) *App {
	t.Helper()
	dir, bin := t.TempDir(), t.TempDir()
	if installBinary {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		payload, err := os.ReadFile(exe)
		if err != nil {
			t.Fatal(err)
		}
		name := "frpc"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		if err = os.WriteFile(filepath.Join(bin, name), payload, 0700); err != nil {
			t.Fatal(err)
		}
	}
	store, err := newStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := store.encrypt("autostart-test-secret")
	if err != nil {
		t.Fatal(err)
	}
	state := store.DB.State.clone()
	state.Instances = append(state.Instances, Instance{
		ID: randomHex(8), Name: "Auto start fixture", Kind: "frpc",
		Host: "127.0.0.1", Port: 7000, Token: secret, AutoStart: true,
	})
	if err = store.commit(state, "seed auto-start instance"); err != nil {
		t.Fatal(err)
	}
	app, err := New(dir, bin, "127.0.0.1:9000", fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}})
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func TestAutoStartRestoresOptedInInstances(t *testing.T) {
	t.Setenv("FRP_TEST_HELPER", "1")
	a := autoStartFixture(t, true)
	defer a.Close()
	<-a.startupDone
	i := a.store.DB.State.Instances[0]
	p := a.procs[i.ID]
	if p == nil || p.view().State != "running" || p.view().PID < 1 {
		t.Fatalf("auto-start did not launch the instance: %#v", a.statuses()[i.ID])
	}
	if status := a.statuses()[i.ID]; status.Pending {
		t.Fatal("auto-started instance is unexpectedly pending")
	}
}

func TestAutoStartFailureIsVisibleAndDoesNotClaimRunning(t *testing.T) {
	a := autoStartFixture(t, false)
	defer a.Close()
	<-a.startupDone
	i := a.store.DB.State.Instances[0]
	status := a.statuses()[i.ID]
	if status.State != "autostart-failed" || status.Exit == "" || status.PID != 0 {
		t.Fatalf("startup failure was not reported accurately: %#v", status)
	}
	if a.procs[i.ID] != nil {
		t.Fatal("failed auto-start created a process")
	}
}

func waitForVerifier(t *testing.T, marker string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("automatic verification did not start")
}

func TestAutoStartDoesNotApplyStaleConfiguration(t *testing.T) {
	t.Setenv("FRP_TEST_HELPER", "1")
	marker := filepath.Join(t.TempDir(), "verify")
	t.Setenv("FRP_TEST_VERIFY_MARKER", marker)
	a := autoStartFixture(t, true)
	defer a.Close()
	waitForVerifier(t, marker)
	i := a.store.DB.State.public().Instances[0]
	i.Host = "localhost"
	w := request(a, "POST", "/api/save", map[string]any{"revision": a.store.DB.Revision, "kind": "instance", "value": i}, a.AccessToken(), "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if err := os.WriteFile(marker+".release", []byte("ok"), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.startupDone:
	case <-time.After(5 * time.Second):
		t.Fatal("startup did not finish")
	}
	if a.procs[i.ID] != nil {
		t.Fatal("stale instance was started")
	}
	if s := a.statuses()[i.ID]; s.State != "autostart-failed" {
		t.Fatalf("stale start not reported: %#v", s)
	}
}

func TestCloseCancelsAutomaticVerification(t *testing.T) {
	t.Setenv("FRP_TEST_HELPER", "1")
	marker := filepath.Join(t.TempDir(), "verify")
	t.Setenv("FRP_TEST_VERIFY_MARKER", marker)
	a := autoStartFixture(t, true)
	waitForVerifier(t, marker)
	done := make(chan error, 1)
	go func() { done <- a.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not cancel verifier")
	}
	if len(a.procs) != 0 {
		t.Fatal("process launched during shutdown")
	}
}
