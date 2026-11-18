package manager

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Presikid/KurumiFrp/internal/version"
)

func TestNativeLinuxInstallKeepsPackageManagerOwnership(t *testing.T) {
	old := version.Distribution
	defer func() { version.Distribution = old }()
	version.Distribution = "linux-system"
	if can, mode, _ := canPlatformSelfUpdateApp(filepath.Join(t.TempDir(), "kurumifrp")); can || mode != "linux-package" {
		t.Fatal("native installation in writable directory offered portable update")
	}
}

func TestDockerInstallUsesImageUpdates(t *testing.T) {
	old := version.Distribution
	defer func() { version.Distribution = old }()
	version.Distribution = "docker"
	can, mode, hint := canPlatformSelfUpdateApp(filepath.Join(t.TempDir(), "kurumifrp"))
	if can || mode != "manual" || hint == "" {
		t.Fatalf("writable container offered binary replacement: %t %s %q", can, mode, hint)
	}
}

func portableFixture(t *testing.T, dir string) string {
	t.Helper()
	pkgDir := filepath.Join(dir, "download")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(pkgDir, "release.tar.gz")
	f, err := os.Create(pkg)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, body := range map[string]string{
		"kurumifrp":    "#!/bin/sh\nif [ \"$1\" = -version ]; then echo 1.2.3; else printf '%s\\n' \"$PWD\" \"$@\" > launched; fi\n",
		"bundled/frpc": "new client", "bundled/frps": "new server", "bundled/LICENSE": "license", "bundled/manifest.json": "{}",
	} {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	for _, close := range []func() error{tw.Close, gz.Close, f.Close} {
		if err := close(); err != nil {
			t.Fatal(err)
		}
	}
	return pkg
}

func TestPortableUpdateRestoresPreviousFiles(t *testing.T) {
	dir := t.TempDir()
	pkg := portableFixture(t, dir)
	exe := filepath.Join(dir, "kurumifrp")
	bundled := filepath.Join(dir, "bundled")
	// A directory occupying the server's filename makes its replacement fail after frpc.
	if err := os.MkdirAll(filepath.Join(bundled, "frps"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{exe, filepath.Join(bundled, "frpc"), filepath.Join(bundled, "custom")} {
		if err := os.WriteFile(p, []byte("old"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, version := range []string{"wrong-version", "1.2.3"} {
		if err := installPortableUpdate(pkg, exe, version, nil); err == nil {
			t.Fatal("accepted failed upgrade", version)
		}
		for _, p := range []string{exe, filepath.Join(bundled, "frpc"), filepath.Join(bundled, "custom")} {
			if b, err := os.ReadFile(p); err != nil || string(b) != "old" {
				t.Fatalf("%s: %q %v", p, b, err)
			}
		}
	}
}

func TestPortableRestartWaitsForExit(t *testing.T) {
	const childEnv = "KURUMI_RESTART_TEST_DIR"
	if dir := os.Getenv(childEnv); dir != "" {
		err := executePlatformSelfUpdate(filepath.Join(dir, "download", "release.tar.gz"), filepath.Join(dir, "kurumifrp"), "", "127.0.0.1:9020", "1.2.3", []string{"--open", "--data", "relative data"}, func(code int) { fmt.Println(code) })
		if err != nil {
			t.Fatal(err)
		}
		bufio.NewReader(os.Stdin).ReadString('\n')
		return
	}
	dir := t.TempDir()
	portableFixture(t, dir)
	cwd := filepath.Join(dir, "working directory")
	if err := os.Mkdir(cwd, 0755); err != nil {
		t.Fatal(err)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestPortableRestartWaitsForExit$")
	child.Dir = cwd
	child.Env = append(os.Environ(), childEnv+"="+dir, "INVOCATION_ID=")
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	child.Stderr = os.Stderr
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer child.Process.Kill()
	line, err := bufio.NewReader(output).ReadString('\n')
	if line != "0\n" || err != nil {
		t.Fatalf("restart handoff: %q %v", line, err)
	}
	marker := filepath.Join(cwd, "launched")
	time.Sleep(1200 * time.Millisecond)
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("replacement launched before old PID %s exited", strconv.Itoa(child.Process.Pid))
	}
	input.Close()
	if err = child.Wait(); err != nil {
		t.Fatal(err)
	}
	want := cwd + "\n--data\nrelative data\n-listen=127.0.0.1:9020\n"
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(marker)
		if err == nil && string(data) == want {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("new process failed to restart in %s with its original arguments", cwd)
}

func TestPortableUpdateRestoresFilesWhenRestartHelperIsMissing(t *testing.T) {
	dir := t.TempDir()
	pkg := portableFixture(t, dir)
	exe := filepath.Join(dir, "kurumifrp")
	if err := os.Mkdir(filepath.Join(dir, "bundled"), 0755); err != nil {
		t.Fatal(err)
	}
	files := []string{"kurumifrp", "bundled/frpc", "bundled/frps", "bundled/LICENSE", "bundled/manifest.json"}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("old "+name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	helper := exec.Command(filepath.Join(dir, "missing-shell"))
	if err := installPortableUpdate(pkg, exe, "1.2.3", helper); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected missing restart helper error, got %v", err)
	}
	for _, name := range files {
		if data, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(data) != "old "+name {
			t.Fatalf("%s changed after restart helper failed: %q %v", name, data, err)
		}
	}
}

func TestPortableUpdateReapsRestartHelperWhenExecutableSwitchFails(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "kurumifrp")
	// A directory at the destination makes the last rename fail.
	if err := os.Mkdir(exe, 0755); err != nil {
		t.Fatal(err)
	}
	helper := exec.Command("sleep", "30")
	if err := installPortableUpdate(portableFixture(t, dir), exe, "1.2.3", helper); err == nil {
		t.Fatal("expected the executable replacement to fail")
	}
	if helper.ProcessState == nil || helper.ProcessState.Success() {
		t.Fatalf("restart helper was not stopped and reaped: %v", helper.ProcessState)
	}
}
