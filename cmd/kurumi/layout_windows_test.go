//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsPrivateProfileACL(t *testing.T) {
	sid, e := currentSID()
	if e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(t.TempDir(), "private")
	if e = createPrivateDirectory(p, profileACL(sid)); e != nil {
		t.Fatal(e)
	}
	if e = createPrivateDirectory(p, profileACL(sid)); e != nil {
		t.Fatal("not repeatable", e)
	}
	if e = verifyDirectoryACL(p, machineACL); e == nil {
		t.Fatal("accepted another security policy")
	}
	outside := t.TempDir()
	link := filepath.Join(t.TempDir(), "linked")
	if e = os.Symlink(outside, link); e == nil {
		if createPrivateDirectory(link, profileACL(sid)) == nil {
			t.Fatal("accepted a linked profile")
		}
	}
}
func TestWindowsProgramDataPaths(t *testing.T) {
	root, e := windowsDataRoot()
	if e != nil {
		t.Fatal(e)
	}
	user, e := windowsUserRoot()
	if e != nil {
		t.Fatal(e)
	}
	sid, _ := currentSID()
	if user != filepath.Join(root, "profiles", sid) {
		t.Fatal("not isolated by SID")
	}
	// A forged environment must not redirect known folders into an attacker path.
	t.Setenv("ProgramData", filepath.Join(t.TempDir(), "fake"))
	again, e := windowsDataRoot()
	if e != nil || !strings.EqualFold(again, root) {
		t.Fatal("trusted an environment override")
	}
}
