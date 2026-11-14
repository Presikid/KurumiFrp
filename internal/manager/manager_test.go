package manager

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSingleUserAuthAndLegacyMigration(t *testing.T) {
	dir := t.TempDir()

	// Simulate legacy v0.8 admin-password.json without username field
	salt := []byte("0123456789abcdef")
	hash := pbkdf2SHA256([]byte("oldpass123"), salt, pbkdf2Iterations, 32)
	legacyJSON := `{"version":1,"salt":"MDEyMzQ1Njc4OWFiY2RlZg","hash":"` + encodeRawBase64(hash) + `"}`
	if err := os.WriteFile(filepath.Join(dir, "admin-password.json"), []byte(legacyJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	auth, err := NewAuthManager(dir)
	if err != nil {
		t.Fatalf("NewAuthManager failed: %v", err)
	}
	if auth.Username() != "admin" {
		t.Fatalf("expected default migrated username 'admin', got %q", auth.Username())
	}

	if _, err := auth.Login("127.0.0.1", "wronguser", "oldpass123"); err == nil {
		t.Fatal("expected wrong username to be rejected")
	}
	if _, err := auth.Login("127.0.0.1", "admin", "wrongpass"); err == nil {
		t.Fatal("expected wrong password to be rejected")
	}
	sid, err := auth.Login("127.0.0.1", "admin", "oldpass123")
	if err != nil || sid == "" {
		t.Fatalf("expected valid login, got err=%v", err)
	}

	// Update single user's username and password
	if err := auth.UpdateCredentials("oldpass123", "kurumi", "zafkiel777"); err != nil {
		t.Fatalf("UpdateCredentials failed: %v", err)
	}
	if auth.Username() != "kurumi" {
		t.Fatalf("expected updated username 'kurumi', got %q", auth.Username())
	}
	if _, err := auth.Login("127.0.0.1", "kurumi", "zafkiel777"); err != nil {
		t.Fatalf("expected login with updated credentials to succeed: %v", err)
	}
}

func TestStoreMultiConfigAndZeroWearWrite(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	err = store.Update(func(doc *ConfigDocument) error {
		doc.Servers = append(doc.Servers, ServerProfile{
			Name:     "主转发端 A",
			BindPort: 7000,
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Switch to second independent config file inside software folder
	docB, err := store.SelectOrCreateConfig("backup-plan", "备用方案 B")
	if err != nil {
		t.Fatal(err)
	}
	if len(docB.Servers) != 0 {
		t.Fatalf("expected clean second config file, got %d servers", len(docB.Servers))
	}

	files, err := store.ListConfigFiles()
	if err != nil || len(files) != 2 {
		t.Fatalf("expected 2 config files, got %d (err=%v)", len(files), err)
	}

	// Verify writeIfChanged skips identical writes
	testFile := filepath.Join(dir, "wear-check.txt")
	wrote, err := writeIfChanged(testFile, []byte("kurumi"), 0o600)
	if err != nil || !wrote {
		t.Fatalf("expected first write=true, err=%v", err)
	}
	wroteSecond, err := writeIfChanged(testFile, []byte("kurumi"), 0o600)
	if err != nil || wroteSecond {
		t.Fatalf("expected identical second write=false, got %v (err=%v)", wroteSecond, err)
	}
}

func TestTOMLRenderEscapingAndImport(t *testing.T) {
	cli := ClientProfile{
		ID:         "cli-1",
		Name:       "家里 NAS",
		ServerAddr: "frp.example.com",
		ServerPort: 7000,
		AuthToken:  `tok"with\special`,
		Protocol:   "quic",
		TLSEnable:  true,
		Tunnels: []TunnelRule{
			{
				ID:               "tun-1",
				Name:             "nas-web",
				Type:             "http",
				Enabled:          true,
				LocalIP:          "127.0.0.1",
				LocalPort:        5000,
				CustomDomains:    []string{"nas.example.com"},
				BandwidthLimitKB: 2048,
			},
			{
				ID:             "tun-2",
				Name:           "p2p-visitor",
				Mode:           "visitor",
				Type:           "xtcp",
				Enabled:        true,
				ServerName:     "remote-desktop",
				SecretKey:      "sec123",
				LocalIP:        "127.0.0.1",
				LocalPort:      13389,
				KeepTunnelOpen: true,
			},
		},
	}

	res := RenderClientTOML(cli, ClientRuntimeInjections{
		Preview:   false,
		AdminPort: 17400,
		AdminUser: "kurumi",
		AdminPass: "adm123",
	})

	// Ensure environment variable value does NOT contain outer literal quotes (BUG-01 regression check)
	foundTokenEnv := false
	for _, kv := range res.Env {
		if strings.HasPrefix(kv, "KURUMI_AUTH_TOKEN_1=") {
			foundTokenEnv = true
			val := strings.TrimPrefix(kv, "KURUMI_AUTH_TOKEN_1=")
			if strings.HasPrefix(val, `"`) || strings.HasSuffix(val, `"`) {
				t.Fatalf("env var must not wrap outer quotes: %s", kv)
			}
			if val != `tok\"with\\special` {
				t.Fatalf("unexpected inner escape: %s", val)
			}
		}
	}
	if !foundTokenEnv {
		t.Fatal("missing KURUMI_AUTH_TOKEN_1 in rendered env")
	}
	if !strings.Contains(res.TOML, "[[visitors]]") || !strings.Contains(res.TOML, `transport.bandwidthLimit = "2048KB"`) {
		t.Fatalf("missing expected blocks in TOML:\n%s", res.TOML)
	}

	preview := RenderClientTOML(cli, ClientRuntimeInjections{Preview: true})
	_, importedCli, err := ParseTOMLAsProfile("导入测试", "frpc", preview.TOML)
	if err != nil || importedCli == nil {
		t.Fatalf("ParseTOMLAsProfile failed: %v", err)
	}
	if importedCli.ServerAddr != "frp.example.com" || len(importedCli.Tunnels) != 2 {
		t.Fatalf("unexpected imported client profile: %+v", importedCli)
	}
}

func TestConnectionDashboardPluginEnforcement(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	pastTime := time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339)
	err = store.Update(func(doc *ConfigDocument) error {
		doc.Servers = append(doc.Servers, ServerProfile{
			ID:       "srv-guard",
			Name:     "公网节点 A",
			BindPort: 7000,
			ConnPolicies: map[string]ConnPolicy{
				"disabled-ssh":  {ProxyName: "disabled-ssh", Enabled: false},
				"expired-web":   {ProxyName: "expired-web", Enabled: true, ExpiresAt: pastTime},
				"capped-rdp":    {ProxyName: "capped-rdp", Enabled: true, TrafficLimitMB: 1, RecordedInBytes: 2 * 1024 * 1024},
				"healthy- game": {ProxyName: "healthy-game", Enabled: true, TrafficLimitMB: 100},
			},
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	rt, err := NewRuntimeSupervisor(dir, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.StopAll()

	// Register a mock running server entry to test webhook handler directly
	rt.mu.Lock()
	rt.procs["srv-guard"] = &managedProc{
		id:           "srv-guard",
		kind:         "frps",
		pluginSecret: "sec999",
		state:        "running",
		conns:        make(map[string]ConnLiveItem),
	}
	rt.mu.Unlock()

	cases := []struct {
		proxy      string
		wantReject bool
	}{
		{"disabled-ssh", true},
		{"expired-web", true},
		{"capped-rdp", true},
		{"brand-new-proxy", false},
	}

	for _, tc := range cases {
		body, _ := json.Marshal(map[string]any{
			"content": map[string]any{"proxy_name": tc.proxy},
		})
		req := httptest.NewRequest(http.MethodPost, "/internal/frps-plugin/srv-guard?token=sec999&op=NewUserConn", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		rt.HandleFRPSPlugin(rec, req, "srv-guard")
		if rec.Code != http.StatusOK {
			t.Fatalf("proxy %s: status=%d", tc.proxy, rec.Code)
		}
		var resp struct {
			Reject bool `json:"reject"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		if resp.Reject != tc.wantReject {
			t.Fatalf("proxy %s: got reject=%v, want %v (body=%s)", tc.proxy, resp.Reject, tc.wantReject, rec.Body.String())
		}
	}
}

func encodeRawBase64(b []byte) string {
	const std = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var out strings.Builder
	for i := 0; i < len(b); i += 3 {
		rem := len(b) - i
		val := uint(b[i]) << 16
		if rem > 1 {
			val |= uint(b[i+1]) << 8
		}
		if rem > 2 {
			val |= uint(b[i+2])
		}
		out.WriteByte(std[(val>>18)&0x3F])
		out.WriteByte(std[(val>>12)&0x3F])
		if rem > 1 {
			out.WriteByte(std[(val>>6)&0x3F])
		}
		if rem > 2 {
			out.WriteByte(std[val&0x3F])
		}
	}
	return out.String()
}

func TestCompareSemVer(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.9.0", "0.9.0", 0},
		{"v0.9.1", "0.9.0", 1},
		{"0.10.0", "0.9.9", 1},
		{"1.0.0", "0.99.9", 1},
		{"0.9.0", "0.9.2", -1},
	}
	for _, tc := range cases {
		if got := compareSemVer(tc.a, tc.b); got != tc.want {
			t.Fatalf("compareSemVer(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestStatelessSessionAndFRPAutoUpdate(t *testing.T) {
	dir := t.TempDir()

	// Part A: Verify HMAC session cookie survives daemon restart (NewAuthManager reload) and invalidates on Logout / password update
	am1, err := NewAuthManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	sid, err := am1.Login("127.0.0.1", "admin", "")
	if err != nil {
		t.Fatalf("initial login failed: %v", err)
	}
	reqWithCookie := func(c string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/state", nil)
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: c})
		return r
	}
	if _, ok := am1.Authenticated(reqWithCookie(sid)); !ok {
		t.Fatal("expected session valid on am1")
	}

	// Simulate daemon restart during self-update
	am2, err := NewAuthManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := am2.Authenticated(reqWithCookie(sid)); !ok {
		t.Fatal("expected HMAC session to survive daemon restart seamlessly")
	}
	am2.Logout(reqWithCookie(sid))
	if _, ok := am2.Authenticated(reqWithCookie(sid)); ok {
		t.Fatal("expected session invalidated after Logout")
	}

	// Changing password must immediately invalidate pre-restart cookies across fresh reload too
	sid2, _ := am2.Login("127.0.0.1", "admin", "")
	if err := am2.UpdateCredentials("", "kurumi", "zafkiel-secret"); err != nil {
		t.Fatal(err)
	}
	if _, ok := am2.Authenticated(reqWithCookie(sid2)); ok {
		t.Fatal("expected session invalidated after credential rotation")
	}

	// Part B: Verify UpdateManager proxy validation & SHA-256 verified official FRP hot-update
	slug, ext, supported := frpPlatformSlug()
	if !supported {
		t.Skip("skipping archive hot-update test on unsupported GOOS/GOARCH")
	}

	// Build a valid mock frpc + frps executable for the current test OS
	var mockBin []byte
	if norm, err := normalizeProxyURL("127.0.0.1:7890"); err != nil || norm != "http://127.0.0.1:7890" {
		t.Fatalf("normalizeProxyURL got %q, %v", norm, err)
	}
	if runtime.GOOS == "windows" {
		// On Windows, copy cmd.exe / whoami.exe or skip shell-script execution after testing ZIP extraction
		var zbuf bytes.Buffer
		zw := zip.NewWriter(&zbuf)
		for name, body := range map[string][]byte{
			"frp/frpc.exe": []byte("MZ-mock"),
			"frp/frps.exe": []byte("MZ-mock"),
			"frp/LICENSE":  []byte("Apache-2.0"),
		} {
			w, _ := zw.Create(name)
			_, _ = w.Write(body)
		}
		_ = zw.Close()
		c, s, _, err := extractFRPMembers("frp_0.99.0_windows_amd64.zip", zbuf.Bytes())
		if err != nil || string(c) != "MZ-mock" || string(s) != "MZ-mock" {
			t.Fatalf("extractFRPMembers windows zip failed: %v", err)
		}
		return
	}
	mockBin = []byte("#!/bin/sh\necho 0.99.0\n")

	assetName := "frp_0.99.0_" + slug + ext
	var archiveBuf bytes.Buffer
	if ext == ".zip" {
		zw := zip.NewWriter(&archiveBuf)
		for name, body := range map[string][]byte{
			"frp_0.99.0/frpc":    mockBin,
			"frp_0.99.0/frps":    mockBin,
			"frp_0.99.0/LICENSE": []byte("Apache-2.0"),
		} {
			w, _ := zw.Create(name)
			_, _ = w.Write(body)
		}
		_ = zw.Close()
	} else {
		gz := gzip.NewWriter(&archiveBuf)
		tw := tar.NewWriter(gz)
		for name, body := range map[string][]byte{
			"frp_0.99.0/frpc":    mockBin,
			"frp_0.99.0/frps":    mockBin,
			"frp_0.99.0/LICENSE": []byte("Apache-2.0"),
		} {
			_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
			_, _ = tw.Write(body)
		}
		_ = tw.Close()
		_ = gz.Close()
	}
	archiveBytes := archiveBuf.Bytes()
	sum := sha256.Sum256(archiveBytes)
	sumHex := hex.EncodeToString(sum[:])

	var srvURL string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/fatedier/frp/releases/latest":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tag_name": "v0.99.0",
				"html_url": srvURL + "/rel",
				"assets": []map[string]any{
					{"name": assetName, "size": len(archiveBytes), "browser_download_url": srvURL + "/dl/" + assetName},
					{"name": "frp_sha256_checksums.txt", "size": 100, "browser_download_url": srvURL + "/dl/checksums.txt"},
				},
			})
		case "/repos/Presikid/KurumiFrp/releases":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"tag_name": "v0.9.1", "html_url": srvURL + "/app"},
			})
		case "/dl/checksums.txt":
			_, _ = w.Write([]byte(sumHex + "  " + assetName + "\n"))
		case "/dl/" + assetName:
			_, _ = w.Write(archiveBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	srvURL = ts.URL

	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	rt, err := NewRuntimeSupervisor(dir, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.StopAll()

	um := NewUpdateManager(dir, rt, nil)
	um.apiBase = ts.URL
	um.allowLocal = true

	coreInfo, _, err := um.ApplyFRPUpdate(context.Background(), nil)
	if err != nil {
		t.Fatalf("ApplyFRPUpdate failed: %v", err)
	}
	if !coreInfo.FrpcFound || !coreInfo.FrpsFound || coreInfo.FrpcVersion != "0.99.0" || coreInfo.CoreSource != "data-bin" {
		t.Fatalf("unexpected updated coreInfo: %+v", coreInfo)
	}
}
