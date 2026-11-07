package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAutoListenRejectsInvalidSavedAddress(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:9000", "evil.test:9000", "127.0.0.1:443", "127.0.0.1:9011"} {
		dir := t.TempDir()
		if e := os.WriteFile(filepath.Join(dir, "listen.addr"), []byte(addr), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e := autoListen(dir); e == nil {
			t.Fatal("invalid saved address accepted", addr)
		}
	}
}
func TestRememberAddress(t *testing.T) {
	dir := t.TempDir()
	if e := saveListen(dir, "127.0.0.1:9007"); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "manager.lock"), []byte("existing"), 0600); e != nil {
		t.Fatal(e)
	}
	addr, e := autoListen(dir)
	if e != nil || addr != "127.0.0.1:9007" {
		t.Fatal(addr, e)
	}
}
