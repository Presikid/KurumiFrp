package manager

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegistrationAppliedDraftAndRedaction(t *testing.T) {
	a := testApp(t)
	i := seed(t, a)
	tunnel := Tunnel{ID: randomHex(8), InstanceID: i.ID, Name: "web", Type: "tcp", LocalHost: "127.0.0.1", LocalPort: 80, RemotePort: 6000, Enabled: true}
	state := a.store.DB.State.clone()
	state.Tunnels = append(state.Tunnels, tunnel)
	if err := a.store.commit(state, "test"); err != nil {
		t.Fatal(err)
	}
	s, err := newPrivateStatus(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.cleanup()
	key, err := tls.LoadX509KeyPair(filepath.Join(s.dir, "cert.pem"), filepath.Join(s.dir, "key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", s.port))
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "kurumi" || pass != s.password {
			http.Error(w, "no", 401)
			return
		}
		_, _ = w.Write([]byte(`{"tcp":[{"name":"web","type":"tcp","status":"running","remote_addr":"test-secret-1234"}]}`))
	})}
	go server.Serve(tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{key}}))
	defer server.Close()
	applied := a.store.DB.State.clone().Tunnels[0]
	signature, err := a.store.tunnelSignature(applied)
	if err != nil {
		t.Fatal(err)
	}
	p := &process{status: RuntimeStatus{State: "running"}, logs: newLogBuffer([]string{"test-secret-1234", s.password}), privateStatus: s, appliedTunnels: map[string]Tunnel{tunnel.ID: applied}, appliedTunnelSignatures: map[string][32]byte{tunnel.ID: signature}}
	a.mu.Lock()
	a.procs[i.ID] = p
	a.mu.Unlock()
	defer func() { a.mu.Lock(); delete(a.procs, i.ID); a.mu.Unlock() }()
	if w := request(a, "GET", "/api/registration", nil, "", ""); w.Code != 401 {
		t.Fatalf("unauthenticated %d", w.Code)
	}
	read := func() (registrationItem, string) {
		w := request(a, "GET", "/api/registration", nil, a.AccessToken(), "")
		if w.Code != 200 {
			t.Fatalf("registration %d %s", w.Code, w.Body.String())
		}
		var body struct {
			Tunnels map[string]registrationItem `json:"tunnels"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.Tunnels[tunnel.ID], w.Body.String()
	}
	item, body := read()
	if item.State != "registered" || strings.Contains(body, "test-secret-1234") || strings.Contains(body, s.password) {
		t.Fatalf("unsafe state %#v %s", item, body)
	}
	if got := safeStatusText(strings.Repeat("x", 4090)+"test-secret-1234", []string{"test-secret-1234"}); got != "[状态内容过长，已隐藏]" {
		t.Fatalf("oversized status must be hidden: %q", got)
	}
	a.mu.Lock()
	state = a.store.DB.State.clone()
	state.Tunnels[0].LocalPort = 81
	err = a.store.commit(state, "draft change")
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	item, _ = read()
	if item.State != "pending" {
		t.Fatalf("changed draft misreported %#v", item)
	}
	a.mu.Lock()
	state = a.store.DB.State.clone()
	state.Tunnels[0].Enabled = false
	err = a.store.commit(state, "disabled draft")
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	item, _ = read()
	if item.State != "pending-stop" {
		t.Fatalf("disabled active rule misreported %#v", item)
	}
}
