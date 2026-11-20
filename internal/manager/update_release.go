package manager

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/Presikid/KurumiFrp/internal/version"
)

type releaseRepo struct {
	name         string
	checksumFile string
}

var (
	appRepo          = releaseRepo{name: "Presikid/KurumiFrp", checksumFile: "SHA256SUMS.txt"}
	frpRepo          = releaseRepo{name: "fatedier/frp", checksumFile: "frp_sha256_checksums.txt"}
	stableReleaseTag = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)
	errAppUpToDate   = errors.New("当前已是最新版本，无需更新")
	errFRPUpToDate   = errors.New("官方 FRP 核心已是最新版本，无需更新")
)

// latestRelease queries GitHub Releases redirect + SHA-256 manifest live on every check without local TTL caching.
func (u *UpdateManager) latestRelease(ctx context.Context, client *http.Client, repo releaseRepo) (ghRelease, error) {
	return u.webRelease(ctx, client, repo)
}

func (u *UpdateManager) webRelease(ctx context.Context, client *http.Client, repo releaseRepo) (ghRelease, error) {
	base := strings.TrimRight(u.webBase, "/") + "/" + repo.name + "/releases"
	latestURL := base + "/latest"
	parsedLatest, err := url.Parse(latestURL)
	if err != nil {
		return ghRelease{}, err
	}
	if err := validateDownloadURL(parsedLatest, u.allowLocal); err != nil {
		return ghRelease{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, latestURL, nil)
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
		return ghRelease{}, fmt.Errorf("GitHub Releases 返回状态码 %d", resp.StatusCode)
	}

	finalURL := resp.Request.URL
	if finalURL == nil {
		return ghRelease{}, errors.New("无法解析最新发布重定向地址")
	}
	tag := path.Base(finalURL.Path)
	expectedTagURL := base + "/tag/" + tag
	if !stableReleaseTag.MatchString(tag) || finalURL.String() != expectedTagURL {
		return ghRelease{}, fmt.Errorf("未识别的发布标签重定向地址: %s", finalURL.String())
	}

	downloadBase := base + "/download/" + tag + "/"
	sums, err := u.downloadChecksums(ctx, client, downloadBase+repo.checksumFile)
	if err != nil {
		return ghRelease{}, err
	}

	assets := make([]ghAsset, 0, len(sums))
	for name, digest := range sums {
		assets = append(assets, ghAsset{
			Name:               name,
			BrowserDownloadURL: downloadBase + url.PathEscape(name),
			sha256:             digest,
		})
	}
	return ghRelease{
		TagName: tag,
		HTMLURL: expectedTagURL,
		Assets:  assets,
	}, nil
}

func (u *UpdateManager) downloadChecksums(ctx context.Context, client *http.Client, checksumURL string) (map[string]string, error) {
	raw, err := u.downloadBytes(ctx, client, checksumURL, 2<<20)
	if err != nil {
		return nil, fmt.Errorf("下载官方 SHA-256 校验清单失败: %w", err)
	}
	sums := make(map[string]string)
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		digest := strings.ToLower(strings.TrimSpace(fields[0]))
		if len(digest) != 64 {
			continue
		}
		if _, err := hex.DecodeString(digest); err != nil {
			continue
		}
		name := strings.TrimPrefix(strings.TrimSpace(fields[len(fields)-1]), "*")
		name = path.Base(strings.ReplaceAll(name, "\\", "/"))
		if name == "" || name == "." || name == "/" {
			continue
		}
		if prev, exists := sums[name]; exists && prev != digest {
			return nil, fmt.Errorf("SHA-256 校验清单中 %s 存在冲突条目", name)
		}
		sums[name] = digest
	}
	if len(sums) == 0 {
		return nil, errors.New("SHA-256 校验清单为空或格式无效")
	}
	return sums, nil
}

// compareSemVer compares two SemVer strings including prerelease precedence (e.g. 0.62.0-rc.1 < 0.62.0).
func compareSemVer(a, b string) int {
	parse := func(s string) ([]int, []string) {
		s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "v"))
		if plus := strings.IndexByte(s, '+'); plus >= 0 {
			s = s[:plus]
		}
		var pre []string
		if dash := strings.IndexByte(s, '-'); dash >= 0 {
			preStr := s[dash+1:]
			s = s[:dash]
			if preStr != "" {
				pre = strings.Split(preStr, ".")
			}
		}
		parts := strings.Split(s, ".")
		nums := make([]int, len(parts))
		for i, p := range parts {
			v, _ := strconv.Atoi(strings.TrimSpace(p))
			nums[i] = v
		}
		for len(nums) > 3 && nums[len(nums)-1] == 0 {
			nums = nums[:len(nums)-1]
		}
		return nums, pre
	}

	na, pa := parse(a)
	nb, pb := parse(b)
	n := len(na)
	if len(nb) > n {
		n = len(nb)
	}
	if n < 3 {
		n = 3
	}
	for i := 0; i < n; i++ {
		var va, vb int
		if i < len(na) {
			va = na[i]
		}
		if i < len(nb) {
			vb = nb[i]
		}
		if va != vb {
			if va > vb {
				return 1
			}
			return -1
		}
	}
	if len(pa) == 0 && len(pb) == 0 {
		return 0
	}
	if len(pa) == 0 {
		return 1 // stable > prerelease
	}
	if len(pb) == 0 {
		return -1
	}
	for i := 0; i < len(pa) && i < len(pb); i++ {
		if c := comparePreIdent(pa[i], pb[i]); c != 0 {
			return c
		}
	}
	if len(pa) > len(pb) {
		return 1
	}
	if len(pa) < len(pb) {
		return -1
	}
	return 0
}

func comparePreIdent(a, b string) int {
	if a == b {
		return 0
	}
	ia, errA := strconv.Atoi(a)
	ib, errB := strconv.Atoi(b)
	if errA == nil && errB == nil {
		if ia > ib {
			return 1
		}
		return -1
	}
	if errA == nil {
		return -1 // numeric identifiers have lower precedence than non-numeric per SemVer 2.0
	}
	if errB == nil {
		return 1
	}
	if a > b {
		return 1
	}
	return -1
}
