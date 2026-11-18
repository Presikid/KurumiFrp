package manager

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// legacyDatabase supports automatic one-time import of existing v0.7/v0.8 state.json tunnels & instances.
type legacyDatabase struct {
	State *struct {
		Instances []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			Kind          string `json:"kind"`
			Host          string `json:"host"`
			Port          int    `json:"port"`
			HTTPPort      int    `json:"httpPort"`
			HTTPSPort     int    `json:"httpsPort"`
			Token         string `json:"token"`
			AutoStart     bool   `json:"autoStart"`
			User          string `json:"user"`
			KCPBindPort   int    `json:"kcpBindPort"`
			QUICBindPort  int    `json:"quicBindPort"`
			SubDomainHost string `json:"subDomainHost"`
		} `json:"instances"`
		Tunnels []struct {
			ID         string   `json:"id"`
			InstanceID string   `json:"instanceId"`
			Name       string   `json:"name"`
			Type       string   `json:"type"`
			Enabled    bool     `json:"enabled"`
			LocalHost  string   `json:"localHost"`
			LocalPort  int      `json:"localPort"`
			RemotePort int      `json:"remotePort"`
			Domains    []string `json:"domains"`
			Subdomain  string   `json:"subdomain"`
		} `json:"tunnels"`
	} `json:"state"`
}

// Store manages self-contained configuration files inside <dataDir>/configs/*.json.
type Store struct {
	mu         sync.RWMutex
	dataDir    string
	configsDir string
	metaPath   string
	activeFile string
	generation uint64
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
		// Keep open browser tabs distinguishable after a restart. Microseconds
		// fit exactly in the JSON numbers used by the WebUI.
		generation: uint64(time.Now().UnixMicro()),
	}
	if err := s.initOrLoad(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) initOrLoad() error {
	var meta activeMetaFile
	if raw, err := os.ReadFile(s.metaPath); err == nil {
		if err := json.Unmarshal(raw, &meta); err != nil {
			return fmt.Errorf("解析 active_config.json 失败: %w", err)
		}
		if sanitizeConfigFileName(meta.ActiveFile) == "" {
			return errors.New("active_config.json 缺少有效的活动配置文件名")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	target := sanitizeConfigFileName(meta.ActiveFile)
	if target == "" {
		target = "default.json"
	}
	fullPath := filepath.Join(s.configsDir, target)
	if _, err := os.Stat(fullPath); errors.Is(err, os.ErrNotExist) {
		entries, err := os.ReadDir(s.configsDir)
		if err != nil {
			return err
		}
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
			doc, err := s.tryMigrateLegacyOrDefault()
			if err != nil {
				return err
			}
			normalizeDocument(&doc, doc.Name)
			s.activeFile = "default.json"
			s.doc = doc
			if err := s.saveActiveLocked(); err != nil {
				return err
			}
			return s.saveMetaLocked(s.activeFile)
		}
	}
	doc, err := readConfigDocument(fullPath)
	if err != nil {
		return err
	}
	s.activeFile = target
	s.doc = doc
	if err := s.saveActiveLocked(); err != nil {
		return err
	}
	return s.saveMetaLocked(s.activeFile)
}

func (s *Store) tryMigrateLegacyOrDefault() (ConfigDocument, error) {
	doc := ConfigDocument{
		Version: 1,
		Name:    "默认配置方案",
		Updated: time.Now().UTC().Format(time.RFC3339),
		Servers: []ServerProfile{},
		Clients: []ClientProfile{},
	}
	legacyPath := filepath.Join(s.dataDir, "state.json")
	raw, err := os.ReadFile(legacyPath)
	if errors.Is(err, os.ErrNotExist) {
		return doc, nil
	}
	if err != nil {
		return ConfigDocument{}, err
	}
	var leg legacyDatabase
	if err := json.Unmarshal(raw, &leg); err != nil {
		return ConfigDocument{}, fmt.Errorf("迁移 state.json 失败: %w", err)
	}
	if leg.State == nil {
		return ConfigDocument{}, errors.New("迁移 state.json 失败：缺少 state 字段")
	}
	clientIndex := make(map[string]int)
	for _, inst := range leg.State.Instances {
		if inst.Kind == "frps" {
			addr := inst.Host
			if addr == "" {
				addr = "0.0.0.0"
			}
			port := inst.Port
			if port <= 0 {
				port = 7000
			}
			doc.Servers = append(doc.Servers, ServerProfile{
				ID:             nonEmptyID(inst.ID, "srv"),
				Name:           nonEmptyStr(inst.Name, "服务端配置"),
				AutoStart:      inst.AutoStart,
				BindAddr:       addr,
				BindPort:       port,
				VhostHTTPPort:  inst.HTTPPort,
				VhostHTTPSPort: inst.HTTPSPort,
				KCPBindPort:    inst.KCPBindPort,
				QUICBindPort:   inst.QUICBindPort,
				SubDomainHost:  inst.SubDomainHost,
				AuthToken:      inst.Token,
				ConnPolicies:   make(map[string]ConnPolicy),
			})
		} else {
			port := inst.Port
			if port <= 0 {
				port = 7000
			}
			cp := ClientProfile{
				ID:         nonEmptyID(inst.ID, "cli"),
				Name:       nonEmptyStr(inst.Name, "客户端配置"),
				AutoStart:  inst.AutoStart,
				ServerAddr: nonEmptyStr(inst.Host, "127.0.0.1"),
				ServerPort: port,
				User:       inst.User,
				AuthToken:  inst.Token,
				Protocol:   "tcp",
				Tunnels:    []TunnelRule{},
			}
			doc.Clients = append(doc.Clients, cp)
			clientIndex[inst.ID] = len(doc.Clients) - 1
		}
	}
	for _, t := range leg.State.Tunnels {
		idx, ok := clientIndex[t.InstanceID]
		if !ok {
			continue
		}
		cp := &doc.Clients[idx]
		cp.Tunnels = append(cp.Tunnels, TunnelRule{
			ID:            nonEmptyID(t.ID, "tun"),
			Name:          nonEmptyStr(t.Name, "rule"),
			Mode:          "proxy",
			Type:          nonEmptyStr(t.Type, "tcp"),
			Enabled:       t.Enabled,
			LocalIP:       nonEmptyStr(t.LocalHost, "127.0.0.1"),
			LocalPort:     t.LocalPort,
			RemotePort:    t.RemotePort,
			CustomDomains: t.Domains,
			SubDomain:     t.Subdomain,
		})
	}
	return doc, nil
}

func readConfigDocument(path string) (ConfigDocument, error) {
	f, err := os.Open(path)
	if err != nil {
		return ConfigDocument{}, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxConfigBytes+1))
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
	name, doc, _ := s.snapshotWithGeneration()
	return name, doc
}

func (s *Store) snapshotWithGeneration() (string, ConfigDocument, uint64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.activeFile, cloneConfigDocument(s.doc), s.generation
}

func cloneConfigDocument(doc ConfigDocument) ConfigDocument {
	raw, _ := json.Marshal(doc)
	var cp ConfigDocument
	_ = json.Unmarshal(raw, &cp)
	normalizeDocument(&cp, doc.Name)
	return cp
}

// Update applies a mutation function to the active ConfigDocument and writes to disk only if changed.
func (s *Store) Update(fn func(doc *ConfigDocument) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.updateLocked(fn)
}

func (s *Store) updateLocked(fn func(doc *ConfigDocument) error) error {
	draft := cloneConfigDocument(s.doc)

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
	if len(b)+1 > maxConfigBytes {
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
	return s.listConfigFiles(active)
}

func (s *Store) listConfigFiles(active string) ([]ConfigFileSummary, error) {
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
	} else if !errors.Is(err, os.ErrNotExist) {
		return ConfigDocument{}, err
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
	}
	// Persist IDs and control credentials generated when opening an older config.
	if err := s.commitDocumentLocked(clean, doc, true); err != nil {
		return ConfigDocument{}, err
	}
	return cloneConfigDocument(doc), nil
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

	s.mu.Lock()
	defer s.mu.Unlock()

	if !activate && sameConfigFileName(clean, s.activeFile) {
		return "", errors.New("不能在不激活状态下覆盖当前正在使用的配置文件，请勾选立即启用或更改导入文件名")
	}
	if err := s.commitDocumentLocked(clean, doc, activate); err != nil {
		return "", err
	}
	return clean, nil
}

// If activating a different document fails, restore the previous version of the
// target as well. The active filename and document change together under s.mu.
func (s *Store) commitDocumentLocked(name string, doc ConfigDocument, activate bool) error {
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if len(raw) > maxConfigBytes {
		return errors.New("配置内容超过 32 MiB 上限")
	}
	target := filepath.Join(s.configsDir, name)
	same := sameConfigFileName(s.activeFile, name)
	var previous []byte
	var existed bool
	if activate && !same {
		previous, err = os.ReadFile(target)
		existed = err == nil
		if !existed && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if _, err = writeIfChanged(target, raw, 0600); err != nil {
		return err
	}
	if !activate {
		return nil
	}
	if err = s.activateLocked(name, doc); err != nil {
		var undo error
		if existed {
			_, undo = writeIfChanged(target, previous, 0600)
		} else {
			undo = os.Remove(target)
		}
		return errors.Join(err, undo)
	}
	return nil
}

func (s *Store) activateLocked(name string, doc ConfigDocument) error {
	if !sameConfigFileName(s.activeFile, name) {
		if err := s.saveMetaLocked(name); err != nil {
			return err
		}
	}
	s.activeFile = name
	s.doc = doc
	s.generation++
	return nil
}

func (s *Store) activeGeneration() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.generation
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
	if len(b)+1 > maxConfigBytes {
		return errors.New("配置内容超过 32 MiB 上限")
	}
	full := filepath.Join(s.configsDir, s.activeFile)
	_, err = writeIfChanged(full, append(b, '\n'), 0o600)
	return err
}

func (s *Store) saveMetaLocked(file string) error {
	b, err := json.MarshalIndent(activeMetaFile{ActiveFile: file}, "", "  ")
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
	tmpPath := fmt.Sprintf("%s.tmp.%s", path, randomTokenHex(4))
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return false, err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return false, err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return false, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return false, err
	}
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
