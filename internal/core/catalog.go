// Package core installs official frp releases into immutable version directories.
package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const API = "https://api.github.com/repos/fatedier/frp/releases"
const MaxArchive = 160 << 20

var tagRE = regexp.MustCompile(`^v[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}$`)
var digestRE = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var assetRE = regexp.MustCompile(`^frp_([0-9]+\.[0-9]+\.[0-9]+)_([a-z0-9]+)_([a-z0-9_]+)\.(tar\.gz|zip)$`)

func Host() string { return runtime.GOOS + "_" + runtime.GOARCH }
func supported(platform string) bool {
	switch platform {
	case "linux_amd64", "linux_arm64", "windows_amd64", "windows_arm64", "darwin_amd64", "darwin_arm64":
		return true
	}
	return false
}
func validTag(tag string) bool {
	if !tagRE.MatchString(tag) {
		return false
	}
	var major, minor, patch int
	_, err := fmt.Sscanf(tag, "v%d.%d.%d", &major, &minor, &patch)
	return err == nil && (major > 0 || minor >= 60)
}

type Asset struct {
	Name        string `json:"name"`
	URL         string `json:"browser_download_url"`
	Size        int64  `json:"size"`
	Digest      string `json:"digest"`
	Platform    string `json:"platform"`
	Compatible  bool   `json:"compatible"`
	Installable bool   `json:"installable"`
}
type Release struct {
	Tag        string  `json:"tag_name"`
	Name       string  `json:"name"`
	Published  string  `json:"published_at"`
	Prerelease bool    `json:"prerelease"`
	Draft      bool    `json:"draft"`
	Assets     []Asset `json:"assets,omitempty"`
}

func newClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 20 * time.Second
	return &http.Client{Transport: transport, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !allowedURL(r.URL) {
			return fmt.Errorf("download redirect is not an official HTTPS endpoint")
		}
		return nil
	}}
}
func allowedURL(u *url.URL) bool {
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
	u, err := url.Parse(address)
	if err != nil || !allowedURL(u) {
		return nil, fmt.Errorf("untrusted download address")
	}
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}
		req, err := http.NewRequestWithContext(ctx, "GET", address, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "KurumiFRP/0.2")
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		resp, err := m.client.Do(req)
		if err != nil {
			last = fmt.Errorf("official download connection failed; check network or HTTPS_PROXY")
			continue
		}
		if resp.StatusCode == 200 {
			return resp, nil
		}
		resp.Body.Close()
		if resp.StatusCode == 403 || resp.StatusCode == 429 {
			return nil, fmt.Errorf("GitHub rate limit/permission error (HTTP %d); retry later", resp.StatusCode)
		}
		last = fmt.Errorf("official download returned HTTP %d", resp.StatusCode)
		if resp.StatusCode < 500 {
			return nil, last
		}
	}
	return nil, last
}
func (m *Manager) json(ctx context.Context, address string, v any) error {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	resp, err := m.get(ctx, address)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("release metadata read failed")
	}
	if len(b) >= 4<<20 {
		return fmt.Errorf("release metadata too large")
	}
	return json.Unmarshal(b, v)
}
func cleanRelease(r Release) Release {
	out := []Asset{}
	for _, a := range r.Assets {
		parts := assetRE.FindStringSubmatch(a.Name)
		if parts == nil || "v"+parts[1] != r.Tag {
			continue
		}
		want := "https://github.com/fatedier/frp/releases/download/" + r.Tag + "/" + a.Name
		if a.URL != want {
			continue
		}
		a.Platform = parts[2] + "_" + parts[3]
		a.Compatible = a.Platform == Host() && supported(a.Platform)
		a.Installable = a.Compatible && digestRE.MatchString(a.Digest) && a.Size > 0 && a.Size <= MaxArchive
		out = append(out, a)
	}
	r.Assets = out
	return r
}
func (m *Manager) Releases(ctx context.Context) ([]Release, error) {
	m.mu.Lock()
	cached := append([]Release(nil), m.catalog...)
	fresh := time.Since(m.catalogAt) < 5*time.Minute
	m.mu.Unlock()
	if fresh && len(cached) > 0 {
		return cached, nil
	}
	var all []Release
	if err := m.json(ctx, API+"?per_page=20", &all); err != nil {
		return nil, err
	}
	out := []Release{}
	for _, r := range all {
		if !r.Draft && !r.Prerelease && validTag(r.Tag) {
			r.Assets = nil
			out = append(out, r)
		}
	}
	m.mu.Lock()
	m.catalog = out
	m.catalogAt = time.Now()
	m.mu.Unlock()
	return out, nil
}
func (m *Manager) Release(ctx context.Context, tag string) (Release, error) {
	if !validTag(tag) {
		return Release{}, fmt.Errorf("choose a stable frp version >= v0.60.0")
	}
	var r Release
	if err := m.json(ctx, API+"/tags/"+tag, &r); err != nil {
		return r, err
	}
	if r.Tag != tag || r.Draft || r.Prerelease {
		return r, fmt.Errorf("unexpected release metadata")
	}
	return cleanRelease(r), nil
}
func choose(r Release, platform string) (Asset, error) {
	if platform != Host() || !supported(platform) {
		return Asset{}, fmt.Errorf("install target must match this server: %s", Host())
	}
	for _, a := range r.Assets {
		if a.Platform == platform && a.Installable {
			return a, nil
		}
	}
	return Asset{}, fmt.Errorf("no compatible official archive with SHA-256 metadata")
}
func binaryName(kind, platform string) string {
	if strings.HasPrefix(platform, "windows_") {
		return kind + ".exe"
	}
	return kind
}
