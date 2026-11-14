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

// InvalidateBinaryVersionCache clears cached frpc/frps version strings after hot-updating binaries.
func InvalidateBinaryVersionCache() {
	binVerMu.Lock()
	binVerCache = make(map[string]cachedBinVer)
	binVerMu.Unlock()
}

func binaryFileName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}

// LocateBinaries resolves bundled or hot-updated official frpc and frps executables.
// If both data/bin and bundled exist, it prefers whichever holds the newer SemVer so future Setup.exe upgrades never get shadowed by an older data/bin.
func LocateBinaries(dataDir string) CoreBinaryInfo {
	exePath, _ := os.Executable()
	exeDir := filepath.Dir(exePath)

	dataBinDir := filepath.Join(dataDir, "bin")
	exeBundledDir := filepath.Join(exeDir, "bundled")
	cwdBundledDir := filepath.Join(".", "bundled")

	frpcPath, frpcVer, frpcSource, frpcFound := resolveBestBinary(binaryFileName("frpc"), dataBinDir, exeBundledDir, cwdBundledDir)
	frpsPath, frpsVer, frpsSource, frpsFound := resolveBestBinary(binaryFileName("frps"), dataBinDir, exeBundledDir, cwdBundledDir)

	activeDir := exeBundledDir
	source := frpsSource
	if source == "" {
		source = frpcSource
	}
	if source == "data-bin" {
		activeDir = dataBinDir
	} else if frpsPath != "" {
		activeDir = filepath.Dir(frpsPath)
	} else if frpcPath != "" {
		activeDir = filepath.Dir(frpcPath)
	}

	return CoreBinaryInfo{
		FrpcPath:    frpcPath,
		FrpcVersion: frpcVer,
		FrpcFound:   frpcFound,
		FrpsPath:    frpsPath,
		FrpsVersion: frpsVer,
		FrpsFound:   frpsFound,
		BundledDir:  activeDir,
		CoreSource:  source,
	}
}

func resolveBestBinary(fileName, dataBinDir, exeBundledDir, cwdBundledDir string) (string, string, string, bool) {
	dataPath, hasData := statFile(filepath.Join(dataBinDir, fileName))
	bundPath, hasBund := statFile(filepath.Join(exeBundledDir, fileName))
	if !hasBund {
		bundPath, hasBund = statFile(filepath.Join(cwdBundledDir, fileName))
	}

	if hasData && hasBund {
		dVer := probeBinaryVersion(dataPath)
		bVer := probeBinaryVersion(bundPath)
		if bVer != "unknown" && dVer != "unknown" && compareSemVer(bVer, dVer) > 0 {
			return bundPath, bVer, "bundled", true
		}
		return dataPath, dVer, "data-bin", true
	}
	if hasData {
		return dataPath, probeBinaryVersion(dataPath), "data-bin", true
	}
	if hasBund {
		return bundPath, probeBinaryVersion(bundPath), "bundled", true
	}
	if p, err := exec.LookPath(fileName); err == nil {
		return p, probeBinaryVersion(p), "path", true
	}
	return "", "", "", false
}

func statFile(full string) (string, bool) {
	if st, err := os.Stat(full); err == nil && !st.IsDir() {
		if abs, err := filepath.Abs(full); err == nil {
			return abs, true
		}
		return full, true
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
		v = strings.TrimPrefix(v, "v")
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
