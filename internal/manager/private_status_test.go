package manager

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPrivateStatusAuthenticatedAndPinned(t *testing.T) {
	s, err := newPrivateStatus(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.cleanup()
	cert, err := tls.LoadX509KeyPair(filepath.Join(s.dir, "cert.pem"), filepath.Join(s.dir, "key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", s.port))
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "kurumi" || pass != s.password || r.URL.Path != "/api/status" {
			http.Error(w, "no", 401)
			return
		}
		_, _ = w.Write([]byte(`{"tcp":[{"name":"example","type":"tcp","status":"running"}]}`))
	})}
	go server.Serve(tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}))
	defer server.Close()
	got, err := s.read(context.Background())
	if err != nil || got["tcp\x00example"].Status != "running" {
		t.Fatalf("%#v, %v", got, err)
	}
	base := Rendered{Text: "serverPort = 7000\n", Digest: "base", SecretDigest: "secrets"}
	current := s.runtimeConfig(base)
	if strings.Contains(base.Text, s.password) || strings.Contains(current.Text, s.password) || current.Digest != base.Digest || current.SecretDigest != base.SecretDigest {
		t.Fatal("status credential entered text or changed digest")
	}
	if !strings.Contains(strings.Join(current.Env, "\n"), s.password) {
		t.Fatal("child lacks password")
	}
	path := s.dir
	s.cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("status files remain: %v", err)
	}
}

func TestPrivateStatusRealFRPC(t *testing.T) {
	binary := os.Getenv("KURUMI_STATUS_TEST_FRPC")
	if binary == "" {
		t.Skip("set KURUMI_STATUS_TEST_FRPC to an official frpc")
	}
	parent := filepath.Join(t.TempDir(), "root-&-'{{ .Envs.UNEXPECTED }}")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	s, err := newPrivateStatus(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer s.cleanup()
	base := Rendered{Text: "serverAddr = \"127.0.0.1\"\nserverPort = 1\nloginFailExit = false\n"}
	r := s.runtimeConfig(base)
	config := filepath.Join(t.TempDir(), "frpc.toml")
	if err := os.WriteFile(config, []byte(r.Text), 0600); err != nil {
		t.Fatal(err)
	}
	verify := exec.Command(binary, "verify", "-c", config)
	verify.Env = childEnv(r.Env)
	if out, err := verify.CombinedOutput(); err != nil {
		t.Fatalf("official verify: %v: %s; config: %s", err, out, r.Text)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, binary, "-c", config)
	cmd.Env = childEnv(r.Env)
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	defer func() { cancel(); _ = cmd.Wait() }()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err = s.read(context.Background()); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("official frpc TLS status did not respond: %v", err)
}

func TestPrivateStatusRejectsPortImpersonation(t *testing.T) {
	s, err := newPrivateStatus(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.cleanup()
	var hit atomic.Bool
	fake := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit.Store(true); _, _ = w.Write([]byte(`{}`)) }))
	defer fake.Close()
	s.port = fake.Listener.Addr().(*net.TCPAddr).Port
	if _, err = s.read(context.Background()); err == nil {
		t.Fatal("accepted an untrusted listener")
	}
	if hit.Load() {
		t.Fatal("sent HTTP authorization to an impersonator")
	}
}

func TestPrivateStatusPortCollisionReportsExit(t *testing.T) {
	binary := os.Getenv("KURUMI_STATUS_TEST_FRPC")
	if binary == "" {
		t.Skip("set KURUMI_STATUS_TEST_FRPC")
	}
	a := testApp(t)
	i := seed(t, a)
	s, err := newPrivateStatus(a.store.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.cleanup()
	occupied, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", s.port))
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	r := s.runtimeConfig(Rendered{Text: "serverAddr = \"127.0.0.1\"\nserverPort = 1\nloginFailExit = false\n"})
	path, err := a.prepareRuntimeConfig(i.ID, r)
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	err = a.startPrepared(i, r, binary, path, "", s)
	p := a.procs[i.ID]
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.done:
	case <-time.After(10 * time.Second):
		t.Fatal("frpc did not exit with status port occupied")
	}
	if state := p.view(); state.State != "exited" || !strings.Contains(state.Exit, "状态端口") {
		t.Fatalf("missing port-collision guidance: %#v", state)
	}
	if _, err = os.Stat(s.dir); !os.IsNotExist(err) {
		t.Fatalf("certificate directory remains: %v", err)
	}
}
