package manager

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Presikid/KurumiFrp/internal/childprocess"
)

type cachedBinVer struct {
	size    int64
	modNano int64
	version string
}

var (
	binVerMu    sync.Mutex
	binVerCache = make(map[string]cachedBinVer)
)

func binaryFileName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}

// LocateBinaries resolves bundled or user-placed official frpc and frps executables.
func LocateBinaries(dataDir string) CoreBinaryInfo {
	exePath, _ := os.Executable()
	exeDir := filepath.Dir(exePath)

	candidateDirs := []string{
		filepath.Join(dataDir, "bin"),
		filepath.Join(exeDir, "bundled"),
		filepath.Join(".", "bundled"),
	}

	frpcPath, frpcFound := findBinary(binaryFileName("frpc"), candidateDirs)
	frpsPath, frpsFound := findBinary(binaryFileName("frps"), candidateDirs)

	bundledDir := filepath.Join(exeDir, "bundled")
	if st, err := os.Stat(filepath.Join(dataDir, "bin")); err == nil && st.IsDir() {
		bundledDir = filepath.Join(dataDir, "bin")
	}

	info := CoreBinaryInfo{
		FrpcPath:   frpcPath,
		FrpcFound:  frpcFound,
		FrpsPath:   frpsPath,
		FrpsFound:  frpsFound,
		BundledDir: bundledDir,
	}
	if frpcFound {
		info.FrpcVersion = probeBinaryVersion(frpcPath)
	}
	if frpsFound {
		info.FrpsVersion = probeBinaryVersion(frpsPath)
	}
	return info
}

func findBinary(fileName string, dirs []string) (string, bool) {
	for _, d := range dirs {
		if d == "" {
			continue
		}
		full := filepath.Join(d, fileName)
		if st, err := os.Stat(full); err == nil && !st.IsDir() {
			if abs, err := filepath.Abs(full); err == nil {
				return abs, true
			}
			return full, true
		}
	}
	if p, err := exec.LookPath(fileName); err == nil {
		return p, true
	}
	return "", false
}

func probeBinaryVersion(binPath string) string {
	if st, err := os.Stat(binPath); err == nil {
		binVerMu.Lock()
		cached, ok := binVerCache[binPath]
		binVerMu.Unlock()
		if ok && cached.size == st.Size() && cached.modNano == st.ModTime().UnixNano() {
			return cached.version
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, binPath, "-v")
	childprocess.Configure(cmd)
	out, err := cmd.CombinedOutput()
	ver := "unknown"
	if err == nil {
		v := strings.TrimSpace(string(bytes.TrimSpace(out)))
		if idx := strings.IndexAny(v, "\r\n"); idx >= 0 {
			v = strings.TrimSpace(v[:idx])
		}
		if v != "" {
			ver = v
		}
	}

	if st, statErr := os.Stat(binPath); statErr == nil && ver != "unknown" {
		binVerMu.Lock()
		binVerCache[binPath] = cachedBinVer{
			size:    st.Size(),
			modNano: st.ModTime().UnixNano(),
			version: ver,
		}
		binVerMu.Unlock()
	}
	return ver
}
