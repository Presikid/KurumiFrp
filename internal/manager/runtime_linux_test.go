package manager

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A zombie no longer owns sockets or executes, but the container's init process
// may reap an orphan later. Do not mistake that for a surviving descendant.
func linuxProcessExecuting(pid int) bool {
	// In some container executors /proc is mounted from an outer PID namespace.
	// Always use the syscall first, otherwise an unrelated host PID can look like
	// our already-reaped fixture.
	group, err := syscall.Getpgid(pid)
	if errors.Is(err, syscall.ESRCH) {
		return false
	}
	if err != nil {
		return true
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return true
	}
	end := strings.LastIndexByte(string(stat), ')')
	if end < 0 {
		return true
	}
	fields := strings.Fields(string(stat[end+1:]))
	if len(fields) < 3 || fields[2] != strconv.Itoa(group) {
		return true // procfs entry belongs to another namespace/process
	}
	return fields[0] != "Z" && fields[0] != "X"
}

func assertLinuxProcessExited(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !linuxProcessExecuting(pid) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("descendant %d survived group cleanup", pid)
}

func TestOwnedGroupCleansDescendantsWhenLeaderExits(t *testing.T) {
	for _, mode := range []string{"group-child", "natural-exit"} {
		t.Run(mode, func(t *testing.T) {
			a := testApp(t)
			i := seed(t, a)
			dir := installRuntimeAuditFixture(t, a, mode)
			revision := a.store.DB.Revision
			w := request(a, "POST", "/api/control", map[string]any{"revision": revision, "id": i.ID, "action": "start"}, a.AccessToken(), "")
			if w.Code != http.StatusOK {
				t.Fatal(w.Body.String())
			}
			child, err := strconv.Atoi(string(awaitRuntimeFile(t, filepath.Join(dir, "descendant.pid"))))
			if err != nil {
				t.Fatal(err)
			}
			// Ensure even a failing assertion cannot leave the fixture behind.
			t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })
			leader, err := strconv.Atoi(string(awaitRuntimeFile(t, filepath.Join(dir, "running"))))
			if err != nil {
				t.Fatal(err)
			}
			group, err := syscall.Getpgid(child)
			if err != nil || group != leader || group == syscall.Getpgrp() {
				t.Fatal("descendant did not inherit a separate owned process group", group, leader, err)
			}
			if mode == "natural-exit" {
				if err := os.WriteFile(filepath.Join(dir, "exit-parent"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "group-child" {
				w = request(a, "POST", "/api/control", map[string]any{"revision": revision, "id": i.ID, "action": "stop"}, a.AccessToken(), "")
				if w.Code != http.StatusOK {
					t.Fatal(w.Body.String())
				}
			}
			a.mu.Lock()
			p := a.procs[i.ID]
			a.mu.Unlock()
			select {
			case <-p.done:
			case <-time.After(5 * time.Second):
				t.Fatal("child pipes prevented process supervision from completing")
			}
			assertLinuxProcessExited(t, child)
		})
	}
}

func TestCancelledVerifierCleansProcessGroup(t *testing.T) {
	a := testApp(t)
	i := seed(t, a)
	dir := installRuntimeAuditFixture(t, a, "verify-group")
	rendered, err := a.store.render(i.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	binary, err := a.binary(i.Kind)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := a.verifyBinary(ctx, binary, rendered)
		result <- err
	}()
	awaitRuntimeFile(t, filepath.Join(dir, "verifying"))
	child, err := strconv.Atoi(string(awaitRuntimeFile(t, filepath.Join(dir, "descendant.pid"))))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("cancelled verifier reported success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("verifier cancellation left inherited pipes open")
	}
	assertLinuxProcessExited(t, child)
}
