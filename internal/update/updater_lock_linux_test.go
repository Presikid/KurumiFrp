//go:build linux

package update

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUpdaterLockExcludesConcurrentOperation(t *testing.T) {
	dir := t.TempDir()
	uid, gid := uint32(os.Geteuid()), uint32(os.Getegid())
	first, err := openUpdateLock(dir, uid, gid)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := openUpdateLock(dir, uid, gid); err == nil {
		second.Close()
		first.Close()
		t.Fatal("simultaneous lock accepted")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := openUpdateLock(dir, uid, gid)
	if err != nil {
		t.Fatal(err)
	}
	second.Close()
}

func TestUpdaterLockRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, ".updater.lock")); err != nil {
		t.Fatal(err)
	}
	if f, err := openUpdateLock(dir, uint32(os.Geteuid()), uint32(os.Getegid())); err == nil {
		f.Close()
		t.Fatal("linked lock accepted")
	}
	b, err := os.ReadFile(outside)
	if err != nil || string(b) != "keep" {
		t.Fatal("linked file changed")
	}
}
