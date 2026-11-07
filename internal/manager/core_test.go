package manager

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestCoreAPIRequiresAuth(t *testing.T) {
	a := testApp(t)
	for _, p := range []string{"/api/core", "/api/core/releases", "/api/core/install", "/api/core/activate"} {
		method := "GET"
		var body any
		if strings.HasSuffix(p, "install") || strings.HasSuffix(p, "activate") {
			method = "POST"
			body = map[string]string{}
		}
		if r := request(a, method, p, body, "", ""); r.Code != 401 {
			t.Fatal(p, r.Code)
		}
	}
	if r := request(a, "GET", "/api/core", nil, a.AccessToken(), ""); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r := request(a, "POST", "/api/core/install", map[string]string{"tag": "../../evil", "platform": "bad"}, a.AccessToken(), ""); r.Code != 409 {
		t.Fatal(r.Code)
	}
	if r := request(a, "POST", "/api/core/activate", map[string]string{"id": "bad"}, a.AccessToken(), "https://evil"); r.Code != 403 {
		t.Fatal(r.Code)
	}
	a.procs["test"] = &process{status: RuntimeStatus{State: "running"}}
	if r := request(a, "POST", "/api/core/activate", map[string]string{"id": "bad"}, a.AccessToken(), ""); r.Code != 409 {
		t.Fatal("activation not blocked while running", r.Code)
	}
	delete(a.procs, "test")
}
func TestOneTimeBrowserTicket(t *testing.T) {
	a := testApp(t)
	address := a.BrowserURL()
	if strings.Contains(address, a.AccessToken()) {
		t.Fatal("persistent token in URL")
	}
	u, _ := url.Parse(address)
	ticket := strings.TrimPrefix(u.Fragment, "launch=")
	r := request(a, "POST", "/api/launch-login", map[string]string{"ticket": ticket}, "", "")
	if r.Code != 200 || len(r.Result().Cookies()) != 1 {
		t.Fatal(r.Code, r.Body.String())
	}
	cookie := r.Result().Cookies()[0]
	req := httptest.NewRequest("GET", "http://127.0.0.1:9000/api/core", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	if r = request(a, "POST", "/api/launch-login", map[string]string{"ticket": ticket}, "", ""); r.Code != 401 {
		t.Fatal("ticket replay accepted")
	}
	expired := strings.TrimPrefix(strings.Split(a.BrowserURL(), "#")[1], "launch=")
	a.auth.mu.Lock()
	a.auth.tickets[expired] = time.Now().Add(-time.Second)
	a.auth.mu.Unlock()
	if r = request(a, "POST", "/api/launch-login", map[string]string{"ticket": expired}, "", ""); r.Code != 401 {
		t.Fatal("expired ticket accepted")
	}
	r = request(a, "POST", "/api/browser-ticket", map[string]any{}, a.AccessToken(), "")
	var data map[string]string
	json.Unmarshal(r.Body.Bytes(), &data)
	if !strings.Contains(data["url"], "#launch=") {
		t.Fatal(data)
	}
}
func TestQuitRequiresAuthentication(t *testing.T) {
	a := testApp(t)
	if r := request(a, "POST", "/api/quit", map[string]any{}, "", ""); r.Code != 401 {
		t.Fatal(r.Code)
	}
	if r := request(a, "POST", "/api/quit", map[string]any{}, a.AccessToken(), ""); r.Code != 200 {
		t.Fatal(r.Code)
	}
	select {
	case <-a.Done():
	default:
		t.Fatal("quit not signalled")
	}
}
