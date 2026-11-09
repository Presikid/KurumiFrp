//go:build linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestMigrationRootOwnedManualCoreException(t *testing.T) {
	path := filepath.Join(t.TempDir(), "binary")
	if err := os.WriteFile(path, []byte("core"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0755); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	st := *info.Sys().(*syscall.Stat_t)
	st.Uid = 0
	st.Gid = 0
	st.Nlink = 1
	if !safeRuntimeEntry("cores/frpc", info, &st, 123456) || !safeRuntimeEntry("cores/frps", info, &st, 123456) {
		t.Fatal("trusted manual core rejected")
	}
	if safeRuntimeEntry("data/state.json", info, &st, 123456) || safeRuntimeEntry("cores/other", info, &st, 123456) {
		t.Fatal("root-owned exception too broad")
	}
	st.Uid = 123456
	for _, mode := range []os.FileMode{0755, 0711, 0744, 0555} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		info, err = os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !safeRuntimeEntry("cores/frpc", info, &st, 123456) {
			t.Fatalf("service-owned mode %o rejected", mode)
		}
	}
	st.Nlink = 2
	if safeRuntimeEntry("cores/frpc", info, &st, 123456) {
		t.Fatal("hard-linked core accepted")
	}
	st.Nlink = 1
	st.Uid = 999
	if safeRuntimeEntry("cores/frpc", info, &st, 123456) {
		t.Fatal("untrusted core owner accepted")
	}
	st.Uid = 123456
	if err := os.Chmod(path, 0777); err != nil {
		t.Fatal(err)
	}
	info, err = os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if safeRuntimeEntry("cores/frpc", info, &st, 123456) {
		t.Fatal("writable core accepted")
	}
}

func TestMigrationMountInfoRejectsBindMountsAndMalformedInput(t *testing.T) {
	base := "1 0 8:1 / / rw - ext4 /dev/root rw\n"
	inside := "2 1 8:1 /other /legacy\\040data/nested rw - ext4 /dev/root rw\n"
	if err := mountTargetsInTrees(strings.NewReader(base), "/legacy data"); err != nil {
		t.Fatal(err)
	}
	if err := mountTargetsInTrees(strings.NewReader(base+inside), "/legacy data"); err == nil {
		t.Fatal("same-device bind mount accepted")
	}
	if err := mountTargetsInTrees(strings.NewReader(base+inside), "/legacy database"); err != nil {
		t.Fatal("rejected sibling prefix", err)
	}
	if err := mountTargetsInTrees(strings.NewReader("malformed"), "/legacy data"); err == nil {
		t.Fatal("malformed mount info accepted")
	}
	if err := mountTargetsInTrees(strings.NewReader(""), "/legacy data"); err == nil {
		t.Fatal("empty mount info accepted")
	}
	if err := mountTargetsInTrees(strings.NewReader("2 1 8:1 / /legacy\\777bad rw - ext4 /dev/root rw\n"), "/legacy"); err == nil {
		t.Fatal("unknown mount escape accepted")
	}
}

func TestMigrationGuardPreventsPublishing(t *testing.T) {
	source, dest := migrationFixture(t)
	err := copyLegacyRuntimeGuarded(source, dest, uint32(os.Geteuid()), func() error { return fmt.Errorf("a process started") })
	if err == nil {
		t.Fatal("process guard failure accepted")
	}
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		t.Fatal("destination published despite process guard")
	}
	if _, err := os.Lstat(filepath.Join(source, "data", "state.json")); err != nil {
		t.Fatal("legacy source changed", err)
	}
}

func migrationFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(root, "old")
	if err := os.MkdirAll(filepath.Join(source, "data"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "data", "state.json"), []byte("secret configuration"), 0600); err != nil {
		t.Fatal(err)
	}
	return source, filepath.Join(root, "new")
}

func TestCopyLegacyRuntimePreservesSourceAndContents(t *testing.T) {
	source, dest := migrationFixture(t)
	if err := copyLegacyRuntime(source, dest, uint32(os.Geteuid())); err != nil {
		t.Fatal(err)
	}
	if err := compareLegacyRuntime(source, dest, uint32(os.Geteuid())); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{source, dest} {
		b, err := os.ReadFile(filepath.Join(root, "data", "state.json"))
		if err != nil || string(b) != "secret configuration" {
			t.Fatalf("lost data in %s: %s %v", root, b, err)
		}
	}
	if err := copyLegacyRuntime(source, dest, uint32(os.Geteuid())); err == nil {
		t.Fatal("overwrote an existing destination")
	}
	if err := os.WriteFile(filepath.Join(dest, "data", "state.json"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := compareLegacyRuntime(source, dest, uint32(os.Geteuid())); err == nil {
		t.Fatal("different copies accepted")
	}
}

func TestCopyLegacyRuntimeRejectsLinksAndUnsafePermissions(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(string) error
	}{
		{"symlink", func(s string) error { return os.Symlink("/etc/passwd", filepath.Join(s, "link")) }},
		{"hardlink", func(s string) error {
			return os.Link(filepath.Join(s, "data", "state.json"), filepath.Join(s, "data", "alias"))
		}},
		{"world-readable", func(s string) error { return os.Chmod(filepath.Join(s, "data", "state.json"), 0644) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, dest := migrationFixture(t)
			if err := tc.setup(source); err != nil {
				t.Fatal(err)
			}
			if err := copyLegacyRuntime(source, dest, uint32(os.Geteuid())); err == nil {
				t.Fatal("unsafe source accepted")
			}
			if _, err := os.Lstat(dest); !os.IsNotExist(err) {
				t.Fatal("destination was published on failure")
			}
			stages, err := filepath.Glob(filepath.Join(filepath.Dir(dest), ".kurumi-migration-*"))
			if err != nil || len(stages) != 0 {
				t.Fatalf("failed staging retained: %v %v", stages, err)
			}
			if _, err := os.Lstat(filepath.Join(source, "data", "state.json")); err != nil {
				t.Fatal("source removed", err)
			}
		})
	}
}

func TestCopyLegacyRuntimeRejectsOverlappingAndLinkedPaths(t *testing.T) {
	source, dest := migrationFixture(t)
	if err := copyLegacyRuntime(source, filepath.Join(source, "data", "new"), uint32(os.Geteuid())); err == nil {
		t.Fatal("destination inside source accepted")
	}
	link := filepath.Join(filepath.Dir(dest), "linked-parent")
	if err := os.Symlink(source, link); err != nil {
		t.Fatal(err)
	}
	if err := copyLegacyRuntime(link, dest, uint32(os.Geteuid())); err == nil {
		t.Fatal("linked source accepted")
	}
}
