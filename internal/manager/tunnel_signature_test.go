package manager

import "testing"

func TestTunnelSignatureFollowsFrpRendering(t *testing.T) {
	a := testApp(t)
	i := seed(t, a)
	tunnel := Tunnel{ID: randomHex(8), InstanceID: i.ID, Name: "web", Type: "http", LocalHost: "127.0.0.1", LocalPort: 80, Domains: []string{"example.com"}, Enabled: true, ProbePath: "/health", ProbeMethod: "HEAD"}
	first, err := a.store.tunnelSignature(tunnel)
	if err != nil {
		t.Fatal(err)
	}
	tunnel.ProbePath = "/ready"
	tunnel.ProbeMethod = "GET"
	second, err := a.store.tunnelSignature(tunnel)
	if err != nil || first != second {
		t.Fatal("diagnostic options incorrectly changed applied config", err)
	}
	tunnel.LocalPort = 81
	third, err := a.store.tunnelSignature(tunnel)
	if err != nil || third == second {
		t.Fatal("actual proxy config change was ignored", err)
	}
	secret1, err := a.store.encrypt("pair-secret-1234")
	if err != nil {
		t.Fatal(err)
	}
	secret2, err := a.store.encrypt("pair-secret-1234")
	if err != nil {
		t.Fatal(err)
	}
	tunnel.Type = "stcp"
	tunnel.Domains = nil
	tunnel.Secret = secret1
	first, err = a.store.tunnelSignature(tunnel)
	if err != nil {
		t.Fatal(err)
	}
	tunnel.Secret = secret2
	second, err = a.store.tunnelSignature(tunnel)
	if err != nil || first != second {
		t.Fatal("identical re-encrypted key incorrectly changed applied config", err)
	}
	tunnel.Secret, err = a.store.encrypt("different-secret-1234")
	if err != nil {
		t.Fatal(err)
	}
	third, err = a.store.tunnelSignature(tunnel)
	if err != nil || third == second {
		t.Fatal("changed STCP key was ignored", err)
	}
}
