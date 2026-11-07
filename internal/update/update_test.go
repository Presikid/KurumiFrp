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
func TestVersionsAndOfficialAsset(t *testing.T) {
	if !newer("v0.6.0", current) || newer("v0.4.0", current) || tagRE.MatchString("v1.2.3;bad") {
		t.Fatal("version comparison")
	}
	name := packageName("v0.6.0", "windows-installed")
	a := Asset{Name: name, URL: "https://github.com/Presikid/KurumiFrp/releases/download/v0.6.0/" + name, Size: 3, Digest: "sha256:" + strings.Repeat("a", 64)}
	if _, ok := assetFor(release{Tag: "v0.6.0", Assets: []Asset{a}}, "windows-installed"); !ok {
		t.Fatal("official asset rejected")
	}
	a.URL = "https://example.com/evil"
	if _, ok := assetFor(release{Tag: "v0.6.0", Assets: []Asset{a}}, "windows-installed"); ok {
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
