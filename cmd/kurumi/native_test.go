package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNativeReceiptAndPurge(t *testing.T) {
	root := t.TempDir()
	if err := initNative(root); err != nil {
		t.Fatal(err)
	}
	if err := initNative(root); err != nil {
		t.Fatal(err)
	}
	os.Mkdir(filepath.Join(root, "data"), 0700)
	os.Mkdir(filepath.Join(root, "cores"), 0700)
	os.WriteFile(filepath.Join(root, "data", "key"), []byte("secret"), 0600)
	outside := t.TempDir()
	keep := filepath.Join(outside, "keep")
	os.WriteFile(keep, []byte("user data"), 0600)
	_ = os.Symlink(outside, filepath.Join(root, "cores", "linked"))
	if err := purgeNative(root); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"data", "cores"} {
		if _, err := os.Lstat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("residue: %s", name)
		}
	}
	if b, err := os.ReadFile(keep); err != nil || string(b) != "user data" {
		t.Fatal("external file changed")
	}
	if err := readNativeReceipt(root); err != nil {
		t.Fatal("receipt should remain for installer engine", err)
	}
}

func TestNativeRefusesUnknownAndFailedShutdown(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "data"), 0700)
	if initNative(root) == nil {
		t.Fatal("adopted unknown data")
	}
	os.Remove(filepath.Join(root, "data"))
	if err := initNative(root); err != nil {
		t.Fatal(err)
	}
	os.Mkdir(filepath.Join(root, "data"), 0700)
	lock := filepath.Join(root, "data", "manager.lock")
	os.WriteFile(lock, []byte("123"), 0600)
	if purgeNative(root) == nil {
		t.Fatal("removed unresolved processes")
	}
	if _, err := os.Stat(lock); err != nil {
		t.Fatal("lost lock")
	}
	if _, err := os.Lstat(filepath.Join(root, ".uninstalling")); !os.IsNotExist(err) {
		t.Fatal("maintenance marker not released")
	}
}

func TestNativeInstallerCleanup(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "Setup.exe")
	os.WriteFile(p, []byte("synthetic installer"), 0600)
	sum, _ := fileHash(p)
	if removeSetup(p, sum, p, time.Second) == nil {
		t.Fatal("deleted running executable")
	}
	if removeSetup(p, "0000000000000000000000000000000000000000000000000000000000000000", filepath.Join(root, "kurumi.exe"), time.Second) == nil {
		t.Fatal("accepted tampered file")
	}
	if err := removeSetup(p, sum, filepath.Join(root, "kurumi.exe"), time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("installer remains")
	}
}

func TestDistributionPaths(t *testing.T) {
	old := distribution
	defer func() { distribution = old }()
	distribution = "linux-system"
	data, cores, listen, err := defaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if data != "/var/lib/kurumifrp/data" || cores != "/var/lib/kurumifrp/cores" || listen != "auto" {
		t.Fatal("incorrect system package paths")
	}
	distribution = "source"
	data, _, _, err = defaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if data != "./data" {
		t.Fatal("source CLI changed")
	}
}
