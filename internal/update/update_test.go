package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"testing"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

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
	jsonBody := `[{"tag_name":"v0.7.1.1","prerelease":true,"assets":[{"name":"KurumiFRP-0.7.1.1-Setup.exe","browser_download_url":"https://github.com/Presikid/KurumiFrp/releases/download/v0.7.1.1/KurumiFRP-0.7.1.1-Setup.exe","size":3,"digest":"sha256:` + strings.Repeat("a", 64) + `"}]}]`
	m.client = &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(jsonBody)), Header: make(http.Header)}, nil
	})}
	versions, err := m.Versions(context.Background())
	if err != nil || len(versions) != 1 || !versions[0].Available || !versions[0].Newer {
		t.Fatal(versions, err)
	}
	if _, err = m.selectAsset(context.Background(), "v0.7.1.1"); err != nil {
		t.Fatal(err)
	}
	if _, err = m.selectAsset(context.Background(), "v0.4.0"); err == nil {
		t.Fatal("downgrade accepted")
	}
}
