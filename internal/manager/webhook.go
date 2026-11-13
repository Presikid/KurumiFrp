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

// URL, SigningSecret and JSONTemplate are encrypted with the store key.
type webhookConfig struct {
	Enabled             bool   `json:"enabled"`
	URL                 string `json:"url,omitempty"`
	Format              string `json:"format,omitempty"`
	OnStatus            bool   `json:"onStatus"`
	StatusProblemsOnly  bool   `json:"statusProblemsOnly,omitempty"`
	StatusScope         string `json:"statusScope,omitempty"`
	OnConfig            bool   `json:"onConfig"`
	OnLoginLimit        bool   `json:"onLoginLimit,omitempty"`
	OnUpdate            bool   `json:"onUpdate,omitempty"`
	SigningSecret       string `json:"signingSecret,omitempty"`
	JSONTemplate        string `json:"jsonTemplate,omitempty"`
	JSONTemplateEnabled bool   `json:"jsonTemplateEnabled,omitempty"`
}

func (cfg webhookConfig) hasEvents() bool {
	return cfg.OnStatus || cfg.OnConfig || cfg.OnLoginLimit || cfg.OnUpdate
}

func validWebhookStatusScope(scope string) bool {
	switch scope {
	case "", "instances", "nodes":
		return true
	default:
		kind, id, ok := strings.Cut(scope, ":")
		return ok && (kind == "instance" || kind == "node") && idPattern.MatchString(id)
	}
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
	if !validWebhookStatusScope(cfg.StatusScope) {
		return fmt.Errorf("invalid webhook status scope")
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
	if cfg.JSONTemplate != "" {
		plain, err := s.decrypt(cfg.JSONTemplate)
		if err != nil {
			return fmt.Errorf("invalid webhook JSON template")
		}
		if _, err = parseWebhookJSONTemplate(plain); err != nil {
			return fmt.Errorf("invalid webhook JSON template")
		}
	}
	if cfg.JSONTemplateEnabled && (cfg.Format != "" || cfg.JSONTemplate == "") {
		return fmt.Errorf("invalid webhook JSON template settings")
	}
	if cfg.Enabled && (cfg.URL == "" || !cfg.hasEvents()) {
		return fmt.Errorf("incomplete webhook settings")
	}
	return nil
}

type webhookEvent struct {
	Event      string `json:"event"`
	At         string `json:"at"`
	InstanceID string `json:"instanceId,omitempty"`
	NodeID     string `json:"nodeId,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Revision   uint64 `json:"revision,omitempty"`
	SourceIP   string `json:"sourceIp,omitempty"`
	Version    string `json:"version,omitempty"`
}

// To connect a new lifecycle implementation, emitInstance is called only
// after publishing a real state transition. Config commits call emitConfig via
// Store.onCommit. Both accept only fixed event fields, never logs or config.
// Import/backup restore ignores the destination in the imported file and
// preserves the local one; old files without this field default to disabled.

type webhookRecipient struct {
	id         string
	generation string
}

type queuedWebhook struct {
	event      webhookEvent
	recipients []webhookRecipient
}

type webhookRuntimeConfig struct {
	id            string
	config        webhookConfig
	target        string
	signingSecret string
	templateRaw   string
	template      *webhookJSONTemplate
}

type webhookDelivery struct {
	webhookRuntimeConfig
	generation string
	ctx        context.Context
	cancel     context.CancelFunc
	sent       uint64
	failed     uint64
	dropped    uint64
	lastResult string
	lastAt     string
}

// One bounded queue and worker serve all targets. Each queued event remembers
// each recipient's generation, so one edit cannot redirect old deliveries.
type webhookDispatcher struct {
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	queue   chan queuedWebhook
	targets map[string]*webhookDelivery
	order   []string
	testing bool
}

func newWebhookDispatcher() *webhookDispatcher {
	ctx, cancel := context.WithCancel(context.Background())
	d := &webhookDispatcher{ctx: ctx, cancel: cancel, queue: make(chan queuedWebhook, 64), targets: make(map[string]*webhookDelivery)}
	go d.run()
	return d
}

func (d *webhookDispatcher) configure(configs []webhookRuntimeConfig) {
	d.mu.Lock()
	defer d.mu.Unlock()
	seen := make(map[string]bool, len(configs))
	order := make([]string, 0, len(configs))
	for _, next := range configs {
		seen[next.id] = true
		order = append(order, next.id)
		old := d.targets[next.id]
		if old != nil && old.target == next.target && old.signingSecret == next.signingSecret && old.templateRaw == next.templateRaw &&
			old.config.Enabled == next.config.Enabled && old.config.Format == next.config.Format &&
			old.config.OnStatus == next.config.OnStatus && old.config.OnConfig == next.config.OnConfig &&
			old.config.StatusProblemsOnly == next.config.StatusProblemsOnly &&
			old.config.StatusScope == next.config.StatusScope &&
			old.config.OnLoginLimit == next.config.OnLoginLimit && old.config.OnUpdate == next.config.OnUpdate &&
			old.config.JSONTemplateEnabled == next.config.JSONTemplateEnabled {
			old.config = next.config // ciphertext may change during key migration
			continue
		}
		if old != nil {
			old.cancel()
		}
		ctx, cancel := context.WithCancel(d.ctx)
		d.targets[next.id] = &webhookDelivery{webhookRuntimeConfig: next, generation: randomHex(16), ctx: ctx, cancel: cancel}
	}
	for id, old := range d.targets {
		if !seen[id] {
			old.cancel()
			delete(d.targets, id)
		}
	}
	d.order = order
}

func (d *webhookDispatcher) close() { d.cancel() }

func (d *webhookDispatcher) emitConfig(revision uint64) {
	if d != nil && revision != 0 {
		d.enqueue(webhookEvent{Event: "config.saved", At: now(), Revision: revision})
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
	d.enqueue(webhookEvent{Event: event, At: now(), InstanceID: id, Kind: kind})
}

func (d *webhookDispatcher) emitNode(event, id string) {
	if d == nil || !idPattern.MatchString(id) || (event != "node.available" && event != "node.unavailable") {
		return
	}
	d.enqueue(webhookEvent{Event: event, At: now(), NodeID: id})
}

func (d *webhookDispatcher) emitLoginLimit(peer string) {
	if d == nil {
		return
	}
	if ip, err := netip.ParseAddr(peer); err == nil {
		peer = ip.WithZone("").Unmap().String()
	} else {
		peer = ""
	}
	d.enqueue(webhookEvent{Event: "auth.login_limited", At: now(), SourceIP: peer})
}

var webhookVersionPattern = regexp.MustCompile(`^v[0-9]{1,5}\.[0-9]{1,5}\.[0-9]{1,5}(\.[0-9]{1,5})?$`)

func (d *webhookDispatcher) emitUpdateAvailable(version string) bool {
	if d == nil || !webhookVersionPattern.MatchString(version) {
		return false
	}
	_, queued := d.enqueueWithStatus(webhookEvent{Event: "update.available", At: now(), Version: version})
	return queued
}

func (cfg webhookConfig) receives(event string) bool {
	switch event {
	case "instance.up", "node.available":
		return cfg.OnStatus && !cfg.StatusProblemsOnly
	case "instance.down", "instance.start_failed", "node.unavailable":
		return cfg.OnStatus
	case "config.saved":
		return cfg.OnConfig
	case "auth.login_limited":
		return cfg.OnLoginLimit
	case "update.available":
		return cfg.OnUpdate
	default:
		return false
	}
}

// Object scope applies only to status events. Other event classes keep their
// individual subscriptions regardless of this setting.
func (cfg webhookConfig) receivesFor(event webhookEvent) bool {
	if !cfg.receives(event.Event) {
		return false
	}
	switch event.Event {
	case "instance.up", "instance.down", "instance.start_failed":
		if cfg.StatusScope == "" || cfg.StatusScope == "instances" {
			return true
		}
		return cfg.StatusScope == "instance:"+event.InstanceID && idPattern.MatchString(event.InstanceID)
	case "node.available", "node.unavailable":
		if cfg.StatusScope == "" || cfg.StatusScope == "nodes" {
			return true
		}
		return cfg.StatusScope == "node:"+event.NodeID && idPattern.MatchString(event.NodeID)
	default:
		return true
	}
}

// The return value reports that at least one enabled target was considered,
// including a bounded-queue drop. It does not imply delivery.
func (d *webhookDispatcher) enqueue(event webhookEvent) bool {
	considered, _ := d.enqueueWithStatus(event)
	return considered
}

// Return whether recipients were considered and whether the event was queued.
func (d *webhookDispatcher) enqueueWithStatus(event webhookEvent) (bool, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ctx.Err() != nil {
		return false, false
	}
	recipients := make([]webhookRecipient, 0, len(d.order))
	for _, id := range d.order {
		target := d.targets[id]
		if target != nil && target.config.Enabled && target.target != "" && target.config.receivesFor(event) {
			recipients = append(recipients, webhookRecipient{id: id, generation: target.generation})
		}
	}
	if len(recipients) == 0 {
		return false, false
	}
	select {
	case d.queue <- queuedWebhook{event: event, recipients: recipients}:
		return true, true
	default:
		for _, recipient := range recipients {
			d.targets[recipient.id].dropped++
		}
		return true, false
	}
}

func (d *webhookDispatcher) run() {
	for {
		select {
		case <-d.ctx.Done():
			return
		case item := <-d.queue:
			for _, recipient := range item.recipients {
				d.mu.Lock()
				target := d.targets[recipient.id]
				if target == nil || target.generation != recipient.generation || !target.config.Enabled || target.target == "" || d.ctx.Err() != nil {
					d.mu.Unlock()
					continue
				}
				address, format, secret, template, ctx := target.target, target.config.Format, target.signingSecret, target.template, target.ctx
				d.mu.Unlock()
				result, ok := sendWebhook(ctx, address, format, secret, item.event, template)
				d.mu.Lock()
				if d.targets[recipient.id] == target && target.generation == recipient.generation {
					if ctx.Err() != nil {
						target.dropped++
					} else {
						if ok {
							target.sent++
						} else {
							target.failed++
						}
						target.lastAt, target.lastResult = now(), result
					}
				}
				d.mu.Unlock()
			}
		}
	}
}

// Neither destination nor remote/network error details are returned to the UI.
func (d *webhookDispatcher) statusFor(id string) map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()
	target := d.targets[id]
	if target == nil {
		return map[string]any{"ready": false, "sent": 0, "failed": 0, "dropped": 0, "lastResult": "", "lastAt": ""}
	}
	return map[string]any{"ready": target.config.Enabled && target.target != "", "sent": target.sent, "failed": target.failed, "dropped": target.dropped, "lastResult": target.lastResult, "lastAt": target.lastAt}
}

func (d *webhookDispatcher) status() map[string]any { return d.statusFor(legacyWebhookID) }

// Public HTTPS destinations only. Validation is repeated at dial time because
// DNS may change after the configuration is saved.
func validPublicHTTPSURL(raw string) bool {
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
	if ip, err := netip.ParseAddr(host); err == nil && !publicDestinationIP(ip) {
		return false
	}
	return true
}

var telegramChatIDPattern = regexp.MustCompile(`^(?:-?[0-9]{1,32}|@[A-Za-z0-9_]{1,64})$`)

// Telegram expects chat_id and text in the JSON body. The administrator enters
// a complete sendMessage URL containing exactly one chat_id query parameter;
// keep the token in the path and remove that parameter from the request URL.
func telegramDestination(raw string) (string, string, bool) {
	if !validPublicHTTPSURL(raw) {
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
	return validPublicHTTPSURL(raw)
}

var nonPublicDestinationRanges = func() []netip.Prefix {
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

func publicDestinationIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	if ip.Is6() && !globalIPv6.Contains(ip) {
		return false
	}
	for _, block := range nonPublicDestinationRanges {
		if block.Contains(ip) {
			return false
		}
	}
	return true
}

func dialPublicHTTPS(ctx context.Context, network, address string) (net.Conn, error) {
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
		if !publicDestinationIP(ip) {
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
	case "node.available":
		description = "远程节点检查已恢复"
	case "node.unavailable":
		description = "远程节点连续检查未成功"
	case "config.saved":
		description = fmt.Sprintf("配置草稿已更新（r%d）", event.Revision)
	case "auth.login_limited":
		description = "登录尝试已限流"
	case "update.available":
		description = "发现可用的软件更新"
	case "webhook.test":
		description = "手动发送的测试通知"
	default:
		return nil, "", fmt.Errorf("unsupported event")
	}
	message := fmt.Sprintf("KurumiFRP · %s\n时间：%s", description, event.At)
	if event.InstanceID != "" {
		message += fmt.Sprintf("\n实例：%s (%s)", event.InstanceID, event.Kind)
	}
	if event.NodeID != "" {
		message += fmt.Sprintf("\n节点：%s", event.NodeID)
	}
	if event.SourceIP != "" {
		message += fmt.Sprintf("\n连接对端 IP：%s", event.SourceIP)
	}
	if event.Version != "" {
		message += fmt.Sprintf("\n版本：%s", event.Version)
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

func sendWebhook(ctx context.Context, target, format, signingSecret string, event webhookEvent, template *webhookJSONTemplate) (string, bool) {
	destination, chatID := target, ""
	if format == "telegram" {
		var ok bool
		destination, chatID, ok = telegramDestination(target)
		if !ok {
			return "通知地址无效", false
		}
	} else if !validPublicHTTPSURL(target) {
		return "通知地址无效", false
	}
	var body []byte
	var contentType string
	var err error
	if template != nil {
		if format != "" {
			return "通知生成失败", false
		}
		body, err = template.render(event)
		contentType = "application/json"
	} else {
		body, contentType, err = formatWebhookEventWithChat(format, event, chatID)
	}
	if err != nil {
		return "通知生成失败", false
	}
	transport := &http.Transport{
		Proxy: nil, DialContext: dialPublicHTTPS, DisableKeepAlives: true,
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

func (a *App) configureWebhooks() {
	configs := make([]webhookRuntimeConfig, 0, len(a.store.DB.Webhooks)+1)
	add := func(id string, cfg webhookConfig) {
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
		templateRaw := ""
		var template *webhookJSONTemplate
		if cfg.JSONTemplate != "" {
			plain, err := a.store.decrypt(cfg.JSONTemplate)
			var parsed *webhookJSONTemplate
			if err == nil {
				parsed, err = parseWebhookJSONTemplate(plain)
			}
			if err != nil {
				target = ""
			} else {
				templateRaw = plain
				if cfg.JSONTemplateEnabled {
					template = parsed
				}
			}
		}
		if cfg.JSONTemplateEnabled && (cfg.Format != "" || template == nil) {
			target = ""
		}
		configs = append(configs, webhookRuntimeConfig{id: id, config: cfg, target: target, signingSecret: signingSecret, templateRaw: templateRaw, template: template})
	}
	if a.store.DB.Webhook != nil {
		add(legacyWebhookID, *a.store.DB.Webhook)
	}
	for _, target := range a.store.DB.Webhooks {
		add(target.ID, target.Config)
	}
	a.webhooks.configure(configs)
}

// Called after authentication and same-origin checks. Decode the request before
// acquiring a.mu so a slow request body cannot delay instance controls.
type webhookRequest struct {
	Revision            uint64  `json:"revision"`
	Enabled             bool    `json:"enabled"`
	URL                 string  `json:"url"`
	Format              *string `json:"format"`
	ClearURL            bool    `json:"clearURL"`
	OnStatus            bool    `json:"onStatus"`
	StatusProblemsOnly  *bool   `json:"statusProblemsOnly"`
	StatusScope         *string `json:"statusScope"`
	OnConfig            bool    `json:"onConfig"`
	OnLoginLimit        *bool   `json:"onLoginLimit"`
	OnUpdate            *bool   `json:"onUpdate"`
	SigningSecret       string  `json:"signingSecret"`
	ClearSigningSecret  bool    `json:"clearSigningSecret"`
	JSONTemplate        string  `json:"jsonTemplate"`
	ClearJSONTemplate   bool    `json:"clearJSONTemplate"`
	JSONTemplateEnabled *bool   `json:"jsonTemplateEnabled"`
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
			"format": cfg.Format, "onStatus": cfg.OnStatus, "onConfig": cfg.OnConfig,
			"statusProblemsOnly": cfg.StatusProblemsOnly, "statusScope": cfg.StatusScope,
			"onLoginLimit": cfg.OnLoginLimit, "onUpdate": cfg.OnUpdate,
			"hasJSONTemplate": cfg.JSONTemplate != "", "jsonTemplateEnabled": cfg.JSONTemplateEnabled, "status": status,
		})
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req webhookRequest
	if err := decode(w, r, &req); err != nil {
		fail(w, http.StatusBadRequest, fmt.Errorf("通知设置请求格式无效"))
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
	previous := a.store.DB.webhookConfig()
	cfg, err := a.prepareWebhookConfig(previous, req)
	if err != nil {
		return 0, http.StatusBadRequest, err
	}
	if cfg == previous {
		return a.store.DB.Revision, http.StatusOK, nil
	}
	db := a.store.DB
	if db.Webhook == nil && cfg != (webhookConfig{}) && len(db.Webhooks) >= maxWebhookTargets {
		return 0, http.StatusBadRequest, fmt.Errorf("最多设置 8 个通知目标")
	}
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
	a.configureWebhooks()
	return db.Revision, http.StatusOK, nil
}
