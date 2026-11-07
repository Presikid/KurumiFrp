package manager

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func testApp(t *testing.T) *App {
	t.Helper()
	assets := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}}
	a, e := New(t.TempDir(), t.TempDir(), "127.0.0.1:9000", fs.FS(assets))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if err := a.Close(); err != nil {
			t.Error(err)
		}
	})
	return a
}
func request(a *App, method, path string, v any, token, origin string) *httptest.ResponseRecorder {
	var b bytes.Buffer
	if v != nil {
		_ = json.NewEncoder(&b).Encode(v)
	}
	r := httptest.NewRequest(method, "http://127.0.0.1:9000"+path, &b)
	r.RemoteAddr = "127.0.0.1:12345"
	if v != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	return w
}
func seed(t *testing.T, a *App) Instance {
	t.Helper()
	v := Instance{Name: "Test client", Kind: "frpc", Host: "127.0.0.1", Port: 7000, Token: "test-secret-1234"}
	w := request(a, "POST", "/api/save", map[string]any{"revision": a.store.DB.Revision, "kind": "instance", "value": v}, a.AccessToken(), "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	return a.store.DB.State.Instances[0]
}
func TestAuthentication(t *testing.T) {
	a := testApp(t)
	if w := request(a, "GET", "/api/state", nil, "", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := request(a, "GET", "/api/state", nil, a.AccessToken(), ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := request(a, "POST", "/api/login", map[string]string{"token": a.AccessToken()}, "", "http://evil.test"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	w := request(a, "POST", "/api/login", map[string]string{"token": a.AccessToken()}, "", "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	c := w.Result().Cookies()[0]
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
		t.Fatal("weak cookie")
	}
	r := httptest.NewRequest("GET", "http://127.0.0.1:9000/api/state", nil)
	r.AddCookie(c)
	w = httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	r = httptest.NewRequest("POST", "http://127.0.0.1:9000/api/logout", nil)
	r.AddCookie(c)
	w = httptest.NewRecorder()
	a.ServeHTTP(w, r)
	r = httptest.NewRequest("GET", "http://127.0.0.1:9000/api/state", nil)
	r.AddCookie(c)
	w = httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("logout did not revoke")
	}
}
func TestHostAndCrossSite(t *testing.T) {
	a := testApp(t)
	for _, h := range []string{"evil.test:9000", "127.0.0.1:9001"} {
		r := httptest.NewRequest("GET", "http://"+h+"/", nil)
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal(h, w.Code)
		}
	}
	r := httptest.NewRequest("GET", "http://127.0.0.1:9000/", nil)
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}
func TestLoginRateLimit(t *testing.T) {
	a := testApp(t)
	for j := 0; j < 10; j++ {
		request(a, "POST", "/api/login", map[string]string{"token": "wrong"}, "", "")
	}
	w := request(a, "POST", "/api/login", map[string]string{"token": a.AccessToken()}, "", "")
	if w.Code != 401 {
		t.Fatal("rate limit bypassed")
	}
}
func TestSecretsAndPersistence(t *testing.T) {
	a := testApp(t)
	i := seed(t, a)
	b, e := os.ReadFile(filepath.Join(a.store.dir, "state.json"))
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(b, []byte("test-secret-1234")) {
		t.Fatal("secret on disk")
	}
	w := request(a, "GET", "/api/state", nil, a.AccessToken(), "")
	if strings.Contains(w.Body.String(), "test-secret-1234") || strings.Contains(w.Body.String(), i.Token) {
		t.Fatal("secret exposed")
	}
	s, e := newStore(a.store.dir)
	if e != nil {
		t.Fatal(e)
	}
	plain, e := s.decrypt(s.DB.State.Instances[0].Token)
	if e != nil || plain != "test-secret-1234" {
		t.Fatal("reload decryption", e)
	}
	pub := a.store.DB.State.public().Instances[0]
	pub.Name = "New label"
	w = request(a, "POST", "/api/save", map[string]any{"revision": a.store.DB.Revision, "kind": "instance", "value": pub}, a.AccessToken(), "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if a.store.DB.State.Instances[0].Token != i.Token {
		t.Fatal("blank edit cleared token")
	}
	if e = os.Remove(filepath.Join(a.store.dir, "master.key")); e != nil {
		t.Fatal(e)
	}
	if _, e = newStore(a.store.dir); e == nil {
		t.Fatal("regenerated a lost master key")
	}
}
func TestRevisionAndHistory(t *testing.T) {
	a := testApp(t)
	seed(t, a)
	w := request(a, "POST", "/api/delete", map[string]any{"revision": 1, "kind": "instance", "id": a.store.DB.State.Instances[0].ID}, a.AccessToken(), "")
	if w.Code != 409 {
		t.Fatal(w.Code)
	}
	w = request(a, "POST", "/api/restore", map[string]any{"revision": a.store.DB.Revision, "target": 1}, a.AccessToken(), "")
	if w.Code != 200 || len(a.store.DB.State.Instances) != 0 || a.store.DB.Revision != 3 {
		t.Fatal(w.Body.String())
	}
	for j := 0; j < 40; j++ {
		if e := a.store.commit(a.store.DB.State.clone(), "test"); e != nil {
			t.Fatal(e)
		}
	}
	if len(a.store.DB.History) != 30 {
		t.Fatal("unbounded history")
	}
}
func TestInputRejection(t *testing.T) {
	a := testApp(t)
	for _, host := range []string{"host\nremotePort=123", "{{ .Envs.HOME }}", "x\";rm", "-x", "a..b", "https://test"} {
		v := Instance{Name: "bad", Kind: "frpc", Host: host, Port: 7000, Token: "secret-12345"}
		w := request(a, "POST", "/api/save", map[string]any{"revision": 1, "kind": "instance", "value": v}, a.AccessToken(), "")
		if w.Code != 400 {
			t.Fatal(host, w.Code)
		}
	}
	if a.store.DB.Revision != 1 {
		t.Fatal("failed validation persisted")
	}
	for _, addr := range []string{"0.0.0.0:9000", ":9000", "example.com:9000", "127.0.0.1:0"} {
		if loopbackListen(addr) == nil {
			t.Fatal(addr)
		}
	}
}
func TestConfigRenderAndDisabled(t *testing.T) {
	a := testApp(t)
	i := seed(t, a)
	secret, e := a.store.encrypt("a\"quoted\\secret")
	if e != nil {
		t.Fatal(e)
	}
	st := a.store.DB.State.clone()
	st.Tunnels = []Tunnel{{ID: randomHex(8), InstanceID: i.ID, Name: "web", Type: "tcp", LocalHost: "127.0.0.1", LocalPort: 80, RemotePort: 6000, Enabled: true}, {ID: randomHex(8), InstanceID: i.ID, Name: "off", Type: "udp", LocalHost: "127.0.0.1", LocalPort: 80, RemotePort: 6000, Enabled: false}, {ID: randomHex(8), InstanceID: i.ID, Name: "private", Type: "stcp", LocalHost: "127.0.0.1", LocalPort: 80, Secret: secret, Enabled: true}}
	if e = a.store.commit(st, "rules"); e != nil {
		t.Fatal(e)
	}
	r, e := a.store.render(i.ID, false)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(r.Text, "test-secret-1234") || strings.Contains(r.Text, `name = "off"`) || !strings.Contains(r.Text, ".Envs.KURUMI_AUTH_TOML") {
		t.Fatal(r.Text)
	}
	if len(r.Env) != 2 {
		t.Fatal(r.Env)
	}
	if !strings.Contains(r.Env[1], `\"`) {
		t.Fatal("TOML injection in environment")
	}
	preview, e := a.store.render(i.ID, true)
	if e != nil || len(preview.Env) != 0 || !strings.Contains(preview.Text, "<redacted>") {
		t.Fatal("unsafe preview", e)
	}
	before := r.Digest
	st.Tunnels[0].LocalPort = 81
	if e = a.store.commit(st, "changed"); e != nil {
		t.Fatal(e)
	}
	r, _ = a.store.render(i.ID, false)
	if r.Digest == before {
		t.Fatal("change invisible")
	}
}
func TestConflictsAndRelations(t *testing.T) {
	a := testApp(t)
	i := seed(t, a)
	base := Tunnel{ID: randomHex(8), InstanceID: i.ID, Name: "one", Type: "tcp", LocalHost: "127.0.0.1", LocalPort: 80, RemotePort: 6000, Enabled: true}
	st := a.store.DB.State.clone()
	st.Tunnels = []Tunnel{base}
	second := base
	second.ID = randomHex(8)
	second.Name = "two"
	st.Tunnels = append(st.Tunnels, second)
	if validate(st) == nil {
		t.Fatal("duplicate remote port accepted")
	}
	st.Tunnels[1].Type = "udp"
	if e := validate(st); e != nil {
		t.Fatal("TCP and UDP should coexist", e)
	}
	st.Tunnels[1].InstanceID = "missing"
	if validate(st) == nil {
		t.Fatal("dangling instance accepted")
	}
	st.Tunnels = st.Tunnels[:1]
	if e := a.store.commit(st, "rule"); e != nil {
		t.Fatal(e)
	}
	w := request(a, "POST", "/api/delete", map[string]any{"revision": a.store.DB.Revision, "kind": "instance", "id": i.ID}, a.AccessToken(), "")
	if w.Code != 400 {
		t.Fatal("instance with children deleted")
	}
}
func TestMissingBinaryAndBoundedLogs(t *testing.T) {
	a := testApp(t)
	i := seed(t, a)
	w := request(a, "POST", "/api/control", map[string]any{"revision": a.store.DB.Revision, "id": i.ID, "action": "start"}, a.AccessToken(), "")
	if w.Code != 400 || a.procs[i.ID] != nil {
		t.Fatal("missing binary reported running")
	}
	l := &logBuffer{secrets: []string{"split-secret"}}
	l.Write([]byte("hello split-"))
	l.Write([]byte("secret\n"))
	if strings.Contains(strings.Join(l.read(), ""), "split-secret") {
		t.Fatal("fragment leak")
	}
	l.Write([]byte(strings.Repeat("x", 18000)))
	l.Write([]byte("\n"))
	if !strings.Contains(strings.Join(l.read(), ""), "omitted") {
		t.Fatal("oversized log not dropped")
	}
	for j := 0; j < 500; j++ {
		l.Write([]byte("line\n"))
	}
	if len(l.read()) != 200 {
		t.Fatal("log limit")
	}
}
func TestStrictRequestAndEncryptionTamper(t *testing.T) {
	a := testApp(t)
	r := httptest.NewRequest("POST", "http://127.0.0.1:9000/api/login", strings.NewReader(`{"token":"x"} {}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal("trailing JSON accepted")
	}
	secret, e := a.store.encrypt("original-secret")
	if e != nil {
		t.Fatal(e)
	}
	raw := []byte(secret)
	raw[len(raw)/2] = 'A'
	if string(raw) == secret {
		raw[len(raw)/2] = 'B'
	}
	if _, e = a.store.decrypt(string(raw)); e == nil {
		t.Fatal("ciphertext tamper accepted")
	}
}
