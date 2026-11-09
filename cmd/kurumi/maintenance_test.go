package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Presikid/KurumiFrp/internal/version"
)

func packageFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(root, "package")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	m := packageManifest{Product: productID, Version: version.Current, Platform: "linux_amd64", Files: map[string]string{}}
	names, _ := packageNames(m.Platform)
	for _, name := range names {
		p := filepath.Join(source, name)
		if err := os.WriteFile(p, []byte("test "+name), 0600); err != nil {
			t.Fatal(err)
		}
		sum, err := fileHash(p)
		if err != nil {
			t.Fatal(err)
		}
		m.Files[name] = sum
	}
	b, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(source, "PACKAGE-MANIFEST.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	return source, filepath.Join(root, "installed", "kurumi")
}
func TestPackageCleanupAndUnrelatedFiles(t *testing.T) {
	for _, unrelated := range []bool{false, true} {
		source, exe := packageFixture(t)
		if unrelated {
			os.WriteFile(filepath.Join(source, "my-document.txt"), []byte("keep"), 0600)
		}
		if err := cleanPackage(source, exe); err != nil {
			t.Fatal(err)
		}
		if unrelated {
			b, e := os.ReadFile(filepath.Join(source, "my-document.txt"))
			if e != nil || string(b) != "keep" {
				t.Fatal("unrelated data removed")
			}
		} else if _, err := os.Stat(source); !os.IsNotExist(err) {
			t.Fatal("empty payload remains")
		}
	}
}
func TestPackageCleanupRefusesTamperingWithoutPartialDeletion(t *testing.T) {
	for _, which := range []string{"hash", "path", "product", "self"} {
		t.Run(which, func(t *testing.T) {
			source, exe := packageFixture(t)
			switch which {
			case "hash":
				os.WriteFile(filepath.Join(source, "INSTALL.md"), []byte("user edit"), 0600)
			case "self":
				exe = filepath.Join(source, "kurumi")
			default:
				p := filepath.Join(source, "PACKAGE-MANIFEST.json")
				raw, _ := os.ReadFile(p)
				var m packageManifest
				json.Unmarshal(raw, &m)
				if which == "path" {
					delete(m.Files, "INSTALL.md")
					m.Files["../other.txt"] = strings.Repeat("a", 64)
				} else {
					m.Product = "not-kurumi"
				}
				raw, _ = json.Marshal(m)
				os.WriteFile(p, raw, 0600)
			}
			if err := cleanPackage(source, exe); err == nil {
				t.Fatal("unsafe cleanup accepted")
			}
			if _, err := os.Stat(filepath.Join(source, "kurumi")); err != nil {
				t.Fatal("partial cleanup before validation")
			}
		})
	}
}
func TestPackageCleanupRejectsSymlinks(t *testing.T) {
	source, exe := packageFixture(t)
	original := filepath.Join(source, "INSTALL.md")
	external := filepath.Join(t.TempDir(), "document")
	os.WriteFile(external, []byte("keep"), 0600)
	os.Remove(original)
	if err := os.Symlink(external, original); err != nil {
		t.Skip("symlink unavailable")
	}
	if err := cleanPackage(source, exe); err == nil {
		t.Fatal("symlink accepted")
	}
	b, _ := os.ReadFile(external)
	if string(b) != "keep" {
		t.Fatal("outside file changed")
	}
}
func localFixture(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "access.token"), []byte(strings.Repeat("a", 64)), 0600)
	os.WriteFile(filepath.Join(dir, "listen.addr"), []byte(strings.TrimPrefix(server.URL, "http://")), 0600)
	return dir
}
func TestMaintenanceChecksAuthAndShutdown(t *testing.T) {
	var dir string
	dir = localFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("a", 64) {
			t.Error("wrong auth")
			w.WriteHeader(401)
			return
		}
		if r.URL.Path == "/api/quit" {
			if r.Method != "POST" {
				t.Error("wrong method")
			}
			os.Remove(filepath.Join(dir, "manager.lock"))
			w.Write([]byte(`{"ok":true}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"version": version.Current})
	})
	if err := checkRunning(dir, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "manager.lock"), []byte("123"), 0600)
	if err := stopExisting(dir, time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "manager.lock")); !os.IsNotExist(err) {
		t.Fatal("lock still present")
	}
}
func TestMaintenanceFailurePreservesFiles(t *testing.T) {
	dir := localFixture(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) })
	lock := filepath.Join(dir, "manager.lock")
	os.WriteFile(lock, []byte("123"), 0600)
	if err := stopExisting(dir, time.Millisecond); err == nil {
		t.Fatal("unauthenticated stop succeeded")
	}
	if _, err := os.Stat(lock); err != nil {
		t.Fatal("lock removed on failure")
	}
	os.WriteFile(filepath.Join(dir, "listen.addr"), []byte("example.com:9000"), 0600)
	if err := localRequest(dir, "GET", "/api/state"); err == nil {
		t.Fatal("non-loopback accepted")
	}
}

func TestOriginalArchiveCleanupIsContentVerified(t *testing.T) {
	for _, format := range []string{"zip", "tar.gz"} {
		for _, extra := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s-extra-%v", format, extra), func(t *testing.T) {
				source, exe := packageFixture(t)
				archive := filepath.Join(filepath.Dir(source), "source."+format)
				f, err := os.Create(archive)
				if err != nil {
					t.Fatal(err)
				}
				entries, _ := os.ReadDir(source)
				if format == "zip" {
					z := zip.NewWriter(f)
					for _, e := range entries {
						w, _ := z.Create("KurumiFRP/" + e.Name())
						b, _ := os.ReadFile(filepath.Join(source, e.Name()))
						w.Write(b)
					}
					if extra {
						w, _ := z.Create("my-document.txt")
						w.Write([]byte("keep"))
					}
					z.Close()
				} else {
					gz := gzip.NewWriter(f)
					tw := tar.NewWriter(gz)
					for _, e := range entries {
						b, _ := os.ReadFile(filepath.Join(source, e.Name()))
						tw.WriteHeader(&tar.Header{Name: "KurumiFRP/" + e.Name(), Mode: 0600, Size: int64(len(b))})
						tw.Write(b)
					}
					if extra {
						tw.WriteHeader(&tar.Header{Name: "my-document.txt", Mode: 0600, Size: 4})
						tw.Write([]byte("keep"))
					}
					tw.Close()
					gz.Close()
				}
				f.Close()
				err = cleanPackage(source, exe, archive)
				if extra {
					if err == nil {
						t.Fatal("archive with unrelated content deleted")
					}
					if _, e := os.Stat(filepath.Join(source, "kurumi")); e != nil {
						t.Fatal("payload deleted on archive mismatch")
					}
					if _, e := os.Stat(archive); e != nil {
						t.Fatal("unrelated archive lost")
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					if _, e := os.Stat(archive); !os.IsNotExist(e) {
						t.Fatal("archive remains")
					}
					if _, e := os.Stat(source); !os.IsNotExist(e) {
						t.Fatal("source remains")
					}
				}
			})
		}
	}
}
