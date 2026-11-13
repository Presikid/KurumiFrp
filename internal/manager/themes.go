package manager

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Theme data is deliberately compiled into the application, not downloaded.
// Add an entry only after the artwork, immutable URL, byte size and digest have
// been verified. Catalog entries are trusted 8-bit RGBA atlases without extra
// colour profiles; source caBX metadata is recorded in the artwork JSON, not
// copied into the atlas. Rectangles name the atlas's UI roles.
type themePart struct {
	name string
	rect image.Rectangle
}

type themeSpec struct {
	id, name, description string
	url, sha256           string
	size                  int64
	width, height         int
	parts                 []themePart
}

// The offline default remains in web.Files. These independently downloaded
// atlases use the same coordinate layout and are pinned to a published commit.
var themeAtlasParts = []themePart{
	{name: "overview", rect: image.Rect(0, 0, 1536, 1024)},
	{name: "overview-mobile", rect: image.Rect(0, 0, 1536, 1024)},
	{name: "login-desktop", rect: image.Rect(1536, 0, 2560, 1536)},
	{name: "login-mobile", rect: image.Rect(1536, 0, 2560, 1536)},
	{name: "empty-search", rect: image.Rect(0, 1536, 1024, 2560)},
	{name: "empty-welcome", rect: image.Rect(1024, 1536, 2048, 2560)},
}

var themeCatalog = []themeSpec{
	{
		id: "sunlit-white", name: "白裙晴空", description: "白裙与晴空的明亮场景",
		url:    "https://raw.githubusercontent.com/Presikid/KurumiFrp/9b044be52f99640438ead8c73cfe29798bbf51db/assets/themes/sunlit-white.png",
		sha256: "9c88dc2c8f6cf2e29c0051aae3da96cd1e413a949414e1e69d20744767da8a3c",
		size:   7908995, width: 2560, height: 2560, parts: themeAtlasParts,
	},
	{
		id: "detective-study", name: "魔术侦探书房", description: "魔术侦探与温暖书房",
		url:    "https://raw.githubusercontent.com/Presikid/KurumiFrp/9b044be52f99640438ead8c73cfe29798bbf51db/assets/themes/detective-study.png",
		sha256: "8e1a4d098bf0c562b4d49626da93ff24284b5cb89b6629d08578d3aa8b6271b0",
		size:   8021583, width: 2560, height: 2560, parts: themeAtlasParts,
	},
	{
		id: "moon-witch", name: "紫色魔女月夜", description: "紫色魔女与月夜",
		url:    "https://raw.githubusercontent.com/Presikid/KurumiFrp/9b044be52f99640438ead8c73cfe29798bbf51db/assets/themes/moon-witch.png",
		sha256: "cce2ac451eab800ec080deef1b5932a9da7c9d70ca8f68dad34856777e6b6ed5",
		size:   8443539, width: 2560, height: 2560, parts: themeAtlasParts,
	},
}

const (
	maxThemeDownload     = 32 << 20
	maxThemePixels       = 8 << 20
	maxThemeOutputPixels = 16 << 20
	maxThemeImageBytes   = 72 << 20
	maxThemeCopies       = 16
)

var themeNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
var themeURLPartPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
var themeCommitPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)

type themeManager struct {
	dir    string
	client *http.Client
	mu     sync.Mutex
}

func newThemeManager(dir string) *themeManager {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 15 * time.Second
	return &themeManager{dir: dir, client: &http.Client{
		Transport: transport,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) >= 5 || !allowedThemeHost(r.URL) {
				return fmt.Errorf("主题下载跳转不受信任")
			}
			return nil
		},
	}}
}

func allowedThemeHost(u *url.URL) bool {
	if u.Scheme != "https" || u.User != nil || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	switch u.Hostname() {
	case "github.com", "raw.githubusercontent.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com":
		return true
	}
	return false
}

func trustedThemeURL(address string) bool {
	u, err := url.Parse(address)
	if err != nil || !allowedThemeHost(u) || u.RawQuery != "" || u.ForceQuery || u.EscapedPath() != u.Path {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	for _, part := range parts {
		if !themeURLPartPattern.MatchString(part) || part == "." || part == ".." {
			return false
		}
	}
	if len(parts) < 4 || parts[0] != "Presikid" || parts[1] != "KurumiFrp" || !strings.HasSuffix(parts[len(parts)-1], ".png") {
		return false
	}
	if u.Hostname() == "raw.githubusercontent.com" {
		return len(parts) >= 4 && themeCommitPattern.MatchString(parts[2])
	}
	return u.Hostname() == "github.com" && len(parts) == 6 && parts[2] == "releases" && parts[3] == "download"
}

func validThemeSpec(spec themeSpec) bool {
	if spec.id == "default" || len(spec.id) > 32 || !themeNamePattern.MatchString(spec.id) || spec.name == "" || !trustedThemeURL(spec.url) || spec.size <= 0 || spec.size > maxThemeDownload || spec.width <= 0 || spec.width > 4096 || spec.height <= 0 || spec.height > 4096 || spec.width*spec.height > maxThemePixels || len(spec.parts) == 0 || len(spec.parts) > 16 {
		return false
	}
	digest, err := hex.DecodeString(spec.sha256)
	if err != nil || len(digest) != sha256.Size || spec.sha256 != strings.ToLower(spec.sha256) {
		return false
	}
	seen := make(map[string]bool)
	total := 0
	for _, part := range spec.parts {
		r := part.rect
		if len(part.name) > 32 || !themeNamePattern.MatchString(part.name) || seen[part.name] || r.Min.X < 0 || r.Min.Y < 0 || r.Max.X > spec.width || r.Max.Y > spec.height || r.Dx() <= 0 || r.Dy() <= 0 {
			return false
		}
		seen[part.name] = true
		total += r.Dx() * r.Dy()
		if total > maxThemeOutputPixels {
			return false
		}
	}
	return true
}

func lookupTheme(id string) (themeSpec, bool) {
	for _, spec := range themeCatalog {
		if spec.id == id && validThemeSpec(spec) {
			return spec, true
		}
	}
	return themeSpec{}, false
}

func openPlainThemeDirectory(root *os.Root, name string) (*os.Root, error) {
	info, err := root.Lstat(name)
	if err != nil || info.Mode().Type() != os.ModeDir {
		return nil, fmt.Errorf("主题目录不安全，原有文件已保留")
	}
	dir, err := root.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	opened, err := dir.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		dir.Close()
		return nil, fmt.Errorf("主题目录不安全，原有文件已保留")
	}
	return dir, nil
}

func (m *themeManager) themeRoot(create bool) (*os.Root, error) {
	data, err := os.OpenRoot(m.dir)
	if err != nil {
		return nil, err
	}
	defer data.Close()
	if create {
		if err := data.Mkdir("themes", 0700); err != nil && !os.IsExist(err) {
			return nil, err
		}
	}
	return openPlainThemeDirectory(data, "themes")
}

func themeDirectory(spec themeSpec, copy int) string {
	name := spec.id + "-" + spec.sha256
	if copy > 0 {
		name += "-" + strconv.Itoa(copy)
	}
	return name
}

func plainThemeFile(root *os.Root, name string, max int64) (*os.File, error) {
	info, err := root.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > max {
		return nil, fmt.Errorf("主题文件不可用")
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		f.Close()
		return nil, fmt.Errorf("主题文件不安全")
	}
	return f, nil
}

func validThemeDirectory(root *os.Root, name string, spec themeSpec) bool {
	dir, err := openPlainThemeDirectory(root, name)
	if err != nil {
		return false
	}
	defer dir.Close()
	markerFile, err := plainThemeFile(dir, "ready", 64)
	if err != nil {
		return false
	}
	marker, err := io.ReadAll(io.LimitReader(markerFile, 65))
	markerFile.Close()
	if err != nil || string(marker) != spec.sha256 {
		return false
	}
	for _, part := range spec.parts {
		f, err := plainThemeFile(dir, part.name+".png", maxThemeImageBytes)
		if err != nil {
			return false
		}
		config, err := png.DecodeConfig(f)
		valid := err == nil && config.Width == part.rect.Dx() && config.Height == part.rect.Dy()
		if valid {
			// A complete encoder output ends with IEND; this catches truncation
			// without decoding every image on each status or asset request.
			info, statErr := f.Stat()
			var ending [12]byte
			if statErr != nil || info.Size() < int64(len(ending)) {
				valid = false
			} else {
				_, readErr := f.ReadAt(ending[:], info.Size()-int64(len(ending)))
				valid = readErr == nil && ending == [12]byte{0, 0, 0, 0, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82}
			}
		}
		f.Close()
		if !valid {
			return false
		}
	}
	return true
}

func (m *themeManager) installed(spec themeSpec) (string, bool) {
	root, err := m.themeRoot(false)
	if err != nil {
		return "", false
	}
	defer root.Close()
	for i := 0; i < maxThemeCopies; i++ {
		name := themeDirectory(spec, i)
		if validThemeDirectory(root, name, spec) {
			return name, true
		}
	}
	return "", false
}

type availableTheme struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Installed   bool   `json:"installed"`
}

func (m *themeManager) available() []availableTheme {
	list := make([]availableTheme, 0, len(themeCatalog))
	for _, spec := range themeCatalog {
		if validThemeSpec(spec) {
			_, installed := m.installed(spec)
			list = append(list, availableTheme{spec.id, spec.name, spec.description, installed})
		}
	}
	return list
}

func themeDownloadError(ctx context.Context, err error) error {
	switch {
	case ctx.Err() == context.Canceled:
		return fmt.Errorf("主题下载已取消")
	case ctx.Err() == context.DeadlineExceeded || errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("主题下载超时，请检查网络后重试")
	default:
		return fmt.Errorf("主题下载失败，请检查网络后重试")
	}
}

func (m *themeManager) download(ctx context.Context, spec themeSpec) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, spec.url, nil)
	if err != nil {
		return nil, fmt.Errorf("主题下载地址无效")
	}
	req.Header.Set("User-Agent", "KurumiFRP-theme")
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, themeDownloadError(ctx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub 返回 HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > spec.size {
		return nil, fmt.Errorf("主题文件大小不符")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, spec.size+1))
	if err != nil {
		return nil, themeDownloadError(ctx, err)
	}
	if int64(len(body)) != spec.size {
		return nil, fmt.Errorf("主题文件大小不符")
	}
	want, _ := hex.DecodeString(spec.sha256)
	hash := sha256.Sum256(body)
	if subtle.ConstantTimeCompare(hash[:], want) != 1 {
		return nil, fmt.Errorf("主题 SHA-256 校验失败")
	}
	return body, nil
}

type limitedThemeWriter struct {
	w         io.Writer
	remaining int64
}

func (w *limitedThemeWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, fmt.Errorf("展开后的主题图片过大")
	}
	n, err := w.w.Write(p)
	w.remaining -= int64(n)
	return n, err
}

func createThemeStage(root *os.Root) (string, *os.Root, error) {
	for i := 0; i < 8; i++ {
		var random [12]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", nil, err
		}
		name := ".kurumi-theme-" + hex.EncodeToString(random[:])
		if err := root.Mkdir(name, 0700); err != nil {
			if os.IsExist(err) {
				continue
			}
			return "", nil, err
		}
		dir, err := openPlainThemeDirectory(root, name)
		if err != nil {
			_ = root.Remove(name)
			return "", nil, err
		}
		return name, dir, nil
	}
	return "", nil, fmt.Errorf("无法创建主题临时目录")
}

func (m *themeManager) install(ctx context.Context, id string) error {
	spec, ok := lookupTheme(id)
	if !ok {
		return fmt.Errorf("没有此可下载主题")
	}
	if !m.mu.TryLock() {
		return fmt.Errorf("已有主题正在安装，请稍后重试")
	}
	defer m.mu.Unlock()
	if _, installed := m.installed(spec); installed {
		return nil
	}
	root, err := m.themeRoot(true)
	if err != nil {
		return fmt.Errorf("无法使用主题目录：%w", err)
	}
	defer root.Close()
	final := ""
	for i := 0; i < maxThemeCopies; i++ {
		name := themeDirectory(spec, i)
		if _, err := root.Lstat(name); os.IsNotExist(err) {
			final = name
			break
		} else if err != nil {
			return err
		}
	}
	if final == "" {
		return fmt.Errorf("主题安装目录已占满，原有文件已保留，请检查主题数据目录")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	downloadCtx, cancelDownload := context.WithTimeout(ctx, 90*time.Second)
	body, err := m.download(downloadCtx, spec)
	cancelDownload()
	if err != nil {
		return err
	}
	config, err := png.DecodeConfig(bytes.NewReader(body))
	if err != nil || config.Width != spec.width || config.Height != spec.height {
		return fmt.Errorf("主题 PNG 尺寸无效")
	}
	// DecodeConfig has validated the PNG header. Its IHDR fields identify the
	// catalog's required 8-bit RGBA format before the full image is decoded.
	if len(body) < 26 || body[24] != 8 || body[25] != 6 {
		return fmt.Errorf("主题图集必须是 8 位 RGBA PNG")
	}
	atlas, err := png.Decode(bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("无法解码主题 PNG")
	}
	// SubImage retains the unassociated RGB bytes, including under alpha zero;
	// drawing/compositing into a new image would not preserve those bytes.
	source, ok := atlas.(*image.NRGBA)
	if !ok || source.Bounds() != image.Rect(0, 0, spec.width, spec.height) {
		return fmt.Errorf("主题图集像素格式不受支持")
	}
	stageName, stage, err := createThemeStage(root)
	if err != nil {
		return err
	}
	created := make([]string, 0, len(spec.parts)+1)
	published := false
	defer func() {
		if published {
			return
		}
		if stage == nil {
			stage, _ = openPlainThemeDirectory(root, stageName)
		}
		if stage == nil {
			return
		}
		for _, name := range created {
			_ = stage.Remove(name)
		}
		_ = stage.Close()
		_ = root.Remove(stageName)
	}()
	for _, part := range spec.parts {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("主题安装已中断")
		}
		name := part.name + ".png"
		f, err := stage.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		created = append(created, name)
		writer := &limitedThemeWriter{w: f, remaining: maxThemeImageBytes}
		err = png.Encode(writer, source.SubImage(part.rect))
		if err == nil {
			err = f.Sync()
		}
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return fmt.Errorf("无法展开主题图片：%w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("主题安装已中断")
	}
	marker, err := stage.OpenFile("ready", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	created = append(created, "ready")
	_, err = marker.WriteString(spec.sha256)
	if err == nil {
		err = marker.Sync()
	}
	if closeErr := marker.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if _, err = root.Lstat(final); err == nil {
		return fmt.Errorf("主题目录已存在，原有文件已保留")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err = stage.Close(); err != nil {
		stage = nil
		return err
	}
	stage = nil
	if err = root.Rename(stageName, final); err != nil {
		return fmt.Errorf("无法完成主题安装：%w", err)
	}
	published = true
	return nil
}

func (a *App) themesAPI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/themes" && r.Method == http.MethodGet {
		reply(w, http.StatusOK, map[string]any{"themes": a.themes.available()})
		return
	}
	if r.URL.Path == "/api/themes/install" && r.Method == http.MethodPost {
		// PNG crops can take longer than the normal API write timeout on small devices.
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(2*time.Minute + 15*time.Second))
		var input struct {
			ID string `json:"id"`
		}
		if err := decode(w, r, &input); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		if err := a.themes.install(r.Context(), input.ID); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		reply(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	w.WriteHeader(http.StatusMethodNotAllowed)
}

func defaultThemeAsset(role string) string {
	switch role {
	case "login-desktop", "login-mobile", "overview", "overview-mobile", "empty-welcome", "empty-search":
		return "/assets/" + role + ".webp"
	}
	return ""
}

func (a *App) serveThemeAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/theme-assets/"), "/")
	if len(parts) != 2 || len(parts[0]) > 32 || !themeNamePattern.MatchString(parts[0]) || !strings.HasSuffix(parts[1], ".png") {
		http.NotFound(w, r)
		return
	}
	spec, ok := lookupTheme(parts[0])
	role := strings.TrimSuffix(parts[1], ".png")
	allowed := false
	for _, part := range spec.parts {
		allowed = allowed || part.name == role
	}
	if !allowed && defaultThemeAsset(role) == "" {
		http.NotFound(w, r)
		return
	}
	if ok && allowed {
		if installedName, installed := a.themes.installed(spec); installed {
			root, err := a.themes.themeRoot(false)
			if err == nil {
				dir, dirErr := openPlainThemeDirectory(root, installedName)
				root.Close()
				if dirErr == nil {
					f, fileErr := plainThemeFile(dir, role+".png", maxThemeImageBytes)
					dir.Close()
					if fileErr == nil {
						defer f.Close()
						w.Header().Set("Content-Type", "image/png")
						w.Header().Set("Cache-Control", "private, no-cache")
						http.ServeContent(w, r, "", time.Time{}, f)
						return
					}
				}
			}
		}
	}
	if fallback := defaultThemeAsset(role); fallback != "" {
		copy := r.Clone(r.Context())
		u := *r.URL
		u.Path, u.RawPath = fallback, ""
		copy.URL = &u
		a.assets.ServeHTTP(w, copy)
		return
	}
	http.NotFound(w, r)
}
