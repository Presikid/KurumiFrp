package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRateLimitMessage(t *testing.T) {
	m := New(t.TempDir(), "windows-installed")
	m.client = &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		header := make(http.Header)
		header.Set("X-RateLimit-Remaining", "0")
		return &http.Response{StatusCode: http.StatusForbidden, Body: io.NopCloser(strings.NewReader("")), Header: header}, nil
	})}
	if _, err := m.Versions(context.Background()); err == nil || !strings.Contains(err.Error(), "请求次数已用完") {
		t.Fatalf("missing useful rate limit message: %v", err)
	}
}

func TestHTMLFallbackPaginationAndDigest(t *testing.T) {
	m := New(t.TempDir(), "windows-installed")
	sha := "sha256:" + strings.Repeat("a", 64)
	m.client = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header), ContentLength: 3}
		if r.URL.Host == "api.github.com" {
			resp.StatusCode = 403
			resp.Header.Set("X-RateLimit-Remaining", "0")
			return resp, nil
		}
		if r.Method == "HEAD" {
			return resp, nil
		}
		if r.URL.Path == "/Presikid/KurumiFrp/releases" {
			if r.Header.Get("Accept") != "text/html" {
				t.Error("HTML fallback sent the API Accept header")
			}
			page := `<a href="/Presikid/KurumiFrp/releases/tag/v999.0.0.1">v</a>`
			if r.URL.Query().Get("page") == "1" {
				page += `<a href="/Presikid/KurumiFrp/releases?page=2">next</a>`
			} else {
				page = `<a href="/Presikid/KurumiFrp/releases/tag/v999.0.0.0">v</a>`
			}
			resp.Body = io.NopCloser(strings.NewReader(page))
			return resp, nil
		}
		tag := strings.TrimPrefix(r.URL.Path, "/Presikid/KurumiFrp/releases/expanded_assets/")
		name := packageName(tag, "windows-installed")
		page := `<a href="/Presikid/KurumiFrp/releases/download/` + tag + `/` + name + `">x</a><clipboard-copy aria-label="Copy to clipboard digest for ` + name + `" value="` + sha + `">`
		resp.Body = io.NopCloser(strings.NewReader(page))
		return resp, nil
	})}
	versions, err := m.Versions(context.Background())
	if err != nil || len(versions) != 2 || !versions[0].Available || versions[0].Tag != "v999.0.0.1" {
		t.Fatalf("%#v %v", versions, err)
	}
	a, err := m.selectAsset(context.Background(), "v999.0.0.1")
	if err != nil || a.Digest != sha || a.Size != 3 {
		t.Fatalf("%#v %v", a, err)
	}
	if _, _, err := htmlAssetDigest(`<a href="/Presikid/KurumiFrp/releases/download/v999.0.0.1/KurumiFRP-999.0.0.1-Setup.exe">`, "v999.0.0.1", "KurumiFRP-999.0.0.1-Setup.exe"); err == nil {
		t.Fatal("accepted missing digest")
	}
	name := "KurumiFRP-999.0.0.1-Setup.exe"
	link := `<a href="/Presikid/KurumiFrp/releases/download/v999.0.0.1/` + name + `">`
	digest := `<clipboard-copy aria-label="Copy to clipboard digest for ` + name + `" value="` + sha + `">`
	if _, _, err := htmlAssetDigest(link+digest+digest, "v999.0.0.1", name); err == nil {
		t.Fatal("accepted ambiguous digest")
	}
}

func TestLiveHTMLFallbackDownload(t *testing.T) {
	if os.Getenv("KURUMI_LIVE_UPDATES") != "1" {
		t.Skip("live network test is opt-in")
	}
	m := New(t.TempDir(), "windows-installed")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	releases, err := m.releasesHTML(ctx)
	if err != nil || len(releases) < 1 {
		t.Fatalf("releases: %v", err)
	}
	for _, r := range releases {
		if asset, ok := assetFor(r, "windows-installed"); ok {
			if _, err = m.download(ctx, asset, t.TempDir()); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatal("no verified official installer")
}

func TestFourPartVersionOrdering(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"v0.7.1.1", "v0.7.1.0", true},
		{"v0.7.2.0", "v0.7.1.999", true},
		{"v0.8.0.0", "v0.7.999.999", true},
		{"v1.0.0.0", "v0.999.999.999", true},
		{"v0.7.1.0", "v0.7.1", false},
		{"v0.7.1", "v0.7.1.1", false},
		{"v0.6.0", "v0.7.1.0", false},
		{"v0.7.1.1;bad", "v0.7.1.0", false},
		{"v0.7.1.1", "invalid", false},
		{"v0.7.1.1000", "v0.7.1.999", true},
		{"v0.7.1.65536", "v0.7.1.999", false},
	}
	for _, tc := range cases {
		if got := newer(tc.a, tc.b); got != tc.want {
			t.Errorf("newer(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestFourPartReleaseSelection(t *testing.T) {
	name := packageName("v0.7.1.1", "windows-installed")
	a := Asset{Name: name, URL: "https://github.com/Presikid/KurumiFrp/releases/download/v0.7.1.1/" + name, Size: 3, Digest: "sha256:" + strings.Repeat("a", 64)}
	if name != "KurumiFRP-0.7.1.1-Setup.exe" {
		t.Fatal(name)
	}
	if _, ok := assetFor(release{Tag: "v0.7.1.1", Assets: []Asset{a}}, "windows-installed"); !ok {
		t.Fatal("four-part release asset rejected")
	}
}
func TestVersionsAndOfficialAsset(t *testing.T) {
	if !newer("v0.8.0", current) || newer("v0.4.0", current) || tagRE.MatchString("v1.2.3;bad") {
		t.Fatal("version comparison")
	}
	name := packageName("v0.8.0", "windows-installed")
	a := Asset{Name: name, URL: "https://github.com/Presikid/KurumiFrp/releases/download/v0.8.0/" + name, Size: 3, Digest: "sha256:" + strings.Repeat("a", 64)}
	if _, ok := assetFor(release{Tag: "v0.8.0", Assets: []Asset{a}}, "windows-installed"); !ok {
		t.Fatal("official asset rejected")
	}
	a.URL = "https://example.com/evil"
	if _, ok := assetFor(release{Tag: "v0.8.0", Assets: []Asset{a}}, "windows-installed"); ok {
		t.Fatal("untrusted asset accepted")
	}
}
func TestDownloadChecksDigestAndSize(t *testing.T) {
	m := New(t.TempDir(), "windows-installed")
	m.client = &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("abc")), Header: make(http.Header)}, nil
	})}
	sum := sha256.Sum256([]byte("abc"))
	a := Asset{Name: "setup.exe", URL: "https://github.com/a", Size: 3, Digest: "sha256:" + hex.EncodeToString(sum[:])}
	if _, e := m.download(context.Background(), a, t.TempDir()); e != nil {
		t.Fatal(e)
	}
	a.Digest = "sha256:" + strings.Repeat("0", 64)
	if _, e := m.download(context.Background(), a, t.TempDir()); e == nil {
		t.Fatal("invalid digest accepted")
	}
}

func TestReleaseListAndSelection(t *testing.T) {
	m := New(t.TempDir(), "windows-installed")
	jsonBody := `[{"tag_name":"v999.0.0.0","prerelease":true,"assets":[{"name":"KurumiFRP-999.0.0.0-Setup.exe","browser_download_url":"https://github.com/Presikid/KurumiFrp/releases/download/v999.0.0.0/KurumiFRP-999.0.0.0-Setup.exe","size":3,"digest":"sha256:` + strings.Repeat("a", 64) + `"}]}]`
	m.client = &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(jsonBody)), Header: make(http.Header)}, nil
	})}
	versions, err := m.Versions(context.Background())
	if err != nil || len(versions) != 1 || !versions[0].Available || !versions[0].Newer {
		t.Fatal(versions, err)
	}
	if _, err = m.selectAsset(context.Background(), "v999.0.0.0"); err != nil {
		t.Fatal(err)
	}
	if _, err = m.selectAsset(context.Background(), "v0.4.0"); err == nil {
		t.Fatal("downgrade accepted")
	}
}

func TestVersionsSortNumerically(t *testing.T) {
	m := New(t.TempDir(), "source")
	jsonBody := `[
		{"tag_name":"v0.7.1.9"},
		{"tag_name":"v0.7.1.10"},
		{"tag_name":"v0.7.2"},
		{"tag_name":"v0.7.1.4"},
		{"tag_name":"v0.7.1.5"}
	]`
	m.client = &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(jsonBody)), Header: make(http.Header)}, nil
	})}
	versions, err := m.Versions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"v0.7.2", "v0.7.1.10", "v0.7.1.9", "v0.7.1.5", "v0.7.1.4"}
	if len(versions) != len(want) {
		t.Fatalf("got %d versions, want %d", len(versions), len(want))
	}
	for i, tag := range want {
		if versions[i].Tag != tag {
			t.Errorf("version %d = %q, want %q", i, versions[i].Tag, tag)
		}
	}
}
