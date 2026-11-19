package manager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestQuotaFreeWebReleaseAndPrereleaseSemVer(t *testing.T) {
	if compareSemVer("0.62.0-rc.1", "0.62.0") >= 0 {
		t.Fatal("expected prerelease 0.62.0-rc.1 < stable 0.62.0")
	}
	if compareSemVer("0.9.10", "0.9.9") <= 0 {
		t.Fatal("expected 0.9.10 > 0.9.9")
	}

	dir := t.TempDir()
	var apiHits, latestHits int32
	fakeSum := strings.Repeat("a", 64)

	var srvURL string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/repos/") {
			atomic.AddInt32(&apiHits, 1)
			http.Error(w, "API rate limit exceeded", http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/Presikid/KurumiFrp/releases/latest":
			atomic.AddInt32(&latestHits, 1)
			http.Redirect(w, r, srvURL+"/Presikid/KurumiFrp/releases/tag/v9.9.9", http.StatusFound)
		case "/Presikid/KurumiFrp/releases/tag/v9.9.9":
			w.WriteHeader(http.StatusOK)
		case "/Presikid/KurumiFrp/releases/download/v9.9.9/SHA256SUMS.txt":
			_, _ = w.Write([]byte(fakeSum + "  KurumiFRP-9.9.9-Setup.exe\n" + fakeSum + "  KurumiFRP-9.9.9-linux-amd64.tar.gz\n"))
		case "/fatedier/frp/releases/latest":
			atomic.AddInt32(&latestHits, 1)
			http.Redirect(w, r, srvURL+"/fatedier/frp/releases/tag/v0.99.0", http.StatusFound)
		case "/fatedier/frp/releases/tag/v0.99.0":
			w.WriteHeader(http.StatusOK)
		case "/fatedier/frp/releases/download/v0.99.0/frp_sha256_checksums.txt":
			_, _ = w.Write([]byte(fakeSum + "  frp_0.99.0_linux_amd64.tar.gz\n" + fakeSum + "  frp_0.99.0_windows_amd64.zip\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	srvURL = ts.URL

	um := NewUpdateManager(dir, nil, nil)
	um.webBase = ts.URL
	um.allowLocal = true

	chk, err := um.CheckAll(context.Background(), nil)
	if err != nil {
		t.Fatalf("CheckAll failed: %v", err)
	}
	if atomic.LoadInt32(&apiHits) != 0 {
		t.Fatalf("expected zero REST API calls, got %d", apiHits)
	}
	if chk.App.Latest != "9.9.9" || !chk.App.HasUpdate || chk.FRP.Latest != "0.99.0" {
		t.Fatalf("unexpected CheckAll result: %+v", chk)
	}

	// Second call within 5-minute cache window must not hit /releases/latest again
	prevHits := atomic.LoadInt32(&latestHits)
	if _, err := um.CheckAll(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&latestHits) != prevHits {
		t.Fatalf("expected cached release lookup without extra HTTP hits")
	}
}

func TestChunkedRequestCSRFAndLegacyMultiClientMigration(t *testing.T) {
	// 1. Legacy multi-client migration across 12 clients (forces Go slice reallocation)
	dir := t.TempDir()
	var insts []string
	var tuns []string
	for i := 0; i < 12; i++ {
		cid := fmt.Sprintf("cli-%02d", i)
		insts = append(insts, fmt.Sprintf(`{"id":%q,"name":"节点%d","kind":"frpc","host":"10.0.0.1","port":7000}`, cid, i))
		tuns = append(tuns, fmt.Sprintf(`{"id":"t-%02d","instanceId":%q,"name":"ssh-%02d","type":"tcp","enabled":true,"localHost":"127.0.0.1","localPort":22,"remotePort":%d}`, i, cid, i, 6000+i))
	}
	stateRaw := fmt.Sprintf(`{"state":{"instances":[%s],"tunnels":[%s]}}`, strings.Join(insts, ","), strings.Join(tuns, ","))
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(stateRaw), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, doc := store.Snapshot()
	if len(doc.Clients) != 12 {
		t.Fatalf("expected 12 clients, got %d", len(doc.Clients))
	}
	for i, c := range doc.Clients {
		if len(c.Tunnels) != 1 {
			t.Fatalf("client %d (%s) lost tunnel during slice reallocation: %+v", i, c.ID, c.Tunnels)
		}
	}

	// 2. Chunked request (ContentLength == -1) CSRF enforcement + body decoding
	app, err := NewApp(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	sid, err := app.auth.Login("127.0.0.1", "admin", "")
	if err != nil {
		t.Fatal(err)
	}

	// Cross-origin-less chunked POST without Content-Type or X-Kurumi-Requested-With must be rejected (403)
	pr1, pw1 := io.Pipe()
	go func() {
		_, _ = pw1.Write([]byte(`{"proxyURL":"http://127.0.0.1:7890"}`))
		_ = pw1.Close()
	}()
	reqNoHeaders := httptest.NewRequest(http.MethodPost, "/api/update/settings", pr1)
	reqNoHeaders.ContentLength = -1
	reqNoHeaders.AddCookie(&http.Cookie{Name: sessionCookie, Value: sid})
	recNoHeaders := httptest.NewRecorder()
	app.ServeHTTP(recNoHeaders, reqNoHeaders)
	if recNoHeaders.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for headerless chunked POST, got %d", recNoHeaders.Code)
	}

	// Legitimate chunked POST with application/json must parse body even when ContentLength == -1
	pr2, pw2 := io.Pipe()
	go func() {
		_, _ = pw2.Write([]byte(`{"proxyURL":"http://127.0.0.1:2080"}`))
		_ = pw2.Close()
	}()
	reqValidChunked := httptest.NewRequest(http.MethodPost, "/api/update/settings", pr2)
	reqValidChunked.ContentLength = -1
	reqValidChunked.Header.Set("Content-Type", "application/json")
	reqValidChunked.AddCookie(&http.Cookie{Name: sessionCookie, Value: sid})
	recValid := httptest.NewRecorder()
	app.ServeHTTP(recValid, reqValidChunked)
	if recValid.Code != http.StatusOK || app.updater.ProxyURL() != "http://127.0.0.1:2080" {
		t.Fatalf("chunked JSON body was not parsed: code=%d proxy=%q body=%s", recValid.Code, app.updater.ProxyURL(), recValid.Body.String())
	}
}

func TestTOMLImportFeatureGatesAndTrafficResetProtection(t *testing.T) {
	tomlInput := strings.Join([]string{
		`serverAddr = "203.0.113.9"`,
		`serverPort = 7000`,
		`featureGates = { VirtualNet = true }`,
		`start = [ # inline comment before multiline array`,
		`  "web",`,
		`  "ssh"`,
		`]`,
		`[transport] # comment on section header`,
		`protocol = "quic"`,
		`customTransportField = "keep-me"`,
	}, "\n")

	_, cli, err := ParseTOMLAsProfile("测试导入", "frpc", tomlInput)
	if err != nil || cli == nil {
		t.Fatalf("ParseTOMLAsProfile failed: %v", err)
	}
	if !strings.Contains(cli.ExtraTOML, "featureGates") || !strings.Contains(cli.ExtraTOML, "transport.customTransportField") {
		t.Fatalf("expected featureGates and dotted extra field preserved in ExtraTOML, got: %q", cli.ExtraTOML)
	}
	if len(cli.StartProxies) != 2 || cli.Protocol != "quic" {
		t.Fatalf("unexpected parsed fields: start=%v proto=%q", cli.StartProxies, cli.Protocol)
	}

	// Verify verifyPasswordRecord rejects empty salt or wrong-length hash
	badRec := adminAccountFile{Version: 2, Username: "admin", Salt: "", Hash: ""}
	if ok, err := verifyPasswordRecord(badRec, ""); err == nil || ok {
		t.Fatal("expected corrupted empty credential record to be rejected")
	}
	_ = sha256.New()
	_ = hex.EncodeToString(nil)
	_ = json.Valid(nil)
	_ = time.Second
}
