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
func TestWindowsInstallDirectoryPaths(t *testing.T) {
	root, e := windowsRuntimeRoot()
	if e != nil {
		t.Fatal(e)
	}
	user, e := windowsUserRoot()
	if e != nil {
		t.Fatal(e)
	}
	sid, _ := currentSID()
	if user != filepath.Join(root, "profiles", sid) {
		t.Fatal("not isolated by SID beneath install runtime")
	}
	// A forged environment must not redirect the installation-local runtime root.
	t.Setenv("ProgramData", filepath.Join(t.TempDir(), "fake"))
	again, e := windowsRuntimeRoot()
	if e != nil || !strings.EqualFold(again, root) {
		t.Fatal("trusted an environment override")
	}
}

func windowsMigrationFixture(t *testing.T) (legacyRoot, runtimeRoot, install, sid string) {
	t.Helper()
	sid, err := currentSID()
	if err != nil {
		t.Fatal(err)
	}
	legacyRoot = filepath.Join(t.TempDir(), "ProgramData", "KurumiFRP")
	runtimeRoot = filepath.Join(t.TempDir(), "ProgramFiles", "KurumiFRP", "runtime")
	install = filepath.Dir(runtimeRoot)
	for _, dir := range []string{filepath.Dir(legacyRoot), install} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, root := range []string{legacyRoot, runtimeRoot} {
		if err := createPrivateDirectory(root, machineACL); err != nil {
			t.Fatal(err)
		}
		if err := createPrivateDirectory(filepath.Join(root, "profiles"), profilesACL); err != nil {
			t.Fatal(err)
		}
	}
	receipt := machineReceipt{productID, install, 2}
	if err := exclusiveJSON(filepath.Join(install, "native-install.json"), receipt); err != nil {
		t.Fatal(err)
	}
	if err := exclusiveJSON(filepath.Join(legacyRoot, "machine-install.json"), receipt); err != nil {
		t.Fatal(err)
	}
	if err := exclusiveJSON(filepath.Join(runtimeRoot, "machine-install.json"), receipt); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(legacyRoot, "profiles", sid)
	if err := createPrivateDirectory(profile, profileACL(sid)); err != nil {
		t.Fatal(err)
	}
	if err := exclusiveJSON(filepath.Join(profile, "profile.json"), profileReceipt{productID, sid, 1}); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"data", "cores", filepath.Join("tmp", "session-old")} {
		if err := os.MkdirAll(filepath.Join(profile, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for path, value := range map[string]string{
		filepath.Join("data", "access.token"): "0123456789abcdef",
		filepath.Join("data", "master.key"):   "test-master-key",
		filepath.Join("data", "state.json"):   "{\"format\":3,\"instances\":[],\"tunnels\":[]}",
		filepath.Join("cores", "frpc.exe"):    "synthetic frpc core",
		filepath.Join("tmp", "session-old", "probe.tmp"): "temporary child file",
	} {
		if err := os.WriteFile(filepath.Join(profile, path), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(legacyRoot, ".uninstalling"), []byte(productID), 0600); err != nil {
		t.Fatal(err)
	}
	return legacyRoot, runtimeRoot, install, sid
}

func TestMigrateLegacyWindowsProfilesIntoInstallDirectory(t *testing.T) {
	legacyRoot, runtimeRoot, install, sid := windowsMigrationFixture(t)
	if err := migrateLegacyWindowsProfiles(legacyRoot, runtimeRoot, install); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(legacyRoot); !os.IsNotExist(err) {
		t.Fatalf("legacy ProgramData directory remains: %v", err)
	}
	profile := filepath.Join(runtimeRoot, "profiles", sid)
	if _, err := verifyLegacyProfile(profile, sid); err != nil {
		t.Fatal("migrated profile failed validation:", err)
	}
	for path, want := range map[string]string{
		filepath.Join("data", "access.token"): "0123456789abcdef",
		filepath.Join("data", "master.key"):   "test-master-key",
		filepath.Join("data", "state.json"):   "{\"format\":3,\"instances\":[],\"tunnels\":[]}",
		filepath.Join("cores", "frpc.exe"):    "synthetic frpc core",
		filepath.Join("tmp", "session-old", "probe.tmp"): "temporary child file",
	} {
		got, err := os.ReadFile(filepath.Join(profile, path))
		if err != nil || string(got) != want {
			t.Fatalf("migrated %s = %q, %v; want %q", path, got, err, want)
		}
	}
	if err := verifyMachine(runtimeRoot, install); err != nil {
		t.Fatal("new installation runtime receipt failed validation:", err)
	}
}

func TestMigrateLegacyWindowsProfilesRefusesUnownedData(t *testing.T) {
	legacyRoot, runtimeRoot, install, _ := windowsMigrationFixture(t)
	if err := os.Remove(filepath.Join(legacyRoot, "machine-install.json")); err != nil {
		t.Fatal(err)
	}
	if err := migrateLegacyWindowsProfiles(legacyRoot, runtimeRoot, install); err == nil {
		t.Fatal("migrated legacy data without a valid machine ownership receipt")
	}
	if _, err := os.Stat(filepath.Join(legacyRoot, "profiles")); err != nil {
		t.Fatal("unverified legacy data was changed:", err)
	}
}

func TestMigrateLegacyWindowsProfilesResumesAfterProfileMove(t *testing.T) {
	legacyRoot, runtimeRoot, install, sid := windowsMigrationFixture(t)
	source := filepath.Join(legacyRoot, "profiles", sid)
	target := filepath.Join(runtimeRoot, "profiles", sid)
	if err := os.Rename(source, target); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(legacyRoot, "profiles")); err != nil {
		t.Fatal(err)
	}
	if err := migrateLegacyWindowsProfiles(legacyRoot, runtimeRoot, install); err != nil {
		t.Fatal("migration did not resume after profile move:", err)
	}
	if _, err := os.Lstat(legacyRoot); !os.IsNotExist(err) {
		t.Fatalf("legacy root remains after resumed migration: %v", err)
	}
	if _, err := verifyLegacyProfile(target, sid); err != nil {
		t.Fatal("resumed migration damaged the destination profile:", err)
	}
}

func TestCopyLegacyWindowsProfilePreservesContents(t *testing.T) {
	legacyRoot, _, _, sid := windowsMigrationFixture(t)
	source := filepath.Join(legacyRoot, "profiles", sid)
	target := filepath.Join(t.TempDir(), "copy")
	if err := createPrivateDirectory(target, profileACL(sid)); err != nil {
		t.Fatal(err)
	}
	before, err := profileTreeDigest(source)
	if err != nil {
		t.Fatal(err)
	}
	if err = copyLegacyProfile(source, target); err != nil {
		t.Fatal(err)
	}
	after, err := profileTreeDigest(target)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("cross-volume copy changed the profile contents")
	}
	if _, err = verifyLegacyProfile(target, sid); err != nil {
		t.Fatal("copied profile failed receipt/ACL validation:", err)
	}
}
