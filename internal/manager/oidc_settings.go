package manager

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// A provider subject is bound only to an existing local account. Public OIDC
// clients use PKCE and do not require a client secret in this configuration.
type oidcBinding struct {
	Subject   string `json:"subject"`
	AccountID string `json:"accountId"`
}

type oidcSettings struct {
	Version  int           `json:"version"`
	Revision uint64        `json:"revision"`
	Enabled  bool          `json:"enabled"`
	Issuer   string        `json:"issuer"`
	ClientID string        `json:"clientId"`
	Bindings []oidcBinding `json:"bindings"`
}

func oidcTextOK(value string, max int) bool {
	if value == "" || len(value) > max || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func validateOIDCSettings(cfg oidcSettings) error {
	if cfg.Version != 1 || cfg.Revision == 0 || cfg.Revision > maxJSONRevision || len(cfg.Bindings) > maxAccounts {
		return fmt.Errorf("单点登录配置无效")
	}
	if cfg.Issuer == "" && cfg.ClientID == "" && len(cfg.Bindings) == 0 && !cfg.Enabled {
		return nil
	}
	u, err := url.Parse(cfg.Issuer)
	if err != nil || !validPublicHTTPSURL(cfg.Issuer) || u.RawQuery != "" || u.ForceQuery {
		return fmt.Errorf("提供方发行者须为不含查询参数的公网 HTTPS 地址")
	}
	if !oidcTextOK(cfg.ClientID, 512) {
		return fmt.Errorf("客户端 ID 无效")
	}
	if cfg.Enabled && len(cfg.Bindings) == 0 {
		return fmt.Errorf("启用前至少绑定一个现有账户")
	}
	seen := make(map[string]bool, len(cfg.Bindings))
	for _, binding := range cfg.Bindings {
		if !oidcTextOK(binding.Subject, 255) || !accountIDPattern.MatchString(binding.AccountID) || seen[binding.Subject] {
			return fmt.Errorf("账户绑定无效或提供方主体重复")
		}
		seen[binding.Subject] = true
	}
	return nil
}

func loadOIDCSettings(dir string) (oidcSettings, error) {
	defaults := oidcSettings{Version: 1, Revision: 1, Bindings: []oidcBinding{}}
	data, err := privateFileLimit(filepath.Join(dir, "oidc.json"), 64<<10)
	if os.IsNotExist(err) {
		return defaults, nil
	}
	if err != nil {
		return defaults, err
	}
	var cfg oidcSettings
	if decodeStrictJSON(data, &cfg) != nil || validateOIDCSettings(cfg) != nil {
		return defaults, fmt.Errorf("单点登录配置无法读取，原文件已保留")
	}
	return cfg, nil
}

func (a *App) oidcSettingsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		a.auth.mu.Lock()
		cfg, loadErr := a.auth.oidcSettings, a.auth.oidcLoadErr
		a.auth.mu.Unlock()
		if loadErr != nil {
			fail(w, http.StatusServiceUnavailable, fmt.Errorf("单点登录配置无法读取，原文件已保留"))
			return
		}
		reply(w, http.StatusOK, cfg)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Revision uint64        `json:"revision"`
		Enabled  bool          `json:"enabled"`
		Issuer   string        `json:"issuer"`
		ClientID string        `json:"clientId"`
		Bindings []oidcBinding `json:"bindings"`
	}
	if err := decode(w, r, &req); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	a.auth.mu.Lock()
	defer a.auth.mu.Unlock()
	if a.auth.oidcLoadErr != nil {
		fail(w, http.StatusServiceUnavailable, fmt.Errorf("单点登录配置无法读取，原文件已保留"))
		return
	}
	if req.Revision != a.auth.oidcSettings.Revision || req.Revision >= maxJSONRevision {
		fail(w, http.StatusConflict, fmt.Errorf("单点登录配置已变化，请刷新"))
		return
	}
	next := oidcSettings{Version: 1, Revision: req.Revision + 1, Enabled: req.Enabled, Issuer: req.Issuer, ClientID: req.ClientID, Bindings: append([]oidcBinding{}, req.Bindings...)}
	if err := validateOIDCSettings(next); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if a.auth.accountLoadErr != nil && len(next.Bindings) != 0 {
		fail(w, http.StatusServiceUnavailable, fmt.Errorf("账户文件不可读取，无法保存绑定"))
		return
	}
	for _, binding := range next.Bindings {
		found := false
		for _, account := range a.auth.accounts {
			if account.ID == binding.AccountID {
				found = true
				break
			}
		}
		if !found {
			fail(w, http.StatusBadRequest, fmt.Errorf("绑定引用了不存在的账户"))
			return
		}
	}
	data, err := json.Marshal(next)
	path := filepath.Join(a.store.dir, "oidc.json")
	if err == nil {
		_, err = writeIfChanged(path, data)
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, fmt.Errorf("无法保存单点登录配置"))
		return
	}
	a.auth.oidcSettings = next
	if err = syncMigrationDir(a.store.dir); err != nil {
		fail(w, http.StatusInternalServerError, fmt.Errorf("配置已写入但持久化未确认，请刷新核对"))
		return
	}
	reply(w, http.StatusOK, next)
}
