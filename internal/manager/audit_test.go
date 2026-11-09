package manager

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"fmt"
	htmltemplate "html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"text/template"
	"time"
)

func TestTOMLQuoteControlsAndUnicode(t *testing.T) {
	values := []string{"", "a\"b\\c'd", "日本語 <&> \u2028 \u2029 😀", "\x7f"}
	for c := byte(0); c < 32; c++ {
		values = append(values, "before"+string([]byte{c})+"after")
	}
	for _, value := range values {
		encoded := quote(value)
		for _, r := range encoded {
			if r < 0x20 || r == 0x7f {
				t.Fatalf("literal control U+%04X in %q", r, encoded)
			}
		}
		// This encoder deliberately uses the shared subset of TOML/JSON
		// escapes. Decode independently to catch dropped or doubled quotes.
		var decoded string
		if err := json.Unmarshal([]byte(encoded), &decoded); err != nil || decoded != value {
			t.Fatalf("roundtrip %q => %q => %q: %v", value, encoded, decoded, err)
		}
	}
	if quote("\x7f") != `"\u007F"` {
		t.Fatalf("DEL was not escaped for TOML: %q", quote("\x7f"))
	}
}

func TestRenderedEnvironmentWorksWithBothTemplateEngines(t *testing.T) {
	a := testApp(t)
	i := seed(t, a)
	plain := `prefix-"quoted"\path=秘'密<&>{{ .Envs.UNEXPECTED }}`
	secret, err := a.store.encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	st := a.store.DB.State.clone()
	st.Instances[0].Token = secret
	st.Tunnels = []Tunnel{
		{ID: "0000000000000001", InstanceID: i.ID, Name: "private", Type: "stcp", LocalHost: "127.0.0.1", LocalPort: 8080, Secret: secret, Enabled: true},
		{ID: "0000000000000002", InstanceID: i.ID, Name: "visitor", Type: "stcp-visitor", ServerName: "private", LocalHost: "127.0.0.1", LocalPort: 8081, Secret: secret, Enabled: true},
	}
	if err := a.store.commit(st, "literal regression"); err != nil {
		t.Fatal(err)
	}
	rendered, err := a.store.render(i.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"UNEXPECTED": "must-not-expand"}
	for _, entry := range rendered.Env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || value != templateCredential(plain) {
			t.Fatal("environment did not contain escaped credential")
		}
		env[key] = value
	}
	textTmpl, err := template.New("frp").Parse(rendered.Text)
	if err != nil {
		t.Fatal(err)
	}
	htmlTmpl, err := htmltemplate.New("frp").Parse(rendered.Text)
	if err != nil {
		t.Fatal(err)
	}
	for _, tmpl := range []interface{ Execute(io.Writer, any) error }{textTmpl, htmlTmpl} {
		var expanded bytes.Buffer
		if err := tmpl.Execute(&expanded, struct{ Envs map[string]string }{env}); err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, line := range strings.Split(expanded.String(), "\n") {
			if strings.HasPrefix(line, "auth.token = ") || strings.HasPrefix(line, "secretKey = ") {
				_, value, _ := strings.Cut(line, " = ")
				var decoded string
				if err := json.Unmarshal([]byte(value), &decoded); err != nil || decoded != plain {
					t.Fatalf("credential changed during one-pass expansion: %q, %v", line, err)
				}
				count++
			}
		}
		if count != 3 || strings.Contains(rendered.Text, plain) {
			t.Fatal("missing credentials or secret embedded in runtime file")
		}
	}
	preview, err := a.store.render(i.ID, true)
	if err != nil || len(preview.Env) != 0 || strings.Contains(preview.Text, plain) {
		t.Fatal("preview exposed credential", err)
	}
}

func TestInvalidUTF8SecretsAreRejected(t *testing.T) {
	a := testApp(t)
	plain := "invalid-\xff-secret"
	if _, err := a.store.encrypt(plain); err == nil {
		t.Fatal("invalid UTF-8 accepted for encryption")
	}
	// An authenticated legacy/imported ciphertext can still hold invalid text.
	block, err := aes.NewCipher(a.store.key)
	if err != nil {
		t.Fatal(err)
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, g.NonceSize())
	sealed := g.Seal(nonce, nonce, []byte(plain), []byte("kurumi-v1"))
	if _, err := a.store.decrypt(base64.RawStdEncoding.EncodeToString(sealed)); err == nil {
		t.Fatal("invalid UTF-8 silently changed while rendering imported secret")
	}
}

func TestWriteIfChangedRetainsUnchangedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if changed, err := writeIfChanged(path, []byte("original")); err != nil || !changed {
		t.Fatal("initial write", changed, err)
	}
	stamp := time.Unix(946684800, 0)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := writeIfChanged(path, []byte("original")); err != nil || changed {
		t.Fatal("unchanged write", changed, err)
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("unchanged file was replaced", err)
	}
	if changed, err := writeIfChanged(path, []byte("new-data")); err != nil || !changed {
		t.Fatal("changed write", changed, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "new-data" {
		t.Fatal("replacement contents", err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatal("temporary files retained", err)
	}
	if changed, err := writeIfChanged(filepath.Dir(path), []byte("bad")); err == nil || changed {
		t.Fatal("non-regular destination accepted")
	}
}

func TestWriteIfChangedRestoresPrivatePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not Windows ACLs")
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("same"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if changed, err := writeIfChanged(path, []byte("same")); err != nil || !changed {
		t.Fatal("public file was incorrectly skipped", changed, err)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatal("private mode not restored", err)
	}
	link := filepath.Join(filepath.Dir(path), "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := writeIfChanged(link, []byte("replacement")); err == nil {
		t.Fatal("symlink accepted")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "same" {
		t.Fatal("symlink target changed", err)
	}
}

func TestStateConditionalRequestsAndInvalidation(t *testing.T) {
	a := testApp(t)
	i := seed(t, a)
	get := func(tag, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://127.0.0.1:9000/api/state", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("If-None-Match", tag)
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		return w
	}
	first := get("", a.AccessToken())
	etag := first.Header().Get("ETag")
	if first.Code != 200 || etag == "" {
		t.Fatal("missing state validator", first.Code)
	}
	cache := a.stateCache
	for _, tag := range []string{etag, "W/" + etag, `"stale", W/` + etag, "*"} {
		w := get(tag, a.AccessToken())
		if w.Code != 304 || w.Body.Len() != 0 || w.Header().Get("ETag") != etag || a.stateCache != cache {
			t.Fatal("unchanged response was serialized or sent", tag, w.Code)
		}
	}
	if w := get(etag, "wrong"); w.Code != 401 || w.Header().Get("ETag") != "" {
		t.Fatal("validator bypassed authentication")
	}
	st := a.store.DB.State.clone()
	st.Instances[0].Name = "changed"
	if err := a.store.commit(st, "change"); err != nil {
		t.Fatal(err)
	}
	w := get(etag, a.AccessToken())
	if w.Code != 200 || w.Header().Get("ETag") == etag || !strings.Contains(w.Body.String(), "changed") {
		t.Fatal("configuration cache did not invalidate")
	}
	etag = w.Header().Get("ETag")
	// No OS child is needed to test independent process-exit invalidation.
	a.procs[i.ID] = &process{status: RuntimeStatus{State: "exited", Exit: "exit 1"}}
	w = get(etag, a.AccessToken())
	if w.Code != 200 || !strings.Contains(w.Body.String(), "exit 1") {
		t.Fatal("runtime transition did not invalidate")
	}
	etag = w.Header().Get("ETag")
	a.procs[i.ID].mu.Lock()
	a.procs[i.ID].status.Exit = "exit 2"
	a.procs[i.ID].mu.Unlock()
	if w := get(etag, a.AccessToken()); w.Code != 200 || !strings.Contains(w.Body.String(), "exit 2") {
		t.Fatal("exit details did not invalidate")
	}
}

func TestStateCacheDetectsBinaryAvailability(t *testing.T) {
	a := testApp(t)
	first := request(a, "GET", "/api/state", nil, a.AccessToken(), "")
	before := a.stateCache
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	if err := os.WriteFile(filepath.Join(a.bin, "frpc"+suffix), []byte("placeholder"), 0700); err != nil {
		t.Fatal(err)
	}
	second := request(a, "GET", "/api/state", nil, a.AccessToken(), "")
	if second.Code != 200 || a.stateCache == before || second.Header().Get("ETag") == first.Header().Get("ETag") {
		t.Fatal("binary availability change did not invalidate state cache")
	}
}

func TestLoginSourceCapacityKeepsExistingCounters(t *testing.T) {
	a := testApp(t)
	now := time.Now()
	for j := 0; j < maxLoginSources; j++ {
		a.auth.attempts[fmt.Sprintf("192.0.2.%d", j)] = attempt{count: 1, since: now}
	}
	a.auth.nextCleanup = now.Add(time.Second)
	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9000/api/login", nil)
	r.RemoteAddr = "198.51.100.1:1234"
	if _, err := a.auth.login(r, a.AccessToken()); err == nil || len(a.auth.attempts) != maxLoginSources {
		t.Fatal("capacity exceeded or an active counter was evicted")
	}
	r.RemoteAddr = "192.0.2.1:1234"
	if _, err := a.auth.login(r, a.AccessToken()); err != nil {
		t.Fatal("other sources prevented an existing source logging in", err)
	}
	at := a.auth.attempts["192.0.2.1"]
	at.count = 10
	a.auth.attempts["192.0.2.1"] = at
	if _, err := a.auth.login(r, a.AccessToken()); err == nil {
		t.Fatal("rate-limit counter reset at capacity")
	}
	at.since = now.Add(-loginWindow)
	a.auth.attempts["192.0.2.1"] = at
	if _, err := a.auth.login(r, a.AccessToken()); err != nil {
		t.Fatal("expired current source was not immediately reset", err)
	}
}
