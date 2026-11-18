package manager

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Presikid/KurumiFrp/internal/childprocess"
	"github.com/Presikid/KurumiFrp/internal/version"
)

var errFRPUpToDate = errors.New("FRP 核心已是最新版本")

const maxUpdateArchiveBytes = 160 << 20 // 160 MiB safety ceiling

// UpdateSettings stores optional proxy configuration in data/update_settings.json.
type UpdateSettings struct {
	ProxyURL string `json:"proxyURL"`
}

// ComponentUpdateStatus describes the check result for either KurumiFrp itself or bundled official fatedier/frp.
type ComponentUpdateStatus struct {
	sha256        string
	Current       string `json:"current"`
	Latest        string `json:"latest"`
	HasUpdate     bool   `json:"hasUpdate"`
	CanAutoUpdate bool   `json:"canAutoUpdate"`
	UpdateMode    string `json:"updateMode,omitempty"`
	AssetName     string `json:"assetName,omitempty"`
	AssetURL      string `json:"assetURL,omitempty"`
	AssetSize     int64  `json:"assetSize,omitempty"`
	ReleaseURL    string `json:"releaseURL"`
	ManualHint    string `json:"manualHint,omitempty"`
	Error         string `json:"error,omitempty"`
}

// CombinedUpdateCheck is returned by /api/update/check.
type CombinedUpdateCheck struct {
	ProxyURL string                `json:"proxyURL"`
	App      ComponentUpdateStatus `json:"app"`
	FRP      ComponentUpdateStatus `json:"frp"`
}

type ghRelease struct {
	TagName string
	HTMLURL string
	Assets  []ghAsset
}

type ghAsset struct {
	Name               string
	BrowserDownloadURL string
	sha256             string
}

// UpdateManager coordinates version checks, official FRP hot-updates, and KurumiFrp self-updates.
type UpdateManager struct {
	mu           sync.Mutex
	busy         atomic.Bool
	dataDir      string
	settingsPath string
	settings     UpdateSettings
	runtime      *RuntimeSupervisor
	onShutdown   func(exitCode int)
	webBase      string
	releases     map[releaseRepo]cachedRelease
	allowLocal   bool // enabled only in unit tests for 127.0.0.1 httptest servers
}

// NewUpdateManager initializes UpdateManager and loads optional proxy settings.
func NewUpdateManager(dataDir string, rt *RuntimeSupervisor, onShutdown func(exitCode int)) *UpdateManager {
	um := &UpdateManager{
		dataDir:      dataDir,
		settingsPath: filepath.Join(dataDir, "update_settings.json"),
		runtime:      rt,
		onShutdown:   onShutdown,
		webBase:      "https://github.com",
		releases:     make(map[releaseRepo]cachedRelease),
	}
	if raw, err := os.ReadFile(um.settingsPath); err == nil {
		var s UpdateSettings
		if json.Unmarshal(raw, &s) == nil {
			if norm, err := normalizeProxyURL(s.ProxyURL); err == nil {
				um.settings.ProxyURL = norm
			}
		}
	}
	return um
}

// ProxyURL returns the currently saved optional download proxy URL.
func (u *UpdateManager) ProxyURL() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.settings.ProxyURL
}

// SaveProxyURL validates and persists the optional HTTP/HTTPS/SOCKS5 download proxy URL.
func (u *UpdateManager) SaveProxyURL(raw string) (string, error) {
	norm, err := normalizeProxyURL(raw)
	if err != nil {
		return "", err
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.settings.ProxyURL = norm
	b, _ := json.MarshalIndent(u.settings, "", "  ")
	if _, err := writeIfChanged(u.settingsPath, append(b, '\n'), 0o600); err != nil {
		return "", err
	}
	return norm, nil
}

func normalizeProxyURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	parsed, err := url.Parse(s)
	if err != nil || parsed.Host == "" {
		return "", errors.New("代理地址格式无效，请填写如 http://127.0.0.1:7890 或 socks5://127.0.0.1:10808")
	}
	scheme := strings.ToLower(parsed.Scheme)
	switch scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return "", errors.New("代理协议仅支持 http://、https://、socks5:// 或 socks5h://")
	}
	return parsed.String(), nil
}

func (u *UpdateManager) resolveProxy(override *string) (string, error) {
	if override != nil {
		return u.SaveProxyURL(*override)
	}
	return u.ProxyURL(), nil
}

func (u *UpdateManager) httpClient(proxyStr string, timeout time.Duration) (*http.Client, error) {
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       30 * time.Second,
	}
	effectiveProxy := proxyStr
	if effectiveProxy == "" && !u.allowLocal {
		effectiveProxy = detectPlatformSystemProxy()
	}
	if effectiveProxy != "" {
		pu, err := url.Parse(effectiveProxy)
		if err != nil {
			return nil, err
		}
		tr.Proxy = http.ProxyURL(pu)
	}
	allowLocal := u.allowLocal
	return &http.Client{
		Timeout:   timeout,
		Transport: tr,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			if err := validateDownloadURL(req.URL, allowLocal); err != nil {
				return err
			}
			// Never forward Authorization headers across host redirects
			if len(via) > 0 && !strings.EqualFold(req.URL.Host, via[0].URL.Host) {
				req.Header.Del("Authorization")
			}
			return nil
		},
	}, nil
}

func validateDownloadURL(u *url.URL, allowLocal bool) error {
	if u == nil {
		return errors.New("empty url")
	}
	host := strings.ToLower(u.Hostname())
	if allowLocal && (host == "127.0.0.1" || host == "localhost") {
		return nil
	}
	if u.Scheme != "https" {
		return fmt.Errorf("拒绝非 HTTPS 下载源: %s", u.Scheme)
	}
	allowed := map[string]bool{
		"github.com":                           true,
		"raw.githubusercontent.com":            true,
		"objects.githubusercontent.com":        true,
		"release-assets.githubusercontent.com": true,
		"codeload.github.com":                  true,
	}
	if !allowed[host] {
		return fmt.Errorf("拒绝不受信任的下载域名: %s", host)
	}
	return nil
}

// CheckAll checks both KurumiFrp self-updates and upstream official fatedier/frp updates concurrently.
func (u *UpdateManager) CheckAll(ctx context.Context, proxyOverride *string) (CombinedUpdateCheck, error) {
	proxyStr, err := u.resolveProxy(proxyOverride)
	if err != nil {
		return CombinedUpdateCheck{}, err
	}
	client, err := u.httpClient(proxyStr, 10*time.Second)
	if err != nil {
		return CombinedUpdateCheck{}, err
	}

	var appStatus, frpStatus ComponentUpdateStatus
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		appStatus = u.checkAppRelease(ctx, client, proxyStr)
	}()
	go func() {
		defer wg.Done()
		frpStatus = u.checkFRPRelease(ctx, client, proxyStr)
	}()
	wg.Wait()

	return CombinedUpdateCheck{
		ProxyURL: proxyStr,
		App:      appStatus,
		FRP:      frpStatus,
	}, nil
}

func (u *UpdateManager) checkAppRelease(ctx context.Context, client *http.Client, proxyStr string) ComponentUpdateStatus {
	exePath, _ := os.Executable()
	canSelf, mode, hint := canPlatformSelfUpdateApp(exePath)
	st := ComponentUpdateStatus{
		Current:       version.Current,
		Latest:        version.Current,
		CanAutoUpdate: canSelf,
		UpdateMode:    mode,
		ReleaseURL:    "https://github.com/Presikid/KurumiFrp/releases",
		ManualHint:    hint,
	}

	chosen, err := u.latestRelease(ctx, client, proxyStr, appRepo)
	if err != nil {
		st.Error = err.Error()
		return st
	}

	st.Latest = strings.TrimPrefix(chosen.TagName, "v")
	if chosen.HTMLURL != "" {
		st.ReleaseURL = chosen.HTMLURL
	}
	st.HasUpdate = compareSemVer(st.Latest, st.Current) > 0

	if asset, ok := matchAppAsset(chosen.Assets, st.Latest, canSelf); ok {
		st.AssetName = asset.Name
		st.AssetURL = asset.BrowserDownloadURL
		st.sha256 = asset.sha256
	} else {
		st.CanAutoUpdate = false
	}
	return st
}

func matchAppAsset(assets []ghAsset, ver string, preferSelfUpdateArchive bool) (ghAsset, bool) {
	var wantNames []string
	switch runtime.GOOS {
	case "windows":
		wantNames = []string{fmt.Sprintf("KurumiFRP-%s-Setup.exe", ver)}
	case "linux":
		armVariant := detectLinuxARMVariant()
		archMap := map[string]string{
			"amd64":    "amd64",
			"arm64":    "arm64",
			"arm":      armVariant,
			"riscv64":  "riscv64",
			"loong64":  "loong64",
			"mips":     "mips",
			"mipsle":   "mipsle",
			"mips64":   "mips64",
			"mips64le": "mips64le",
		}
		if a, ok := archMap[runtime.GOARCH]; ok {
			tarName := fmt.Sprintf("KurumiFRP-%s-linux-%s.tar.gz", ver, a)
			debArch := runtime.GOARCH
			if debArch == "arm" {
				if armVariant == "arm" {
					debArch = "armel"
				} else {
					debArch = "armhf"
				}
			}
			debName := fmt.Sprintf("kurumifrp_%s_%s.deb", ver, debArch)
			if preferSelfUpdateArchive {
				wantNames = []string{tarName, debName}
			} else {
				wantNames = []string{debName, tarName}
			}
		}
	}
	for _, want := range wantNames {
		for _, a := range assets {
			if strings.EqualFold(a.Name, want) && a.BrowserDownloadURL != "" {
				return a, true
			}
		}
	}
	return ghAsset{}, false
}

func frpPlatformSlug() (string, string, bool) {
	switch runtime.GOOS {
	case "windows":
		if runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64" {
			return "windows_" + runtime.GOARCH, ".zip", true
		}
	case "linux":
		switch runtime.GOARCH {
		case "amd64", "arm64", "riscv64", "loong64":
			return "linux_" + runtime.GOARCH, ".tar.gz", true
		case "arm":
			return "linux_" + detectLinuxARMVariant(), ".tar.gz", true
		}
	case "darwin":
		if runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64" {
			return "darwin_" + runtime.GOARCH, ".tar.gz", true
		}
	}
	return "", "", false
}

func (u *UpdateManager) checkFRPRelease(ctx context.Context, client *http.Client, proxyStr string) ComponentUpdateStatus {
	core := LocateBinaries(u.dataDir)
	curVer := core.FrpsVersion
	if curVer == "" || curVer == "unknown" {
		curVer = core.FrpcVersion
	}
	if core.FrpcVersion != "" && core.FrpcVersion != "unknown" && curVer != "" && curVer != "unknown" && compareSemVer(curVer, core.FrpcVersion) > 0 {
		curVer = core.FrpcVersion
	}
	if curVer == "" {
		curVer = "未安装"
	}

	slug, ext, supported := frpPlatformSlug()
	st := ComponentUpdateStatus{
		Current:       curVer,
		Latest:        curVer,
		CanAutoUpdate: supported,
		UpdateMode:    "hot-swap-data-bin",
		ReleaseURL:    "https://github.com/fatedier/frp/releases",
	}

	rel, err := u.latestRelease(ctx, client, proxyStr, frpRepo)
	if err != nil {
		st.Error = err.Error()
		return st
	}

	latest := strings.TrimPrefix(rel.TagName, "v")
	if latest != "" {
		st.Latest = latest
	}
	if rel.HTMLURL != "" {
		st.ReleaseURL = rel.HTMLURL
	}
	st.HasUpdate = !core.FrpcFound || !core.FrpsFound || core.FrpcVersion == "unknown" || core.FrpsVersion == "unknown" || compareSemVer(st.Latest, curVer) > 0

	if supported && latest != "" {
		wantAsset := fmt.Sprintf("frp_%s_%s%s", latest, slug, ext)
		for _, a := range rel.Assets {
			if a.Name == wantAsset && a.BrowserDownloadURL != "" {
				st.AssetName = a.Name
				st.AssetURL = a.BrowserDownloadURL
				st.sha256 = a.sha256
				break
			}
		}
		if st.AssetURL == "" {
			st.CanAutoUpdate = false
		}
	}
	return st
}

// ApplyFRPUpdate downloads, SHA-256 verifies, and hot-swaps official frpc + frps into data/bin/ without any UAC popup.
func (u *UpdateManager) ApplyFRPUpdate(ctx context.Context, proxyOverride *string) (CoreBinaryInfo, int, error) {
	if !u.busy.CompareAndSwap(false, true) {
		return CoreBinaryInfo{}, 0, errors.New("已有更新任务正在进行中，请稍候")
	}
	defer u.busy.Store(false)

	proxyStr, err := u.resolveProxy(proxyOverride)
	if err != nil {
		return CoreBinaryInfo{}, 0, err
	}
	client, err := u.httpClient(proxyStr, 5*time.Minute)
	if err != nil {
		return CoreBinaryInfo{}, 0, err
	}

	st := u.checkFRPRelease(ctx, client, proxyStr)
	if st.Error != "" {
		return CoreBinaryInfo{}, 0, errors.New(st.Error)
	}
	if !st.HasUpdate {
		return LocateBinaries(u.dataDir), 0, errFRPUpToDate
	}
	if !st.CanAutoUpdate || st.AssetURL == "" || st.sha256 == "" {
		return CoreBinaryInfo{}, 0, errors.New("当前系统架构暂不支持自动拉取上游 FRP 二进制包")
	}

	// 2. Download archive and verify exact SHA-256 before touching disk binaries
	archiveBytes, err := u.downloadBytes(ctx, client, st.AssetURL, maxUpdateArchiveBytes)
	if err != nil {
		return CoreBinaryInfo{}, 0, fmt.Errorf("下载官方 FRP 压缩包失败: %w", err)
	}
	actualSum := sha256.Sum256(archiveBytes)
	if !strings.EqualFold(hex.EncodeToString(actualSum[:]), st.sha256) {
		return CoreBinaryInfo{}, 0, errors.New("官方 FRP 压缩包 SHA-256 校验不一致，已拒绝安装")
	}

	// 3. Extract frpc, frps, and LICENSE in memory and stage .new files inside data/bin/
	frpcRaw, frpsRaw, licRaw, err := extractFRPMembers(st.AssetName, archiveBytes)
	if err != nil {
		return CoreBinaryInfo{}, 0, err
	}

	swapFn, err := stageCorePair(ctx, filepath.Join(u.dataDir, "bin"), st.Latest, frpcRaw, frpsRaw, licRaw)
	if err != nil {
		return CoreBinaryInfo{}, 0, err
	}
	restarted := 0
	if u.runtime != nil {
		restarted, err = u.runtime.HotSwapBinaries(swapFn)
	} else {
		err = swapFn()
	}
	if err != nil {
		return LocateBinaries(u.dataDir), restarted, err
	}
	return LocateBinaries(u.dataDir), restarted, nil
}

// Stage both executables in the inactive slot, then publish the pair with a
// single same-directory rename. Even a process exit between writes leaves the
// previously selected pair intact. The next update reuses the inactive slot.
func stageCorePair(ctx context.Context, binDir, version string, client, server, license []byte) (func() error, error) {
	slots := filepath.Join(binDir, ".kurumi-cores")
	active, err := readCoreSlot(slots)
	if err != nil {
		return nil, err
	}
	next := "a"
	if active == "a" {
		next = "b"
	}
	dir := filepath.Join(slots, next)
	for _, file := range []struct {
		name    string
		content []byte
	}{{"frpc", client}, {"frps", server}} {
		exe := filepath.Join(dir, binaryFileName(file.name))
		if _, err := writeIfChanged(exe, file.content, 0755); err != nil {
			return nil, fmt.Errorf("写入 %s 失败: %w", file.name, err)
		}
		probe, cancel := context.WithTimeout(ctx, 4*time.Second)
		cmd := exec.CommandContext(probe, exe, "-v")
		childprocess.Configure(cmd)
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			return nil, fmt.Errorf("%s 运行预检失败: %w", file.name, err)
		}
		if strings.TrimPrefix(strings.TrimSpace(string(out)), "v") != version {
			return nil, fmt.Errorf("%s 版本预检失败，预期 %s", file.name, version)
		}
	}
	if len(license) > 0 {
		if _, err := writeIfChanged(filepath.Join(dir, "LICENSE"), license, 0644); err != nil {
			return nil, err
		}
	}
	return func() error {
		_, err := writeIfChanged(filepath.Join(slots, "active"), []byte(next+"\n"), 0600)
		if err == nil {
			InvalidateBinaryVersionCache()
		}
		return err
	}, nil
}

// ApplyAppUpdate downloads, verifies, and launches a silent self-update for KurumiFrp itself.
func (u *UpdateManager) ApplyAppUpdate(ctx context.Context, proxyOverride *string, listenSpec string, origArgs []string) (string, error) {
	if !u.busy.CompareAndSwap(false, true) {
		return "", errors.New("已有更新任务正在进行中，请稍候")
	}
	defer u.busy.Store(false)

	proxyStr, err := u.resolveProxy(proxyOverride)
	if err != nil {
		return "", err
	}
	client, err := u.httpClient(proxyStr, 8*time.Minute)
	if err != nil {
		return "", err
	}

	st := u.checkAppRelease(ctx, client, proxyStr)
	if st.Error != "" {
		return "", errors.New(st.Error)
	}
	if !st.HasUpdate {
		return st.Current, errors.New("当前已是最新版本，无需更新")
	}
	if !st.CanAutoUpdate || st.AssetURL == "" || st.sha256 == "" {
		if st.ManualHint != "" {
			return "", errors.New(st.ManualHint)
		}
		return "", errors.New("当前安装环境暂不支持自动覆盖升级，请点击发布页链接手动下载")
	}

	tmpDir, err := os.MkdirTemp("", "kurumifrp-update-*")
	if err != nil {
		return "", fmt.Errorf("创建临时更新目录失败: %w", err)
	}

	pkgBytes, err := u.downloadBytes(ctx, client, st.AssetURL, maxUpdateArchiveBytes)
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", fmt.Errorf("下载新版安装包 (%s) 失败: %w", st.AssetName, err)
	}
	got := sha256.Sum256(pkgBytes)
	if !strings.EqualFold(hex.EncodeToString(got[:]), st.sha256) {
		_ = os.RemoveAll(tmpDir)
		return "", errors.New("新版安装包 SHA-256 校验未通过，已中止升级")
	}

	pkgPath := filepath.Join(tmpDir, st.AssetName)
	if err := os.WriteFile(pkgPath, pkgBytes, 0o755); err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", err
	}

	exePath, _ := os.Executable()
	if err := executePlatformSelfUpdate(pkgPath, exePath, listenSpec, origArgs, u.onShutdown); err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", err
	}
	return st.Latest, nil
}

func (u *UpdateManager) downloadBytes(ctx context.Context, client *http.Client, rawURL string, maxBytes int64) ([]byte, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if err := validateDownloadURL(parsed, u.allowLocal); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "KurumiFrp/"+version.Current)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, errors.New("下载文件超出安全体积上限")
	}
	return data, nil
}

func extractFRPMembers(assetName string, archiveBytes []byte) ([]byte, []byte, []byte, error) {
	wantFrpc := binaryFileName("frpc")
	wantFrps := binaryFileName("frps")
	var frpcRaw, frpsRaw, licRaw []byte

	if strings.HasSuffix(strings.ToLower(assetName), ".zip") {
		zr, err := zip.NewReader(bytes.NewReader(archiveBytes), int64(len(archiveBytes)))
		if err != nil {
			return nil, nil, nil, fmt.Errorf("解压官方 ZIP 失败: %w", err)
		}
		for _, f := range zr.File {
			if f.FileInfo().IsDir() {
				continue
			}
			base := path.Base(strings.ReplaceAll(f.Name, "\\", "/"))
			if base != wantFrpc && base != wantFrps && base != "LICENSE" {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return nil, nil, nil, err
			}
			b, err := io.ReadAll(io.LimitReader(rc, maxUpdateArchiveBytes))
			rc.Close()
			if err != nil {
				return nil, nil, nil, err
			}
			switch base {
			case wantFrpc:
				frpcRaw = b
			case wantFrps:
				frpsRaw = b
			case "LICENSE":
				licRaw = b
			}
		}
	} else {
		gz, err := gzip.NewReader(bytes.NewReader(archiveBytes))
		if err != nil {
			return nil, nil, nil, fmt.Errorf("解压官方 tar.gz 失败: %w", err)
		}
		defer gz.Close()
		tr := tar.NewReader(gz)
		for {
			hdr, err := tr.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, nil, nil, err
			}
			if hdr.Typeflag != tar.TypeReg {
				continue
			}
			base := path.Base(hdr.Name)
			if base != wantFrpc && base != wantFrps && base != "LICENSE" {
				continue
			}
			b, err := io.ReadAll(io.LimitReader(tr, maxUpdateArchiveBytes))
			if err != nil {
				return nil, nil, nil, err
			}
			switch base {
			case wantFrpc:
				frpcRaw = b
			case wantFrps:
				frpsRaw = b
			case "LICENSE":
				licRaw = b
			}
		}
	}

	if len(frpcRaw) == 0 || len(frpsRaw) == 0 {
		return nil, nil, nil, errors.New("压缩包内未找到完整的 frpc 与 frps 可执行文件")
	}
	return frpcRaw, frpsRaw, licRaw, nil
}

func replaceFileAtomic(tmpPath, dstPath string, perm os.FileMode) error {
	if err := os.Chmod(tmpPath, perm); err != nil {
		return err
	}
	return os.Rename(tmpPath, dstPath)
}

// DetectedSystemProxy returns the currently active OS default proxy URL (e.g. Windows WinINET system proxy), if any.
func (u *UpdateManager) DetectedSystemProxy() string {
	return detectPlatformSystemProxy()
}

// parseWinINETProxyString converts Windows Internet Settings ProxyServer strings
// (such as "127.0.0.1:2080" or "https=127.0.0.1:2080;http=127.0.0.1:2080;socks=127.0.0.1:1080")
// into a standard proxy URL understood by Go net/http.Transport.
func parseWinINETProxyString(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if !strings.Contains(s, "=") && !strings.Contains(s, ";") {
		norm, err := normalizeProxyURL(s)
		if err == nil {
			return norm
		}
		return ""
	}
	kv := make(map[string]string)
	for _, part := range strings.Split(s, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if eq := strings.IndexByte(part, '='); eq > 0 {
			k := strings.ToLower(strings.TrimSpace(part[:eq]))
			v := strings.TrimSpace(part[eq+1:])
			if v != "" {
				kv[k] = v
			}
		} else if kv["default"] == "" {
			kv["default"] = part
		}
	}
	for _, key := range []string{"https", "http", "default"} {
		if v := kv[key]; v != "" {
			if norm, err := normalizeProxyURL(v); err == nil {
				return norm
			}
		}
	}
	if v := kv["socks"]; v != "" {
		if !strings.Contains(v, "://") {
			v = "socks5://" + v
		}
		if norm, err := normalizeProxyURL(v); err == nil {
			return norm
		}
	}
	return ""
}

func detectLinuxARMVariant() string {
	v := strings.TrimSpace(version.BuildVariant)
	if v == "arm" || v == "arm_hf" {
		return v
	}
	// Fallback: inspect ELF e_flags (EF_ARM_ABI_FLOAT_HARD = 0x00000400) on /proc/self/exe
	if exe, err := os.Executable(); err == nil {
		if hdr, err := os.ReadFile(exe); err == nil && len(hdr) >= 40 && hdr[0] == 0x7f && hdr[1] == 'E' && hdr[2] == 'L' && hdr[3] == 'F' && hdr[4] == 1 {
			// 32-bit little-endian ELF: e_flags at offset 36..40
			eFlags := uint32(hdr[36]) | uint32(hdr[37])<<8 | uint32(hdr[38])<<16 | uint32(hdr[39])<<24
			if eFlags&0x00000400 == 0 && eFlags != 0 {
				return "arm"
			}
		}
	}
	return "arm_hf"
}
