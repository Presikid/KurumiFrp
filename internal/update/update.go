package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Presikid/KurumiFrp/internal/version"
)

const current = "v" + version.Current
const releaseAPI = "https://api.github.com/repos/Presikid/KurumiFrp/releases"
const maxPackage = 256 << 20

// Three-part tags remain readable for releases published before the four-part scheme.
var tagRE = regexp.MustCompile(`^v[0-9]{1,5}\.[0-9]{1,5}\.[0-9]{1,5}(\.[0-9]{1,5})?$`)
var digestRE = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

type Asset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}
type release struct {
	Tag        string  `json:"tag_name"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Assets     []Asset `json:"assets"`
}
type Version struct {
	Tag        string `json:"tag"`
	Prerelease bool   `json:"prerelease"`
	Newer      bool   `json:"newer"`
	Available  bool   `json:"available"`
}
type Manager struct {
	dir, distribution string
	mu                sync.Mutex
	busy              bool
	status            string
	client            *http.Client
}

func New(dir, distribution string) *Manager {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = 20 * time.Second
	return &Manager{dir: dir, distribution: distribution, client: &http.Client{Transport: tr, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !allowed(r.URL) {
			return fmt.Errorf("不安全的下载跳转")
		}
		return nil
	}}}
}
func allowed(u *url.URL) bool {
	if u.Scheme != "https" || u.User != nil || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	switch u.Hostname() {
	case "api.github.com", "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com":
		return true
	}
	return false
}
func (m *Manager) get(ctx context.Context, address string) (*http.Response, error) {
	u, e := url.Parse(address)
	if e != nil || !allowed(u) {
		return nil, fmt.Errorf("不受信任的下载地址")
	}
	req, e := http.NewRequestWithContext(ctx, "GET", address, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("User-Agent", "KurumiFRP-updater")
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, e := m.client.Do(req)
	if e != nil {
		return nil, fmt.Errorf("无法连接 GitHub：%w", e)
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, fmt.Errorf("GitHub 返回 HTTP %d", resp.StatusCode)
	}
	return resp, nil
}
func (m *Manager) releases(ctx context.Context) ([]release, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	resp, e := m.get(ctx, releaseAPI+"?per_page=100")
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if e != nil || len(b) >= 8<<20 {
		return nil, fmt.Errorf("版本列表读取失败或过大")
	}
	var all []release
	e = json.Unmarshal(b, &all)
	return all, e
}
func parseVersion(tag string) ([4]int, bool) {
	var result [4]int
	if !tagRE.MatchString(tag) {
		return result, false
	}
	for i, part := range strings.Split(strings.TrimPrefix(tag, "v"), ".") {
		n, err := strconv.Atoi(part)
		if err != nil || n > 65535 {
			return result, false
		}
		result[i] = n
	}
	return result, true
}

func newer(a, b string) bool {
	left, okA := parseVersion(a)
	right, okB := parseVersion(b)
	if !okA || !okB {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return left[i] > right[i]
		}
	}
	return false
}
func packageName(tag, distribution string) string {
	version := strings.TrimPrefix(tag, "v")
	if distribution == "windows-installed" {
		return "KurumiFRP-" + version + "-Setup.exe"
	}
	if distribution == "linux-deb" && (runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64") {
		return "kurumifrp_" + version + "_" + runtime.GOARCH + ".deb"
	}
	arch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
	if distribution == "linux-rpm" && arch != "" {
		return "kurumifrp-" + version + "-1." + arch + ".rpm"
	}
	return ""
}
func assetFor(r release, distribution string) (Asset, bool) {
	name := packageName(r.Tag, distribution)
	expected := "https://github.com/Presikid/KurumiFrp/releases/download/" + r.Tag + "/" + name
	for _, a := range r.Assets {
		if name != "" && a.Name == name && a.URL == expected && a.Size > 0 && a.Size <= maxPackage && digestRE.MatchString(a.Digest) {
			return a, true
		}
	}
	return Asset{}, false
}
func (m *Manager) Versions(ctx context.Context) ([]Version, error) {
	list, e := m.releases(ctx)
	if e != nil {
		return nil, e
	}
	out := []Version{}
	for _, r := range list {
		_, valid := parseVersion(r.Tag)
		if r.Draft || !valid {
			continue
		}
		_, ok := assetFor(r, m.distribution)
		out = append(out, Version{r.Tag, r.Prerelease, newer(r.Tag, current), ok})
	}
	return out, nil
}
func (m *Manager) selectAsset(ctx context.Context, tag string) (Asset, error) {
	if !tagRE.MatchString(tag) || !newer(tag, current) {
		return Asset{}, fmt.Errorf("只能更新到更新的版本")
	}
	list, e := m.releases(ctx)
	if e != nil {
		return Asset{}, e
	}
	for _, r := range list {
		if r.Tag == tag && !r.Draft {
			if a, ok := assetFor(r, m.distribution); ok {
				return a, nil
			}
		}
	}
	return Asset{}, fmt.Errorf("没有适用于当前安装方式的官方安装包")
}
func (m *Manager) download(ctx context.Context, a Asset, dir string) (string, error) {
	resp, e := m.get(ctx, a.URL)
	if e != nil {
		return "", e
	}
	defer resp.Body.Close()
	path := filepath.Join(dir, a.Name)
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return "", e
	}
	hash := sha256.New()
	n, e := io.Copy(io.MultiWriter(f, hash), io.LimitReader(resp.Body, maxPackage+1))
	if e == nil {
		e = f.Sync()
	}
	if ce := f.Close(); e == nil {
		e = ce
	}
	if e != nil {
		return "", e
	}
	if n != a.Size || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != a.Digest {
		return "", fmt.Errorf("更新包大小或 SHA-256 校验失败")
	}
	return path, nil
}
func (m *Manager) Status() string { m.mu.Lock(); defer m.mu.Unlock(); return m.status }
func (m *Manager) Start(tag string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.busy {
		return fmt.Errorf("更新任务正在进行")
	}
	if !tagRE.MatchString(tag) || !newer(tag, current) {
		return fmt.Errorf("请选择更新的版本")
	}
	m.busy = true
	m.status = "正在检查并准备更新"
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		err := m.start(ctx, tag)
		m.mu.Lock()
		defer m.mu.Unlock()
		m.busy = false
		if err != nil {
			m.status = "更新失败：" + err.Error()
		} else {
			m.status = "更新请求已提交；工作台可能暂时断开，请在重新打开后确认版本"
		}
	}()
	return nil
}
func (m *Manager) start(ctx context.Context, tag string) error {
	a, e := m.selectAsset(ctx, tag)
	if e != nil {
		return e
	}
	if strings.HasPrefix(m.distribution, "linux-") {
		return requestSystemUpdate(tag)
	}
	if m.distribution != "windows-installed" {
		return fmt.Errorf("此安装方式不支持自动更新")
	}
	dir := filepath.Join(m.dir, "updates")
	if e = os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	st, e := os.Lstat(dir)
	if e != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("更新目录不安全")
	}
	stage, e := os.MkdirTemp(dir, "update-")
	if e != nil {
		return e
	}
	path, e := m.download(ctx, a, stage)
	if e != nil {
		os.RemoveAll(stage)
		return e
	}
	return launchInstaller(path)
}
