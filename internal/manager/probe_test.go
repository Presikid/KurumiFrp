package manager

import (
	"crypto/x509"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"
)

func probeAddress(t *testing.T, server *httptest.Server) (string, int) {
	t.Helper()
	addr, ok := server.Listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("unexpected listener address type %T", server.Listener.Addr())
	}
	return addr.IP.String(), addr.Port
}

func TestProbeTargetTCP(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	_, portText, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	got := probeTarget(Tunnel{Type: "tcp", LocalHost: "127.0.0.1", LocalPort: port})
	if got.Status != "reachable" || got.Address == "" || got.CheckedAt == "" {
		t.Fatalf("expected reachable target with timestamp, got %#v", got)
	}
}

func TestProbeTargetHTTPUsesConfiguredVHost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("expected HEAD request, got %s", r.Method)
		}
		if r.Host != "public.example.test" {
			t.Errorf("expected configured Host header, got %q", r.Host)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	host, port := probeAddress(t, server)

	got := probeTarget(Tunnel{
		Type: "http", LocalHost: host, LocalPort: port,
		Domains: []string{"public.example.test"},
	})
	if got.Status != "healthy" || got.HTTPStatus != http.StatusNoContent || got.Host != "public.example.test" {
		t.Fatalf("expected healthy HTTP response, got %#v", got)
	}
}

func TestProbeTargetHTTPReportsApplicationStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	host, port := probeAddress(t, server)

	got := probeTarget(Tunnel{Type: "http", LocalHost: host, LocalPort: port})
	if got.Status != "responding" || got.HTTPStatus != http.StatusServiceUnavailable {
		t.Fatalf("expected responding status with HTTP 503, got %#v", got)
	}
}

func TestProbeTargetHTTPSVerifiesTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	host, port := probeAddress(t, server)

	untrusted := probeTarget(Tunnel{Type: "https", LocalHost: host, LocalPort: port})
	if untrusted.Status != "unreachable" {
		t.Fatalf("self-signed certificate should not be trusted by default, got %#v", untrusted)
	}

	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	trusted := probeTargetWithRoots(Tunnel{Type: "https", LocalHost: host, LocalPort: port}, roots)
	if trusted.Status != "healthy" || trusted.HTTPStatus != http.StatusOK {
		t.Fatalf("expected HTTPS response with trusted test root, got %#v", trusted)
	}
}

func TestProbeTargetUnsupportedUDP(t *testing.T) {
	got := probeTarget(Tunnel{Type: "udp", LocalHost: "127.0.0.1", LocalPort: 53})
	if got.Status != "unsupported" {
		t.Fatalf("expected unsupported, got %#v", got)
	}
}

func TestProbeTargetInvalidPort(t *testing.T) {
	got := probeTarget(Tunnel{Type: "tcp", LocalHost: "127.0.0.1", LocalPort: 0})
	if got.Status != "invalid" {
		t.Fatalf("expected invalid, got %#v", got)
	}
}

func TestProbeAPIAuthenticationAndRevision(t *testing.T) {
	a := testApp(t)
	unauthorized := request(a, "POST", "/api/probe", map[string]any{"revision": a.store.DB.Revision, "id": "0123456789abcdef"}, "", "")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated probe returned %d", unauthorized.Code)
	}

	instance := seed(t, a)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_, portText, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(portText)

	state := a.store.DB.State.clone()
	state.Tunnels = append(state.Tunnels, Tunnel{
		ID: randomHex(8), InstanceID: instance.ID, Name: "probe-test",
		Type: "tcp", LocalHost: "127.0.0.1", LocalPort: port, RemotePort: 6000, Enabled: true,
	})
	if err := a.store.commit(state, "probe test"); err != nil {
		t.Fatal(err)
	}
	revision := a.store.DB.Revision
	id := a.store.DB.State.Tunnels[0].ID

	response := request(a, "POST", "/api/probe", map[string]any{"revision": revision, "id": id}, a.AccessToken(), "")
	if response.Code != http.StatusOK {
		t.Fatalf("probe API returned %d: %s", response.Code, response.Body.String())
	}
	var result ProbeResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "reachable" {
		t.Fatalf("expected reachable API result, got %#v", result)
	}

	stale := request(a, "POST", "/api/probe", map[string]any{"revision": revision - 1, "id": id}, a.AccessToken(), "")
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale revision returned %d", stale.Code)
	}
}

func TestProbeDoesNotHoldApplicationMutex(t *testing.T) {
	a := testApp(t)
	instance := seed(t, a)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	defer unblock()
	host, port := probeAddress(t, server)

	state := a.store.DB.State.clone()
	state.Tunnels = append(state.Tunnels, Tunnel{
		ID: randomHex(8), InstanceID: instance.ID, Name: "slow-probe",
		Type: "http", LocalHost: host, LocalPort: port, Enabled: true,
	})
	if err := a.store.commit(state, "slow probe test"); err != nil {
		t.Fatal(err)
	}
	revision := a.store.DB.Revision
	id := a.store.DB.State.Tunnels[0].ID

	probeDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		probeDone <- request(a, "POST", "/api/probe", map[string]any{"revision": revision, "id": id}, a.AccessToken(), "")
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("HTTP probe did not reach the test service")
	}

	stateDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		stateDone <- request(a, "GET", "/api/state", nil, a.AccessToken(), "")
	}()
	select {
	case response := <-stateDone:
		if response.Code != http.StatusOK {
			t.Fatalf("state request returned %d", response.Code)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("slow target probe blocked unrelated manager API requests")
	}

	unblock()
	select {
	case response := <-probeDone:
		if response.Code != http.StatusOK {
			t.Fatalf("probe request returned %d: %s", response.Code, response.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("probe request did not finish after target was released")
	}
}
