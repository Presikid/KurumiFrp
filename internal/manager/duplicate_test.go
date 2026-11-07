package manager

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestDuplicateInstanceAndRules(t *testing.T) {
	a := testApp(t)
	original := seed(t, a)
	source := Tunnel{ID: randomHex(8), InstanceID: original.ID, Name: "demo", Type: "tcp", LocalHost: "127.0.0.1", LocalPort: 8080, RemotePort: 6000, Enabled: true}
	st := a.store.DB.State.clone()
	st.Tunnels = append(st.Tunnels, source)
	if err := a.store.commit(st, "seed duplicate rules"); err != nil {
		t.Fatal(err)
	}
	oldRev := a.store.DB.Revision
	w := request(a, "POST", "/api/duplicate", map[string]any{"revision": oldRev, "kind": "instance", "id": original.ID, "includeTunnels": true}, a.AccessToken(), "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var result struct {
		ID          string `json:"id"`
		RulesCopied int    `json:"rulesCopied"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.RulesCopied != 1 || result.ID == original.ID {
		t.Fatalf("unexpected clone response: %s", w.Body.String())
	}
	copyInstance, ok := a.store.DB.State.Instance(result.ID)
	if !ok || copyInstance.Name == original.Name || copyInstance.Token != original.Token || copyInstance.Kind != original.Kind {
		t.Fatal("bad instance clone")
	}
	found := false
	for _, tunnel := range a.store.DB.State.Tunnels {
		if tunnel.InstanceID != result.ID {
			continue
		}
		found = true
		if tunnel.ID == source.ID || tunnel.Name != source.Name || tunnel.Enabled || tunnel.LocalPort != source.LocalPort {
			t.Fatal("unsafe tunnel clone")
		}
	}
	if !found {
		t.Fatal("rules not duplicated")
	}
	if w = request(a, "POST", "/api/duplicate", map[string]any{"revision": oldRev, "kind": "instance", "id": original.ID}, a.AccessToken(), ""); w.Code != http.StatusConflict {
		t.Fatal("stale revision accepted")
	}
	if w = request(a, "POST", "/api/duplicate", map[string]any{"revision": a.store.DB.Revision, "kind": "instance", "id": original.ID}, "", ""); w.Code != http.StatusUnauthorized {
		t.Fatal("anonymous duplication accepted")
	}
}

func TestDuplicateTunnelToOtherClientAndNameSuffix(t *testing.T) {
	a := testApp(t)
	first := seed(t, a)
	second := Instance{ID: randomHex(8), Name: "Standby", Kind: "frpc", Host: "127.0.0.2", Port: 7000, Token: first.Token}
	src := Tunnel{ID: randomHex(8), InstanceID: first.ID, Name: "demo", Type: "http", LocalHost: "127.0.0.1", LocalPort: 8080, Domains: []string{"example.com"}, Enabled: true}
	state := a.store.DB.State.clone()
	state.Instances = append(state.Instances, second)
	state.Tunnels = append(state.Tunnels, src)
	if err := a.store.commit(state, "seed copy test"); err != nil {
		t.Fatal(err)
	}
	w := request(a, "POST", "/api/duplicate", map[string]any{"revision": a.store.DB.Revision, "kind": "tunnel", "id": src.ID, "targetInstanceId": second.ID}, a.AccessToken(), "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	copyTunnel := a.store.DB.State.Tunnels[len(a.store.DB.State.Tunnels)-1]
	if copyTunnel.InstanceID != second.ID || copyTunnel.Enabled || copyTunnel.Name != "demo-copy" {
		t.Fatal("bad rule clone")
	}
	copyTunnel.Domains[0] = "mutated.example"
	if a.store.DB.State.Tunnels[0].Domains[0] != "example.com" {
		t.Fatal("domains slice aliased")
	}
	w = request(a, "POST", "/api/duplicate", map[string]any{"revision": a.store.DB.Revision, "kind": "tunnel", "id": src.ID, "targetInstanceId": second.ID}, a.AccessToken(), "")
	if w.Code != 200 || a.store.DB.State.Tunnels[len(a.store.DB.State.Tunnels)-1].Name != "demo-copy-2" {
		t.Fatal("duplicate suffix failed")
	}
}

func TestDuplicateServerPortAndAtomicRejection(t *testing.T) {
	a := testApp(t)
	first := seed(t, a)
	st := a.store.DB.State.clone()
	server := Instance{ID: randomHex(8), Name: "Server", Kind: "frps", Host: "0.0.0.0", Port: 7000, HTTPPort: 8000, HTTPSPort: 8001, Token: first.Token}
	st.Instances = append(st.Instances, server)
	if err := a.store.commit(st, "seed server"); err != nil {
		t.Fatal(err)
	}
	rev := a.store.DB.Revision
	w := request(a, "POST", "/api/duplicate", map[string]any{"revision": rev, "kind": "instance", "id": server.ID}, a.AccessToken(), "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	duplicate := a.store.DB.State.Instances[len(a.store.DB.State.Instances)-1]
	if duplicate.Port == server.Port || duplicate.HTTPPort != 0 || duplicate.HTTPSPort != 0 {
		t.Fatalf("listener collision: %#v", duplicate)
	}
	if a.store.DB.Revision != rev+1 {
		t.Fatal("expected single atomic state change")
	}
	before := a.store.DB.Revision
	w = request(a, "POST", "/api/duplicate", map[string]any{"revision": before, "kind": "tunnel", "id": "not-found"}, a.AccessToken(), "")
	if w.Code != 400 || a.store.DB.Revision != before {
		t.Fatal("invalid copy changed state")
	}
	w = request(a, "POST", "/api/duplicate", map[string]any{"revision": before, "kind": "instance", "id": server.ID, "targetInstanceId": first.ID}, a.AccessToken(), "")
	if w.Code != 400 || a.store.DB.Revision != before {
		t.Fatal("invalid request changed state")
	}
}

func TestDuplicateNameBoundariesAndSecrets(t *testing.T) {
	a := testApp(t)
	first := seed(t, a)
	st := a.store.DB.State.clone()
	st.Instances[0].Name = strings.Repeat("A", 80)
	secret, err := a.store.encrypt("stcp-secret-123456")
	if err != nil {
		t.Fatal(err)
	}
	tunnel := Tunnel{ID: randomHex(8), InstanceID: first.ID, Name: strings.Repeat("a", 64), Type: "stcp", LocalHost: "127.0.0.1", LocalPort: 8080, Secret: secret, Enabled: true}
	st.Tunnels = append(st.Tunnels, tunnel)
	if err = a.store.commit(st, "seed long labels"); err != nil {
		t.Fatal(err)
	}
	w := request(a, "POST", "/api/duplicate", map[string]any{"revision": a.store.DB.Revision, "kind": "instance", "id": first.ID, "includeTunnels": true}, a.AccessToken(), "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	clone := a.store.DB.State.Instances[len(a.store.DB.State.Instances)-1]
	if len([]rune(clone.Name)) > 80 || clone.Token != first.Token {
		t.Fatal("invalid instance copy")
	}
	copied := a.store.DB.State.Tunnels[len(a.store.DB.State.Tunnels)-1]
	if copied.Secret != secret || copied.Enabled {
		t.Fatal("secret lost or clone activated")
	}
	w = request(a, "POST", "/api/duplicate", map[string]any{"revision": a.store.DB.Revision, "kind": "tunnel", "id": tunnel.ID}, a.AccessToken(), "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	rule := a.store.DB.State.Tunnels[len(a.store.DB.State.Tunnels)-1]
	if len(rule.Name) > 64 || !namePattern.MatchString(rule.Name) {
		t.Fatal("invalid name suffix")
	}
	if strings.Contains(w.Body.String(), "stcp-secret-123456") || strings.Contains(w.Body.String(), secret) {
		t.Fatal("secret leaked")
	}
}
