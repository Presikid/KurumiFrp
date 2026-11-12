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
	file, _, err := prepareAdminPassword(dir)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := verifyAdminPassword(file, "")
	if err != nil || !ok {
		t.Fatalf("empty password: valid=%v err=%v", ok, err)
	}
}

func TestAdminPasswordInitializationWithExistingData(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	lock, err := acquireDataDirectoryLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.release()
	file, _, err := prepareAdminPassword(dir)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := verifyAdminPassword(file, "")
	if err != nil || !ok {
		t.Fatalf("empty password with existing data: valid=%v err=%v", ok, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "admin-password.json")); err != nil {
		t.Fatalf("password file was not created: %v", err)
	}
}
