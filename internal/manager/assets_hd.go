package manager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Presikid/KurumiFrp/web"
)

const (
	defaultHDAssetPackID  = "webui_v094"
	defaultGitHubRawBase  = "https://raw.githubusercontent.com/Presikid/KurumiFrp"
	defaultGitHubRepoSlug = "Presikid/KurumiFrp"
	maxSingleHDAssetBytes = 64 << 20 // 64 MiB per artwork safety cap
	hdPullConcurrency     = 6
)

var validAssetKeyRe = regexp.MustCompile(`^k_[0-9a-zA-Z_]{1,48}$`)

// AssetFileMeta describes either an embedded lightweight WebP or a full-res original/cutout file in manifest.json.
type AssetFileMeta struct {
	Path      string `json:"path"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	SizeBytes int64  `json:"sizeBytes"`
	SHA256    string `json:"sha256"`
}

// AssetManifestEntry maps a WebUI slot key (e.g. "k_02") to its embedded and full-resolution repository files.
type AssetManifestEntry struct {
	ID        string         `json:"id"`
	Key       string         `json:"key"`
	Category  string         `json:"category"`
	Title     string         `json:"title"`
	Subtitle  string         `json:"subtitle,omitempty"`
	IsSticker bool           `json:"isSticker"`
	Embedded  AssetFileMeta  `json:"embedded"`
	Original  AssetFileMeta  `json:"original"`
	HDCutout  *AssetFileMeta `json:"hdCutout,omitempty"`
}

// AssetPackManifest matches assets/material/<packId>/manifest.json in the GitHub repository.
type AssetPackManifest struct {
	SchemaVersion      int                  `json:"schemaVersion"`
	PackVersion        string               `json:"packVersion"`
	Description        string               `json:"description"`
	TotalAssets        int                  `json:"totalAssets"`
	TotalOriginalBytes int64                `json:"totalOriginalBytes"`
	TotalCutoutBytes   int64                `json:"totalCutoutBytes"`
	Assets             []AssetManifestEntry `json:"assets"`
}

// persistedHDState is stored at <dataDir>/hd_assets/state.json.
type persistedHDState struct {
	PreferHD    bool              `json:"preferHD"`
	PackID      string            `json:"packId"`
	PackVersion string            `json:"packVersion"`
	SourceRef   string            `json:"sourceRef"`
	InstalledAt string            `json:"installedAt,omitempty"`
	Files       map[string]string `json:"files"`      // key -> filename inside display/
	FileSHA256  map[string]string `json:"fileSha256"` // key -> verified sha256
	Dimensions  map[string]string `json:"dimensions"` // key -> WIDTHxHEIGHT
	TotalBytes  int64             `json:"totalBytes"`
}

// HDAssetStatus is returned to the WebUI via /api/state and /api/assets/hd/status.
type HDAssetStatus struct {
	ActiveTier     string            `json:"activeTier"` // "embedded" | "hd"
	PreferHD       bool              `json:"preferHD"`
	Installed      bool              `json:"installed"`
	InstalledCount int               `json:"installedCount"`
	ExpectedCount  int               `json:"expectedCount"`
	InstalledBytes int64             `json:"installedBytes"`
	EmbeddedBytes  int64             `json:"embeddedBytes"`
	PackID         string            `json:"packId"`
	PackVersion    string            `json:"packVersion"`
	SourceRepo     string            `json:"sourceRepo"`
	SourcePath     string            `json:"sourcePath"`
	SourceRef      string            `json:"sourceRef"`
	InstalledAt    string            `json:"installedAt,omitempty"`
	CacheToken     string            `json:"cacheToken"`
	Dimensions     map[string]string `json:"dimensions,omitempty"`

	Downloading   bool   `json:"downloading"`
	ProgressFiles int    `json:"progressFiles"`
	ProgressTotal int    `json:"progressTotal"`
	ProgressBytes int64  `json:"progressBytes"`
	ExpectedBytes int64  `json:"expectedBytes"`
	CurrentAsset  string `json:"currentAsset,omitempty"`
	LastError     string `json:"lastError,omitempty"`
}

// AssetPackManager manages default embedded compressed WebP serving alongside optional on-demand GitHub HD packs.
type AssetPackManager struct {
	mu            sync.RWMutex
	dataDir       string
	rootDir       string
	statePath     string
	updater       *UpdateManager
	onStateChange func()
	rawBase       string

	embeddedBytes int64
	embeddedCount int
	state         persistedHDState
	rev           uint64

	downloading   bool
	cancelPull    context.CancelFunc
	progressFiles int
	progressTotal int
	progressBytes int64
	expectedBytes int64
	currentAsset  string
	lastError     string
}

// NewAssetPackManager initializes the HD resource pack manager; defaults strictly to lightweight embedded WebP mode.
func NewAssetPackManager(dataDir string, updater *UpdateManager, onStateChange func()) *AssetPackManager {
	rootDir := filepath.Join(dataDir, "hd_assets")
	embBytes, embCount := measureEmbeddedWebAssets()
	if embCount == 0 {
		embCount = 35
	}
	m := &AssetPackManager{
		dataDir:       dataDir,
		rootDir:       rootDir,
		statePath:     filepath.Join(rootDir, "state.json"),
		updater:       updater,
		onStateChange: onStateChange,
		rawBase:       defaultGitHubRawBase,
		embeddedBytes: embBytes,
		embeddedCount: embCount,
		rev:           1,
		state: persistedHDState{
			PreferHD:    false,
			PackID:      defaultHDAssetPackID,
			PackVersion: "0.9.4",
			SourceRef:   "main",
			Files:       make(map[string]string),
			FileSHA256:  make(map[string]string),
			Dimensions:  make(map[string]string),
		},
	}
	m.loadPersistedState()
	return m
}

func measureEmbeddedWebAssets() (int64, int) {
	var total int64
	var count int
	entries, err := fs.ReadDir(web.Files, "assets")
	if err != nil {
		return 0, 0
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if info, err := e.Info(); err == nil {
			total += info.Size()
			count++
		}
	}
	return total, count
}

func (m *AssetPackManager) displayDirLocked() string {
	packID := sanitizePackID(m.state.PackID)
	return filepath.Join(m.rootDir, packID, "display")
}

func sanitizePackID(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return defaultHDAssetPackID
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
			return defaultHDAssetPackID
		}
	}
	return s
}

func (m *AssetPackManager) loadPersistedState() {
	raw, err := os.ReadFile(m.statePath)
	if err != nil {
		return
	}
	var st persistedHDState
	if json.Unmarshal(raw, &st) != nil {
		return
	}
	st.PackID = sanitizePackID(st.PackID)
	if st.Files == nil {
		st.Files = make(map[string]string)
	}
	if st.FileSHA256 == nil {
		st.FileSHA256 = make(map[string]string)
	}
	if st.Dimensions == nil {
		st.Dimensions = make(map[string]string)
	}

	dispDir := filepath.Join(m.rootDir, st.PackID, "display")
	var verifiedBytes int64
	validFiles := make(map[string]string, len(st.Files))
	for k, fn := range st.Files {
		if !validAssetKeyRe.MatchString(k) || filepath.Base(fn) != fn {
			continue
		}
		info, err := os.Stat(filepath.Join(dispDir, fn))
		if err == nil && !info.IsDir() && info.Size() > 0 {
			validFiles[k] = fn
			verifiedBytes += info.Size()
		}
	}
	st.Files = validFiles
	st.TotalBytes = verifiedBytes
	if len(st.Files) == 0 {
		st.PreferHD = false
	}
	m.state = st
}

func (m *AssetPackManager) saveStateLocked() error {
	if err := os.MkdirAll(m.rootDir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m.state, "", "  ")
	if err != nil {
		return err
	}
	_, err = writeIfChanged(m.statePath, append(b, '\n'), 0o600)
	return err
}

func (m *AssetPackManager) notifyChanged() {
	if m.onStateChange != nil {
		m.onStateChange()
	}
}

// ActiveTier returns "hd" when original-quality assets are downloaded and enabled, otherwise "embedded".
func (m *AssetPackManager) ActiveTier() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.state.PreferHD && len(m.state.Files) > 0 {
		return "hd"
	}
	return "embedded"
}

// CacheToken returns a compact revision token so WebUI images immediately refresh when toggling tiers.
func (m *AssetPackManager) CacheToken() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	tier := "std"
	if m.state.PreferHD && len(m.state.Files) > 0 {
		tier = "hd"
	}
	return fmt.Sprintf("%s-%d-%d", tier, len(m.state.Files), m.rev)
}

// Status returns a thread-safe snapshot of current HD pack state and live GitHub download progress.
func (m *AssetPackManager) Status() HDAssetStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	installedCount := len(m.state.Files)
	installed := installedCount > 0
	tier := "embedded"
	if m.state.PreferHD && installed {
		tier = "hd"
	}
	dimsCopy := make(map[string]string, len(m.state.Dimensions))
	for k, v := range m.state.Dimensions {
		dimsCopy[k] = v
	}
	packID := sanitizePackID(m.state.PackID)
	expectedCnt := m.embeddedCount
	if m.progressTotal > 0 {
		expectedCnt = m.progressTotal
	}
	return HDAssetStatus{
		ActiveTier:     tier,
		PreferHD:       m.state.PreferHD && installed,
		Installed:      installed,
		InstalledCount: installedCount,
		ExpectedCount:  expectedCnt,
		InstalledBytes: m.state.TotalBytes,
		EmbeddedBytes:  m.embeddedBytes,
		PackID:         packID,
		PackVersion:    nonEmptyStr(m.state.PackVersion, "0.9.4"),
		SourceRepo:     defaultGitHubRepoSlug,
		SourcePath:     "assets/material/" + packID,
		SourceRef:      nonEmptyStr(m.state.SourceRef, "main"),
		InstalledAt:    m.state.InstalledAt,
		CacheToken:     fmt.Sprintf("%s-%d-%d", map[bool]string{true: "hd", false: "std"}[tier == "hd"], installedCount, m.rev),
		Dimensions:     dimsCopy,
		Downloading:    m.downloading,
		ProgressFiles:  m.progressFiles,
		ProgressTotal:  m.progressTotal,
		ProgressBytes:  m.progressBytes,
		ExpectedBytes:  m.expectedBytes,
		CurrentAsset:   m.currentAsset,
		LastError:      m.lastError,
	}
}

// SetPreferHD switches between default embedded WebP ("embedded") and downloaded GitHub originals ("hd").
func (m *AssetPackManager) SetPreferHD(prefer bool) (HDAssetStatus, error) {
	m.mu.Lock()
	if prefer && len(m.state.Files) == 0 {
		m.mu.Unlock()
		return m.Status(), errors.New("本地尚未拉取原画质素材包，请先点击「从 GitHub 仓库拉取原画包」")
	}
	m.state.PreferHD = prefer
	m.lastError = ""
	m.rev++
	err := m.saveStateLocked()
	m.mu.Unlock()
	if err != nil {
		return m.Status(), err
	}
	m.notifyChanged()
	return m.Status(), nil
}

// CancelPull stops an active GitHub HD asset pack download if one is running.
func (m *AssetPackManager) CancelPull() HDAssetStatus {
	m.mu.Lock()
	cancel := m.cancelPull
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return m.Status()
}

// Purge removes locally cached HD original files and reverts cleanly to the lightweight embedded WebP assets.
func (m *AssetPackManager) Purge() (HDAssetStatus, error) {
	m.mu.Lock()
	if m.downloading && m.cancelPull != nil {
		m.cancelPull()
	}
	packDir := filepath.Join(m.rootDir, sanitizePackID(m.state.PackID))
	m.state.PreferHD = false
	m.state.Files = make(map[string]string)
	m.state.FileSHA256 = make(map[string]string)
	m.state.Dimensions = make(map[string]string)
	m.state.TotalBytes = 0
	m.state.InstalledAt = ""
	m.lastError = ""
	m.rev++
	_ = os.RemoveAll(packDir)
	err := m.saveStateLocked()
	m.mu.Unlock()
	if err != nil {
		return m.Status(), err
	}
	m.notifyChanged()
	return m.Status(), nil
}

// ServeIfHD serves the verified original-resolution file from disk when HD mode is active; returns false to fall back to embedded WebP.
func (m *AssetPackManager) ServeIfHD(w http.ResponseWriter, r *http.Request) bool {
	rel := strings.TrimPrefix(r.URL.Path, "/assets/")
	ext := path.Ext(rel)
	key := strings.TrimSuffix(rel, ext)
	if !validAssetKeyRe.MatchString(key) {
		return false
	}

	forceTier := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("tier")))
	if forceTier == "embedded" || forceTier == "std" {
		return false
	}

	m.mu.RLock()
	useHD := (m.state.PreferHD || forceTier == "hd") && len(m.state.Files) > 0
	fileName := m.state.Files[key]
	sumHex := m.state.FileSHA256[key]
	dispDir := m.displayDirLocked()
	m.mu.RUnlock()

	if !useHD || fileName == "" {
		return false
	}

	fullPath := filepath.Join(dispDir, filepath.Base(fileName))
	f, err := os.Open(fullPath)
	if err != nil {
		return false
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil || stat.IsDir() {
		return false
	}

	etag := fmt.Sprintf(`"hd-%s-%d"`, shortHex(sumHex, 16), stat.Size())
	w.Header().Set("X-Kurumi-Asset-Tier", "hd")
	w.Header().Set("Cache-Control", "private, max-age=0, must-revalidate")
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	// Passing fileName (e.g. k_02.jpg, k_17.webp, or .png/.gif) sets the exact Content-Type header automatically.
	http.ServeContent(w, r, fileName, stat.ModTime(), f)
	return true
}

func shortHex(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// StartPull launches an asynchronous, SHA-256-verified pull of the original-quality asset pack from GitHub.
func (m *AssetPackManager) StartPull(proxyOverride *string, customRef string, packID string) (HDAssetStatus, error) {
	proxyStr, err := m.updater.resolveProxy(proxyOverride)
	if err != nil {
		return m.Status(), err
	}

	m.mu.Lock()
	if m.downloading {
		m.mu.Unlock()
		return m.Status(), nil
	}
	targetPack := sanitizePackID(nonEmptyStr(packID, m.state.PackID))
	m.state.PackID = targetPack
	ctx, cancel := context.WithCancel(context.Background())
	m.downloading = true
	m.cancelPull = cancel
	m.progressFiles = 0
	m.progressTotal = 0
	m.progressBytes = 0
	m.expectedBytes = 0
	m.currentAsset = "正在读取 GitHub 仓库原画清单 manifest.json…"
	m.lastError = ""
	m.mu.Unlock()

	go m.runPullJob(ctx, proxyStr, strings.TrimSpace(customRef), targetPack)
	return m.Status(), nil
}

func selectHDTargetMeta(entry AssetManifestEntry) AssetFileMeta {
	if entry.HDCutout != nil && strings.TrimSpace(entry.HDCutout.Path) != "" {
		return *entry.HDCutout
	}
	return entry.Original
}

func (m *AssetPackManager) runPullJob(ctx context.Context, proxyStr, customRef, packID string) {
	var finalErr error
	defer func() {
		m.mu.Lock()
		m.downloading = false
		m.cancelPull = nil
		m.currentAsset = ""
		if finalErr != nil {
			if errors.Is(finalErr, context.Canceled) {
				m.lastError = "已手动取消原画包拉取（已完成校验的图片已保留，下次可断点续传）"
			} else {
				m.lastError = finalErr.Error()
			}
		}
		m.rev++
		_ = m.saveStateLocked()
		m.mu.Unlock()
		m.notifyChanged()
	}()

	manifestClient, err := m.updater.httpClient(proxyStr, 25*time.Second)
	if err != nil {
		finalErr = err
		return
	}
	fileClient, err := m.updater.httpClient(proxyStr, 95*time.Second)
	if err != nil {
		finalErr = err
		return
	}

	var candidateRefs []string
	if customRef != "" {
		candidateRefs = []string{customRef}
	} else {
		candidateRefs = []string{"main", "feat/v0.9.4-webui-remote-control"}
	}

	var manifest AssetPackManifest
	var resolvedRef string
	var fetchErr error
	for _, ref := range candidateRefs {
		manifestURL := fmt.Sprintf("%s/%s/assets/material/%s/manifest.json", strings.TrimRight(m.rawBase, "/"), ref, packID)
		manifest, fetchErr = fetchRemoteAssetManifest(ctx, manifestClient, manifestURL)
		if fetchErr == nil && len(manifest.Assets) > 0 {
			resolvedRef = ref
			break
		}
	}
	if resolvedRef == "" {
		if fetchErr != nil {
			finalErr = fmt.Errorf("无法从 GitHub 仓库读取原画包清单（可配置代理后重试）: %w", fetchErr)
		} else {
			finalErr = errors.New("GitHub 仓库原画包清单为空")
		}
		return
	}

	var expectedBytes int64
	validEntries := make([]AssetManifestEntry, 0, len(manifest.Assets))
	for _, entry := range manifest.Assets {
		if !validAssetKeyRe.MatchString(entry.Key) {
			continue
		}
		meta := selectHDTargetMeta(entry)
		if strings.TrimSpace(meta.Path) == "" {
			continue
		}
		expectedBytes += meta.SizeBytes
		validEntries = append(validEntries, entry)
	}
	if len(validEntries) == 0 {
		finalErr = errors.New("manifest.json 中没有可拉取的有效原画条目")
		return
	}

	dispDir := filepath.Join(m.rootDir, packID, "display")
	if err := os.MkdirAll(dispDir, 0o700); err != nil {
		finalErr = err
		return
	}

	m.mu.Lock()
	m.progressTotal = len(validEntries)
	m.expectedBytes = expectedBytes
	m.state.SourceRef = resolvedRef
	if manifest.PackVersion != "" {
		m.state.PackVersion = manifest.PackVersion
	}
	m.mu.Unlock()
	m.notifyChanged()

	type jobItem struct {
		entry AssetManifestEntry
		meta  AssetFileMeta
	}
	jobs := make(chan jobItem, len(validEntries))
	for _, e := range validEntries {
		jobs <- jobItem{entry: e, meta: selectHDTargetMeta(e)}
	}
	close(jobs)

	var wg sync.WaitGroup
	var errOnce sync.Once
	workers := hdPullConcurrency
	if len(validEntries) < workers {
		workers = len(validEntries)
	}

	for wIdx := 0; wIdx < workers; wIdx++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for jb := range jobs {
				if ctx.Err() != nil {
					return
				}
				ext := strings.ToLower(path.Ext(jb.meta.Path))
				if ext == "" {
					ext = ".jpg"
				}
				localName := jb.entry.Key + ext
				destPath := filepath.Join(dispDir, localName)
				wantSHA := strings.ToLower(strings.TrimSpace(jb.meta.SHA256))

				m.mu.Lock()
				m.currentAsset = fmt.Sprintf("#%s %s", jb.entry.ID, jb.entry.Title)
				m.mu.Unlock()

				// Fast-path resume check: skip network download if existing local file already matches SHA-256!
				if sz, ok := verifyExistingFileSHA256(destPath, wantSHA); ok {
					m.recordVerifiedAsset(jb.entry, jb.meta, localName, wantSHA, sz)
					continue
				}

				rawURL := fmt.Sprintf("%s/%s/%s", strings.TrimRight(m.rawBase, "/"), resolvedRef, strings.TrimPrefix(jb.meta.Path, "/"))
				sz, dlErr := downloadAndVerifyAsset(ctx, fileClient, rawURL, destPath, wantSHA)
				if dlErr != nil {
					errOnce.Do(func() {
						finalErr = fmt.Errorf("拉取 #%s (%s) 失败: %w", jb.entry.ID, jb.entry.Title, dlErr)
					})
					m.CancelPull()
					return
				}
				m.recordVerifiedAsset(jb.entry, jb.meta, localName, wantSHA, sz)
			}
		}()
	}
	wg.Wait()

	if finalErr != nil {
		return
	}
	if ctx.Err() != nil {
		finalErr = ctx.Err()
		return
	}

	m.mu.Lock()
	var sumBytes int64
	for _, fn := range m.state.Files {
		if st, err := os.Stat(filepath.Join(dispDir, fn)); err == nil {
			sumBytes += st.Size()
		}
	}
	m.state.TotalBytes = sumBytes
	m.state.PreferHD = true
	m.state.InstalledAt = time.Now().UTC().Format(time.RFC3339)
	m.lastError = ""
	m.mu.Unlock()
}

func (m *AssetPackManager) recordVerifiedAsset(entry AssetManifestEntry, meta AssetFileMeta, localName, shaHex string, sizeBytes int64) {
	m.mu.Lock()
	if m.state.Files == nil {
		m.state.Files = make(map[string]string)
	}
	if m.state.FileSHA256 == nil {
		m.state.FileSHA256 = make(map[string]string)
	}
	if m.state.Dimensions == nil {
		m.state.Dimensions = make(map[string]string)
	}
	m.state.Files[entry.Key] = localName
	m.state.FileSHA256[entry.Key] = shaHex
	if meta.Width > 0 && meta.Height > 0 {
		m.state.Dimensions[entry.Key] = fmt.Sprintf("%dx%d", meta.Width, meta.Height)
	}
	m.progressFiles++
	m.progressBytes += sizeBytes
	m.state.TotalBytes = m.progressBytes
	m.mu.Unlock()
}

func fetchRemoteAssetManifest(ctx context.Context, client *http.Client, manifestURL string) (AssetPackManifest, error) {
	var out AssetPackManifest
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return out, err
	}
	req.Header.Set("User-Agent", "KurumiFrp-HDAssetPuller/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&out); err != nil {
		return out, err
	}
	return out, nil
}

func verifyExistingFileSHA256(filePath, wantSHA string) (int64, bool) {
	f, err := os.Open(filePath)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() || st.Size() == 0 {
		return 0, false
	}
	if wantSHA == "" {
		return st.Size(), true
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return 0, false
	}
	got := hex.EncodeToString(h.Sum(nil))
	return st.Size(), strings.EqualFold(got, wantSHA)
}

func downloadAndVerifyAsset(ctx context.Context, client *http.Client, rawURL, destPath, wantSHA string) (int64, error) {
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		sz, err := downloadAssetOnce(ctx, client, rawURL, destPath, wantSHA)
		if err == nil {
			return sz, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(time.Duration(attempt) * 800 * time.Millisecond):
		}
	}
	return 0, lastErr
}

func downloadAssetOnce(ctx context.Context, client *http.Client, rawURL, destPath, wantSHA string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "KurumiFrp-HDAssetPuller/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	tmpPath := fmt.Sprintf("%s.tmp.%d", destPath, time.Now().UnixNano())
	outFile, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, err
	}

	h := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(outFile, h), io.LimitReader(resp.Body, maxSingleHDAssetBytes))
	closeErr := outFile.Close()
	if copyErr != nil {
		_ = os.Remove(tmpPath)
		return 0, copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return 0, closeErr
	}
	if written == 0 {
		_ = os.Remove(tmpPath)
		return 0, errors.New("下载内容为空")
	}

	if wantSHA != "" {
		gotSHA := hex.EncodeToString(h.Sum(nil))
		if !strings.EqualFold(gotSHA, wantSHA) {
			_ = os.Remove(tmpPath)
			return 0, fmt.Errorf("SHA-256 校验不一致 (期望 %s，实际 %s)", shortHex(wantSHA, 12), shortHex(gotSHA, 12))
		}
	}

	if err := replaceFileAtomic(tmpPath, destPath, 0o600); err != nil {
		_ = os.Remove(tmpPath)
		return 0, err
	}
	return written, nil
}
