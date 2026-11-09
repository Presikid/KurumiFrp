package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

// Filled by the package builder; source builds have no implicit core.
var bundledManifestSHA string
var bundledVersion string
var bundleVersionRE = regexp.MustCompile(`^[0-9]{1,5}\.[0-9]{1,5}\.[0-9]{1,5}\.[0-9]{1,5}$`)

// VerifyBundled checks the package-owned files without changing any user state.
func VerifyBundled() error {
	if !bundleVersionRE.MatchString(bundledVersion) {
		return fmt.Errorf("此程序没有有效的预置核心")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	_, err = readBundled(filepath.Join(filepath.Dir(exe), "bundled", bundledVersion), bundledManifestSHA)
	return err
}

type Bundled struct {
	Tag           string            `json:"tag"`
	Platform      string            `json:"platform"`
	ArchiveSHA256 string            `json:"archiveSHA256"`
	Files         map[string]string `json:"files"`
}

func readBundled(dir, expected string) (*Bundled, error) {
	if !digestRE.MatchString("sha256:" + expected) {
		return nil, fmt.Errorf("invalid bundled manifest digest")
	}
	st, err := os.Lstat(dir)
	if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("invalid bundled core directory")
	}
	var v Bundled
	path := filepath.Join(dir, "manifest.json")
	st, err = os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() || st.Size() > 32768 {
		return nil, fmt.Errorf("invalid bundled manifest")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256(b)
	if hex.EncodeToString(h[:]) != expected || json.Unmarshal(b, &v) != nil || !validTag(v.Tag) || v.Platform != Host() || !digestRE.MatchString("sha256:"+v.ArchiveSHA256) {
		return nil, fmt.Errorf("bundled core manifest mismatch")
	}
	for _, name := range []string{binaryName("frpc", v.Platform), binaryName("frps", v.Platform), "LICENSE"} {
		p := filepath.Join(dir, name)
		st, err := os.Lstat(p)
		if err != nil || !st.Mode().IsRegular() || st.Size() > 120<<20 || !digestRE.MatchString("sha256:"+v.Files[name]) {
			return nil, fmt.Errorf("invalid bundled core file")
		}
		f, err := os.Open(p)
		if err != nil {
			return nil, err
		}
		sum := sha256.New()
		_, err = io.Copy(sum, f)
		if e := f.Close(); err == nil {
			err = e
		}
		if err != nil || hex.EncodeToString(sum.Sum(nil)) != v.Files[name] {
			return nil, fmt.Errorf("bundled core file digest mismatch")
		}
		if name != "LICENSE" {
			if err := checkExecutable(p, v.Platform); err != nil {
				return nil, err
			}
		}
	}
	return &v, nil
}
