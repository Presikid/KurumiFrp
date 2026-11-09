package core

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
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "-v" {
		fmt.Println("0.72.0")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

var fixtureOnce sync.Once
var fixture []byte

func bundle(t *testing.T) []byte {
	t.Helper()
	fixtureOnce.Do(func() {
		p, e := os.Executable()
		if e != nil {
			t.Fatal(e)
		}
		b, e := os.ReadFile(p)
		if e != nil {
			t.Fatal(e)
		}
		prefix := "frp_0.72.0_" + Host() + "/"
		var out bytes.Buffer
		if strings.HasPrefix(Host(), "windows_") {
			z := zip.NewWriter(&out)
			for _, k := range []string{"frpc", "frps"} {
				w, e := z.Create(prefix + binaryName(k, Host()))
				if e != nil {
					t.Fatal(e)
				}
				w.Write(b)
			}
			z.Close()
		} else {
			gz := gzip.NewWriter(&out)
			tw := tar.NewWriter(gz)
			for _, k := range []string{"frpc", "frps"} {
				tw.WriteHeader(&tar.Header{Name: prefix + k, Mode: 0700, Size: int64(len(b)), Typeflag: tar.TypeReg})
				tw.Write(b)
			}
			tw.Close()
			gz.Close()
		}
		fixture = out.Bytes()
	})
	return fixture
}
func fixtureManager(t *testing.T, corrupt bool) (*Manager, []byte) {
	t.Helper()
	m, e := New(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(m.Close)
	b := bundle(t)
	digest := sha256.Sum256(b)
	ext := "tar.gz"
	if strings.HasPrefix(Host(), "windows_") {
		ext = "zip"
	}
	a := Asset{Name: "frp_0.72.0_" + Host() + "." + ext, Size: int64(len(b)), Digest: "sha256:" + hex.EncodeToString(digest[:])}
	if corrupt {
		a.Digest = "sha256:" + strings.Repeat("0", 64)
	}
	a.URL = "https://github.com/fatedier/frp/releases/download/v0.72.0/" + a.Name
	release := Release{Tag: "v0.72.0", Assets: []Asset{a}}
	m.client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() == a.URL {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(b)), Header: make(http.Header)}, nil
		}
		var v any = release
		if strings.Contains(r.URL.RawQuery, "per_page") {
			v = []Release{release}
		}
		body, _ := json.Marshal(v)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}, nil
	})}
	return m, b
}
func TestCatalogSelection(t *testing.T) {
	m, _ := fixtureManager(t, false)
	releases, e := m.Releases(context.Background())
	if e != nil || len(releases) != 1 {
		t.Fatal(releases, e)
	}
	r, e := m.Release(context.Background(), "v0.72.0")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = choose(r, Host()); e != nil {
		t.Fatal(e)
	}
	for _, p := range []string{"../linux_amd64", "alien_amd64", "linux_386"} {
		if _, e = choose(r, p); e == nil {
			t.Fatal("accepted", p)
		}
	}
	for _, tag := range []string{"v0.51.0", "../latest", "v1.2.3-rc1", "https://evil", "v0000.1.0"} {
		if validTag(tag) {
			t.Fatal(tag)
		}
	}
	if !validTag("v1.2.3") {
		t.Fatal("stable version rejected")
	}
	r.Assets[0].URL = "https://evil.example/binary"
	if len(cleanRelease(r).Assets) != 0 {
		t.Fatal("foreign download URL accepted")
	}
}

func TestPinnedMetadataFallbackOnAPILimit(t *testing.T) {
	m, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	m.client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 403, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}
	list, err := m.Releases(context.Background())
	if err != nil || len(list) != 1 || list[0].Tag != "v0.72.0" || !list[0].PinnedFallback {
		t.Fatalf("fallback list: %#v %v", list, err)
	}
	r, err := m.Release(context.Background(), "v0.72.0")
	if err != nil || !r.PinnedFallback {
		t.Fatalf("fallback metadata: %#v %v", r, err)
	}
	a, err := choose(r, Host())
	if err != nil || !digestRE.MatchString(a.Digest) || a.Size <= 0 || a.Size > MaxArchive {
		t.Fatalf("fallback asset: %#v %v", a, err)
	}
	if _, err = m.Release(context.Background(), "v0.71.1"); err == nil {
		t.Fatal("unverified old version accepted")
	}
	for _, asset := range r.Assets {
		if !digestRE.MatchString(asset.Digest) || asset.Size <= 0 || asset.Size > MaxArchive || !strings.HasPrefix(asset.URL, "https://github.com/fatedier/frp/releases/download/v0.72.0/") {
			t.Fatalf("unsafe asset: %#v", asset)
		}
	}
}

func TestPinnedFallbackRejectsAlteredDownload(t *testing.T) {
	m, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	m.client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "api.github.com" {
			return &http.Response{StatusCode: 403, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("tampered")), Header: make(http.Header)}, nil
	})}
	if _, err = m.install(context.Background(), "v0.72.0", Host()); err == nil {
		t.Fatal("accepted altered archive")
	}
}
func TestInstallActivateAndTamper(t *testing.T) {
	m, _ := fixtureManager(t, false)
	id, e := m.install(context.Background(), "v0.72.0", Host())
	if e != nil {
		t.Fatal(e)
	}
	s, e := m.Status()
	if e != nil || len(s.Installed) != 1 || s.Active != "" {
		t.Fatal(s, e)
	}
	if e = m.Activate(context.Background(), id); e != nil {
		t.Fatal(e)
	}
	bin, e := m.Binary("frpc")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(bin+".new", []byte("corrupted"), 0700); e != nil {
		t.Fatal(e)
	}
	// Windows may briefly deny replacement after the version probe exits.
	// Retry only test setup; persistent permission errors still fail the test.
	deadline := time.Now().Add(3 * time.Second)
	for {
		e = os.Rename(bin+".new", bin)
		if e == nil {
			break
		}
		if runtime.GOOS != "windows" || !os.IsPermission(e) || time.Now().After(deadline) {
			t.Fatal(e)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if e = m.Activate(context.Background(), id); e == nil || e.Error() != "installed binary checksum mismatch" {
		t.Fatalf("expected checksum mismatch for tampered binary, got %v", e)
	}
	s, e = m.Status()
	if e != nil || s.Active != id {
		t.Fatal("active pointer changed on failure")
	}
	if _, e = m.Binary("sh"); e == nil {
		t.Fatal("arbitrary executable selected")
	}
}
func TestStatusCacheInvalidatedAfterInstall(t *testing.T) {
	m, _ := fixtureManager(t, false)
	before, err := m.Status()
	if err != nil || len(before.Installed) != 0 {
		t.Fatalf("initial status = %#v, %v", before.Installed, err)
	}
	id, err := m.install(context.Background(), "v0.72.0", Host())
	if err != nil {
		t.Fatal(err)
	}
	after, err := m.Status()
	if err != nil || len(after.Installed) != 1 || after.Installed[0].ID != id {
		t.Fatalf("status did not observe the newly installed core: %#v, %v", after.Installed, err)
	}
}

func TestStatusCacheReturnsDefensiveManifestCopies(t *testing.T) {
	m, _ := fixtureManager(t, false)
	if _, err := m.install(context.Background(), "v0.72.0", Host()); err != nil {
		t.Fatal(err)
	}
	first, err := m.Status()
	if err != nil || len(first.Installed) != 1 {
		t.Fatalf("first status = %#v, %v", first.Installed, err)
	}
	for name := range first.Installed[0].Files {
		first.Installed[0].Files[name] = "caller-mutated"
		break
	}
	second, err := m.Status()
	if err != nil || len(second.Installed) != 1 {
		t.Fatalf("second status = %#v, %v", second.Installed, err)
	}
	for _, digest := range second.Installed[0].Files {
		if digest == "caller-mutated" {
			t.Fatal("status caller mutated the manager's cached manifest")
		}
	}
}

func TestDigestFailureDoesNotReplace(t *testing.T) {
	m, _ := fixtureManager(t, true)
	if _, e := m.install(context.Background(), "v0.72.0", Host()); e == nil {
		t.Fatal("digest accepted")
	}
	entries, _ := os.ReadDir(m.root)
	for _, e := range entries {
		if e.Name() != "versions" {
			t.Fatal("left partial files", e.Name())
		}
	}
	s, e := m.Status()
	if e != nil || len(s.Installed) != 0 || s.Active != "" {
		t.Fatal(s, e)
	}
}
func TestCoreAsyncCancelAndBusy(t *testing.T) {
	m, e := New(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	entered := make(chan struct{})
	once := sync.Once{}
	m.client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		once.Do(func() { close(entered) })
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	if e = m.Start("v0.72.0", Host()); e != nil {
		t.Fatal(e)
	}
	<-entered
	if e = m.Start("v0.72.0", Host()); e == nil {
		t.Fatal("concurrent install accepted")
	}
	m.Cancel()
	m.Close()
	s, e := m.Status()
	if e != nil || s.Job.State != "cancelled" {
		t.Fatal(s, e)
	}
	if e = m.Start("v0.72.0", Host()); e == nil {
		t.Fatal("started after closing")
	}
}
func TestArchiveRejectsUnsafeMembers(t *testing.T) {
	for _, kind := range []string{"traversal", "symlink", "hardlink", "size", "duplicate", "missing"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			var b bytes.Buffer
			gz := gzip.NewWriter(&b)
			tw := tar.NewWriter(gz)
			header := &tar.Header{Name: "frp_0.72.0_linux_amd64/frpc", Size: 1, Mode: 0700, Typeflag: tar.TypeReg}
			switch kind {
			case "traversal":
				header.Name = "../frpc"
			case "symlink":
				header.Typeflag = tar.TypeSymlink
				header.Size = 0
				header.Linkname = "/bin/sh"
			case "hardlink":
				header.Typeflag = tar.TypeLink
				header.Size = 0
				header.Linkname = "/bin/sh"
			case "size":
				header.Size = 121 << 20
			case "missing":
				header.Name = "frp_0.72.0_linux_amd64/readme"
			}
			tw.WriteHeader(header)
			if header.Size == 1 {
				tw.Write([]byte("x"))
			}
			if kind == "duplicate" {
				tw.WriteHeader(header)
				tw.Write([]byte("x"))
			}
			tw.Close()
			gz.Close()
			p := filepath.Join(root, "archive")
			os.WriteFile(p, b.Bytes(), 0600)
			dest := filepath.Join(root, "out")
			os.Mkdir(dest, 0700)
			if _, e := extract(p, dest, "test.tar.gz", "linux_amd64", "0.72.0"); e == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
}
func TestZipTraversalAndSymlink(t *testing.T) {
	for _, name := range []string{"../../frpc.exe", "frp_0.72.0_windows_amd64/../frpc.exe", "frp_0.72.0_windows_amd64/frpc.exe"} {
		t.Run(name, func(t *testing.T) {
			var b bytes.Buffer
			z := zip.NewWriter(&b)
			h := &zip.FileHeader{Name: name}
			h.SetMode(os.ModeSymlink | 0777)
			w, _ := z.CreateHeader(h)
			w.Write([]byte("C:/bad"))
			z.Close()
			root := t.TempDir()
			p := filepath.Join(root, "a.zip")
			os.WriteFile(p, b.Bytes(), 0600)
			dir := filepath.Join(root, "out")
			os.Mkdir(dir, 0700)
			if _, e := extract(p, dir, "a.zip", "windows_amd64", "0.72.0"); e == nil {
				t.Fatal("unsafe zip accepted")
			}
		})
	}
}
func TestRedirectPolicy(t *testing.T) {
	for _, address := range []string{"http://github.com/a", "https://github.com.evil/a", "https://user:secret@github.com/a", "https://github.com:8443/a", "https://127.0.0.1/a", "https://github.com/a#x"} {
		u, _ := url.Parse(address)
		if allowedURL(u) {
			t.Fatal(address)
		}
	}
	u, _ := url.Parse("https://release-assets.githubusercontent.com/a?sig=ok")
	if !allowedURL(u) {
		t.Fatal("official asset host rejected")
	}
	client := newClient()
	if client.CheckRedirect(&http.Request{URL: u}, make([]*http.Request, 5)) == nil {
		t.Fatal("redirect limit ignored")
	}
}
func TestNoDigestNoInstall(t *testing.T) {
	m, _ := fixtureManager(t, false)
	r, e := m.Release(context.Background(), "v0.72.0")
	if e != nil {
		t.Fatal(e)
	}
	r.Assets[0].Digest = ""
	r = cleanRelease(r)
	if _, e = choose(r, Host()); e == nil {
		t.Fatal("missing digest accepted")
	}
}
func TestCoreJobCompletes(t *testing.T) {
	m, _ := fixtureManager(t, false)
	if e := m.Start("v0.72.0", Host()); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 400; i++ {
		s, _ := m.Status()
		if s.Job.State == "installed" {
			if s.Job.Bytes != s.Job.Total {
				t.Fatal(s.Job)
			}
			return
		}
		if s.Job.State == "failed" {
			t.Fatal(s.Job)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("job did not finish")
}
