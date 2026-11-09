package manager

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
)

func TestHealthUsesLocalProbeAndKeepsSecretsOut(t *testing.T) {
	a := testApp(t)
	i := seed(t, a)
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	st := a.store.DB.State.clone()
	st.Tunnels = append(st.Tunnels, Tunnel{ID: randomHex(8), InstanceID: i.ID, Name: "test-secret-1234", Type: "tcp", LocalHost: "127.0.0.1", LocalPort: port, RemotePort: 6000, Enabled: true})
	if err := a.store.commit(st, "test"); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"revision": a.store.DB.Revision, "id": i.ID}
	if w := request(a, "POST", "/api/health", body, "", ""); w.Code != 401 {
		t.Fatalf("unauthenticated %d", w.Code)
	}
	w := request(a, "POST", "/api/health", body, a.AccessToken(), "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "test-secret-1234") {
		t.Fatal("credential leaked into report")
	}
	var result healthReport
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Rules) != 1 || result.Rules[0].Target.Status != "reachable" || result.Rules[0].Registration.State != "stopped" {
		t.Fatalf("unexpected report %#v", result)
	}
	if w = request(a, "POST", "/api/health", map[string]any{"revision": 0, "id": i.ID}, a.AccessToken(), ""); w.Code != 409 {
		t.Fatal("stale revision accepted")
	}
}

func TestHealthLabelsDraftAndSharesProbeLimit(t *testing.T) {
	a := testApp(t)
	i := seed(t, a)
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	st := a.store.DB.State.clone()
	st.Tunnels = append(st.Tunnels, Tunnel{ID: randomHex(8), InstanceID: i.ID, Name: "web", Type: "tcp", LocalHost: "127.0.0.1", LocalPort: port, RemotePort: 6000, Enabled: true})
	if err = a.store.commit(st, "add"); err != nil {
		t.Fatal(err)
	}
	rendered, err := a.store.render(i.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	p := &process{status: RuntimeStatus{State: "running", Applied: rendered.Digest}, logs: newLogBuffer(nil), appliedTunnels: map[string]Tunnel{}, appliedTunnelSignatures: map[string][32]byte{}}
	a.procs[i.ID] = p
	defer func() { delete(a.procs, i.ID) }()
	st = a.store.DB.State.clone()
	st.Instances[0].Port = port
	if err = a.store.commit(st, "change endpoint"); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	a.activeProbes = 12
	a.mu.Unlock()
	w := request(a, "POST", "/api/health", map[string]any{"revision": a.store.DB.Revision, "id": i.ID}, a.AccessToken(), "")
	a.mu.Lock()
	a.activeProbes = 0
	a.mu.Unlock()
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var report healthReport
	if err = json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.ProbeScope != "draft" || !report.Pending || report.Endpoint.State != "busy" || report.Rules[0].Target.Status != "busy" {
		t.Fatalf("misleading scope or bypassed global limit: %#v", report)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result := probeTargetContext(ctx, st.Tunnels[0], nil); result.Status != "cancelled" {
		t.Fatalf("cancelled probe ran: %#v", result)
	}
}
