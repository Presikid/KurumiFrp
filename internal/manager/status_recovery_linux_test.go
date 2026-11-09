//go:build linux

package manager

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestStatusRecoveryRequiresDeadProcessesAndOwnedFiles(t *testing.T) {
	identity := statusSystemIdentity()
	if identity == "" {
		t.Skip("process namespace identity unavailable")
	}
	root := t.TempDir()
	makeStatus := func(owner, child int) *privateStatus {
		s, err := newPrivateStatus(root)
		if err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(statusMetadata{Version: 1, OwnerPID: owner, ChildPID: child, Identity: identity})
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(s.dir, "owner.json"), b, 0600); err != nil {
			t.Fatal(err)
		}
		return s
	}
	const missingOwner = 2147483647
	const missingChild = 2147483646
	if !definitelyDead(missingOwner) || !definitelyDead(missingChild) {
		t.Skip("test process IDs not provably absent")
	}
	dead := makeStatus(missingOwner, missingChild)
	pending := makeStatus(missingOwner, 0)
	liveOwner := makeStatus(os.Getpid(), missingChild)
	liveChild := makeStatus(missingOwner, os.Getpid())
	unknown := makeStatus(missingOwner, missingChild)
	if err := os.WriteFile(filepath.Join(unknown.dir, "unrecognized"), []byte("retain"), 0600); err != nil {
		t.Fatal(err)
	}
	link := makeStatus(missingOwner, missingChild)
	if err := os.Remove(filepath.Join(link.dir, "key.pem")); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "outside")
	if err := os.WriteFile(target, []byte("retain"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(link.dir, "key.pem")); err != nil {
		t.Fatal(err)
	}
	fifo := makeStatus(missingOwner, missingChild)
	if err := os.Remove(filepath.Join(fifo.dir, "key.pem")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(fifo.dir, "key.pem"), 0600); err != nil {
		t.Fatal(err)
	}
	reapPrivateStatuses(root)
	if _, err := os.Stat(dead.dir); !os.IsNotExist(err) {
		t.Fatalf("dead owned directory retained: %v", err)
	}
	for _, s := range []*privateStatus{pending, liveOwner, liveChild, unknown, link, fifo} {
		if _, err := os.Lstat(s.dir); err != nil {
			t.Fatalf("uncertain directory removed: %s: %v", s.dir, err)
		}
	}
	if b, err := os.ReadFile(target); err != nil || string(b) != "retain" {
		t.Fatal("link target changed", err)
	}
}

func TestStatusRecoveryProcessHelper(t *testing.T) {
	if os.Getenv("KURUMI_RECOVERY_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	for {
		time.Sleep(time.Second)
	}
}

func TestStatusRecoveryAfterProcessCrash(t *testing.T) {
	identity := statusSystemIdentity()
	if identity == "" {
		t.Skip("process namespace identity unavailable")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	start := func() *exec.Cmd {
		cmd := exec.Command(exe, "-test.run=^TestStatusRecoveryProcessHelper$")
		cmd.Env = append(os.Environ(), "KURUMI_RECOVERY_HELPER=1")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
		return cmd
	}
	owner, child := start(), start()
	root := t.TempDir()
	s, err := newPrivateStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.cleanup()
	meta := statusMetadata{Version: 1, OwnerPID: owner.Process.Pid, ChildPID: child.Process.Pid, Identity: identity}
	b, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.dir, "owner.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	_ = owner.Process.Kill()
	_ = owner.Wait()
	reapPrivateStatuses(root)
	if _, err = os.Stat(s.dir); err != nil {
		t.Fatal("removed directory while child alive", err)
	}
	_ = child.Process.Kill()
	_ = child.Wait()
	if !definitelyDead(meta.OwnerPID) || !definitelyDead(meta.ChildPID) {
		t.Skip("process IDs reused or liveness indeterminate")
	}
	reapPrivateStatuses(root)
	if _, err = os.Stat(s.dir); !os.IsNotExist(err) {
		t.Fatalf("dead processes' owned directory retained: %v", err)
	}
}
