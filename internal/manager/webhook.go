package manager

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// URL and SigningSecret are encrypted with the store key. Older databases
// omit SigningSecret and therefore send unsigned notifications.
type webhookConfig struct {
	Enabled       bool   `json:"enabled"`
	URL           string `json:"url,omitempty"`
	Format        string `json:"format,omitempty"`
	OnStatus      bool   `json:"onStatus"`
	OnConfig      bool   `json:"onConfig"`
	SigningSecret string `json:"signingSecret,omitempty"`
}

// An empty format preserves the original structured JSON payload.
func validWebhookFormat(format string) bool {
	switch format {
	case "", "discord", "slack", "ntfy", "telegram", "gotify":
		return true
	default:
		return false
	}
}

func (db Database) webhookConfig() webhookConfig {
	if db.Webhook != nil {
		return *db.Webhook
	}
	return webhookConfig{}
}

// Match the store's existing secret constraints without modifying the key.
func validWebhookSigningSecret(secret string) bool {
	if len(secret) < 8 || len(secret) > 1024 || !utf8.ValidString(secret) {
		return false
	}
	for _, c := range secret {
		if c < 32 || c == 127 {
			return false
		}
	}
	return true
}

// Reject damaged local settings without rewriting or silently discarding them.
// Imported webhook settings are ignored, so only the local copy is validated.
func validateStoredWebhook(s *Store, cfg *webhookConfig) error {
	if cfg == nil {
		return nil
	}
	if !validWebhookFormat(cfg.Format) {
		return fmt.Errorf("invalid webhook format")
	}
	if cfg.URL != "" {
		plain, err := s.decrypt(cfg.URL)
		if err != nil || !validWebhookDestination(cfg.Format, plain) {
			return fmt.Errorf("invalid webhook destination")
		}
	}
	if cfg.SigningSecret != "" {
		plain, err := s.decrypt(cfg.SigningSecret)
		if err != nil || !validWebhookSigningSecret(plain) {
			return fmt.Errorf("invalid webhook signing secret")
		}
	}
	if cfg.Enabled && (cfg.URL == "" || (!cfg.OnStatus && !cfg.OnConfig)) {
		return fmt.Errorf("incomplete webhook settings")
	}
	return nil
}

type webhookEvent struct {
	Event      string `json:"event"`
	At         string `json:"at"`
	InstanceID string `json:"instanceId,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Revision   uint64 `json:"revision,omitempty"`
}

// To connect a new lifecycle implementation, emitInstance is called only
// after publishing a real state transition. Config commits call emitConfig via
// Store.onCommit. Both accept only fixed event fields, never logs or config.
// Import/backup restore ignores the destination in the imported file and
// preserves the local one; old files without this field default to disabled.

type queuedWebhook struct {
	event      webhookEvent
	generation uint64
}

// There is one bounded worker per app, never a goroutine per event. Queued
// events are tied to the settings in force when emitted, so changing the URL
// or signing key cannot redirect old events to a new configuration.
type webhookDispatcher struct {
	mu            sync.Mutex
	ctx           context.Context
	cancel        context.CancelFunc
	configCtx     context.Context
	configCancel  context.CancelFunc
	queue         chan queuedWebhook
	config        webhookConfig
	target        string
	signingSecret string
	generation    uint64
	sent          uint64
	failed        uint64
	dropped       uint64
	lastResult    string
	lastAt        string
}

func newWebhookDispatcher() *webhookDispatcher {
	ctx, cancel := context.WithCancel(context.Background())
	d := &webhookDispatcher{ctx: ctx, cancel: cancel, queue: make(chan queuedWebhook, 64)}
	go d.run()
	return d
}

func (d *webhookDispatcher) configure(cfg webhookConfig, target, signingSecret string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	// Ciphertext may change during key migration while its plaintext remains
	// the same. Only effective destination, signing key or options change the
	// generation and cancel deliveries using the previous configuration.
	if d.configCancel != nil && d.target == target && d.signingSecret == signingSecret &&
		d.config.Enabled == cfg.Enabled && d.config.Format == cfg.Format &&
		d.config.OnStatus == cfg.OnStatus && d.config.OnConfig == cfg.OnConfig {
		d.config = cfg
		return
	}
	if d.configCancel != nil {
		d.configCancel()
	}
	d.generation++
	d.config = cfg
	d.target, d.signingSecret = target, signingSecret
	d.configCtx, d.configCancel = context.WithCancel(d.ctx)
}

func (d *webhookDispatcher) close() { d.cancel() }

func (d *webhookDispatcher) emitConfig(revision uint64) {
	if d != nil && revision != 0 {
		d.enqueue(webhookEvent{Event: "config.saved", At: now(), Revision: revision}, false)
	}
}

func (d *webhookDispatcher) emitInstance(event, id, kind string) {
	if d == nil || !idPattern.MatchString(id) || (kind != "frpc" && kind != "frps") {
		return
	}
	switch event {
	case "instance.up", "instance.down", "instance.start_failed":
	default:
		return
	}
	d.enqueue(webhookEvent{Event: event, At: now(), InstanceID: id, Kind: kind}, true)
}

func (d *webhookDispatcher) enqueue(event webhookEvent, status bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ctx.Err() != nil || !d.config.Enabled || d.target == "" || (status && !d.config.OnStatus) || (!status && !d.config.OnConfig) {
		return
	}
	select {
	case d.queue <- queuedWebhook{event: event, generation: d.generation}:
	default:
		d.dropped++
	}
}

func (d *webhookDispatcher) run() {
	for {
		select {
		case <-d.ctx.Done():
			return
		case item := <-d.queue:
			d.mu.Lock()
			if item.generation != d.generation || !d.config.Enabled || d.target == "" || d.ctx.Err() != nil {
				d.dropped++
				d.mu.Unlock()
				continue
			}
			target, format, signingSecret, ctx := d.target, d.config.Format, d.signingSecret, d.configCtx
			d.mu.Unlock()
			result, ok := sendWebhook(ctx, target, format, signingSecret, item.event)
			d.mu.Lock()
			if ctx.Err() != nil {
				d.dropped++
			} else {
				if ok {
					d.sent++
				} else {
					d.failed++
				}
				d.lastAt, d.lastResult = now(), result
			}
			d.mu.Unlock()
		}
	}
}

// The URL, remote response and network error text are never returned to the UI.
func (d *webhookDispatcher) status() map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()
	return map[string]any{
		"ready": d.config.Enabled && d.target != "", "sent": d.sent,
		"failed": d.failed, "dropped": d.dropped, "lastResult": d.lastResult, "lastAt": d.lastAt,
	}
}

// Public HTTPS destinations only. Validation is repeated at dial time because
// DNS may change after the configuration is saved.
func validWebhookURL(raw string) bool {
	if len(raw) < 8 || len(raw) > 1024 || strings.TrimSpace(raw) != raw || strings.Contains(raw, "#") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil || u.Fragment != "" || u.Host == "" {
		return false
	}
	host := u.Hostname()
	if strings.Contains(host, "%") || !hostOK(host) {
		return false
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || !portOK(n) {
			return false
		}
	}
	if ip, err := netip.ParseAddr(host); err == nil && !publicWebhookIP(ip) {
		return false
	}
	return true
}

var telegramChatIDPattern = regexp.MustCompile(`^(?:-?[0-9]{1,32}|@[A-Za-z0-9_]{1,64})$`)

// Telegram expects chat_id and text in the JSON body. The administrator enters
// a complete sendMessage URL containing exactly one chat_id query parameter;
// keep the token in the path and remove that parameter from the request URL.
func telegramDestination(raw string) (string, string, bool) {
	if !validWebhookURL(raw) {
		return "", "", false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", false
	}
	parts := strings.Split(u.Path, "/")
	if len(parts) < 3 || parts[len(parts)-1] != "sendMessage" || !strings.HasPrefix(parts[len(parts)-2], "bot") || len(parts[len(parts)-2]) <= len("bot") {
		return "", "", false
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(query) != 1 || len(query["chat_id"]) != 1 || !telegramChatIDPattern.MatchString(query.Get("chat_id")) {
		return "", "", false
	}
	chatID := query.Get("chat_id")
	u.RawQuery, u.ForceQuery = "", false
	return u.String(), chatID, true
}

func validWebhookDestination(format, raw string) bool {
	if format == "telegram" {
		_, _, ok := telegramDestination(raw)
		return ok
	}
	return validWebhookURL(raw)
}

var nonPublicWebhookRanges = func() []netip.Prefix {
	blocks := []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
		"172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "192.168.0.0/16",
		"198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4",
		"2001::/23", "2001:db8::/32", "2002::/16", "3fff::/20",
	}
	out := make([]netip.Prefix, 0, len(blocks))
	for _, block := range blocks {
		out = append(out, netip.MustParsePrefix(block))
	}
	return out
}()

var globalIPv6 = netip.MustParsePrefix("2000::/3")

func publicWebhookIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	if ip.Is6() && !globalIPv6.Contains(ip) {
		return false
	}
	for _, block := range nonPublicWebhookRanges {
		if block.Contains(ip) {
			return false
		}
	}
	return true
}

func webhookDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("invalid destination")
	}
	var addresses []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		addresses = []netip.Addr{ip}
	} else {
		addresses, err = net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, fmt.Errorf("DNS lookup failed")
		}
	}
	if len(addresses) == 0 || len(addresses) > 32 {
		return nil, fmt.Errorf("invalid DNS result")
	}
	// Reject mixed results as well as private-only results; never ask the
	// transport to resolve the hostname a second time.
	for _, ip := range addresses {
		if !publicWebhookIP(ip) {
			return nil, fmt.Errorf("destination is not public")
		}
	}
	for i, ip := range addresses {
		// Give later checked addresses a chance if this one silently drops packets.
		// Leave the last (or only) address to the overall request timeout.
		dialer := net.Dialer{}
		if i < len(addresses)-1 {
			dialer.Timeout = time.Second
		}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.Unmap().String(), port))
		if err == nil {
			return conn, nil
		}
		if ctx.Err() != nil {
			break
		}
	}
	return nil, fmt.Errorf("connection failed")
}

// All channel-specific payloads are produced here; the original JSON remains
// byte-for-byte compatible when the stored format is empty.
func formatWebhookEvent(format string, event webhookEvent) ([]byte, string, error) {
	return formatWebhookEventWithChat(format, event, "")
}

func formatWebhookEventWithChat(format string, event webhookEvent, chatID string) ([]byte, string, error) {
	if format == "" {
		body, err := json.Marshal(event)
		return body, "application/json", err
	}
	var description string
	switch event.Event {
	case "instance.up":
		description = "实例进程已启动"
	case "instance.down":
		description = "实例进程已退出"
	case "instance.start_failed":
		description = "实例进程启动失败"
	case "config.saved":
		description = fmt.Sprintf("配置草稿已更新（r%d）", event.Revision)
	default:
		return nil, "", fmt.Errorf("unsupported event")
	}
	message := fmt.Sprintf("KurumiFRP · %s\n时间：%s", description, event.At)
	if event.InstanceID != "" {
		message += fmt.Sprintf("\n实例：%s (%s)", event.InstanceID, event.Kind)
	}
	switch format {
	case "discord":
		payload := struct {
			Content         string `json:"content"`
			AllowedMentions struct {
				Parse []string `json:"parse"`
			} `json:"allowed_mentions"`
		}{Content: message}
		payload.AllowedMentions.Parse = []string{}
		body, err := json.Marshal(payload)
		return body, "application/json", err
	case "slack":
		body, err := json.Marshal(struct {
			Text string `json:"text"`
		}{Text: message})
		return body, "application/json", err
	case "ntfy":
		return []byte(message), "text/plain; charset=utf-8", nil
	case "telegram":
		if !telegramChatIDPattern.MatchString(chatID) {
			return nil, "", fmt.Errorf("invalid chat ID")
		}
		body, err := json.Marshal(struct {
			ChatID string `json:"chat_id"`
			Text   string `json:"text"`
		}{ChatID: chatID, Text: message})
		return body, "application/json", err
	case "gotify":
		body, err := json.Marshal(struct {
			Title    string `json:"title"`
			Message  string `json:"message"`
			Priority int    `json:"priority"`
		}{Title: "KurumiFRP", Message: message, Priority: 5})
		return body, "application/json", err
	default:
		return nil, "", fmt.Errorf("unsupported webhook format")
	}
}

func sendWebhook(ctx context.Context, target, format, signingSecret string, event webhookEvent) (string, bool) {
	destination, chatID := target, ""
	if format == "telegram" {
		var ok bool
		destination, chatID, ok = telegramDestination(target)
		if !ok {
			return "通知地址无效", false
		}
	} else if !validWebhookURL(target) {
		return "通知地址无效", false
	}
	body, contentType, err := formatWebhookEventWithChat(format, event, chatID)
	if err != nil {
		return "通知生成失败", false
	}
	transport := &http.Transport{
		Proxy: nil, DialContext: webhookDial, DisableKeepAlives: true,
		TLSHandshakeTimeout: 4 * time.Second, MaxResponseHeaderBytes: 8192,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport, Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, destination, bytes.NewReader(body))
	if err != nil {
		return "通知请求无效", false
	}
	req.Header.Set("Content-Type", contentType)
	if signingSecret != "" {
		mac := hmac.New(sha256.New, []byte(signingSecret))
		_, _ = mac.Write(body)
		req.Header.Set("X-Kurumi-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	resp, err := client.Do(req)
	if err != nil {
		return "发送失败（连接、地址或超时）", false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Sprintf("HTTP %d", resp.StatusCode), false
	}
	if format == "telegram" {
		const maxTelegramResponse = 64 << 10
		response, err := io.ReadAll(io.LimitReader(resp.Body, maxTelegramResponse+1))
		var result struct {
			OK *bool `json:"ok"`
		}
		if err != nil || len(response) > maxTelegramResponse || json.Unmarshal(response, &result) != nil || result.OK == nil {
			return "目标响应无法确认", false
		}
		if !*result.OK {
			return "目标未接受通知", false
		}
	}
	return fmt.Sprintf("HTTP %d", resp.StatusCode), true
}

func (a *App) configureWebhook(cfg webhookConfig) {
	target := ""
	if cfg.URL != "" {
		if plain, err := a.store.decrypt(cfg.URL); err == nil && validWebhookDestination(cfg.Format, plain) {
			target = plain
		}
	}
	signingSecret := ""
	if cfg.SigningSecret != "" {
		plain, err := a.store.decrypt(cfg.SigningSecret)
		if err != nil || !validWebhookSigningSecret(plain) {
			// Never silently send an unsigned notification when a key was set.
			target = ""
		} else {
			signingSecret = plain
		}
	}
	a.webhooks.configure(cfg, target, signingSecret)
}

// Called after authentication and same-origin checks. Decode the request before
// acquiring a.mu so a slow request body cannot delay instance controls.
type webhookRequest struct {
	Revision           uint64  `json:"revision"`
	Enabled            bool    `json:"enabled"`
	URL                string  `json:"url"`
	Format             *string `json:"format"`
	ClearURL           bool    `json:"clearURL"`
	OnStatus           bool    `json:"onStatus"`
	OnConfig           bool    `json:"onConfig"`
	SigningSecret      string  `json:"signingSecret"`
	ClearSigningSecret bool    `json:"clearSigningSecret"`
}

func (a *App) webhookAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		a.mu.Lock()
		cfg := a.store.DB.webhookConfig()
		revision := a.store.DB.Revision
		status := a.webhooks.status()
		a.mu.Unlock()
		reply(w, http.StatusOK, map[string]any{
			"revision": revision, "enabled": cfg.Enabled, "hasURL": cfg.URL != "", "hasSigningSecret": cfg.SigningSecret != "",
			"format": cfg.Format, "onStatus": cfg.OnStatus, "onConfig": cfg.OnConfig, "status": status,
		})
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req webhookRequest
	if err := decode(w, r, &req); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	revision, code, err := a.saveWebhook(req)
	if err != nil {
		fail(w, code, err)
		return
	}
	reply(w, http.StatusOK, map[string]any{"ok": true, "revision": revision})
}

func (a *App) saveWebhook(req webhookRequest) (uint64, int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closing {
		return 0, http.StatusServiceUnavailable, fmt.Errorf("工作台正在关闭")
	}
	if req.Revision != a.store.DB.Revision {
		return 0, http.StatusConflict, fmt.Errorf("配置已在其他窗口更新，请刷新后重试")
	}
	if req.ClearURL && req.URL != "" {
		return 0, http.StatusBadRequest, fmt.Errorf("不能同时清除和设置通知地址")
	}
	if req.ClearSigningSecret && req.SigningSecret != "" {
		return 0, http.StatusBadRequest, fmt.Errorf("不能同时清除和设置签名密钥")
	}
	previous := a.store.DB.webhookConfig()
	cfg := webhookConfig{
		Enabled: req.Enabled, URL: previous.URL, Format: previous.Format,
		OnStatus: req.OnStatus, OnConfig: req.OnConfig, SigningSecret: previous.SigningSecret,
	}
	// A client predating channel formats may omit the field while editing
	// other settings; omission must not reset an existing selection.
	if req.Format != nil {
		cfg.Format = *req.Format
	}
	if !validWebhookFormat(cfg.Format) {
		return 0, http.StatusBadRequest, fmt.Errorf("通知格式无效")
	}
	if req.ClearURL {
		cfg.URL = ""
	} else if req.URL != "" {
		if !validWebhookURL(req.URL) {
			return 0, http.StatusBadRequest, fmt.Errorf("通知地址须为有效的公网 HTTPS 地址，不允许用户信息或片段")
		}
		existing, _ := a.store.decrypt(cfg.URL)
		if req.URL != existing {
			var err error
			cfg.URL, err = a.store.encrypt(req.URL)
			if err != nil {
				return 0, http.StatusBadRequest, fmt.Errorf("通知地址无法保存")
			}
		}
	}
	if req.ClearSigningSecret {
		cfg.SigningSecret = ""
	} else if req.SigningSecret != "" {
		if !validWebhookSigningSecret(req.SigningSecret) {
			return 0, http.StatusBadRequest, fmt.Errorf("签名密钥须为 8–1024 字节的有效 UTF-8 文本，且不能包含 ASCII 控制字符")
		}
		existing, _ := a.store.decrypt(cfg.SigningSecret)
		if req.SigningSecret != existing {
			var err error
			cfg.SigningSecret, err = a.store.encrypt(req.SigningSecret)
			if err != nil {
				return 0, http.StatusBadRequest, fmt.Errorf("签名密钥无法保存")
			}
		}
	}
	signingSecret := ""
	if cfg.SigningSecret != "" {
		plain, err := a.store.decrypt(cfg.SigningSecret)
		if err != nil || !validWebhookSigningSecret(plain) {
			return 0, http.StatusBadRequest, fmt.Errorf("签名密钥无效，请重新设置或清除")
		}
		signingSecret = plain
	}
	target := ""
	if cfg.URL != "" {
		plain, err := a.store.decrypt(cfg.URL)
		if err != nil || !validWebhookDestination(cfg.Format, plain) {
			if cfg.Format == "telegram" {
				return 0, http.StatusBadRequest, fmt.Errorf("Telegram 地址须为完整的 sendMessage 地址，并带一个有效的 chat_id")
			}
			return 0, http.StatusBadRequest, fmt.Errorf("通知地址无效，请重新设置")
		}
		target = plain
	}
	if cfg.Enabled && (target == "" || (!cfg.OnStatus && !cfg.OnConfig)) {
		return 0, http.StatusBadRequest, fmt.Errorf("启用时需要有效地址，并至少选择一种事件")
	}
	if cfg == previous {
		return a.store.DB.Revision, http.StatusOK, nil
	}
	db := a.store.DB
	if cfg == (webhookConfig{}) {
		db.Webhook = nil
	} else {
		db.Webhook = &cfg
	}
	if err := incrementRevision(&db); err != nil {
		return 0, http.StatusConflict, err
	}
	if err := a.store.write(db); err != nil {
		return 0, http.StatusInternalServerError, fmt.Errorf("无法保存通知配置")
	}
	a.store.DB = db
	a.webhooks.configure(cfg, target, signingSecret)
	return db.Revision, http.StatusOK, nil
}
