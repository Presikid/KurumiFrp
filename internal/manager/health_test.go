package manager

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestRenderRuntimeAddsPrivateFRPCAdminAPI(t *testing.T) {
	a := testApp(t)
	i := seed(t, a)

	runtimeConfig, err := a.store.renderRuntime(i.ID)
	if err != nil {
		t.Fatal(err)
	}
	baseConfig, err := a.store.render(i.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := a.store.render(i.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if runtimeConfig.AdminPort < 1 || runtimeConfig.AdminPort > 65535 {
		t.Fatalf("invalid loopback admin port: %d", runtimeConfig.AdminPort)
	}
	for _, want := range []string{
		\`webServer.addr = "127.0.0.1"\`,
		"webServer.port = ",
		\`webServer.user = "kurumi"\`,
		\`webServer.password = {{ .Envs.KURUMI_ADMIN_PASSWORD }}\`,
	} {
		if !strings.Contains(runtimeConfig.Text, want) {
			t.Fatalf("runtime config is missing %q", want)
		}
	}
	if strings.Contains(runtimeConfig.Text, runtimeConfig.AdminPassword) {
		t.Fatal("admin password was written into the config file")
	}
	if !strings.Contains(strings.Join(runtimeConfig.Env, "\n"), "KURUMI_ADMIN_PASSWORD=") {
		t.Fatal("admin password was not injected through the child environment")
	}
	if runtimeConfig.Digest != baseConfig.Digest {
		t.Fatal("ephemeral admin credentials changed the draft digest")
	}
	next := a.store.DB.State.clone()
	next.Tunnels = append(next.Tunnels, Tunnel{
		ID: randomHex(8), InstanceID: i.ID, Name: "health-proxy",
		Type: "tcp", LocalHost: "127.0.0.1", LocalPort: 8080,
		RemotePort: 6000, Enabled: true,
	})
	if err := a.store.commit(next, "add proxy"); err != nil {
		t.Fatal(err)
	}
	withProxy, err := a.store.renderRuntime(i.ID)
	if err != nil {
		t.Fatal(err)
	}
	if root, proxy := strings.Index(withProxy.Text, "webServer.addr"), strings.Index(withProxy.Text, "[[proxies]]"); root < 0 || proxy < 0 || root >= proxy {
		t.Fatal("Admin API settings were nested in the last proxy TOML table")
	}
	if strings.Contains(preview.Text, "webServer.addr") || strings.Contains(preview.Text, "KURUMI_ADMIN_PASSWORD") {
		t.Fatal("internal admin endpoint leaked into the user-facing preview")
	}
	if !healthContainsString(runtimeConfig.Secrets, runtimeConfig.AdminPassword) {
		t.Fatal("admin password is not included in log redaction secrets")
	}
}

func TestHealthAPISeparatesRegistrationAndTargetHealth(t *testing.T) {
	a := testApp(t)
	instance := seed(t, a)

	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	_, targetPortText, err := net.SplitHostPort(target.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	targetPort, err := strconv.Atoi(targetPortText)
	if err != nil {
		t.Fatal(err)
	}

	state := a.store.DB.State.clone()
	state.Tunnels = []Tunnel{{
		ID: randomHex(8), InstanceID: instance.ID, Name: "web",
		Type: "tcp", LocalHost: "127.0.0.1", LocalPort: targetPort,
		RemotePort: 6000, Enabled: true,
	}}
	if err := a.store.commit(state, "add test tunnel"); err != nil {
		t.Fatal(err)
	}

	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "kurumi" || password != "private-admin-password" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/api/status" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"tcp\":[{\"name\":\"web\",\"type\":\"tcp\",\"status\":\"running\",\"err\":\"\",\"localAddr\":\"127.0.0.1:8080\",\"remoteAddr\":\"server.example:6000\"}]}"))
	}))
	defer admin.Close()
	_, adminPortText, err := net.SplitHostPort(strings.TrimPrefix(admin.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	adminPort, err := strconv.Atoi(adminPortText)
	if err != nil {
		t.Fatal(err)
	}

	proc := &process{
		status: RuntimeStatus{State: "running", PID: 4321, Started: now(), Applied: "test"},
		adminPort: adminPort, adminUser: "kurumi", adminPassword: "private-admin-password",
		logs: &logBuffer{secrets: []string{"test-secret-1234", "private-admin-password"}},
	}
	a.mu.Lock()
	a.procs[instance.ID] = proc
	a.mu.Unlock()
	t.Cleanup(func() {
		a.mu.Lock()
		delete(a.procs, instance.ID)
		a.mu.Unlock()
	})

	w := request(a, http.MethodGet, "/api/health?id="+instance.ID, nil, a.AccessToken(), "")
	if w.Code != http.StatusOK {
		t.Fatalf("health API failed: %d %s", w.Code, w.Body.String())
	}
	var report HealthReport
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Runtime.State != "running" {
		t.Fatalf("process state was not preserved: %#v", report.Runtime)
	}
	if report.AdminAPI.Status != "reachable" {
		t.Fatalf("frpc Admin API was not read: %#v", report.AdminAPI)
	}
	if len(report.Proxies) != 1 || report.Proxies[0].Registration != "registered" {
		t.Fatalf("proxy registration was not reported: %#v", report.Proxies)
	}
	if report.Proxies[0].Target.Status != "reachable" {
		t.Fatalf("local target health was not reported: %#v", report.Proxies[0].Target)
	}
	if report.Summary.Registered != 1 || report.Summary.TargetsReachable != 1 {
		t.Fatalf("summary does not match checks: %#v", report.Summary)
	}
	if strings.Contains(w.Body.String(), "test-secret-1234") || strings.Contains(w.Body.String(), "private-admin-password") {
		t.Fatal("diagnostic response exposed an authentication secret")
	}
}

func TestLocalBindProbeHost(t *testing.T) {
	cases := []struct {
		host string
		want string
		ok   bool
	}{
		{host: "0.0.0.0", want: "127.0.0.1", ok: true},
		{host: "::", want: "::1", ok: true},
		{host: "127.0.0.1", want: "127.0.0.1", ok: true},
		{host: "localhost", want: "127.0.0.1", ok: true},
		{host: "server.example", want: "", ok: false},
	}
	for _, test := range cases {
		got, ok := localBindProbeHost(test.host)
		if got != test.want || ok != test.ok {
			t.Fatalf("localBindProbeHost(%q) = %q, %v; want %q, %v", test.host, got, ok, test.want, test.ok)
		}
	}
}

func TestVisitorTargetIsNotProbedAsAService(t *testing.T) {
	got := probeTarget(Tunnel{Type: "stcp-visitor", LocalHost: "127.0.0.1", LocalPort: 6000})
	if got.Status != "unsupported" || !strings.Contains(got.Message, "监听") {
		t.Fatalf("visitor bind address was treated as a backend target: %#v", got)
	}
}

func healthContainsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
