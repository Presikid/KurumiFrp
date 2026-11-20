package manager

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const maxConfigBytes = 32 << 20 // 32 MiB hard safety cap per config document

var validConfigSlug = regexp.MustCompile(`^[a-zA-Z0-9_\-\p{Han}]{1,64}$`)

type activeMetaFile struct {
	ActiveFile string `json:"activeFile"`
}

// Store manages self-contained configuration files inside <dataDir>/configs/*.json.
type Store struct {
	mu         sync.RWMutex
	dataDir    string
	configsDir string
	metaPath   string
	activeFile string
	doc        ConfigDocument
}

// NewStore initializes the self-contained config directory inside the software data folder.
func NewStore(dataDir string) (*Store, error) {
	configsDir := filepath.Join(dataDir, "configs")
	if err := os.MkdirAll(configsDir, 0o700); err != nil {
		return nil, fmt.Errorf("create configs dir: %w", err)
	}
	s := &Store{
		dataDir:    dataDir,
		configsDir: configsDir,
		metaPath:   filepath.Join(dataDir, "active_config.json"),
	}
	if err := s.initOrLoad(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) initOrLoad() error {
	var meta activeMetaFile
	if raw, err := os.ReadFile(s.metaPath); err == nil {
		_ = json.Unmarshal(raw, &meta)
	}
	target := sanitizeConfigFileName(meta.ActiveFile)
	if target == "" {
		target = "default.json"
	}
	fullPath := filepath.Join(s.configsDir, target)
	if _, err := os.Stat(fullPath); errors.Is(err, os.ErrNotExist) {
		entries, _ := os.ReadDir(s.configsDir)
		var existing string
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
				existing = e.Name()
				break
			}
		}
		if existing != "" {
			target = existing
			fullPath = filepath.Join(s.configsDir, target)
		} else {
			doc := defaultConfigDocument()
			normalizeDocument(&doc, doc.Name)
			s.activeFile = "default.json"
			s.doc = doc
			if err := s.saveActiveLocked(); err != nil {
				return err
			}
			return s.saveMetaLocked()
		}
	}
	doc, err := readConfigDocument(fullPath)
	if err != nil {
		return err
	}
	s.activeFile = target
	s.doc = doc
	_ = s.saveActiveLocked()
	return s.saveMetaLocked()
}

func defaultConfigDocument() ConfigDocument {
	return ConfigDocument{
		Version: 1,
		Name:    "默认配置方案",
		Updated: time.Now().UTC().Format(time.RFC3339),
		Servers: []ServerProfile{},
		Clients: []ClientProfile{},
	}
}

func readConfigDocument(path string) (ConfigDocument, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ConfigDocument{}, err
	}
	if len(raw) > maxConfigBytes {
		return ConfigDocument{}, errors.New("配置文件超过 32 MiB 安全上限")
	}
	var doc ConfigDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return ConfigDocument{}, fmt.Errorf("解析配置文件 %s 失败: %w", filepath.Base(path), err)
	}
	normalizeDocument(&doc, strings.TrimSuffix(filepath.Base(path), ".json"))
	return doc, nil
}

func normalizeDocument(doc *ConfigDocument, fallbackName string) {
	if doc.Version <= 0 {
		doc.Version = 1
	}
	if strings.TrimSpace(doc.Name) == "" {
		doc.Name = fallbackName
	}
	if doc.Servers == nil {
		doc.Servers = []ServerProfile{}
	}
	for i := range doc.Servers {
		if doc.Servers[i].ID == "" {
			doc.Servers[i].ID = newShortID("srv")
		}
		if doc.Servers[i].BindAddr == "" {
			doc.Servers[i].BindAddr = "0.0.0.0"
		}
		if doc.Servers[i].BindPort <= 0 {
			doc.Servers[i].BindPort = 7000
		}
		if doc.Servers[i].ConnPolicies == nil {
			doc.Servers[i].ConnPolicies = make(map[string]ConnPolicy)
		}
	}
	if doc.Clients == nil {
		doc.Clients = []ClientProfile{}
	}
	for i := range doc.Clients {
		if doc.Clients[i].ID == "" {
			doc.Clients[i].ID = newShortID("cli")
		}
		if doc.Clients[i].ServerPort <= 0 {
			doc.Clients[i].ServerPort = 7000
		}
		if doc.Clients[i].Protocol == "" {
			doc.Clients[i].Protocol = "tcp"
		}
		if strings.TrimSpace(doc.Clients[i].CtrlSecret) == "" {
			doc.Clients[i].CtrlSecret = randomTokenHex(16)
		}
		if strings.TrimSpace(doc.Clients[i].AgentToken) == "" {
			doc.Clients[i].AgentToken = randomTokenHex(20)
		}
		if doc.Clients[i].Tunnels == nil {
			doc.Clients[i].Tunnels = []TunnelRule{}
		}
		for j := range doc.Clients[i].Tunnels {
			if doc.Clients[i].Tunnels[j].ID == "" {
				doc.Clients[i].Tunnels[j].ID = newShortID("tun")
			}
			if doc.Clients[i].Tunnels[j].Mode == "" {
				doc.Clients[i].Tunnels[j].Mode = "proxy"
			}
			if doc.Clients[i].Tunnels[j].Type == "" {
				doc.Clients[i].Tunnels[j].Type = "tcp"
			}
		}
	}
}

// Snapshot returns a deep copy of the active ConfigDocument and its filename.
func (s *Store) Snapshot() (string, ConfigDocument) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	raw, _ := json.Marshal(s.doc)
	var cp ConfigDocument
	_ = json.Unmarshal(raw, &cp)
	normalizeDocument(&cp, s.doc.Name)
	return s.activeFile, cp
}

// Update applies a mutation function to the active ConfigDocument and writes to disk only if changed.
func (s *Store) Update(fn func(doc *ConfigDocument) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	raw, _ := json.Marshal(s.doc)
	var draft ConfigDocument
	_ = json.Unmarshal(raw, &draft)
	normalizeDocument(&draft, s.doc.Name)

	if err := fn(&draft); err != nil {
		return err
	}
	normalizeDocument(&draft, draft.Name)
	draft.Updated = time.Now().UTC().Format(time.RFC3339)

	// Prepare-then-Commit: validate size limit and atomically persist to disk BEFORE mutating in-memory s.doc!
	b, err := json.MarshalIndent(draft, "", "  ")
	if err != nil {
		return err
	}
	if len(b) > maxConfigBytes {
		return errors.New("配置内容超过 32 MiB 上限")
	}
	full := filepath.Join(s.configsDir, s.activeFile)
	if _, err := writeIfChanged(full, append(b, '\n'), 0o600); err != nil {
		return err
	}
	s.doc = draft
	return nil
}

// ListConfigFiles lists all self-contained config files inside <dataDir>/configs/.
func (s *Store) ListConfigFiles() ([]ConfigFileSummary, error) {
	s.mu.RLock()
	active := s.activeFile
	s.mu.RUnlock()

	entries, err := os.ReadDir(s.configsDir)
	if err != nil {
		return nil, err
	}
	var out []ConfigFileSummary
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
			continue
		}
		full := filepath.Join(s.configsDir, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		doc, err := readConfigDocument(full)
		if err != nil {
			continue
		}
		out = append(out, ConfigFileSummary{
			FileName:    e.Name(),
			DisplayName: doc.Name,
			Active:      sameConfigFileName(e.Name(), active),
			ServerCount: len(doc.Servers),
			ClientCount: len(doc.Clients),
			Updated:     doc.Updated,
			SizeBytes:   info.Size(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Active != out[j].Active {
			return out[i].Active
		}
		return out[i].FileName < out[j].FileName
	})
	return out, nil
}

// SelectOrCreateConfig switches to an existing config file in data/configs/ or creates a new empty one.
func (s *Store) SelectOrCreateConfig(fileName, displayName string) (ConfigDocument, error) {
	clean := sanitizeConfigFileName(fileName)
	if clean == "" {
		return ConfigDocument{}, errors.New("配置文件名不合法（仅允许字母、数字、中文、下划线与连字符）")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	full := filepath.Join(s.configsDir, clean)
	var doc ConfigDocument
	if _, err := os.Stat(full); err == nil {
		loaded, err := readConfigDocument(full)
		if err != nil {
			return ConfigDocument{}, err
		}
		doc = loaded
	} else {
		name := strings.TrimSpace(displayName)
		if name == "" {
			name = strings.TrimSuffix(clean, ".json")
		}
		doc = ConfigDocument{
			Version: 1,
			Name:    name,
			Updated: time.Now().UTC().Format(time.RFC3339),
			Servers: []ServerProfile{},
			Clients: []ClientProfile{},
		}
		b, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return ConfigDocument{}, err
		}
		if _, err := writeIfChanged(full, append(b, '\n'), 0o600); err != nil {
			return ConfigDocument{}, err
		}
	}
	s.activeFile = clean
	s.doc = doc
	if err := s.saveMetaLocked(); err != nil {
		return ConfigDocument{}, err
	}
	return doc, nil
}

// ImportConfigDocument saves an uploaded ConfigDocument JSON into data/configs/ and activates it.
func (s *Store) ImportConfigDocument(fileName string, raw []byte, activate bool) (string, error) {
	if len(raw) > maxConfigBytes {
		return "", errors.New("导入文件超过 32 MiB 上限")
	}
	clean := sanitizeConfigFileName(fileName)
	if clean == "" {
		clean = "imported-" + time.Now().Format("20060102-150405") + ".json"
	}
	var doc ConfigDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", fmt.Errorf("无效的 JSON 配置文件: %w", err)
	}
	normalizeDocument(&doc, strings.TrimSuffix(clean, ".json"))
	doc.Updated = time.Now().UTC().Format(time.RFC3339)

	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if !activate && sameConfigFileName(clean, s.activeFile) {
		return "", errors.New("不能在不激活状态下覆盖当前正在使用的配置文件，请勾选立即启用或更改导入文件名")
	}
	full := filepath.Join(s.configsDir, clean)
	if _, err := writeIfChanged(full, append(b, '\n'), 0o600); err != nil {
		return "", err
	}
	if activate {
		s.activeFile = clean
		s.doc = doc
		if err := s.saveMetaLocked(); err != nil {
			return "", err
		}
	}
	return clean, nil
}

// DeleteConfigFile deletes a non-active configuration file from data/configs/.
func (s *Store) DeleteConfigFile(fileName string) error {
	clean := sanitizeConfigFileName(fileName)
	if clean == "" {
		return errors.New("无效的配置文件名")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if sameConfigFileName(clean, s.activeFile) {
		return errors.New("不能删除当前正在使用的配置文件，请先切换到其他配置文件")
	}
	full := filepath.Join(s.configsDir, clean)
	if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Store) saveActiveLocked() error {
	b, err := json.MarshalIndent(s.doc, "", "  ")
	if err != nil {
		return err
	}
	if len(b) > maxConfigBytes {
		return errors.New("配置内容超过 32 MiB 上限")
	}
	full := filepath.Join(s.configsDir, s.activeFile)
	_, err = writeIfChanged(full, append(b, '\n'), 0o600)
	return err
}

func (s *Store) saveMetaLocked() error {
	b, err := json.MarshalIndent(activeMetaFile{ActiveFile: s.activeFile}, "", "  ")
	if err != nil {
		return err
	}
	_, err = writeIfChanged(s.metaPath, append(b, '\n'), 0o600)
	return err
}

func sanitizeConfigFileName(name string) string {
	name = strings.TrimSpace(filepath.Base(name))
	name = strings.TrimSuffix(name, ".json")
	name = strings.TrimSuffix(name, ".JSON")
	if !validConfigSlug.MatchString(name) {
		return ""
	}
	return name + ".json"
}

func sameConfigFileName(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// writeIfChanged skips disk writes if identical, and otherwise performs an atomic temp-file + Sync + Rename commit.
func writeIfChanged(path string, data []byte, perm os.FileMode) (bool, error) {
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, data) {
		return false, nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, err
	}
	tmpFile, err := os.CreateTemp(dir, ".kurumi-tmp-*")
	if err != nil {
		return false, err
	}
	tmpPath := tmpFile.Name()
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return false, err
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return false, err
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return false, err
	}
	_ = os.Chmod(tmpPath, perm)
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return false, err
	}
	return true, nil
}

func newShortID(prefix string) string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return prefix + "-" + hex.EncodeToString(b[:])
}

func randomTokenHex(nBytes int) string {
	b := make([]byte, nBytes)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func nonEmptyID(v, prefix string) string {
	if strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return newShortID(prefix)
}

func nonEmptyStr(v, fallback string) string {
	if strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return fallback
}
