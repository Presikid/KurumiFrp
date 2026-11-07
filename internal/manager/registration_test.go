package manager

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestEnableClientRegistration(t *testing.T) {
	base := Rendered{Text: "serverAddr = \"127.0.0.1\"\n", Env: []string{"OTHER=ok"}, Digest: "original"}
	got, port, password, err := enableClientRegistration(base)
	if err != nil {
		t.Fatal(err)
	}
	if port == 0 || len(password) != 64 || got.Digest != base.Digest {
		t.Fatalf("invalid ephemeral status config: port=%d digest=%s", port, got.Digest)
	}
	if !strings.Contains(got.Text, "webServer.addr = \"127.0.0.1\"") ||
		!strings.Contains(got.Text, ".Envs.KURUMI_STATUS_PASSWORD_TOML") ||
		strings.Contains(got.Text, password) ||
		!strings.Contains(strings.Join(got.Env, "\n"), "KURUMI_STATUS_PASSWORD_TOML=") {
		t.Fatal("status config leaked credentials or did not enable loopback auth")
	}
}

func TestFetchClientRegistration(t *testing.T) {
	const password = "fixture-ephemeral-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pwd, ok := r.BasicAuth()
		if user != "kurumi" || pwd != password || !ok {
			t.Error("missing status Basic authentication")
		}
		if r.URL.Path != "/api/status" || r.Method != http.MethodGet {
			t.Error("unexpected status endpoint", r.URL.String())
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"tcp": []map[string]string{
			{"name": "public-ssh", "type": "tcp", "status": "running", "remote_addr": "example.test:5001"},
			{"name": "broken", "type": "tcp", "status": "start error", "err": "port already used"},
		}})
	}))
	defer server.Close()
	_, p, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(p)
	status, err := fetchClientRegistration(context.Background(), port, password)
	if err != nil {
		t.Fatal(err)
	}
	if status["tcp\x00public-ssh"].RemoteAddr != "example.test:5001" ||
		status["tcp\x00broken"].Err != "port already used" {
		t.Fatalf("unexpected proxy map: %#v", status)
	}
}

func TestRegistrationStateAndAuthentication(t *testing.T) {
	a := testApp(t)
	i := seed(t, a)
	st := a.store.DB.State.clone()
	tunnel := Tunnel{ID: randomHex(8), Name: "first", InstanceID: i.ID, Type: "tcp",
		LocalHost: "127.0.0.1", LocalPort: 1234, RemotePort: 5001, Enabled: true}
	st.Tunnels = append(st.Tunnels, tunnel)
	if err := a.store.commit(st, "add tunnel"); err != nil {
		t.Fatal(err)
	}
	if w := request(a, "GET", "/api/registration", nil, "", ""); w.Code != http.StatusUnauthorized {
		t.Fatal("unauthenticated status allowed", w.Code)
	}
	w := request(a, "GET", "/api/registration", nil, a.AccessToken(), "")
	if w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	var result RegistrationReport
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Tunnels[tunnel.ID].State != "stopped" || result.CheckedAt == "" {
		t.Fatalf("wrong stopped state: %#v", result)
	}
	w = request(a, "POST", "/api/registration", map[string]any{}, a.AccessToken(), "")
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatal("mutation allowed", w.Code)
	}
}

func TestRegistrationReportsOldAppliedConfig(t *testing.T) {
	a := testApp(t)
	i := seed(t, a)
	st := a.store.DB.State.clone()
	tunnel := Tunnel{ID: randomHex(8), Name: "first", InstanceID: i.ID, Type: "tcp",
		LocalHost: "127.0.0.1", LocalPort: 1234, RemotePort: 5001, Enabled: true}
	st.Tunnels = append(st.Tunnels, tunnel)
	if err := a.store.commit(st, "add tunnel"); err != nil {
		t.Fatal(err)
	}
	// Synthetic managed process: the query must read only the captured applied
	// rules, not assume that the current draft was ever activated.
	p := &process{
		status: RuntimeStatus{State: "running", Applied: "old-digest"},
		applied: map[string]appliedTunnel{tunnel.ID: {Name: tunnel.Name, Type: tunnel.Type}},
		logs: &logBuffer{},
	}
	a.procs[i.ID] = p
	tunnel.Name = "renamed"
	st.Tunnels[0] = tunnel
	if err := a.store.commit(st, "rename draft"); err != nil {
		t.Fatal(err)
	}
	got := a.registration(context.Background()).Tunnels[tunnel.ID]
	if got.State != "pending" || !got.Pending {
		t.Fatalf("draft incorrectly reported as registered: %#v", got)
	}
	// No child process exists in this fixture; avoid Close attempting a stop.
	delete(a.procs, i.ID)
}

func TestRegistrationRedaction(t *testing.T) {
	s := safeRegistrationText("secret\n"+strings.Repeat("x", 500), []string{"secret"})
	if strings.Contains(s, "secret") || strings.Contains(s, "\n") || len([]rune(s)) > 201 {
		t.Fatalf("status details were not sanitized: %q", s)
	}
}
