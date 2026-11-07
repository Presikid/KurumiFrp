package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Installed struct {
	ID            string            `json:"id"`
	Tag           string            `json:"tag"`
	Platform      string            `json:"platform"`
	ArchiveSHA256 string            `json:"archiveSHA256"`
	Files         map[string]string `json:"files"`
	InstalledAt   string            `json:"installedAt"`
}
type Job struct {
	State       string `json:"state"`
	Tag         string `json:"tag"`
	Bytes       int64  `json:"bytes"`
	Total       int64  `json:"total"`
	InstalledID string `json:"installedId"`
	Error       string `json:"error"`
}
type Status struct {
	Host      string      `json:"host"`
	Supported bool        `json:"supported"`
	Active    string      `json:"active"`
	Installed []Installed `json:"installed"`
	Job       Job         `json:"job"`
}
type Manager struct {
	root      string
	client    *http.Client
	mu        sync.Mutex
	job       Job
	cancel    context.CancelFunc
	done      chan struct{}
	closed    bool
	catalog   []Release
	catalogAt time.Time
}

var idRE = regexp.MustCompile(`^v[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}-(linux|windows|darwin)_(amd64|arm64)-[a-f0-9]{16}$`)

func New(root string) (*Manager, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err = privateDir(root); err != nil {
		return nil, err
	}
	if err = privateDir(filepath.Join(root, "versions")); err != nil {
		return nil, err
	}
	return &Manager{root: root, client: newClient(), job: Job{State: "idle"}}, nil
}
func privateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("unsafe core directory")
	}
	return nil
}
func readJSON(path string, v any) error {
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || st.Size() > 32768 {
		return fmt.Errorf("invalid core metadata file")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".core-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err == nil {
		err = ce
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func (m *Manager) manifest(id string) (Installed, error) {
	var v Installed
	if !idRE.MatchString(id) {
		return v, fmt.Errorf("invalid core id")
	}
	dir := filepath.Join(m.root, "versions", id)
	st, err := os.Lstat(dir)
	if err != nil {
		return v, err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return v, fmt.Errorf("unsafe version directory")
	}
	if err = readJSON(filepath.Join(dir, "manifest.json"), &v); err != nil {
		return v, err
	}
	if v.ID != id || !validTag(v.Tag) || !supported(v.Platform) || !digestRE.MatchString("sha256:"+v.ArchiveSHA256) || v.ID != v.Tag+"-"+v.Platform+"-"+v.ArchiveSHA256[:16] {
		return v, fmt.Errorf("invalid installed manifest")
	}
	return v, nil
}
func (m *Manager) active() (string, error) {
	var v struct {
		ID string `json:"id"`
	}
	err := readJSON(filepath.Join(m.root, "active.json"), &v)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !idRE.MatchString(v.ID) {
		return "", fmt.Errorf("invalid active core")
	}
	return v.ID, nil
}
func (m *Manager) Status() (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := Status{Host: Host(), Supported: supported(Host()), Installed: []Installed{}, Job: m.job}
	id, err := m.active()
	if err != nil {
		return s, err
	}
	s.Active = id
	entries, err := os.ReadDir(filepath.Join(m.root, "versions"))
	if err != nil {
		return s, err
	}
	for _, e := range entries {
		if idRE.MatchString(e.Name()) {
			v, err := m.manifest(e.Name())
			if err == nil {
				s.Installed = append(s.Installed, v)
			}
		}
	}
	return s, nil
}
func (m *Manager) Binary(kind string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if kind != "frpc" && kind != "frps" {
		return "", fmt.Errorf("invalid core kind")
	}
	id, err := m.active()
	if err != nil {
		return "", err
	}
	dir := m.root
	if id != "" {
		v, err := m.manifest(id)
		if err != nil {
			return "", err
		}
		if v.Platform != Host() {
			return "", fmt.Errorf("active core platform mismatch")
		}
		dir = filepath.Join(dir, "versions", id)
	}
	p := filepath.Join(dir, binaryName(kind, Host()))
	st, err := os.Lstat(p)
	if err != nil || !st.Mode().IsRegular() {
		return "", fmt.Errorf("请在核心管理中下载安装 frp")
	}
	return p, nil
}
func (m *Manager) Start(tag, platform string) error {
	if !validTag(tag) || platform != Host() || !supported(platform) {
		return fmt.Errorf("invalid version or incompatible host platform")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.cancel != nil {
		return fmt.Errorf("another core task is active or manager is closing")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	m.cancel = cancel
	m.done = make(chan struct{})
	m.job = Job{State: "metadata", Tag: tag}
	go func() {
		id, err := m.install(ctx, tag, platform)
		m.mu.Lock()
		defer m.mu.Unlock()
		if err != nil {
			m.job.State = "failed"
			m.job.Error = err.Error()
			if ctx.Err() != nil {
				m.job.State = "cancelled"
				m.job.Error = "download cancelled or timed out"
			}
		} else {
			m.job.State = "installed"
			m.job.InstalledID = id
		}
		cancel()
		m.cancel = nil
		close(m.done)
	}()
	return nil
}
func (m *Manager) Cancel() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		m.cancel()
	}
}
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	done := m.done
	if m.cancel != nil {
		m.cancel()
	}
	m.mu.Unlock()
	if done != nil {
		<-done
	}
}
func (m *Manager) phase(state string, total int64) {
	m.mu.Lock()
	m.job.State = state
	if total > 0 {
		m.job.Total = total
	}
	m.mu.Unlock()
}

type progress struct{ m *Manager }

func (p progress) Write(b []byte) (int, error) {
	p.m.mu.Lock()
	p.m.job.Bytes += int64(len(b))
	p.m.mu.Unlock()
	return len(b), nil
}
func (m *Manager) install(ctx context.Context, tag, platform string) (string, error) {
	release, err := m.Release(ctx, tag)
	if err != nil {
		return "", err
	}
	asset, err := choose(release, platform)
	if err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(m.root, ".download-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	m.phase("downloading", asset.Size)
	resp, err := m.get(ctx, asset.URL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	archive := filepath.Join(tmp, "archive")
	f, err := os.OpenFile(archive, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, hash, progress{m}), io.LimitReader(resp.Body, MaxArchive+1))
	ce := f.Close()
	if err == nil {
		err = ce
	}
	if err != nil {
		return "", fmt.Errorf("archive download interrupted; retry without replacing installed cores")
	}
	if n != asset.Size || n > MaxArchive || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != asset.Digest {
		return "", fmt.Errorf("archive size/SHA-256 mismatch; rejected")
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	m.phase("extracting", 0)
	dir := filepath.Join(tmp, "core")
	if err = os.Mkdir(dir, 0700); err != nil {
		return "", err
	}
	hashes, err := extract(archive, dir, asset.Name, platform, strings.TrimPrefix(tag, "v"))
	if err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	v := Installed{Tag: tag, Platform: platform, ArchiveSHA256: strings.TrimPrefix(asset.Digest, "sha256:"), Files: hashes, InstalledAt: time.Now().UTC().Format(time.RFC3339)}
	v.ID = tag + "-" + platform + "-" + v.ArchiveSHA256[:16]
	if err = writeJSON(filepath.Join(dir, "manifest.json"), v); err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	target := filepath.Join(m.root, "versions", v.ID)
	if _, err = os.Lstat(target); err == nil {
		if err = m.checkFiles(v); err != nil {
			return "", err
		}
		return v.ID, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	if err = os.Rename(dir, target); err != nil {
		return "", err
	}
	return v.ID, nil
}
func (m *Manager) checkFiles(v Installed) error {
	for _, kind := range []string{"frpc", "frps"} {
		name := binaryName(kind, v.Platform)
		p := filepath.Join(m.root, "versions", v.ID, name)
		st, err := os.Lstat(p)
		if err != nil {
			return err
		}
		if !st.Mode().IsRegular() || st.Size() > 120<<20 {
			return fmt.Errorf("unsafe installed binary")
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil {
			return err
		}
		if hex.EncodeToString(h.Sum(nil)) != v.Files[name] {
			return fmt.Errorf("installed binary checksum mismatch")
		}
	}
	return nil
}

// Activate must be called while the application's process-lifecycle lock is held.
// Downloads never replace a running binary. Switching only updates one small pointer.
func (m *Manager) Activate(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, err := m.manifest(id)
	if err != nil {
		return err
	}
	if v.Platform != Host() {
		return fmt.Errorf("core platform mismatch")
	}
	if err = m.checkFiles(v); err != nil {
		return err
	}
	for _, kind := range []string{"frpc", "frps"} {
		ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		cmd := exec.CommandContext(ctx, filepath.Join(m.root, "versions", id, binaryName(kind, Host())), "-v")
		// Do not pass GitHub tokens, proxy credentials or manager secrets to the probe.
		cmd.Env = []string{}
		for _, key := range []string{"SYSTEMROOT", "WINDIR", "TEMP", "TMP", "HOME"} {
			if value := os.Getenv(key); value != "" {
				cmd.Env = append(cmd.Env, key+"="+value)
			}
		}
		cmd.WaitDelay = time.Second
		var output limitedOutput
		cmd.Stdout = &output
		cmd.Stderr = &output
		err := cmd.Run()
		cancel()
		if err != nil || strings.TrimSpace(output.text) != strings.TrimPrefix(v.Tag, "v") {
			return fmt.Errorf("%s version probe failed; current core unchanged", kind)
		}
	}
	return writeJSON(filepath.Join(m.root, "active.json"), map[string]string{"id": id})
}

type limitedOutput struct{ text string }

func (b *limitedOutput) Write(p []byte) (int, error) {
	if len(b.text) < 4096 {
		remain := 4096 - len(b.text)
		if remain > len(p) {
			remain = len(p)
		}
		b.text += string(p[:remain])
	}
	return len(p), nil
}
