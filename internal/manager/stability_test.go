package manager

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The staged executable is a copy of this test binary. Only the child used as
// frpc/frps answers the version probe; the tests never launch an installer.
func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "-v" && os.Getenv("KURUMI_TEST_CORE_VERSION") != "" {
		fmt.Println(os.Getenv("KURUMI_TEST_CORE_VERSION"))
		return
	}
	os.Exit(m.Run())
}

func TestConfigActivationOwnsTraffic(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Update(func(d *ConfigDocument) error { d.Servers = []ServerProfile{{ID: "shared"}}; return nil }); err != nil {
		t.Fatal(err)
	}
	rs, err := NewRuntimeSupervisor(dir, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rs.StopAll()
	done := make(chan struct{})
	close(done)
	p := &managedProc{id: "shared", kind: "frps", done: done, state: "running", generation: s.activeGeneration(), conns: map[string]ConnLiveItem{"web": {TotalInBytes: 1234}}, rateSamples: map[string]proxyRateSample{"web": {inBytes: 1234}}}
	rs.mu.Lock()
	rs.procs[p.id] = p
	rs.mu.Unlock()
	old, _ := s.Snapshot()
	err = rs.switchConfig(func() error {
		_, e := s.ImportConfigDocument("next", []byte(`{"servers":[{"id":"shared"}]}`), true)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = rs.persistTrafficDeltas(p, map[string][4]int64{"web": {9000, 0, 9000, 0}}); err != nil {
		t.Fatal(err)
	}
	active, next := s.Snapshot()
	if active != "next.json" || len(next.Servers[0].ConnPolicies) != 0 {
		t.Fatalf("old traffic leaked into %s: %+v", active, next.Servers)
	}
	previous, err := readConfigDocument(filepath.Join(s.configsDir, old))
	if err != nil || previous.Servers[0].ConnPolicies["web"].RecordedInBytes != 1234 {
		t.Fatalf("old plan did not retain traffic: %+v %v", previous, err)
	}
}

func TestConfigActivationFailureKeepsDiskAndMemory(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ImportConfigDocument("second", []byte(`{"name":"original"}`), false); err != nil {
		t.Fatal(err)
	}
	active, _ := s.Snapshot()
	existing := filepath.Join(s.configsDir, "second.json")
	before, err := os.ReadFile(existing)
	if err != nil {
		t.Fatal(err)
	}
	s.metaPath = filepath.Join(existing, "invalid-parent")
	if _, err = s.ImportConfigDocument("second", []byte(`{"name":"replacement"}`), true); err == nil {
		t.Fatal("expected metadata error")
	}
	if _, err = s.SelectOrCreateConfig("third", "third"); err == nil {
		t.Fatal("expected metadata error")
	}
	after, err := os.ReadFile(existing)
	if err != nil || string(before) != string(after) {
		t.Fatalf("inactive plan changed: %v", err)
	}
	if _, err = os.Stat(filepath.Join(s.configsDir, "third.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed creation left a plan")
	}
	current, _ := s.Snapshot()
	restored, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	onDisk, _ := restored.Snapshot()
	if current != active || onDisk != active {
		t.Fatalf("memory=%s disk=%s want=%s", current, onDisk, active)
	}
}

func TestTOMLIntegerRoundTrip(t *testing.T) {
	srv, _, err := ParseTOMLAsProfile("s", "frps", "bindPort = 7_123\n[[allowPorts]]\nsingle=0x1_f90")
	if err != nil || srv.BindPort != 7123 || srv.AllowPorts != "8080" {
		t.Fatalf("server: %+v, %v", srv, err)
	}
	_, cli, err := ParseTOMLAsProfile("c", "frpc", "serverPort=7_123\n[[proxies]]\nname='p'\nlocalPort=0b1010\nremotePort=0o20")
	if err != nil || cli.ServerPort != 7123 || cli.Tunnels[0].LocalPort != 10 || cli.Tunnels[0].RemotePort != 16 {
		t.Fatalf("client: %+v, %v", cli, err)
	}
	for _, bad := range []string{"7__123", "0x_ff", "123_", "012", "9223372036854775808", "'7123'"} {
		if _, _, err := ParseTOMLAsProfile("s", "frps", "bindPort="+bad); err == nil || !strings.Contains(err.Error(), "bindPort") {
			t.Errorf("invalid port accepted: %s, %v", bad, err)
		}
	}
}

func TestCorePairActivationAndAlreadyLatest(t *testing.T) {
	const version = "99.0.1"
	t.Setenv("KURUMI_TEST_CORE_VERSION", version)
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	activate, err := stageCorePair(ctx, bin, version, raw, raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	slots := filepath.Join(bin, ".kurumi-cores")
	if active, err := readCoreSlot(slots); err != nil || active != "" {
		t.Fatalf("uncommitted slot active: %s %v", active, err)
	}
	if err = activate(); err != nil {
		t.Fatal(err)
	}
	// Force the second write to fail after frpc has already been staged in b.
	if err = os.MkdirAll(filepath.Join(slots, "b", binaryFileName("frps")), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err = stageCorePair(ctx, bin, version, raw, raw, nil); err == nil {
		t.Fatal("expected inactive slot write failure")
	}
	info := LocateBinaries(dir)
	for _, path := range []string{info.FrpcPath, info.FrpsPath} {
		if filepath.Dir(path) != filepath.Join(slots, "a") {
			t.Fatalf("mixed active pair: %+v", info)
		}
	}
	u := NewUpdateManager(dir, nil, nil)
	u.releases[frpRepo] = cachedRelease{release: &ghRelease{TagName: "v" + version}, expires: time.Now().Add(time.Minute)}
	_, count, err := u.ApplyFRPUpdate(ctx, nil)
	if !errors.Is(err, errFRPUpToDate) || count != 0 {
		t.Fatalf("same-version update: %d %v", count, err)
	}
}

func TestHotSwapReportsFailedInstance(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Update(func(d *ConfigDocument) error {
		d.Servers = []ServerProfile{{ID: "broken", AllowPorts: "invalid"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rs, err := NewRuntimeSupervisor(dir, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rs.StopAll()
	done := make(chan struct{})
	close(done)
	rs.mu.Lock()
	rs.procs["broken"] = &managedProc{id: "broken", state: "running", done: done}
	rs.mu.Unlock()
	count, err := rs.HotSwapBinaries(func() error { return nil })
	if count != 0 || err == nil || !strings.Contains(err.Error(), "broken") || !strings.Contains(err.Error(), "已恢复 0") {
		t.Fatalf("restore result: %d %v", count, err)
	}
}

func TestHotSwapChecksAdminReadiness(t *testing.T) {
	exited := make(chan struct{})
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/serverinfo" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		select {
		case <-exited:
		default:
			close(exited)
		}
	}))
	defer h.Close()
	port, err := strconv.Atoi(strings.TrimPrefix(h.URL, "http://127.0.0.1:"))
	if err != nil {
		t.Fatal(err)
	}
	rs := &RuntimeSupervisor{httpClient: h.Client(), procs: map[string]*managedProc{"s": {kind: "frps", adminPort: port, done: exited}}}
	if err := rs.waitForAdmin("s"); err == nil {
		t.Fatal("exited process reported healthy")
	}
}
