package manager

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAdminPasswordInitializationWithDataLock(t *testing.T) {
	dir := t.TempDir()
	lock, err := acquireDataDirectoryLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.release()
	file, _, legacy, err := prepareAdminPassword(dir)
	if err != nil || legacy {
		t.Fatalf("fresh directory: legacy=%v err=%v", legacy, err)
	}
	ok, err := verifyAdminPassword(file, false, "token", "")
	if err != nil || !ok {
		t.Fatalf("empty password: valid=%v err=%v", ok, err)
	}
}

func TestAdminPasswordInitializationPreservesLegacy(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	lock, err := acquireDataDirectoryLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.release()
	_, _, legacy, err := prepareAdminPassword(dir)
	if err != nil || !legacy {
		t.Fatalf("existing directory: legacy=%v err=%v", legacy, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "admin-password.json")); !os.IsNotExist(err) {
		t.Fatalf("unexpected password file: %v", err)
	}
}
