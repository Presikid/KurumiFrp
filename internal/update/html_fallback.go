package update

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// GitHub's public HTML is a fallback when its anonymous API quota is exhausted.
// It is not a stable API, so unexpected markup is rejected rather than guessed.
var releaseLink = regexp.MustCompile(`href="/Presikid/KurumiFrp/releases/tag/(v[0-9]{1,5}\.[0-9]{1,5}\.[0-9]{1,5}(?:\.[0-9]{1,5})?)"`)

func (m *Manager) html(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil || !allowed(req.URL) {
		return "", fmt.Errorf("不受信任的发行页面地址")
	}
	req.Header.Set("User-Agent", "KurumiFRP-updater")
	req.Header.Set("Accept", "text/html")
	r, err := m.client.Do(req)
	if err != nil {
		return "", err
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub 发行页面返回 HTTP %d", r.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, (2<<20)+1))
	if err != nil || len(b) > 2<<20 {
		return "", fmt.Errorf("GitHub 发行页面读取失败或过大")
	}
	return string(b), nil
}

func htmlAssetDigest(page, tag, name string) (string, bool, error) {
	link := `href="/Presikid/KurumiFrp/releases/download/` + tag + `/` + name + `"`
	count := strings.Count(page, link)
	if count == 0 {
		return "", false, nil
	}
	if count != 1 {
		return "", false, fmt.Errorf("发行附件链接不唯一")
	}
	// Both the name and digest are attributes of GitHub's copy-digest control.
	pattern := regexp.MustCompile(`<clipboard-copy\b[^>]*aria-label="Copy to clipboard digest for ` + regexp.QuoteMeta(name) + `"[^>]*value="(sha256:[a-f0-9]{64})"[^>]*>`)
	matches := pattern.FindAllStringSubmatch(page, -1)
	if len(matches) != 1 {
		return "", false, fmt.Errorf("发行附件摘要缺失或不唯一")
	}
	return matches[0][1], true, nil
}

func (m *Manager) htmlReleaseAsset(ctx context.Context, tag string) (release, error) {
	r := release{Tag: tag, MetadataFallback: true}
	name := packageName(tag, m.distribution)
	if name == "" {
		return r, nil
	}
	page, err := m.html(ctx, "https://github.com/Presikid/KurumiFrp/releases/expanded_assets/"+tag)
	if err != nil {
		return r, err
	}
	digest, exists, err := htmlAssetDigest(page, tag, name)
	if err != nil || !exists {
		return r, err
	}
	url := "https://github.com/Presikid/KurumiFrp/releases/download/" + tag + "/" + name
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return r, err
	}
	req.Header.Set("User-Agent", "KurumiFRP-updater")
	resp, err := m.client.Do(req)
	if err != nil {
		return r, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.ContentLength <= 0 || resp.ContentLength > maxPackage {
		return r, fmt.Errorf("无法确认官方安装包大小")
	}
	r.Assets = []Asset{{Name: name, URL: url, Size: resp.ContentLength, Digest: digest}}
	return r, nil
}

func (m *Manager) releasesHTML(ctx context.Context) ([]release, error) {
	const base = "https://github.com/Presikid/KurumiFrp/releases"
	tags, seen := []string{}, map[string]bool{}
	for pageNo := 1; pageNo <= 20; pageNo++ {
		page, err := m.html(ctx, base+"?page="+strconv.Itoa(pageNo))
		if err != nil {
			return nil, err
		}
		matches := releaseLink.FindAllStringSubmatch(page, -1)
		if len(matches) == 0 {
			return nil, fmt.Errorf("无法读取 GitHub 发行版本列表")
		}
		for _, match := range matches {
			tag := match[1]
			if _, ok := parseVersion(tag); ok && !seen[tag] {
				tags = append(tags, tag)
				seen[tag] = true
				if len(tags) > 200 {
					return nil, fmt.Errorf("GitHub 发行版本列表过长")
				}
			}
		}
		next := `href="/Presikid/KurumiFrp/releases?page=` + strconv.Itoa(pageNo+1) + `"`
		if !strings.Contains(page, next) {
			break
		}
		if pageNo == 20 {
			return nil, fmt.Errorf("GitHub 发行版本列表过长")
		}
	}
	result := make([]release, len(tags))
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for i, tag := range tags {
		wg.Add(1)
		go func(i int, tag string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				mu.Lock()
				if firstErr == nil {
					firstErr = ctx.Err()
				}
				mu.Unlock()
				return
			}
			defer func() { <-sem }()
			r, err := m.htmlReleaseAsset(ctx, tag)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			result[i] = r
		}(i, tag)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, fmt.Errorf("无法验证 GitHub 官方发行页面：%w", firstErr)
	}
	if m.distribution == "windows-installed" || strings.HasPrefix(m.distribution, "linux-") {
		hasAsset := false
		for _, r := range result {
			if _, ok := assetFor(r, m.distribution); ok {
				hasAsset = true
				break
			}
		}
		if !hasAsset {
			return nil, fmt.Errorf("官方发行页面没有可验证的安装包")
		}
	}
	return result, nil
}
