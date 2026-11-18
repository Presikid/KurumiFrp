package manager

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// PanelSettings persists the WebUI HTTP listener port and optional bind host in <dataDir>/panel_settings.json.
type PanelSettings struct {
	Port       int    `json:"port"`
	BindHost   string `json:"bindHost,omitempty"`
	Customized bool   `json:"customized,omitempty"`
	UpdatedAt  string `json:"updatedAt,omitempty"`
}

// PanelStatus exposes the currently bound WebUI listener address, port, and persisted settings to /api/state and /api/panel/settings.
type PanelStatus struct {
	CurrentHost string `json:"currentHost"`
	CurrentPort int    `json:"currentPort"`
	ListenAddr  string `json:"listenAddr"`
	BindHost    string `json:"bindHost"`
	SavedPort   int    `json:"savedPort"`
	Customized  bool   `json:"customized"`
	CanHotSwap  bool   `json:"canHotSwap"`
}

// PanelRebindFunc is registered by cmd/kurumi/main.go to pre-bind and hot-swap the live WebUI TCP listener without dropping FRP tunnels.
type PanelRebindFunc func(bindHost string, port int) (actualListen string, actualHost string, actualPort int, err error)

// PanelManager manages WebUI service port persistence, conflict validation, and live listener hot-rebinding.
type PanelManager struct {
	rebindMu     sync.Mutex
	mu           sync.RWMutex
	settingsPath string
	settings     PanelSettings
	currentHost  string
	currentPort  int
	listenAddr   string
	rebindFn     PanelRebindFunc
}

// LoadPanelSettings reads <dataDir>/panel_settings.json if present (used at CLI startup before binding the initial listener).
func LoadPanelSettings(dataDir string) PanelSettings {
	p := filepath.Join(dataDir, "panel_settings.json")
	raw, err := os.ReadFile(p)
	if err != nil {
		return PanelSettings{}
	}
	var s PanelSettings
	if json.Unmarshal(raw, &s) != nil {
		return PanelSettings{}
	}
	if s.Port < 1 || s.Port > 65535 {
		s.Port = 0
		s.Customized = false
	}
	s.BindHost = normalizePanelBindHost(s.BindHost)
	return s
}

// NewPanelManager initializes PanelManager from <dataDir>/panel_settings.json.
func NewPanelManager(dataDir string) *PanelManager {
	return &PanelManager{
		settingsPath: filepath.Join(dataDir, "panel_settings.json"),
		settings:     LoadPanelSettings(dataDir),
	}
}

func normalizePanelBindHost(raw string) string {
	h := strings.TrimSpace(raw)
	switch h {
	case "", "keep":
		return ""
	case "localhost":
		return "127.0.0.1"
	default:
		return h
	}
}

// ValidatePanelBindHost validates an explicit panel bind IP/host string.
func ValidatePanelBindHost(raw string) (string, error) {
	h := normalizePanelBindHost(raw)
	if h == "" {
		return "", nil
	}
	if ip := net.ParseIP(h); ip == nil {
		return "", fmt.Errorf("监听地址 %q 不是有效的 IPv4/IPv6 地址（常用值：127.0.0.1 或 0.0.0.0）", raw)
	}
	return h, nil
}

// SetRebindHandler registers the live TCP listener hot-rebind callback and records the active bound address.
func (pm *PanelManager) SetRebindHandler(listenAddr string, fn PanelRebindFunc) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.rebindFn = fn
	pm.updateActiveLocked(listenAddr)
}

// RecordBoundAddr updates the active listener metadata after startup or hot-rebind.
func (pm *PanelManager) RecordBoundAddr(listenAddr string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.updateActiveLocked(listenAddr)
}

func (pm *PanelManager) updateActiveLocked(listenAddr string) {
	pm.listenAddr = strings.TrimSpace(listenAddr)
	host, portStr, err := net.SplitHostPort(pm.listenAddr)
	if err == nil {
		pm.currentHost = host
		if p, pErr := strconv.Atoi(portStr); pErr == nil && p > 0 {
			pm.currentPort = p
		}
	}
}

// Status returns a thread-safe snapshot of the current WebUI listener and saved panel port configuration.
func (pm *PanelManager) Status() PanelStatus {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	savedPort := pm.settings.Port
	if savedPort <= 0 {
		savedPort = pm.currentPort
	}
	bindHost := pm.settings.BindHost
	if bindHost == "" {
		bindHost = pm.currentHost
	}
	return PanelStatus{
		CurrentHost: pm.currentHost,
		CurrentPort: pm.currentPort,
		ListenAddr:  pm.listenAddr,
		BindHost:    bindHost,
		SavedPort:   savedPort,
		Customized:  pm.settings.Customized,
		CanHotSwap:  pm.rebindFn != nil,
	}
}

// UpdatePort validates, persists, and (when rebindFn is registered) live-switches the WebUI panel service port.
func (pm *PanelManager) UpdatePort(newPort int, rawBindHost string, store *Store, rt *RuntimeSupervisor) (PanelStatus, bool, error) {
	pm.rebindMu.Lock()
	defer pm.rebindMu.Unlock()

	if newPort < 1 || newPort > 65535 {
		return pm.Status(), false, errors.New("面板服务端口需在 1 ~ 65535 之间")
	}
	cleanHost, err := ValidatePanelBindHost(rawBindHost)
	if err != nil {
		return pm.Status(), false, err
	}

	// Prevent self-collision with local frps ports or internal loopback agent port
	if rt != nil && rt.AgentPort() > 0 && newPort == rt.AgentPort() {
		return pm.Status(), false, fmt.Errorf("端口 %d 正被 KurumiFrp 内部远控桥接通道占用，请更换其他端口", newPort)
	}
	if store != nil {
		_, doc := store.Snapshot()
		for _, s := range doc.Servers {
			if s.BindPort == newPort || s.KCPBindPort == newPort || s.QUICBindPort == newPort || s.VhostHTTPPort == newPort || s.VhostHTTPSPort == newPort || s.TCPMuxHTTPConnectPort == newPort || s.SSHGatewayBindPort == newPort {
				return pm.Status(), false, fmt.Errorf("端口 %d 与当前方案中的 FRPS 实例 %q 配置端口冲突，请选用其他端口", newPort, s.Name)
			}
		}
	}

	pm.mu.Lock()
	curHost := pm.currentHost
	curPort := pm.currentPort
	rebind := pm.rebindFn
	pm.mu.Unlock()

	targetHost := cleanHost
	if targetHost == "" {
		targetHost = curHost
		if targetHost == "" {
			targetHost = "127.0.0.1"
		}
	}

	changed := (targetHost != curHost) || (newPort != curPort)
	actualListen := net.JoinHostPort(targetHost, strconv.Itoa(newPort))
	actualHost := targetHost
	actualPort := newPort

	if rebind != nil && changed {
		var rbErr error
		actualListen, actualHost, actualPort, rbErr = rebind(targetHost, newPort)
		if rbErr != nil {
			return pm.Status(), false, fmt.Errorf("切换至 %s:%d 失败（原端口 %d 仍保持正常运行）: %w", targetHost, newPort, curPort, rbErr)
		}
	}

	pm.mu.Lock()
	nextSettings := PanelSettings{
		Port:       actualPort,
		BindHost:   actualHost,
		Customized: true,
		UpdatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if rebind != nil {
		pm.listenAddr = actualListen
		pm.currentHost = actualHost
		pm.currentPort = actualPort
	}
	b, _ := json.MarshalIndent(nextSettings, "", "  ")
	_, saveErr := writeIfChanged(pm.settingsPath, append(b, '\n'), 0o600)
	if saveErr == nil {
		pm.settings = nextSettings
	}
	pm.mu.Unlock()

	if saveErr != nil {
		return pm.Status(), changed, fmt.Errorf("端口已生效但写入配置文件失败: %w", saveErr)
	}
	return pm.Status(), changed, nil
}
