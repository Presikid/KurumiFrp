package manager

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/Presikid/KurumiFrp/internal/version"
)

type releaseRepo struct {
	name, checksumFile string
}

var (
	appRepo          = releaseRepo{"Presikid/KurumiFrp", "SHA256SUMS.txt"}
	frpRepo          = releaseRepo{"fatedier/frp", "frp_sha256_checksums.txt"}
	stableReleaseTag = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)
)

type cachedRelease struct {
	release *ghRelease
	proxy   string
	expires time.Time
}

// Cache only release metadata. Installed versions and platform capabilities are
// determined again on each check, including when the user clicks Apply.
func (u *UpdateManager) latestRelease(ctx context.Context, client *http.Client, proxy string, repo releaseRepo) (*ghRelease, error) {
	u.mu.Lock()
	cached := u.releases[repo]
	u.mu.Unlock()
	if cached.proxy == proxy && time.Now().Before(cached.expires) {
		return cached.release, nil
	}

	release, err := u.webRelease(ctx, client, repo)
	if err != nil {
		return nil, fmt.Errorf("读取 %s 官方发布信息失败: %w", repo.name, err)
	}

	u.mu.Lock()
	u.releases[repo] = cachedRelease{release: &release, proxy: proxy, expires: time.Now().Add(5 * time.Minute)}
	u.mu.Unlock()
	return &release, nil
}

// Resolve the official latest link once. Pin the checksum list and all asset
// URLs to its tag so that a new publication during an update cannot mix releases.
func (u *UpdateManager) webRelease(ctx context.Context, client *http.Client, repo releaseRepo) (ghRelease, error) {
	base := u.webBase + "/" + repo.name + "/releases"
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, base+"/latest", nil)
	if err != nil {
		return ghRelease{}, err
	}
	req.Header.Set("User-Agent", "KurumiFrp/"+version.Current)
	resp, err := client.Do(req)
	if err != nil {
		return ghRelease{}, err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ghRelease{}, fmt.Errorf("发布页返回 HTTP %d", resp.StatusCode)
	}
	final := resp.Request.URL
	tag := path.Base(final.Path)
	if !stableReleaseTag.MatchString(tag) || final.String() != base+"/tag/"+tag {
		return ghRelease{}, fmt.Errorf("无法确认 %s 的正式发布标签", repo.name)
	}
	downloadBase := base + "/download/" + tag + "/"
	checksums, err := u.downloadChecksums(ctx, client, downloadBase+repo.checksumFile)
	if err != nil {
		return ghRelease{}, err
	}
	rel := ghRelease{TagName: tag, HTMLURL: final.String()}
	for name, digest := range checksums {
		rel.Assets = append(rel.Assets, ghAsset{Name: name, BrowserDownloadURL: downloadBase + url.PathEscape(name), sha256: digest})
	}
	return rel, nil
}

func (u *UpdateManager) downloadChecksums(ctx context.Context, client *http.Client, rawURL string) (map[string]string, error) {
	raw, err := u.downloadBytes(ctx, client, rawURL, 2<<20)
	if err != nil {
		return nil, fmt.Errorf("下载官方 SHA-256 清单失败: %w", err)
	}
	sums := make(map[string]string)
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || len(fields[0]) != 64 {
			continue
		}
		if _, err := hex.DecodeString(fields[0]); err != nil {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		name = path.Base(strings.ReplaceAll(name, "\\", "/"))
		sum := strings.ToLower(fields[0])
		if previous, ok := sums[name]; ok && previous != sum {
			return nil, fmt.Errorf("官方校验清单中 %s 有多个不同的 SHA-256", name)
		}
		sums[name] = sum
	}
	if len(sums) == 0 {
		return nil, fmt.Errorf("官方 SHA-256 清单没有有效记录")
	}
	return sums, nil
}
