package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalPanelURL(t *testing.T) {
	for _, base := range []string{"http://127.0.0.1:9000", "http://[::1]:1234"} {
		if !validLocalPanelURL(base) {
			t.Errorf("rejected %s", base)
		}
	}
	for _, base := range []string{"http://example.com:9000", "http://192.0.2.123:9000", "http://0.0.0.0:9000", "http://127.0.0.1", "http://127.0.0.1:9000/path", "http://name@127.0.0.1:9000"} {
		if validLocalPanelURL(base) {
			t.Errorf("accepted %s", base)
		}
	}
}

func TestStopChecksSessionAndQuitResponse(t *testing.T) {
	for _, tc := range []struct {
		name, session string
		quitStatus    int
		wantQuit      bool
	}{
		{"unrelated HTTP server", "welcome", http.StatusOK, false},
		{"invalid token", `{"version":"1.0.0","defaultUsername":"admin"}`, http.StatusUnauthorized, true},
		{"redirect", `{"version":"1.0.0","defaultUsername":"admin"}`, http.StatusTemporaryRedirect, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			quit := make(chan struct{}, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/session":
					w.Write([]byte(tc.session))
				case "/api/quit":
					quit <- struct{}{}
					if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer test-token" {
						t.Errorf("missing authenticated POST")
					}
					w.Header().Set("Location", "/unexpected")
					w.WriteHeader(tc.quitStatus)
				default:
					t.Errorf("unexpected request to %s", r.URL.Path)
				}
			}))
			defer srv.Close()
			dir := t.TempDir()
			for name, content := range map[string]string{"listen.addr": srv.URL, "access.token": "test-token"} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := stopExistingServer(dir); err == nil {
				t.Fatal("expected a stop error")
			}
			if got := len(quit) > 0; got != tc.wantQuit {
				t.Errorf("quit requested = %v", got)
			}
		})
	}
}
