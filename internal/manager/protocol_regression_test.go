package manager

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestPluginRequestValidation(t *testing.T) {
	rs := &RuntimeSupervisor{procs: map[string]*managedProc{
		"server": {kind: "frps", pluginSecret: "token"},
		"client": {kind: "frpc"},
	}}
	for _, tc := range []struct {
		method, route, query, body string
		want                       int
	}{
		{"GET", "server/token", "", "", 405},
		{"POST", "client", "", `{"op":"Login","content":{}}`, 404},
		{"POST", "server/invalid", "", `{"op":"Login","content":{}}`, 403},
		{"POST", "server/token", "?op=Login", `{}`, 400},
		{"POST", "server/token", "?op=Login", `{"op":"Ping","content":{}}`, 400},
		{"POST", "server/token", "", `{"content":{}}`, 400},
		{"POST", "server/token", "", `{"op":"Login","content":{}}{}`, 400},
		{"POST", "server/token", "", `{"op":"Login","content":{}}`, 200},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(tc.method, "/"+tc.route+tc.query, strings.NewReader(tc.body))
		rs.HandleFRPSPlugin(w, r, tc.route)
		if w.Code != tc.want {
			t.Errorf("%s %s%s body %s: got %d, want %d", tc.method, tc.route, tc.query, tc.body, w.Code, tc.want)
		}
	}
}

func TestPollDistinguishesEmptyAndUnavailableProxyTypes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/proxy/tcp":
			fmt.Fprint(w, `{"proxies":[]}`)
		case "/api/proxy/udp":
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"proxies":[]}`)
		case "/api/proxy/https":
			fmt.Fprint(w, `{}`)
		default:
			fmt.Fprint(w, `{"proxies":[]}`)
		}
	}))
	defer srv.Close()
	p := &managedProc{adminPort: srv.Listener.Addr().(*net.TCPAddr).Port, conns: map[string]ConnLiveItem{
		"web": {ProxyType: "tcp"}, "dns": {ProxyType: "udp"}, "tls": {ProxyType: "https"},
	}}
	rs := &RuntimeSupervisor{httpClient: srv.Client()}
	if !rs.pollServerAdmin(p, ServerProfile{}) || len(p.conns) != 2 || p.conns["dns"].ProxyType != "udp" || p.conns["tls"].ProxyType != "https" {
		t.Fatalf("unexpected poll result: %+v", p.conns)
	}
}

func TestSwapRollsBackOnlyAfterSuccessfulActivation(t *testing.T) {
	for _, failActivation := range []bool{false, true} {
		dir := t.TempDir()
		s, err := NewStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Update(func(d *ConfigDocument) error {
			d.Servers = []ServerProfile{{ID: "invalid", AllowPorts: "invalid"}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		close(done)
		rs := &RuntimeSupervisor{store: s, procs: map[string]*managedProc{"invalid": {id: "invalid", done: done, state: "running"}}, lastErrors: make(map[string]instanceError)}
		called := false
		rollbackErr := errors.New("marker write failed")
		n, err := rs.HotSwapBinaries(func() error {
			if failActivation {
				return errors.New("activation failed")
			}
			return nil
		}, func() error {
			called = true
			return rollbackErr
		})
		if err == nil || n != 0 || called == failActivation || errors.Is(err, rollbackErr) != called {
			t.Errorf("failActivation=%t, rollback=%t, count=%d: %v", failActivation, called, n, err)
		}
	}
}

func TestTrafficFlushRespectsResetCheckpoint(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Update(func(d *ConfigDocument) error {
		d.Servers = []ServerProfile{{ID: "s", ConnPolicies: map[string]ConnPolicy{
			"web": {ProxyName: "web", Enabled: true, LastRawInBytes: 1024, LastRawOutBytes: 2048},
		}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	p := &managedProc{id: "s", generation: s.activeGeneration(),
		conns:       map[string]ConnLiveItem{"web": {TotalInBytes: 991024, TotalOutBytes: 992048}},
		rateSamples: map[string]proxyRateSample{"web": {inBytes: 1024, outBytes: 2048}}}
	rs := &RuntimeSupervisor{store: s, procs: map[string]*managedProc{"s": p}}
	for _, sample := range []proxyRateSample{{inBytes: 1024, outBytes: 2048}, {inBytes: 1040, outBytes: 2080}, {inBytes: 5, outBytes: 7}} {
		p.rateSamples["web"] = sample
		if err := rs.flushServerTrafficSnapshot("s"); err != nil {
			t.Fatal(err)
		}
	}
	_, doc := s.Snapshot()
	if pol := doc.Servers[0].ConnPolicies["web"]; pol.RecordedInBytes != 21 || pol.RecordedOutBytes != 39 {
		t.Fatalf("flush restored traffic from before reset or lost a rollover: %+v", pol)
	}
}

func TestImportTOMLWithCommentedHeadersAndUnmodeledFields(t *testing.T) {
	_, cli, err := ParseTOMLAsProfile("client", "frpc", `featureGates = { VirtualNet = false }
[transport] # official transport settings
protocol = "tcp"
wireProtocol = "v1"
[[proxies]] # first tunnel
name = "web#1"
type = "tcp"
remotePort = 10001
`)
	if err != nil || len(cli.Tunnels) != 1 || cli.Tunnels[0].Name != "web#1" {
		t.Fatalf("commented proxy header: %+v, %v", cli, err)
	}
	out := RenderClientTOML(*cli, ClientRuntimeInjections{Preview: true}).TOML
	for _, field := range []string{`featureGates = { VirtualNet = false }`, `transport.wireProtocol = "v1"`} {
		if !strings.Contains(out, field) {
			t.Errorf("missing %s in client output: %s", field, out)
		}
	}
	srv, _, err := ParseTOMLAsProfile("server", "frps", "[log] # output\nto='console'\n[[allowPorts]] # permitted\nsingle=8000")
	if err != nil || srv.AllowPorts != "8000" || !strings.Contains(srv.ExtraTOML, "log.to = 'console'") {
		t.Fatalf("server tables lost during import: %+v, %v", srv, err)
	}
}

func officialTestRuntime(t *testing.T, cores ...string) *RuntimeSupervisor {
	t.Helper()
	src := os.Getenv("KURUMI_TEST_FRP_DIR")
	if src == "" {
		t.Skip("set KURUMI_TEST_FRP_DIR to run with official FRP binaries")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	for _, core := range cores {
		name := binaryFileName(core)
		raw, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bin, name), raw, 0700); err != nil {
			t.Fatal(err)
		}
	}
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	rs, err := NewRuntimeSupervisor(dir, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rs.StopAll)
	return rs
}

func TestOfficialFRPSRestoreAfterPartialUpgrade(t *testing.T) {
	rs := officialTestRuntime(t, "frps")
	var profiles []ServerProfile
	for _, id := range []string{"first", "second"} {
		port, err := pickFreeLoopbackPort()
		if err != nil {
			t.Fatal(err)
		}
		profile := ServerProfile{ID: id, BindAddr: "127.0.0.1", BindPort: port}
		if err := rs.StartServer(profile); err != nil {
			t.Fatal(err)
		}
		if err := rs.waitForAdmin(id); err != nil {
			t.Fatal(err)
		}
		profiles = append(profiles, profile)
	}
	if err := rs.store.Update(func(d *ConfigDocument) error { d.Servers = profiles; return nil }); err != nil {
		t.Fatal(err)
	}
	var reserved net.Listener
	defer func() {
		if reserved != nil {
			reserved.Close()
		}
	}()
	var newCorePID int
	n, err := rs.HotSwapBinaries(func() error {
		var err error
		reserved, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", profiles[1].BindPort))
		return err
	}, func() error {
		newCorePID = rs.Views(ConfigDocument{Servers: profiles})["first"].PID
		return reserved.Close()
	})
	if err == nil || n != 2 || newCorePID == 0 || !strings.Contains(err.Error(), "已回退") {
		t.Fatalf("partial restore and rollback: restored %d, intermediate PID %d, err %v", n, newCorePID, err)
	}
	views := rs.Views(ConfigDocument{Servers: profiles})
	if views["first"].PID == newCorePID || !views["first"].Running || !views["second"].Running {
		t.Fatalf("expected both instances restored on original core: %+v", views)
	}
}

func TestOfficialConcurrentBridgeOwnership(t *testing.T) {
	rs := officialTestRuntime(t, "frps", "frpc")
	port, err := pickFreeLoopbackPort()
	if err != nil {
		t.Fatal(err)
	}
	server := ServerProfile{ID: "s", BindAddr: "127.0.0.1", BindPort: port}
	if err := rs.StartServer(server); err != nil {
		t.Fatal(err)
	}
	if err := rs.waitForAdmin(server.ID); err != nil {
		t.Fatal(err)
	}
	rs.mu.RLock()
	owner := rs.procs[server.ID]
	rs.mu.RUnlock()
	sess := remoteClientSession{clientID: "c", ctrlProxyName: "__kurumi_ctrl_c", ctrlSecret: "test"}
	var wg sync.WaitGroup
	var ports [2]int
	for i := range ports {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var err error
			ports[i], err = rs.ensureVisitorBridge(server, sess, owner)
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if ports[0] == 0 || ports[0] != ports[1] {
		t.Fatalf("parallel requests created different bridge processes: %v", ports)
	}
	rs.bridgeMu.Lock()
	old := rs.bridges["s::c"]
	rs.bridgeMu.Unlock()
	sess.ctrlSecret = "changed"
	if _, err := rs.ensureVisitorBridge(server, sess, owner); err != nil {
		t.Fatal(err)
	}
	rs.bridgeMu.Lock()
	current := rs.bridges["s::c"]
	rs.bridgeMu.Unlock()
	if old == current || old.cfgPath == current.cfgPath {
		t.Fatal("replaced bridge reused its predecessor's config")
	}
	if _, err := os.Stat(current.cfgPath); err != nil {
		t.Fatal(err)
	}
	if err := rs.StopInstance(server.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := rs.ensureVisitorBridge(server, sess, owner); err == nil {
		t.Fatal("bridge was created after its owner exited")
	}
}
