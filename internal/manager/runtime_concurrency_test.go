package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// This fixture is copied to the temporary core directory. The opt-in environment
// is deliberately distinct from the older lifecycle fixture in TestMain.
func init() {
	if os.Getenv("FRP_AUDIT_HELPER") == "1" {
		runRuntimeAuditHelper()
		os.Exit(0)
	}
}

func runRuntimeAuditHelper() {
	dir, mode := os.Getenv("FRP_AUDIT_DIR"), os.Getenv("FRP_AUDIT_MODE")
	mark := func(name, text string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0600); err != nil {
			os.Exit(2)
		}
	}
	wait := func(name string) {
		for {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	if os.Getenv("FRP_AUDIT_ROLE") == "descendant" {
		signal.Ignore(os.Interrupt)
		mark("descendant.pid", strconv.Itoa(os.Getpid()))
		for {
			time.Sleep(time.Second)
		}
	}
	spawnDescendant := func() {
		cmd := exec.Command(os.Args[0])
		cmd.Env = append(os.Environ(), "FRP_AUDIT_ROLE=descendant")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Start(); err != nil {
			os.Exit(2)
		}
		wait("descendant.pid")
	}
	if len(os.Args) > 1 && os.Args[1] == "verify" {
		if mode == "status-verify-block" && len(os.Args) > 3 {
			config, err := os.ReadFile(os.Args[3])
			if err != nil {
				os.Exit(2)
			}
			if strings.Contains(string(config), "webServer.tls.certFile") {
				mark("status-verifying", "yes")
				wait("status-release")
			}
		}
		if mode == "status-unsupported" && len(os.Args) > 3 {
			config, err := os.ReadFile(os.Args[3])
			if err != nil {
				os.Exit(2)
			}
			if strings.Contains(string(config), "webServer.tls.certFile") {
				fmt.Println("unsupported status config")
				os.Exit(1)
			}
		}
		if mode == "verify-group" {
			spawnDescendant()
		}
		mark("verifying", strconv.Itoa(os.Getpid()))
		if mode == "verify-block" || mode == "verify-group" {
			wait("release")
		}
		fmt.Println("configuration verified by concurrency fixture")
		return
	}
	if mode == "group-child" || mode == "natural-exit" {
		spawnDescendant()
	}
	interrupts := make(chan os.Signal, 1)
	if mode == "hold-stop" {
		signal.Notify(interrupts, os.Interrupt)
		defer signal.Stop(interrupts)
	}
	f, err := os.OpenFile(filepath.Join(dir, "starts"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		os.Exit(2)
	}
	_, _ = fmt.Fprintln(f, os.Getpid())
	_ = f.Close()
	mark("running", strconv.Itoa(os.Getpid()))
	if mode == "natural-exit" {
		wait("exit-parent")
		return
	}
	interrupted := false
	for {
		select {
		case <-interrupts:
			interrupted = true
			mark("interrupted", "yes")
		default:
		}
		if interrupted {
			if _, err := os.Stat(filepath.Join(dir, "release")); err == nil {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func installRuntimeAuditFixture(t *testing.T, a *App, mode string) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	name := "frpc"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err = os.WriteFile(filepath.Join(a.bin, name), data, 0700); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	t.Setenv("FRP_AUDIT_HELPER", "1")
	t.Setenv("FRP_AUDIT_DIR", dir)
	t.Setenv("FRP_AUDIT_MODE", mode)
	t.Cleanup(func() { _ = os.WriteFile(filepath.Join(dir, "release"), nil, 0600) })
	return dir
}

func awaitRuntimeFile(t *testing.T, path string) []byte {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
			return b
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for fixture %s", filepath.Base(path))
	return nil
}

func releaseRuntimeFixture(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
}

func awaitRuntimeResponse(t *testing.T, results <-chan *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("lifecycle request did not finish")
		return nil
	}
}

func assertStateResponsive(t *testing.T, a *App) {
	t.Helper()
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() { result <- request(a, "GET", "/api/state", nil, a.AccessToken(), "") }()
	select {
	case w := <-result:
		if w.Code != http.StatusOK {
			t.Fatal(w.Code, w.Body.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("state polling blocked behind a slow lifecycle operation")
	}
}

func TestChangedDraftDuringStatusVerificationDoesNotStopProcess(t *testing.T) {
	a := testApp(t)
	i := seed(t, a)
	dir := installRuntimeAuditFixture(t, a, "")
	w := request(a, "POST", "/api/control", map[string]any{"revision": a.store.DB.Revision, "id": i.ID, "action": "start"}, a.AccessToken(), "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	pid := a.procs[i.ID].view().PID
	t.Setenv("FRP_AUDIT_MODE", "status-verify-block")
	response := make(chan *httptest.ResponseRecorder, 1)
	revision := a.store.DB.Revision
	go func() {
		response <- request(a, "POST", "/api/control", map[string]any{"revision": revision, "id": i.ID, "action": "restart"}, a.AccessToken(), "")
	}()
	awaitRuntimeFile(t, filepath.Join(dir, "status-verifying"))
	public := a.store.DB.State.public().Instances[0]
	public.Name = "changed while verifying status"
	w = request(a, "POST", "/api/save", map[string]any{"revision": a.store.DB.Revision, "kind": "instance", "value": public}, a.AccessToken(), "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if err := os.WriteFile(filepath.Join(dir, "status-release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	w = awaitRuntimeResponse(t, response)
	if w.Code != 409 || a.procs[i.ID].view().PID != pid {
		t.Fatalf("stale restart stopped process: %d %s", w.Code, w.Body.String())
	}
}

func TestUnsupportedStatusKeepsUserProcessAvailable(t *testing.T) {
	a := testApp(t)
	i := seed(t, a)
	tunnel := Tunnel{ID: randomHex(8), InstanceID: i.ID, Name: "web", Type: "tcp", LocalHost: "127.0.0.1", LocalPort: 80, RemotePort: 6000, Enabled: true}
	st := a.store.DB.State.clone()
	st.Tunnels = append(st.Tunnels, tunnel)
	if err := a.store.commit(st, "add"); err != nil {
		t.Fatal(err)
	}
	installRuntimeAuditFixture(t, a, "status-unsupported")
	w := request(a, "POST", "/api/control", map[string]any{"revision": a.store.DB.Revision, "id": i.ID, "action": "start"}, a.AccessToken(), "")
	if w.Code != 200 {
		t.Fatalf("valid process blocked: %d %s", w.Code, w.Body.String())
	}
	if a.procs[i.ID].privateStatus != nil {
		t.Fatal("unsupported status installed")
	}
	w = request(a, "GET", "/api/registration", nil, a.AccessToken(), "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "当前核心未通过安全状态接口配置验证") {
		t.Fatalf("missing reason: %d %s", w.Code, w.Body.String())
	}
	entries, err := filepath.Glob(filepath.Join(a.store.dir, "status-*"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary status files remain: %v %v", entries, err)
	}
	w = request(a, "POST", "/api/control", map[string]any{"revision": a.store.DB.Revision, "id": i.ID, "action": "stop"}, a.AccessToken(), "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
}

func TestSlowVerificationKeepsStateResponsiveAndRejectsStaleDraft(t *testing.T) {
	a := testApp(t)
	i := seed(t, a)
	dir := installRuntimeAuditFixture(t, a, "verify-block")
	revision := a.store.DB.Revision
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		result <- request(a, "POST", "/api/control", map[string]any{"revision": revision, "id": i.ID, "action": "start"}, a.AccessToken(), "")
	}()
	awaitRuntimeFile(t, filepath.Join(dir, "verifying"))
	assertStateResponsive(t, a)

	// A second start cannot queue up and unexpectedly launch after the first one.
	w := request(a, "POST", "/api/control", map[string]any{"revision": revision, "id": i.ID, "action": "start"}, a.AccessToken(), "")
	if w.Code != http.StatusConflict {
		t.Fatal("overlapping start accepted", w.Code, w.Body.String())
	}
	public := a.store.DB.State.public().Instances[0]
	public.Port++
	w = request(a, "POST", "/api/save", map[string]any{"revision": revision, "kind": "instance", "value": public}, a.AccessToken(), "")
	if w.Code != http.StatusOK {
		t.Fatal("draft editing blocked", w.Code, w.Body.String())
	}
	releaseRuntimeFixture(t, dir)
	w = awaitRuntimeResponse(t, result)
	if w.Code != http.StatusConflict {
		t.Fatal("stale verified draft launched", w.Code, w.Body.String())
	}
	a.mu.Lock()
	p := a.procs[i.ID]
	a.mu.Unlock()
	if p != nil {
		t.Fatal("stale draft left a process behind")
	}
}

func TestSlowStopKeepsStateResponsive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no-console Windows stop intentionally uses immediate termination")
	}
	a := testApp(t)
	i := seed(t, a)
	dir := installRuntimeAuditFixture(t, a, "hold-stop")
	revision := a.store.DB.Revision
	w := request(a, "POST", "/api/control", map[string]any{"revision": revision, "id": i.ID, "action": "start"}, a.AccessToken(), "")
	if w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	awaitRuntimeFile(t, filepath.Join(dir, "running"))
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		result <- request(a, "POST", "/api/control", map[string]any{"revision": revision, "id": i.ID, "action": "stop"}, a.AccessToken(), "")
	}()
	awaitRuntimeFile(t, filepath.Join(dir, "interrupted"))
	assertStateResponsive(t, a)
	w = request(a, "POST", "/api/control", map[string]any{"revision": revision, "id": i.ID, "action": "restart"}, a.AccessToken(), "")
	if w.Code != http.StatusConflict {
		t.Fatal("restart raced the stop wait", w.Code, w.Body.String())
	}
	releaseRuntimeFixture(t, dir)
	if w = awaitRuntimeResponse(t, result); w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
}

func TestRestartRejectsDraftChangedDuringStop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no-console Windows stop intentionally uses immediate termination")
	}
	a := testApp(t)
	i := seed(t, a)
	dir := installRuntimeAuditFixture(t, a, "hold-stop")
	revision := a.store.DB.Revision
	w := request(a, "POST", "/api/control", map[string]any{"revision": revision, "id": i.ID, "action": "start"}, a.AccessToken(), "")
	if w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	awaitRuntimeFile(t, filepath.Join(dir, "running"))
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		result <- request(a, "POST", "/api/control", map[string]any{"revision": revision, "id": i.ID, "action": "restart"}, a.AccessToken(), "")
	}()
	awaitRuntimeFile(t, filepath.Join(dir, "interrupted"))
	public := a.store.DB.State.public().Instances[0]
	public.Port++
	w = request(a, "POST", "/api/save", map[string]any{"revision": revision, "kind": "instance", "value": public}, a.AccessToken(), "")
	if w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	releaseRuntimeFixture(t, dir)
	w = awaitRuntimeResponse(t, result)
	if w.Code != http.StatusConflict {
		t.Fatal("restart launched stale settings after the stop wait", w.Code, w.Body.String())
	}
	starts, err := os.ReadFile(filepath.Join(dir, "starts"))
	if err != nil || len(strings.Fields(string(starts))) != 1 {
		t.Fatal("replacement process launched", err, string(starts))
	}
}

func TestCloseCancelsVerificationAndRejectsNewStarts(t *testing.T) {
	a := testApp(t)
	i := seed(t, a)
	dir := installRuntimeAuditFixture(t, a, "verify-block")
	revision := a.store.DB.Revision
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		result <- request(a, "POST", "/api/control", map[string]any{"revision": revision, "id": i.ID, "action": "start"}, a.AccessToken(), "")
	}()
	awaitRuntimeFile(t, filepath.Join(dir, "verifying"))
	closed := make(chan error, 1)
	go func() { closed <- a.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not cancel the verifier")
	}
	if w := awaitRuntimeResponse(t, result); w.Code == http.StatusOK {
		t.Fatal("cancelled start reported success")
	}
	w := request(a, "POST", "/api/control", map[string]any{"revision": revision, "id": i.ID, "action": "start"}, a.AccessToken(), "")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatal("start accepted during shutdown", w.Code)
	}
}

func TestCancelledValidationReleasesOperation(t *testing.T) {
	a := testApp(t)
	i := seed(t, a)
	dir := installRuntimeAuditFixture(t, a, "verify-block")
	revision := a.store.DB.Revision
	payload, _ := json.Marshal(map[string]any{"revision": revision, "id": i.ID})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest("POST", "http://127.0.0.1:9000/api/validate", bytes.NewReader(payload)).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+a.AccessToken())
	r.Header.Set("Content-Type", "application/json")
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		result <- w
	}()
	awaitRuntimeFile(t, filepath.Join(dir, "verifying"))
	cancel()
	if w := awaitRuntimeResponse(t, result); w.Code == http.StatusOK {
		t.Fatal("cancelled verification reported success")
	}
	releaseRuntimeFixture(t, dir)
	w := request(a, "POST", "/api/validate", map[string]any{"revision": revision, "id": i.ID}, a.AccessToken(), "")
	if w.Code != http.StatusOK {
		t.Fatal("cancelled request retained operation reservation", w.Code, w.Body.String())
	}
}
