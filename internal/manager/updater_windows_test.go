//go:build windows

package manager

import (
	"path/filepath"
	"testing"
)

func TestWindowsUpdateTargetsRegisteredInstall(t *testing.T) {
	// A source build and a copied kurumi.exe must never claim to be updatable.
	copyPath := filepath.Join(t.TempDir(), "kurumi.exe")
	for _, path := range []string{"", copyPath} {
		if ok, _, hint := canPlatformSelfUpdateApp(path); ok || hint == "" {
			t.Fatalf("expected manual upgrade for %q", path)
		}
	}
	t.Run("registered native installation", func(t *testing.T) {
		dir := registeredWindowsInstallDir()
		if dir == "" {
			t.Skip("no native KurumiFRP installation registered on this host")
		}
		if ok, mode, _ := canPlatformSelfUpdateApp(filepath.Join(dir, "kurumi.exe")); !ok || mode != "windows-installer" {
			t.Fatal("registered installer path was not recognized")
		}
		if ok, _, _ := canPlatformSelfUpdateApp(filepath.Join(dir, "other.exe")); ok {
			t.Fatal("a different executable in the installation directory was accepted")
		}
	})
}
