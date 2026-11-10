package manager

import (
	"fmt"
	"net/http"
)

const maxWebhookTargets = 8
const legacyWebhookID = "legacy"
const legacyWebhookName = "原有通知"

// The original singular field remains separate for compatibility. Additional
// targets are local authority and must never enter portable configuration.
type webhookTarget struct {
	ID     string        `json:"id"`
	Name   string        `json:"name"`
	Config webhookConfig `json:"config"`
}

func validateStoredWebhookTargets(s *Store, legacy *webhookConfig, targets []webhookTarget) error {
	count := len(targets)
	if legacy != nil {
		count++
	}
	if count > maxWebhookTargets {
		return fmt.Errorf("too many webhook targets")
	}
	ids := make(map[string]bool, len(targets))
	for _, target := range targets {
		if !idPattern.MatchString(target.ID) || ids[target.ID] || !labelOK(target.Name) {
			return fmt.Errorf("invalid webhook target")
		}
		if err := validateStoredWebhook(s, &target.Config); err != nil {
			return err
		}
		ids[target.ID] = true
	}
	return nil
}

func rewrapWebhookConfig(oldStore, newStore *Store, cfg webhookConfig) (webhookConfig, error) {
	if err := validateStoredWebhook(oldStore, &cfg); err != nil {
		return webhookConfig{}, err
	}
	for _, field := range []*string{&cfg.URL, &cfg.SigningSecret} {
		if *field == "" {
			continue
		}
		plain, err := oldStore.decrypt(*field)
		if err != nil {
			return webhookConfig{}, err
		}
		*field, err = newStore.encrypt(plain)
		if err != nil {
			return webhookConfig{}, err
		}
	}
	if cfg.JSONTemplate != "" {
		plain, err := oldStore.decrypt(cfg.JSONTemplate)
		if err != nil {
			return webhookConfig{}, err
		}
		cfg.JSONTemplate, err = newStore.encryptValue(plain)
		if err != nil {
			return webhookConfig{}, err
		}
	}
	return cfg, nil
}

func rewrapWebhookTargets(oldStore, newStore *Store, legacy *webhookConfig, targets []webhookTarget) ([]webhookTarget, error) {
	if err := validateStoredWebhookTargets(oldStore, legacy, targets); err != nil {
		return nil, err
	}
	next := append([]webhookTarget(nil), targets...)
	for i := range next {
		var err error
		next[i].Config, err = rewrapWebhookConfig(oldStore, newStore, next[i].Config)
		if err != nil {
			return nil, err
		}
	}
	return next, nil
}

// a.mu is held. Omitted secrets preserve the existing values; all errors are
// generic so neither destination nor secret can appear in an API response.
func (a *App) prepareWebhookConfig(previous webhookConfig, req webhookRequest) (webhookConfig, error) {
	if req.ClearURL && req.URL != "" {
		return webhookConfig{}, fmt.Errorf("不能同时清除和设置通知地址")
	}
	if req.ClearSigningSecret && req.SigningSecret != "" {
		return webhookConfig{}, fmt.Errorf("不能同时清除和设置签名密钥")
	}
	if req.ClearJSONTemplate && req.JSONTemplate != "" {
		return webhookConfig{}, fmt.Errorf("不能同时清除和设置 JSON 模板")
	}
	cfg := webhookConfig{
		Enabled: req.Enabled, URL: previous.URL, Format: previous.Format,
		OnStatus: req.OnStatus, OnConfig: req.OnConfig, OnLoginLimit: previous.OnLoginLimit, OnUpdate: previous.OnUpdate,
		SigningSecret: previous.SigningSecret, JSONTemplate: previous.JSONTemplate, JSONTemplateEnabled: previous.JSONTemplateEnabled,
	}
	// Older clients may omit this field; keep the prior format in that case.
	if req.Format != nil {
		cfg.Format = *req.Format
	}
	// An older client cannot clear event choices it does not know about.
	if req.OnLoginLimit != nil {
		cfg.OnLoginLimit = *req.OnLoginLimit
	}
	if req.OnUpdate != nil {
		cfg.OnUpdate = *req.OnUpdate
	}
	if req.JSONTemplateEnabled != nil {
		cfg.JSONTemplateEnabled = *req.JSONTemplateEnabled
	}
	if !validWebhookFormat(cfg.Format) {
		return webhookConfig{}, fmt.Errorf("通知格式无效")
	}
	if req.ClearURL {
		cfg.URL = ""
	} else if req.URL != "" {
		if !validPublicHTTPSURL(req.URL) {
			return webhookConfig{}, fmt.Errorf("通知地址须为有效的公网 HTTPS 地址，不允许用户信息或片段")
		}
		existing, _ := a.store.decrypt(cfg.URL)
		if req.URL != existing {
			var err error
			cfg.URL, err = a.store.encrypt(req.URL)
			if err != nil {
				return webhookConfig{}, fmt.Errorf("通知地址无法保存")
			}
		}
	}
	if req.ClearSigningSecret {
		cfg.SigningSecret = ""
	} else if req.SigningSecret != "" {
		if !validWebhookSigningSecret(req.SigningSecret) {
			return webhookConfig{}, fmt.Errorf("签名密钥须为 8–1024 字节的有效 UTF-8 文本，且不能包含 ASCII 控制字符")
		}
		existing, _ := a.store.decrypt(cfg.SigningSecret)
		if req.SigningSecret != existing {
			var err error
			cfg.SigningSecret, err = a.store.encrypt(req.SigningSecret)
			if err != nil {
				return webhookConfig{}, fmt.Errorf("签名密钥无法保存")
			}
		}
	}
	if cfg.SigningSecret != "" {
		plain, err := a.store.decrypt(cfg.SigningSecret)
		if err != nil || !validWebhookSigningSecret(plain) {
			return webhookConfig{}, fmt.Errorf("签名密钥无效，请重新设置或清除")
		}
	}
	if req.ClearJSONTemplate {
		cfg.JSONTemplate = ""
	} else if req.JSONTemplate != "" {
		if _, err := parseWebhookJSONTemplate(req.JSONTemplate); err != nil {
			return webhookConfig{}, err
		}
		existing, _ := a.store.decrypt(cfg.JSONTemplate)
		if req.JSONTemplate != existing {
			var err error
			cfg.JSONTemplate, err = a.store.encryptValue(req.JSONTemplate)
			if err != nil {
				return webhookConfig{}, fmt.Errorf("JSON 模板无法保存")
			}
		}
	}
	if cfg.JSONTemplate != "" {
		plain, err := a.store.decrypt(cfg.JSONTemplate)
		if err != nil {
			return webhookConfig{}, fmt.Errorf("JSON 模板无效，请重新设置或清除")
		}
		if _, err = parseWebhookJSONTemplate(plain); err != nil {
			return webhookConfig{}, fmt.Errorf("JSON 模板无效，请重新设置或清除")
		}
	}
	if cfg.JSONTemplateEnabled && (cfg.Format != "" || cfg.JSONTemplate == "") {
		return webhookConfig{}, fmt.Errorf("启用 JSON 模板需要已保存的模板和通用 JSON 格式")
	}
	if cfg.URL != "" {
		plain, err := a.store.decrypt(cfg.URL)
		if err != nil || !validWebhookDestination(cfg.Format, plain) {
			if cfg.Format == "telegram" {
				return webhookConfig{}, fmt.Errorf("Telegram 地址须为完整的 sendMessage 地址，并带一个有效的 chat_id")
			}
			return webhookConfig{}, fmt.Errorf("通知地址无效，请重新设置")
		}
	}
	if cfg.Enabled && (cfg.URL == "" || !cfg.hasEvents()) {
		return webhookConfig{}, fmt.Errorf("启用时需要有效地址，并至少选择一种事件")
	}
	return cfg, nil
}

type webhookTargetRequest struct {
	Revision            uint64  `json:"revision"`
	Action              string  `json:"action"`
	ID                  string  `json:"id"`
	Name                string  `json:"name"`
	Enabled             *bool   `json:"enabled"`
	URL                 string  `json:"url"`
	Format              *string `json:"format"`
	ClearURL            bool    `json:"clearURL"`
	OnStatus            *bool   `json:"onStatus"`
	OnConfig            *bool   `json:"onConfig"`
	OnLoginLimit        *bool   `json:"onLoginLimit"`
	OnUpdate            *bool   `json:"onUpdate"`
	SigningSecret       string  `json:"signingSecret"`
	ClearSigningSecret  bool    `json:"clearSigningSecret"`
	JSONTemplate        string  `json:"jsonTemplate"`
	ClearJSONTemplate   bool    `json:"clearJSONTemplate"`
	JSONTemplateEnabled *bool   `json:"jsonTemplateEnabled"`
}

func (a *App) webhookTargetsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		a.mu.Lock()
		type view struct {
			ID                  string         `json:"id"`
			Name                string         `json:"name"`
			Enabled             bool           `json:"enabled"`
			Format              string         `json:"format"`
			HasURL              bool           `json:"hasURL"`
			HasSigningSecret    bool           `json:"hasSigningSecret"`
			OnStatus            bool           `json:"onStatus"`
			OnConfig            bool           `json:"onConfig"`
			OnLoginLimit        bool           `json:"onLoginLimit"`
			OnUpdate            bool           `json:"onUpdate"`
			HasJSONTemplate     bool           `json:"hasJSONTemplate"`
			JSONTemplateEnabled bool           `json:"jsonTemplateEnabled"`
			Status              map[string]any `json:"status"`
		}
		items := make([]view, 0, len(a.store.DB.Webhooks)+1)
		add := func(id, name string, cfg webhookConfig) {
			items = append(items, view{
				ID: id, Name: name, Enabled: cfg.Enabled, Format: cfg.Format,
				HasURL: cfg.URL != "", HasSigningSecret: cfg.SigningSecret != "",
				OnStatus: cfg.OnStatus, OnConfig: cfg.OnConfig, OnLoginLimit: cfg.OnLoginLimit, OnUpdate: cfg.OnUpdate,
				HasJSONTemplate: cfg.JSONTemplate != "", JSONTemplateEnabled: cfg.JSONTemplateEnabled, Status: a.webhooks.statusFor(id),
			})
		}
		if a.store.DB.Webhook != nil {
			add(legacyWebhookID, legacyWebhookName, *a.store.DB.Webhook)
		}
		for _, target := range a.store.DB.Webhooks {
			add(target.ID, target.Name, target.Config)
		}
		revision := a.store.DB.Revision
		a.mu.Unlock()
		reply(w, http.StatusOK, map[string]any{"revision": revision, "targets": items, "limit": maxWebhookTargets})
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req webhookTargetRequest
	if err := decode(w, r, &req); err != nil {
		fail(w, http.StatusBadRequest, fmt.Errorf("通知设置请求格式无效"))
		return
	}
	revision, code, err := a.mutateWebhookTarget(req)
	if err != nil {
		fail(w, code, err)
		return
	}
	reply(w, http.StatusOK, map[string]any{"ok": true, "revision": revision})
}

func (a *App) mutateWebhookTarget(req webhookTargetRequest) (uint64, int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closing {
		return 0, http.StatusServiceUnavailable, fmt.Errorf("工作台正在关闭")
	}
	if req.Revision != a.store.DB.Revision {
		return 0, http.StatusConflict, fmt.Errorf("配置已变化，请刷新后重试")
	}
	if req.Action != "save" && req.Action != "delete" && req.Action != "enable" && req.Action != "disable" {
		return 0, http.StatusBadRequest, fmt.Errorf("未知通知操作")
	}
	db := a.store.DB
	targets := append([]webhookTarget(nil), db.Webhooks...)
	index := -1
	for i, target := range targets {
		if target.ID == req.ID {
			index = i
			break
		}
	}
	isLegacy := req.ID == legacyWebhookID && db.Webhook != nil
	if req.ID != "" && !isLegacy && index < 0 {
		return 0, http.StatusNotFound, fmt.Errorf("通知目标不存在")
	}
	if req.ID == "" && req.Action != "save" {
		return 0, http.StatusBadRequest, fmt.Errorf("请选择通知目标")
	}
	if req.Action == "delete" {
		if isLegacy {
			db.Webhook = nil
		} else {
			targets = append(targets[:index], targets[index+1:]...)
		}
	} else {
		var previous webhookConfig
		name := req.Name
		if isLegacy {
			previous = *db.Webhook
			if name != "" && name != legacyWebhookName {
				return 0, http.StatusBadRequest, fmt.Errorf("原有通知名称不能修改")
			}
		} else if index >= 0 {
			previous = targets[index].Config
		} else {
			count := len(targets)
			if db.Webhook != nil {
				count++
			}
			if count >= maxWebhookTargets {
				return 0, http.StatusBadRequest, fmt.Errorf("最多设置 8 个通知目标")
			}
		}
		cfg := previous
		if req.Action == "save" {
			if (!isLegacy && !labelOK(name)) || req.Enabled == nil || req.OnStatus == nil || req.OnConfig == nil {
				return 0, http.StatusBadRequest, fmt.Errorf("请填写目标名称和完整通知选项")
			}
			input := webhookRequest{
				Enabled: *req.Enabled, URL: req.URL, Format: req.Format, ClearURL: req.ClearURL,
				OnStatus: *req.OnStatus, OnConfig: *req.OnConfig, OnLoginLimit: req.OnLoginLimit, OnUpdate: req.OnUpdate,
				SigningSecret: req.SigningSecret, ClearSigningSecret: req.ClearSigningSecret,
				JSONTemplate: req.JSONTemplate, ClearJSONTemplate: req.ClearJSONTemplate, JSONTemplateEnabled: req.JSONTemplateEnabled,
			}
			var err error
			cfg, err = a.prepareWebhookConfig(previous, input)
			if err != nil {
				return 0, http.StatusBadRequest, err
			}
		} else {
			cfg.Enabled = req.Action == "enable"
			if cfg.Enabled && (cfg.URL == "" || !cfg.hasEvents()) {
				return 0, http.StatusBadRequest, fmt.Errorf("启用时需要有效地址，并至少选择一种事件")
			}
		}
		if isLegacy {
			if cfg == previous {
				return db.Revision, http.StatusOK, nil
			}
			db.Webhook = &cfg
		} else if index >= 0 {
			if req.Action != "save" {
				name = targets[index].Name
			}
			if cfg == previous && name == targets[index].Name {
				return db.Revision, http.StatusOK, nil
			}
			targets[index] = webhookTarget{ID: req.ID, Name: name, Config: cfg}
		} else {
			id := randomHex(8)
			for _, target := range targets {
				if target.ID == id {
					return 0, http.StatusInternalServerError, fmt.Errorf("无法分配通知目标标识，请重试")
				}
			}
			targets = append(targets, webhookTarget{ID: id, Name: name, Config: cfg})
		}
	}
	db.Webhooks = targets
	if err := incrementRevision(&db); err != nil {
		return 0, http.StatusConflict, err
	}
	if err := a.store.write(db); err != nil {
		return 0, http.StatusInternalServerError, fmt.Errorf("无法保存通知设置")
	}
	a.store.DB = db
	a.configureWebhooks()
	return db.Revision, http.StatusOK, nil
}
