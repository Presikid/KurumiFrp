package manager

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestLiveQuotaFreeWebReleaseAndPrereleaseSemVer(t *testing.T) {
	if compareSemVer("0.62.0-rc.1", "0.62.0") >= 0 {
		t.Fatal("expected prerelease 0.62.0-rc.1 < stable 0.62.0")
	}
	if compareSemVer("0.9.11", "0.9.10") <= 0 {
		t.Fatal("expected 0.9.11 > 0.9.10")
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

	// Every explicit check must query GitHub Releases live without stale 5-minute caching
	prevHits := atomic.LoadInt32(&latestHits)
	if _, err := um.CheckAll(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&latestHits) <= prevHits {
		t.Fatalf("expected live release query without local TTL cache, hits stayed at %d", prevHits)
	}
}

func TestChunkedRequestCSRFAndRemovedDeadEndpoints(t *testing.T) {
	app, err := NewApp(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	// Verify removed ticket-bypass endpoint is gone
	reqTicket := httptest.NewRequest(http.MethodPost, "/api/browser-ticket", nil)
	recTicket := httptest.NewRecorder()
	app.ServeHTTP(recTicket, reqTicket)
	if recTicket.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthenticated 401 on /api/* without ticket bypass, got %d", recTicket.Code)
	}

	sid, err := app.auth.Login("127.0.0.1", "admin", "")
	if err != nil {
		t.Fatal(err)
	}

	// Headerless chunked POST must be blocked by CSRF guard (403)
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

	// Valid chunked POST with application/json must parse cleanly
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
		t.Fatalf("chunked JSON body was not parsed: code=%d proxy=%q", recValid.Code, app.updater.ProxyURL())
	}
}

func TestTOMLImportFeatureGatesAndCredentialIntegrity(t *testing.T) {
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

	badRec := adminAccountFile{Version: 2, Username: "admin", Salt: "", Hash: ""}
	if ok, err := verifyPasswordRecord(badRec, ""); err == nil || ok {
		t.Fatal("expected corrupted empty credential record to be rejected")
	}
}
