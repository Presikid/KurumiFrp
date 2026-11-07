package main

// Local maintenance is deliberately not an HTTP API. It cannot kill arbitrary
// PIDs, follow redirects or scan Downloads for files that happen to share a name.
import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const productID = "io.github.Presikid.KurumiFrp"

var hashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func plainPath(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for p := abs; ; p = filepath.Dir(p) {
		st, e := os.Lstat(p)
		if e != nil {
			return e
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing linked path: %s", p)
		}
		if parent := filepath.Dir(p); parent == p {
			break
		}
	}
	return nil
}

func localRequest(dir, method, endpoint string) error {
	for _, name := range []string{"access.token", "listen.addr"} {
		if err := plainPath(filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	token, err := os.ReadFile(filepath.Join(dir, "access.token"))
	if err != nil {
		return err
	}
	if !hashPattern.Match(token) {
		return fmt.Errorf("invalid local management token")
	}
	address, err := os.ReadFile(filepath.Join(dir, "listen.addr"))
	if err != nil {
		return err
	}
	addr := strings.TrimSpace(string(address))
	host, port, err := net.SplitHostPort(addr)
	n, _ := strconv.Atoi(port)
	if err != nil || host != "127.0.0.1" || n < 1024 || n > 65535 {
		return fmt.Errorf("invalid loopback management address")
	}
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	req, err := http.NewRequest(method, "http://"+addr+endpoint, bytes.NewBufferString("{}"))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+string(token))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("local manager is not reachable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("local manager rejected maintenance request (HTTP %d)", resp.StatusCode)
	}
	if method == "GET" {
		var state struct {
			Version string `json:"version"`
		}
		if err = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&state); err != nil {
			return err
		}
		if state.Version != "0.7.0" {
			return fmt.Errorf("unexpected running manager version: %s", state.Version)
		}
	}
	return nil
}

func checkRunning(dir string, timeout time.Duration) error {
	until := time.Now().Add(timeout)
	var err error
	for {
		if err = localRequest(dir, "GET", "/api/state"); err == nil {
			return nil
		}
		if time.Now().After(until) {
			return fmt.Errorf("startup not verified; installer files retained: %w", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func stopExisting(dir string, timeout time.Duration) error {
	lock := filepath.Join(dir, "manager.lock")
	if _, err := os.Lstat(lock); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if err := plainPath(lock); err != nil {
		return err
	}
	if err := localRequest(dir, "POST", "/api/quit"); err != nil {
		return fmt.Errorf("shutdown not confirmed; files retained (do not delete a stale lock without checking owned frp processes): %w", err)
	}
	until := time.Now().Add(timeout)
	for {
		if _, err := os.Lstat(lock); os.IsNotExist(err) {
			return nil
		} else if err != nil {
			return err
		}
		if time.Now().After(until) {
			return fmt.Errorf("shutdown timed out; installation retained")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

type packageManifest struct {
	Product  string            `json:"product"`
	Version  string            `json:"version"`
	Platform string            `json:"platform"`
	Files    map[string]string `json:"files"`
}

func packageNames(platform string) ([]string, error) {
	common := []string{"PLATFORM", "VERSION", "SHA256SUMS", "INSTALL.md"}
	switch platform {
	case "linux_amd64", "linux_arm64":
		return append(common, "kurumi", "install.sh", "uninstall.sh"), nil
	case "windows_amd64", "windows_arm64":
		return append(common, "kurumi.exe", "Install.cmd", "install.ps1", "Uninstall.cmd", "uninstall.ps1"), nil
	default:
		return nil, fmt.Errorf("unsupported package platform")
	}
}

func fileHash(path string) (string, error) {
	st, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() || st.Size() > 128<<20 {
		return "", fmt.Errorf("not a regular bounded package file: %s", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, io.LimitReader(f, (128<<20)+1)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func inside(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// Verify the entire allowlisted payload first. Remove named files, never the
// enclosing directory recursively. User-added files are intentionally preserved.
func cleanPackage(source, executable string, archivePath ...string) error {
	source, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return err
	}
	if inside(source, executable) {
		return fmt.Errorf("cleanup must run from the installed copy, not the source package")
	}
	if err = plainPath(source); err != nil {
		return err
	}
	marker := filepath.Join(source, "PACKAGE-MANIFEST.json")
	if err = plainPath(marker); err != nil {
		return err
	}
	raw, err := os.ReadFile(marker)
	if err != nil {
		return err
	}
	if len(raw) > 16384 {
		return fmt.Errorf("oversized package manifest")
	}
	var manifest packageManifest
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err = d.Decode(&manifest); err != nil {
		return err
	}
	if err = d.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("invalid manifest suffix")
	}
	if manifest.Product != productID || manifest.Version != "0.7.0" {
		return fmt.Errorf("unrecognized package manifest")
	}
	names, err := packageNames(manifest.Platform)
	if err != nil {
		return err
	}
	if len(names) != len(manifest.Files) {
		return fmt.Errorf("unexpected package file set")
	}
	for _, name := range names {
		want, ok := manifest.Files[name]
		if !ok || !hashPattern.MatchString(want) {
			return fmt.Errorf("invalid manifest entry: %s", name)
		}
		got, e := fileHash(filepath.Join(source, name))
		if e != nil {
			return e
		}
		if got != want {
			return fmt.Errorf("modified package file retained: %s", name)
		}
	}
	explicit := ""
	if len(archivePath) > 0 {
		explicit = archivePath[0]
	}
	archive, err := findSourceArchive(source, explicit, manifest, raw)
	if err != nil {
		return err
	}
	for _, name := range append(names, "PACKAGE-MANIFEST.json") {
		if err = os.Remove(filepath.Join(source, name)); err != nil {
			return fmt.Errorf("package cleanup incomplete: %w", err)
		}
	}
	if archive != "" {
		if err = os.Remove(archive); err != nil {
			return fmt.Errorf("source archive cleanup incomplete: %w", err)
		}
		fmt.Println("Verified original release archive removed.")
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		if err = os.Remove(source); err != nil {
			return err
		}
		parent := filepath.Dir(source)
		if filepath.Base(parent) == "KurumiFRP_"+manifest.Version+"_"+manifest.Platform {
			if entries, e := os.ReadDir(parent); e == nil && len(entries) == 0 {
				return os.Remove(parent)
			}
		}
		return nil
	}
	fmt.Println("Package payload removed; unrelated files in its directory were retained.")
	return nil
}

func rejectUninstall(dir string) error {
	if err := checkMachineGate(); err != nil {
		return err
	}
	for _, p := range []string{filepath.Join(dir, ".uninstalling"), filepath.Join(filepath.Dir(dir), ".uninstalling")} {
		if _, err := os.Lstat(p); err == nil {
			return fmt.Errorf("uninstallation in progress")
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
