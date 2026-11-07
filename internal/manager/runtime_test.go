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
	if before.State != "running" || before.PID < 1 {
		t.Fatal(before)
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
