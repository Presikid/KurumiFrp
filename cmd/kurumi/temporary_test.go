package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTemporaryIsolationAndCleanup(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	if e := os.MkdirAll(data, 0700); e != nil {
		t.Fatal(e)
	}
	before := os.TempDir()
	clean, e := prepareTemporary(data)
	if e != nil {
		t.Fatal(e)
	}
	p := os.TempDir()
	if !inside(filepath.Join(data, "tmp"), p) {
		clean()
		t.Fatalf("temporary directory escaped: %s", p)
	}
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		if os.Getenv(key) != p {
			clean()
			t.Fatal("child environment not redirected")
		}
	}
	if e = os.WriteFile(filepath.Join(p, "probe"), []byte("test"), 0600); e != nil {
		clean()
		t.Fatal(e)
	}
	clean()
	if _, e = os.Stat(p); !os.IsNotExist(e) {
		t.Fatal("temporary files remained")
	}
	if os.TempDir() != before {
		t.Fatal("process environment not restored")
	}
	// Only a session carrying this product's marker is reclaimed.
	stale := filepath.Join(data, "tmp", "session-stale")
	unknown := filepath.Join(data, "tmp", "session-user")
	os.Mkdir(stale, 0700)
	os.Mkdir(unknown, 0700)
	os.WriteFile(filepath.Join(stale, ".kurumi-temp"), []byte(productID), 0600)
	os.WriteFile(filepath.Join(unknown, "keep"), []byte("user file"), 0600)
	clean, e = prepareTemporary(data)
	if e != nil {
		t.Fatal(e)
	}
	defer clean()
	if _, e = os.Stat(stale); !os.IsNotExist(e) {
		t.Fatal("owned interrupted session retained")
	}
	if _, e = os.Stat(filepath.Join(unknown, "keep")); e != nil {
		t.Fatal("unowned file removed")
	}
}
