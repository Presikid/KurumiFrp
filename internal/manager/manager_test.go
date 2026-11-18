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
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Presikid/KurumiFrp/web"
)

func TestSingleUserAuthAndLegacyMigration(t *testing.T) {
	dir := t.TempDir()

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

func TestStrictPreLoginAPIGate(t *testing.T) {
	dir := t.TempDir()
	app, err := NewApp(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	// Unauthenticated request to /api/state MUST be strictly rejected with 401
	req := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 before login, got %d", rec.Code)
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

func TestTOMLRenderEscapingAndBidirectionalRoundTrip(t *testing.T) {
	tcpMuxVal := true
	srv := ServerProfile{
		ID:                    "srv-full",
		Name:                  "云服务器 A",
		BindAddr:              "0.0.0.0",
		BindPort:              7000,
		KCPBindPort:           7000,
		QUICBindPort:          7002,
		VhostHTTPPort:         8080,
		VhostHTTPSPort:        8443,
		TCPMuxHTTPConnectPort: 1337,
		SubDomainHost:         "tunnel.example.com",
		SSHGatewayBindPort:    2200,
		AuthMethod:            "token",
		AuthToken:             "super-secret",
		AuthAdditionalScopes:  []string{"HeartBeats", "NewWorkConns"},
		MaxPoolCount:          12,
		TCPMux:                &tcpMuxVal,
		AllowPorts:            "9000-10000,25565",
	}
	srvTOML := RenderServerTOML(srv, ServerRuntimeInjections{Preview: true, MaskSecrets: true})
	updSrv, err := ApplyTOMLToServer(srv, strings.Replace(srvTOML.TOML, "bindPort = 7000", "bindPort = 7001", 1))
	if err != nil {
		t.Fatalf("ApplyTOMLToServer failed: %v", err)
	}
	if updSrv.BindPort != 7001 || updSrv.AuthToken != "super-secret" || updSrv.SSHGatewayBindPort != 2200 || updSrv.AllowPorts != "9000-10000,25565" {
		t.Fatalf("unexpected round-tripped server profile: %+v", updSrv)
	}

	cli := ClientProfile{
		ID:                "cli-1",
		Name:              "家里 NAS",
		ServerAddr:        "frp.example.com",
		ServerPort:        7000,
		User:              "home",
		AuthToken:         `tok"with\special`,
		Protocol:          "quic",
		TLSEnable:         true,
		NatHoleSTUNServer: "stun.easyvoip.com:3478",
		ProxyURL:          "http://127.0.0.1:7890",
		CtrlSecret:        "ctrlsec123",
		AgentToken:        "agenttok456",
		Metadatas:         map[string]string{"zone": "shanghai"},
		Tunnels: []TunnelRule{
			{
				ID:                        "tun-1",
				Name:                      "nas-web",
				Type:                      "http",
				Enabled:                   true,
				LocalIP:                   "127.0.0.1",
				LocalPort:                 5000,
				CustomDomains:             []string{"nas.example.com"},
				BandwidthLimitKB:          2048,
				ProxyProtocolVersion:      "v2",
				LoadBalancerGroup:         "web-ha",
				HealthCheckType:           "http",
				HealthCheckPath:           "/healthz",
				HealthCheckTimeoutSeconds: 3,
				HealthCheckHeaders:        map[string]string{"X-Probe": "1"},
				RequestHeaders:            map[string]string{"X-Real-Env": "home"},
			},
			{
				ID:                "tun-2",
				Name:              "p2p-visitor",
				Mode:              "visitor",
				Type:              "xtcp",
				Enabled:           true,
				ServerName:        "remote-desktop",
				ServerUser:        "peer-e",
				SecretKey:         "sec123",
				LocalIP:           "127.0.0.1",
				LocalPort:         13389,
				VisitorProtocol:   "quic",
				KeepTunnelOpen:    true,
				FallbackTo:        "rdp-stcp-fb",
				FallbackTimeoutMs: 600,
			},
			{
				ID:              "tun-3",
				Name:            "batch-ports",
				Mode:            "proxy",
				Type:            "tcp",
				Enabled:         true,
				LocalIP:         "127.0.0.1",
				PortRangeLocal:  "6000-6005",
				PortRangeRemote: "16000-16005",
			},
		},
	}

	res := RenderClientTOML(cli, ClientRuntimeInjections{
		Preview:    false,
		AdminPort:  17400,
		AdminUser:  "kurumi",
		AdminPass:  "adm123",
		AgentPort:  18900,
		ClientID:   cli.ID,
		ClientName: cli.Name,
		CtrlSecret: cli.CtrlSecret,
		AgentToken: cli.AgentToken,
	})

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
	if !strings.Contains(res.TOML, "[[visitors]]") || !strings.Contains(res.TOML, `transport.bandwidthLimit = "2048KB"`) || !strings.Contains(res.TOML, "__kurumi_ctrl_cli-1") {
		t.Fatalf("missing expected blocks in runtime TOML:\n%s", res.TOML)
	}

	preview := RenderClientTOML(cli, ClientRuntimeInjections{Preview: true, MaskSecrets: true})
	updCli, err := ApplyTOMLToClient(cli, preview.TOML)
	if err != nil {
		t.Fatalf("ApplyTOMLToClient failed: %v", err)
	}
	if updCli.ServerAddr != "frp.example.com" || updCli.AuthToken != `tok"with\special` || len(updCli.Tunnels) != 3 {
		t.Fatalf("unexpected round-tripped client profile: %+v", updCli)
	}
	if updCli.Tunnels[0].HealthCheckPath != "/healthz" || updCli.Tunnels[1].FallbackTo != "rdp-stcp-fb" || updCli.Tunnels[2].PortRangeLocal != "6000-6005" {
		t.Fatalf("tunnel details lost during TOML round-trip: %+v", updCli.Tunnels)
	}
}

func TestABCDECrossInstanceRemoteControlAndP2PScenario(t *testing.T) {
	ctx := context.Background()

	// Host A: Cloud Server running frps A
	dirA := t.TempDir()
	storeA, err := NewStore(dirA)
	if err != nil {
		t.Fatal(err)
	}
	_ = storeA.Update(func(d *ConfigDocument) error {
		d.Servers = append(d.Servers, ServerProfile{ID: "srv-A", Name: "云服务器 A", BindPort: 7000, AuthToken: "shared-tok"})
		return nil
	})
	rtA, err := NewRuntimeSupervisor(dirA, storeA, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rtA.StopAll()
	rtA.mu.Lock()
	rtA.procs["srv-A"] = &managedProc{
		id:            "srv-A",
		kind:          "frps",
		pluginSecret:  "plug-a",
		state:         "running",
		conns:         make(map[string]ConnLiveItem),
		remoteClients: make(map[string]*remoteClientSession),
	}
	rtA.mu.Unlock()

	// Helper simulating an independent remote machine (B, C, D, E) connecting its frpc to Server A
	setupRemoteHost := func(hostLabel string, extraSrv *ServerProfile, cli ClientProfile) (*Store, *RuntimeSupervisor) {
		hDir := t.TempDir()
		hStore, sErr := NewStore(hDir)
		if sErr != nil {
			t.Fatal(sErr)
		}
		var savedCli ClientProfile
		_ = hStore.Update(func(d *ConfigDocument) error {
			if extraSrv != nil {
				d.Servers = append(d.Servers, *extraSrv)
			}
			d.Clients = append(d.Clients, cli)
			return nil
		})
		_, doc := hStore.Snapshot()
		savedCli = doc.Clients[0]

		hRT, rErr := NewRuntimeSupervisor(hDir, hStore, nil)
		if rErr != nil {
			t.Fatal(rErr)
		}
		t.Cleanup(func() { hRT.StopAll() })

		// Simulate frpc Login + NewProxy (__kurumi_ctrl_<id>) arriving at Server A's frps HTTP plugin
		loginBody, _ := json.Marshal(map[string]any{
			"content": map[string]any{
				"user":           savedCli.User,
				"hostname":       hostLabel,
				"os":             runtime.GOOS,
				"arch":           runtime.GOARCH,
				"client_address": "100.64.0.10:52000",
				"metas": map[string]any{
					"kurumi_client_id":   savedCli.ID,
					"kurumi_client_name": savedCli.Name,
					"kurumi_ctrl_proxy":  internalCtrlProxyPrefix + savedCli.ID,
					"kurumi_ctrl_key":    savedCli.CtrlSecret,
					"kurumi_agent_token": savedCli.AgentToken,
				},
			},
		})
		req := httptest.NewRequest(http.MethodPost, "/internal/frps-plugin/srv-A?token=plug-a&op=Login", bytes.NewReader(loginBody))
		rec := httptest.NewRecorder()
		rtA.HandleFRPSPlugin(rec, req, "srv-A")
		if rec.Code != http.StatusOK {
			t.Fatalf("Login plugin failed for %s: %d", hostLabel, rec.Code)
		}

		// Point test session's STCP bridge target to remote host's loopback agent server (simulating STCP tunnel transport)
		rtA.mu.RLock()
		procA := rtA.procs["srv-A"]
		rtA.mu.RUnlock()
		procA.mu.Lock()
		procA.remoteClients[savedCli.ID].ctrlReady = true
		procA.remoteClients[savedCli.ID].directAgentURL = fmt.Sprintf("http://127.0.0.1:%d", hRT.agentPort)
		procA.mu.Unlock()

		return hStore, hRT
	}

	// B: School PC (NAT, frpc -> A)
	storeB, _ := setupRemoteHost("school-pc-B", nil, ClientProfile{
		ID: "cli-B", Name: "学校电脑 B", ServerAddr: "203.0.113.10", ServerPort: 7000, User: "school", AuthToken: "shared-tok",
	})
	// C: Home PC (NAT, frpc -> A)
	storeC, _ := setupRemoteHost("home-pc-C", nil, ClientProfile{
		ID: "cli-C", Name: "家里电脑 C", ServerAddr: "203.0.113.10", ServerPort: 7000, User: "home", AuthToken: "shared-tok",
	})
	// D: Company PC (runs BOTH local frps srv-D AND frpc cli-D -> A mapping UDP 6768 -> 10000)
	storeD, _ := setupRemoteHost("company-pc-D", &ServerProfile{
		ID: "srv-D", Name: "公司独立服务端 D", BindPort: 7100, AuthToken: "company-private-secret",
	}, ClientProfile{
		ID: "cli-D", Name: "公司电脑客户端 D", ServerAddr: "203.0.113.10", ServerPort: 7000, User: "office", AuthToken: "shared-tok",
		Tunnels: []TunnelRule{{ID: "tun-d1", Name: "udp-6768", Mode: "proxy", Type: "udp", Enabled: true, LocalIP: "127.0.0.1", LocalPort: 6768, RemotePort: 10000}},
	})
	// E: Friend PC (NAT, frpc -> A)
	storeE, _ := setupRemoteHost("friend-pc-E", nil, ClientProfile{
		ID: "cli-E", Name: "朋友家电脑 E", ServerAddr: "203.0.113.10", ServerPort: 7000, User: "friend", AuthToken: "shared-tok",
	})

	// 1. Verify Server A sees all 4 connected frpc instances (B, C, D, E)
	_, docA := storeA.Snapshot()
	viewsA := rtA.Views(docA)
	if got := len(viewsA["srv-A"].RemoteClients); got != 4 {
		t.Fatalf("expected 4 connected remote clients on Server A, got %d", got)
	}

	// 2. Scenario Step: From Server A's WebUI, remotely map School PC B's port 7777 -> A's port 9999!
	snapB, err := rtA.FetchRemoteClient(ctx, "srv-A", "cli-B")
	if err != nil {
		t.Fatalf("FetchRemoteClient(cli-B) failed: %v", err)
	}
	profB := snapB.Profile
	profB.Tunnels = append(profB.Tunnels, TunnelRule{
		Name: "school-7777", Mode: "proxy", Type: "tcp", Enabled: true, LocalIP: "127.0.0.1", LocalPort: 7777, RemotePort: 9999,
	})
	if _, _, err := rtA.SaveRemoteClient(ctx, "srv-A", "cli-B", profB, false); err != nil {
		t.Fatalf("SaveRemoteClient(cli-B) failed: %v", err)
	}
	_, verifyDocB := storeB.Snapshot()
	if len(verifyDocB.Clients[0].Tunnels) != 1 || verifyDocB.Clients[0].Tunnels[0].LocalPort != 7777 || verifyDocB.Clients[0].Tunnels[0].RemotePort != 9999 {
		t.Fatalf("School PC B did not receive 7777->9999 mapping: %+v", verifyDocB.Clients[0].Tunnels)
	}

	// 3. Verify Isolation on Company PC D: Server A can inspect cli-D (UDP 6768->10000) but CANNOT see or alter srv-D!
	snapD, err := rtA.FetchRemoteClient(ctx, "srv-A", "cli-D")
	if err != nil || len(snapD.Profile.Tunnels) != 1 || snapD.Profile.Tunnels[0].RemotePort != 10000 {
		t.Fatalf("unexpected cli-D snapshot: %+v, err=%v", snapD, err)
	}
	_, verifyDocD := storeD.Snapshot()
	if verifyDocD.Servers[0].AuthToken != "company-private-secret" {
		t.Fatal("Company PC D's independent frps server must remain completely untouched")
	}

	// 4. Scenario Step: Orchestrate P2P between Home PC C (controlled remotely via Server A) and Friend PC E (configured locally on E)!
	p2pRes, err := rtA.OrchestrateRemoteP2P(ctx, P2PPairRequest{
		ServerID:           "srv-A",
		TunnelBaseName:     "ce-game-p2p",
		P2PType:            "xtcp",
		SecretKey:          "kurumi-p2p-key",
		HostClientID:       "cli-C",
		HostLocalIP:        "127.0.0.1",
		HostLocalPort:      25565,
		VisitorBindPort:    15565,
		KeepTunnelOpen:     true,
		EnableSTCPFallback: true,
	})
	if err != nil {
		t.Fatalf("OrchestrateRemoteP2P failed: %v", err)
	}
	_, verifyDocC := storeC.Snapshot()
	if len(verifyDocC.Clients[0].Tunnels) != 2 {
		t.Fatalf("expected XTCP + STCP fallback tunnels pushed to Home PC C, got %+v", verifyDocC.Clients[0].Tunnels)
	}

	// Friend E applies the generated visitor TOML snippet in E's own local WebUI
	snippet, _ := p2pRes["visitorTomlSnippet"].(string)
	_ = storeE.Update(func(d *ConfigDocument) error {
		curPreview := RenderClientTOML(d.Clients[0], ClientRuntimeInjections{Preview: true, MaskSecrets: false})
		updE, pErr := ApplyTOMLToClient(d.Clients[0], curPreview.TOML+"\n"+snippet)
		if pErr != nil {
			t.Fatalf("Friend E local TOML apply failed: %v", pErr)
		}
		d.Clients[0] = updE
		return nil
	})
	_, verifyDocE := storeE.Snapshot()
	if len(verifyDocE.Clients[0].Tunnels) != 2 || verifyDocE.Clients[0].Tunnels[1].ServerUser != "home" {
		t.Fatalf("Friend PC E visitor tunnels did not pair properly with C's user 'home': %+v", verifyDocE.Clients[0].Tunnels)
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
				"disabled-ssh": {ProxyName: "disabled-ssh", Enabled: false},
				"expired-web":  {ProxyName: "expired-web", Enabled: true, ExpiresAt: pastTime},
				"capped-rdp":   {ProxyName: "capped-rdp", Enabled: true, TrafficLimitMB: 1, RecordedInBytes: 2 * 1024 * 1024},
				"healthy-game": {ProxyName: "healthy-game", Enabled: true, TrafficLimitMB: 100},
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

	rt.mu.Lock()
	rt.procs["srv-guard"] = &managedProc{
		id:            "srv-guard",
		kind:          "frps",
		pluginSecret:  "sec999",
		state:         "running",
		conns:         make(map[string]ConnLiveItem),
		remoteClients: make(map[string]*remoteClientSession),
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
		{"__kurumi_ctrl_cli-test", false},
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
		{"v0.9.4", "0.9.3", 1},
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

	sid2, _ := am2.Login("127.0.0.1", "admin", "")
	if err := am2.UpdateCredentials("", "kurumi", "zafkiel-secret"); err != nil {
		t.Fatal(err)
	}
	if _, ok := am2.Authenticated(reqWithCookie(sid2)); ok {
		t.Fatal("expected session invalidated after credential rotation")
	}

	slug, ext, supported := frpPlatformSlug()
	if !supported {
		t.Skip("skipping archive hot-update test on unsupported GOOS/GOARCH")
	}

	var mockBin []byte
	if norm, err := normalizeProxyURL("127.0.0.1:7890"); err != nil || norm != "http://127.0.0.1:7890" {
		t.Fatalf("normalizeProxyURL got %q, %v", norm, err)
	}
	if runtime.GOOS == "windows" {
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

	const releases = "/fatedier/frp/releases"
	const tag = "v0.99.0"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case releases + "/latest":
			http.Redirect(w, r, releases+"/tag/"+tag, http.StatusFound)
		case releases + "/tag/" + tag:
			w.WriteHeader(http.StatusOK)
		case releases + "/download/" + tag + "/frp_sha256_checksums.txt":
			_, _ = w.Write([]byte(sumHex + "  " + assetName + "\n"))
		case releases + "/download/" + tag + "/" + assetName:
			_, _ = w.Write(archiveBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

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
	um.webBase = ts.URL
	um.allowLocal = true

	coreInfo, _, err := um.ApplyFRPUpdate(context.Background(), nil)
	if err != nil {
		t.Fatalf("ApplyFRPUpdate failed: %v", err)
	}
	if !coreInfo.FrpcFound || !coreInfo.FrpsFound || coreInfo.FrpcVersion != "0.99.0" || coreInfo.CoreSource != "data-bin" {
		t.Fatalf("unexpected updated coreInfo: %+v", coreInfo)
	}
}

func TestWinINETProxyParserAndANSIClean(t *testing.T) {
	cases := map[string]string{
		"":                      "",
		"127.0.0.1:2080":        "http://127.0.0.1:2080",
		"http://127.0.0.1:7890": "http://127.0.0.1:7890",
		"http=127.0.0.1:2080;https=127.0.0.1:2081": "http://127.0.0.1:2081",
		"socks=127.0.0.1:10808":                    "socks5://127.0.0.1:10808",
	}
	for raw, want := range cases {
		if got := parseWinINETProxyString(raw); got != want {
			t.Fatalf("parseWinINETProxyString(%q) = %q, want %q", raw, got, want)
		}
	}
	dirty := "\x1b[1;34m2026-11-15 [I] ok\x1b[0m and [0m[1;34mplain"
	if cleaned := stripANSI(dirty); strings.Contains(cleaned, "1;34m") || strings.Contains(cleaned, "[0m") {
		t.Fatalf("stripANSI left escape codes: %q", cleaned)
	}
}

func TestHDAssetPackPullSwitchAndPurge(t *testing.T) {
	dir := t.TempDir()
	hdSceneBytes := []byte("\xff\xd8\xff\xe0MOCK_FULL_RES_ORIGINAL_JPEG_K02")
	hdStickerBytes := []byte("RIFFmockWEBP_FULL_RES_TRANSPARENT_CUTOUT_K17")
	sceneSum := sha256.Sum256(hdSceneBytes)
	stickerSum := sha256.Sum256(hdStickerBytes)
	sceneHex := hex.EncodeToString(sceneSum[:])
	stickerHex := hex.EncodeToString(stickerSum[:])

	manifest := AssetPackManifest{
		SchemaVersion: 1,
		PackVersion:   "0.9.5",
		TotalAssets:   2,
		Assets: []AssetManifestEntry{
			{
				ID:       "02",
				Key:      "k_02",
				Category: "16:9",
				Title:    "午后花房红茶",
				Original: AssetFileMeta{
					Path:      "assets/material/webui_v094/originals/16x9_stage/k_02.jpg",
					Width:     1376,
					Height:    768,
					SizeBytes: int64(len(hdSceneBytes)),
					SHA256:    sceneHex,
				},
			},
			{
				ID:        "17",
				Key:       "k_17",
				Category:  "3:4",
				Title:     "粉绒披肩挥手Q版",
				IsSticker: true,
				Original: AssetFileMeta{
					Path:   "assets/material/webui_v094/originals/3x4_portraits/k_17.jpg",
					SHA256: sceneHex,
				},
				HDCutout: &AssetFileMeta{
					Path:      "assets/material/webui_v094/cutouts_hd/k_17.webp",
					Width:     896,
					Height:    1200,
					SizeBytes: int64(len(hdStickerBytes)),
					SHA256:    stickerHex,
				},
			},
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/main/assets/material/webui_v094/manifest.json":
			_ = json.NewEncoder(w).Encode(manifest)
		case "/main/assets/material/webui_v094/originals/16x9_stage/k_02.jpg":
			_, _ = w.Write(hdSceneBytes)
		case "/main/assets/material/webui_v094/cutouts_hd/k_17.webp":
			_, _ = w.Write(hdStickerBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	app, err := NewApp(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	app.updater.allowLocal = true
	app.assets.rawBase = ts.URL

	// 1. Default installation MUST serve lightweight embedded WebP
	if tier := app.assets.ActiveTier(); tier != "embedded" {
		t.Fatalf("expected default embedded tier, got %q", tier)
	}
	reqEmb := httptest.NewRequest(http.MethodGet, "/assets/k_02.webp", nil)
	recEmb := httptest.NewRecorder()
	app.ServeHTTP(recEmb, reqEmb)
	if recEmb.Code != http.StatusOK || recEmb.Header().Get("X-Kurumi-Asset-Tier") != "embedded" {
		t.Fatalf("expected embedded serve before pull, got status=%d tier=%q", recEmb.Code, recEmb.Header().Get("X-Kurumi-Asset-Tier"))
	}

	// 2. Pull HD asset pack from GitHub mock server and wait for completion
	if _, err := app.assets.StartPull(nil, "main", "webui_v094"); err != nil {
		t.Fatalf("StartPull failed: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		st := app.assets.Status()
		if !st.Downloading {
			if st.LastError != "" {
				t.Fatalf("pull failed with error: %s", st.LastError)
			}
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	st := app.assets.Status()
	if st.ActiveTier != "hd" || !st.Installed || st.InstalledCount != 2 {
		t.Fatalf("unexpected HD status after pull: %+v", st)
	}

	// 3. Verify /assets/k_02.webp now serves the HD JPEG and /assets/k_17.webp serves the HD transparent cutout WebP
	reqHD := httptest.NewRequest(http.MethodGet, "/assets/k_02.webp", nil)
	recHD := httptest.NewRecorder()
	app.ServeHTTP(recHD, reqHD)
	if recHD.Code != http.StatusOK || recHD.Header().Get("X-Kurumi-Asset-Tier") != "hd" || !bytes.Equal(recHD.Body.Bytes(), hdSceneBytes) {
		t.Fatalf("expected HD scene bytes, got status=%d tier=%q len=%d", recHD.Code, recHD.Header().Get("X-Kurumi-Asset-Tier"), recHD.Body.Len())
	}

	reqSticker := httptest.NewRequest(http.MethodGet, "/assets/k_17.webp", nil)
	recSticker := httptest.NewRecorder()
	app.ServeHTTP(recSticker, reqSticker)
	if recSticker.Code != http.StatusOK || recSticker.Header().Get("X-Kurumi-Asset-Tier") != "hd" || !bytes.Equal(recSticker.Body.Bytes(), hdStickerBytes) {
		t.Fatalf("expected HD sticker cutout bytes, got status=%d tier=%q", recSticker.Code, recSticker.Header().Get("X-Kurumi-Asset-Tier"))
	}

	// 4. Switch back to embedded mode without deleting cache, then back to HD, then purge
	if _, err := app.assets.SetPreferHD(false); err != nil {
		t.Fatalf("SetPreferHD(false) failed: %v", err)
	}
	if app.assets.ActiveTier() != "embedded" {
		t.Fatalf("expected embedded after toggle off")
	}
	if _, err := app.assets.SetPreferHD(true); err != nil {
		t.Fatalf("SetPreferHD(true) failed: %v", err)
	}
	if app.assets.ActiveTier() != "hd" {
		t.Fatalf("expected hd after toggle on")
	}
	if _, err := app.assets.Purge(); err != nil {
		t.Fatalf("Purge failed: %v", err)
	}
	if app.assets.ActiveTier() != "embedded" || app.assets.Status().Installed {
		t.Fatalf("expected clean embedded state after purge: %+v", app.assets.Status())
	}
}

func TestAuditFixes_PluginPathAndDisabledTunnelsAndAllowPorts(t *testing.T) {
	dir := t.TempDir()
	app, err := NewApp(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	// 1. F01: Verify server plugin path uses clean REST segment without '?' and HandleFRPSPlugin parses top-level JSON op
	srv := ServerProfile{
		ID:         "srv_audit",
		Name:       "AuditFRPS",
		BindAddr:   "127.0.0.1",
		BindPort:   17000,
		AllowPorts: "2000-3000, 4000",
	}
	renderedSrv := RenderServerTOML(srv, ServerRuntimeInjections{
		Preview:        false,
		AdminPort:      17500,
		AdminUser:      "kurumi",
		AdminPass:      "pass",
		KurumiCallback: "127.0.0.1:9000",
		PluginPath:     "/internal/frps-plugin/srv_audit/sec12345",
	})
	if strings.Contains(renderedSrv.TOML, "?token=") || !strings.Contains(renderedSrv.TOML, "/internal/frps-plugin/srv_audit/sec12345") {
		t.Fatalf("F01 regression: plugin path still uses query token: %s", renderedSrv.TOML)
	}

	// Simulate upstream frps httpPlugin.do appending "?op=Login&version=0.1.0" onto PluginPath
	app.runtime.mu.Lock()
	app.runtime.procs["srv_audit"] = &managedProc{
		id:            "srv_audit",
		kind:          "frps",
		pluginSecret:  "sec12345",
		state:         "running",
		logs:          newRingLogBuffer(nil),
		conns:         make(map[string]ConnLiveItem),
		rateSamples:   make(map[string]proxyRateSample),
		remoteClients: make(map[string]*remoteClientSession),
	}
	app.runtime.mu.Unlock()

	loginBody := `{"version":"0.1.0","op":"Login","content":{"user":"office","metas":{"kurumi_client_id":"cli_b","kurumi_client_name":"OfficePC","kurumi_ctrl_proxy":"__kurumi_ctrl_cli_b","kurumi_ctrl_key":"k1","kurumi_agent_token":"a1"}}}`
	req := httptest.NewRequest(http.MethodPost, "/internal/frps-plugin/srv_audit/sec12345?op=Login&version=0.1.0", strings.NewReader(loginBody))
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"reject":false`) {
		t.Fatalf("F01 regression: plugin webhook rejected valid frps Login: code=%d body=%s", rec.Code, rec.Body.String())
	}

	// 2. F05, F06, F12, F13, F25, F27: Verify disabled tunnels roundtrip via enabled=false, TLS first byte explicit false, start whitelist & allowUsers=["*"]
	cli := ClientProfile{
		ID:                        "cli_audit",
		Name:                      "AuditFRPC",
		ServerAddr:                "10.0.0.1",
		ServerPort:                7000,
		User:                      "office",
		StartProxies:              []string{"ssh-prod"},
		TLSEnable:                 true,
		TLSDisableCustomFirstByte: false,
		CtrlSecret:                "ctrl_sec",
		AgentToken:                "agent_tok",
		Tunnels: []TunnelRule{
			{ID: "t1", Name: "ssh-prod", Mode: "proxy", Type: "tcp", Enabled: true, LocalIP: "127.0.0.1", LocalPort: 22, RemotePort: 6022},
			{ID: "t2", Name: "backup-rdp", Mode: "proxy", Type: "tcp", Enabled: false, LocalIP: "127.0.0.1", LocalPort: 3389, RemotePort: 13389, Note: "keep-me"},
			{ID: "t3", Name: "vnet-node", Mode: "proxy", Type: "stcp", Enabled: false, SecretKey: "s1", Plugin: "virtual_net", PluginLocalAddr: "100.86.0.2/24"},
		},
	}
	preview := RenderClientTOML(cli, ClientRuntimeInjections{Preview: true, MaskSecrets: true})
	if !strings.Contains(preview.TOML, "transport.tls.disableCustomTLSFirstByte = false") {
		t.Fatalf("F25 regression: missing explicit disableCustomTLSFirstByte = false:\n%s", preview.TOML)
	}
	if !strings.Contains(preview.TOML, "enabled = false") {
		t.Fatalf("F05 regression: disabled tunnel missing enabled = false:\n%s", preview.TOML)
	}
	if strings.Contains(preview.TOML, "plugin.address") || !strings.Contains(preview.TOML, `virtualNet.address = "100.86.0.2/24"`) {
		t.Fatalf("F27 regression: virtual_net address placed in wrong scope:\n%s", preview.TOML)
	}

	roundtrip, err := ApplyTOMLToClient(cli, preview.TOML)
	if err != nil {
		t.Fatalf("ApplyTOMLToClient failed: %v", err)
	}
	if len(roundtrip.Tunnels) != 3 || roundtrip.Tunnels[1].Enabled || roundtrip.Tunnels[1].ID != "t2" || roundtrip.Tunnels[1].Note != "keep-me" {
		t.Fatalf("F05/F06 regression: disabled tunnel lost or inverted after TOML save: %+v", roundtrip.Tunnels)
	}

	runtimeRender := RenderClientTOML(cli, ClientRuntimeInjections{
		Preview:    false,
		AdminPort:  18000,
		AdminUser:  "kurumi",
		AdminPass:  "ap",
		AgentPort:  18001,
		ClientID:   cli.ID,
		ClientName: cli.Name,
		CtrlSecret: cli.CtrlSecret,
		AgentToken: cli.AgentToken,
	})
	if !strings.Contains(runtimeRender.TOML, `"__kurumi_ctrl_cli_audit"`) || !strings.Contains(runtimeRender.TOML, `start = ["ssh-prod", "__kurumi_ctrl_cli_audit"]`) {
		t.Fatalf("F12 regression: ctrlProxyName not appended to non-empty start whitelist:\n%s", runtimeRender.TOML)
	}
	if !strings.Contains(runtimeRender.TOML, `allowUsers = ["*"]`) {
		t.Fatalf("F13 regression: internal STCP ctrl tunnel missing allowUsers = [\"*\"]:\n%s", runtimeRender.TOML)
	}

	// 3. F29: Fail-closed allowPorts validation against full-width comma & reversed range
	if _, err := ValidateAllowPortsSpec("2000-3000，4000"); err == nil {
		t.Fatalf("F29 regression: expected error on full-width comma in allowPorts")
	}
	if _, err := ValidateAllowPortsSpec("5000-3000"); err == nil {
		t.Fatalf("F29 regression: expected error on reversed port range in allowPorts")
	}

	// 4. F23 & F24: Single-quoted '#' literal + multiline array + unknown subtable dotted key retention
	complexTOML := "serverAddr = '10.0.0.8'\nserverPort = 7000\nstart = [\n  'web#1',\n  \"ssh-2\"\n]\n\n[[proxies]]\nname = 'web#1'\ntype = 'tcp'\nlocalPort = 80\nremotePort = 8080\n[proxies.customExt]\ntoken = 'abc#123'\n"
	_, parsedCli, err := ParseTOMLAsProfile("Complex", "frpc", complexTOML)
	if err != nil {
		t.Fatalf("F23 ParseTOMLAsProfile failed: %v", err)
	}
	if len(parsedCli.StartProxies) != 2 || parsedCli.StartProxies[0] != "web#1" {
		t.Fatalf("F23 regression: multiline array or single-quote '#' failed: %+v", parsedCli.StartProxies)
	}
	if len(parsedCli.Tunnels) != 1 || !strings.Contains(parsedCli.Tunnels[0].ExtraTOML, "customExt.token = 'abc#123'") {
		t.Fatalf("F24 regression: unknown subtable key lost prefix: %+v", parsedCli.Tunnels)
	}
}

func TestAuditFixesAndPanelSettingsV097(t *testing.T) {
	dir := t.TempDir()
	app, err := NewApp(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	var reboundAddr string
	var reboundPort int
	app.SetPanelRebindHandler("127.0.0.1:9000", func(bindHost string, port int) (string, string, int, error) {
		reboundAddr = fmt.Sprintf("%s:%d", bindHost, port)
		reboundPort = port
		return reboundAddr, bindHost, port, nil
	})

	// 1. Login on initial empty password
	sid, err := app.auth.Login("127.0.0.1", "admin", "")
	if err != nil {
		t.Fatalf("initial login failed: %v", err)
	}

	// 2. A02: Cross-Site form-POST or Sec-Fetch-Site: cross-site MUST be blocked with 403
	csrfReq := httptest.NewRequest(http.MethodPost, "/api/panel/settings", strings.NewReader(`{"port":9100}`))
	csrfReq.AddCookie(&http.Cookie{Name: sessionCookie, Value: sid})
	csrfReq.Header.Set("Sec-Fetch-Site", "cross-site")
	csrfRec := httptest.NewRecorder()
	app.ServeHTTP(csrfRec, csrfReq)
	if csrfRec.Code != http.StatusForbidden {
		t.Fatalf("A02 regression: expected 403 for cross-site request, got %d", csrfRec.Code)
	}

	// 3. 4-Field Password Modification: reject mismatched confirmPassword, accept matching confirmPassword, then require currentPassword!
	badAccBody := `{"currentPassword":"","newUsername":"kurumi_admin","newPassword":"secret888","confirmPassword":"wrong888"}`
	badAccReq := httptest.NewRequest(http.MethodPost, "/api/account", strings.NewReader(badAccBody))
	badAccReq.Header.Set("Content-Type", "application/json")
	badAccReq.AddCookie(&http.Cookie{Name: sessionCookie, Value: sid})
	badAccRec := httptest.NewRecorder()
	app.ServeHTTP(badAccRec, badAccReq)
	if badAccRec.Code != http.StatusBadRequest || !strings.Contains(badAccRec.Body.String(), "不一致") {
		t.Fatalf("expected 400 mismatch error, got %d: %s", badAccRec.Code, badAccRec.Body.String())
	}

	okAccBody := `{"currentPassword":"","newUsername":"kurumi_admin","newPassword":"secret888","confirmPassword":"secret888"}`
	okAccReq := httptest.NewRequest(http.MethodPost, "/api/account", strings.NewReader(okAccBody))
	okAccReq.Header.Set("Content-Type", "application/json")
	okAccReq.AddCookie(&http.Cookie{Name: sessionCookie, Value: sid})
	okAccRec := httptest.NewRecorder()
	app.ServeHTTP(okAccRec, okAccReq)
	if okAccRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on valid 4-field password update, got %d: %s", okAccRec.Code, okAccRec.Body.String())
	}

	// Subsequent password change without currentPassword MUST be rejected
	if err := app.auth.UpdateCredentialsConfirmed("", "kurumi_admin", "next999", "next999"); err == nil {
		t.Fatalf("expected error when currentPassword is empty after password setup")
	}
	sid2, err := app.auth.Login("127.0.0.1", "kurumi_admin", "secret888")
	if err != nil {
		t.Fatalf("re-login with updated credentials failed: %v", err)
	}

	// 4. Panel Service Port modification: collision check against FRPS bindPort + hot rebind + persistence
	srvSaveReq := httptest.NewRequest(http.MethodPost, "/api/servers/save", strings.NewReader(`{"name":"MainFRPS","bindAddr":"0.0.0.0","bindPort":7000}`))
	srvSaveReq.Header.Set("Content-Type", "application/json")
	srvSaveReq.AddCookie(&http.Cookie{Name: sessionCookie, Value: sid2})
	srvSaveRec := httptest.NewRecorder()
	app.ServeHTTP(srvSaveRec, srvSaveReq)
	if srvSaveRec.Code != http.StatusOK {
		t.Fatalf("save server failed: %d %s", srvSaveRec.Code, srvSaveRec.Body.String())
	}

	// Port 7000 collides with MainFRPS -> must reject!
	colReq := httptest.NewRequest(http.MethodPost, "/api/panel/settings", strings.NewReader(`{"port":7000,"bindHost":"127.0.0.1"}`))
	colReq.Header.Set("Content-Type", "application/json")
	colReq.AddCookie(&http.Cookie{Name: sessionCookie, Value: sid2})
	colRec := httptest.NewRecorder()
	app.ServeHTTP(colRec, colReq)
	if colRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 port collision with FRPS bindPort 7000, got %d", colRec.Code)
	}

	// Valid port 9527 -> hot-rebinds and persists to panel_settings.json!
	portReq := httptest.NewRequest(http.MethodPost, "/api/panel/settings", strings.NewReader(`{"port":9527,"bindHost":"0.0.0.0"}`))
	portReq.Header.Set("Content-Type", "application/json")
	portReq.AddCookie(&http.Cookie{Name: sessionCookie, Value: sid2})
	portRec := httptest.NewRecorder()
	app.ServeHTTP(portRec, portReq)
	if portRec.Code != http.StatusOK || reboundPort != 9527 || reboundAddr != "0.0.0.0:9527" {
		t.Fatalf("expected live rebind to 0.0.0.0:9527, got code=%d addr=%q body=%s", portRec.Code, reboundAddr, portRec.Body.String())
	}
	loadedPanel := LoadPanelSettings(dir)
	if !loadedPanel.Customized || loadedPanel.Port != 9527 || loadedPanel.BindHost != "0.0.0.0" {
		t.Fatalf("panel_settings.json did not persist properly: %+v", loadedPanel)
	}

	// 5. A04: Concurrent /api/state read + /api/configs/select switch MUST be 100% race-free under -race!
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		for i := 0; i < 20; i++ {
			r := httptest.NewRequest(http.MethodGet, "/api/state", nil)
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: sid2})
			w := httptest.NewRecorder()
			app.ServeHTTP(w, r)
		}
	}()
	swReq := httptest.NewRequest(http.MethodPost, "/api/configs/select", strings.NewReader(`{"fileName":"alt_env","displayName":"备用方案"}`))
	swReq.Header.Set("Content-Type", "application/json")
	swReq.AddCookie(&http.Cookie{Name: sessionCookie, Value: sid2})
	swRec := httptest.NewRecorder()
	app.ServeHTTP(swRec, swReq)
	<-doneCh
	if swRec.Code != http.StatusOK {
		t.Fatalf("config switch failed: %d %s", swRec.Code, swRec.Body.String())
	}

	// 6. A08: Verify Go-template {{- end -}} range block parses without swallowing subsequent proxies
	rangeTOML := `
serverAddr = "1.2.3.4"
serverPort = 7000
{{- range $_, $v := parseNumberRangePair "6000-6002" "16000-16002" }}
[[proxies]]
name = "batch-{{ $v.First }}"
type = "tcp"
localPort = {{ $v.First }}
remotePort = {{ $v.Second }}
{{- end -}}

[[proxies]]
name = "after-range-ssh"
type = "tcp"
localPort = 22
remotePort = 6022
`
	_, parsedCli, err := ParseTOMLAsProfile("RangeCheck", "frpc", rangeTOML)
	if err != nil || len(parsedCli.Tunnels) != 2 || parsedCli.Tunnels[1].Name != "after-range-ssh" {
		t.Fatalf("A08 regression: range parser swallowed trailing proxy: err=%v tunnels=%+v", err, parsedCli.Tunnels)
	}
}

func TestV098ConcurrentRebindAndWebUIIntegrity(t *testing.T) {
	dir := t.TempDir()
	app, err := NewApp(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	if err := app.store.Update(func(doc *ConfigDocument) error {
		doc.Servers = append(doc.Servers, ServerProfile{
			ID:           "srv-quic-chk",
			Name:         "QUIC测试服务端",
			BindAddr:     "0.0.0.0",
			BindPort:     17000,
			KCPBindPort:  17001,
			QUICBindPort: 17002,
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	var activeMu sync.Mutex
	boundPort := 19000
	app.SetPanelRebindHandler("127.0.0.1:19000", func(bindHost string, port int) (string, string, int, error) {
		activeMu.Lock()
		defer activeMu.Unlock()
		time.Sleep(5 * time.Millisecond)
		boundPort = port
		spec := fmt.Sprintf("%s:%d", bindHost, port)
		app.SetRuntimeMeta(spec, []string{"-data", dir})
		return spec, bindHost, port, nil
	})

	if _, _, err := app.panel.UpdatePort(17001, "127.0.0.1", app.store, app.runtime); err == nil {
		t.Fatal("expected KCPBindPort conflict error")
	}
	if _, _, err := app.panel.UpdatePort(17002, "127.0.0.1", app.store, app.runtime); err == nil {
		t.Fatal("expected QUICBindPort conflict error")
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(offset int) {
			defer wg.Done()
			target := 19100 + offset
			if _, _, uErr := app.panel.UpdatePort(target, "127.0.0.1", app.store, app.runtime); uErr != nil {
				t.Errorf("concurrent UpdatePort(%d) failed: %v", target, uErr)
			}
			_, _ = app.getRuntimeMeta()
			_ = app.panel.Status()
		}(i)
	}
	wg.Wait()

	activeMu.Lock()
	finalBound := boundPort
	activeMu.Unlock()
	if st := app.panel.Status(); st.CurrentPort != finalBound || st.SavedPort != finalBound {
		t.Fatalf("serialized final port mismatch: status=%+v vs bound=%d", st, finalBound)
	}

	for _, requiredFile := range []string{"index.html", "style.css", "app.js"} {
		b, rErr := web.Files.ReadFile(requiredFile)
		if rErr != nil || len(b) < 512 {
			t.Fatalf("embedded web/%s missing or truncated: err=%v len=%d", requiredFile, rErr, len(b))
		}
	}
}
