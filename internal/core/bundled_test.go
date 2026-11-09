package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBundledVerifiedFallbackDoesNotOverrideUserCore(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("unsupported host")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	dir, root := t.TempDir(), t.TempDir()
	files := map[string]string{}
	for _, name := range []string{binaryName("frpc", Host()), binaryName("frps", Host()), "LICENSE"} {
		body := binary
		if name == "LICENSE" {
			body = []byte("fixture license")
		}
		if err := os.WriteFile(filepath.Join(dir, name), body, 0700); err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256(body)
		files[name] = hex.EncodeToString(h[:])
	}
	meta := Bundled{Tag: "v0.72.0", Platform: Host(), ArchiveSHA256: strings.Repeat("a", 64), Files: files}
	manifest, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(manifest)
	digest := hex.EncodeToString(h[:])
	bundle, err := readBundled(dir, digest)
	if err != nil {
		t.Fatal(err)
	}
	m := &Manager{root: root, bundled: bundle, bundledDir: dir}
	p, err := m.Binary("frpc")
	if err != nil || p != filepath.Join(dir, binaryName("frpc", Host())) {
		t.Fatalf("no bundled fallback: %s %v", p, err)
	}
	manual := filepath.Join(root, binaryName("frpc", Host()))
	if err := os.WriteFile(manual, []byte("manual"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err = m.Binary("frpc")
	if err != nil || p != manual {
		t.Fatal("manual core not preferred", err)
	}
	if _, err = m.Binary("frps"); err == nil {
		t.Fatal("mixed a partial manual core with the bundled core")
	}
	selected := "v0.72.0-" + Host() + "-" + strings.Repeat("a", 16)
	if err := writeJSON(filepath.Join(root, "active.json"), map[string]string{"id": selected}); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Binary("frpc"); err == nil {
		t.Fatal("silently replaced an unavailable selected core")
	}
	if _, err = readBundled(dir, strings.Repeat("0", 64)); err == nil {
		t.Fatal("accepted wrong manifest digest")
	}
	if err := os.WriteFile(filepath.Join(dir, "LICENSE"), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = readBundled(dir, digest); err == nil {
		t.Fatal("accepted modified bundled file")
	}
}
