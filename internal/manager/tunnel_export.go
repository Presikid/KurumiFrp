package manager

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const maxRuleExportSize = 16 << 20

// Reject oversized encrypted inputs before decoding, and account for TOML
// quoting, including brace escapes, before allocating a plaintext credential.
func quoteRuleExportSecret(s *Store, ciphertext string, budget int) (string, error) {
	if budget < 2 || len(ciphertext) > (maxRuleExportSize+64)*4/3+4 {
		return "", fmt.Errorf("认证信息超过导出大小限制")
	}
	value, err := s.decrypt(ciphertext)
	if err != nil {
		return "", err
	}
	size := 2 // TOML quotes
	for _, r := range value {
		switch r {
		case '{', '}':
			size += 6 // exported as a TOML Unicode escape
		case '\\', '"', '\b', '\t', '\n', '\f', '\r':
			size += 2
		default:
			if r < 0x20 || r == 0x7f {
				size += 6
			} else {
				size += utf8.RuneLen(r)
			}
		}
		if size > budget {
			return "", fmt.Errorf("认证信息超过导出大小限制")
		}
	}
	return quote(value), nil
}

// Check disabled drafts too: exporting an incomplete fallback chain would
// leave a broken rule if the user later enables it in the standalone file.
func validateExportFallbacks(rules []Tunnel) error {
	visitors := make(map[string]Tunnel)
	for _, rule := range rules {
		if isVisitor(rule.Type) {
			visitors[rule.InstanceID+"/"+rule.Name] = rule
		}
	}
	for _, rule := range rules {
		if rule.Type != "xtcp-visitor" || rule.FallbackTo == "" {
			continue
		}
		target, ok := visitors[rule.InstanceID+"/"+rule.FallbackTo]
		if !ok || !supportsUnboundVisitor(target.Type) {
			return fmt.Errorf("规则 %q 的回退目标 %q 未选中或不是同一客户端的 STCP/XTCP Visitor，请一并选择有效目标", rule.Name, rule.FallbackTo)
		}
		if rule.Enabled && !target.Enabled {
			return fmt.Errorf("规则 %q 已启用，但其回退目标 %q 已停用", rule.Name, rule.FallbackTo)
		}
	}
	all := State{Tunnels: append([]Tunnel(nil), rules...)}
	for i := range all.Tunnels {
		all.Tunnels[i].Enabled = true
	}
	if err := validateVisitorFallbacks(all); err != nil {
		return fmt.Errorf("所选 Visitor 回退链无效：%w", err)
	}
	return nil
}

func renderRuleExport(s *Store, client Instance, rules []Tunnel, revision uint64, limit int) (string, error) {
	var out strings.Builder
	// Client names appear in a comment rather than a quoted TOML value.
	if !labelOK(client.Name) || !idPattern.MatchString(client.ID) {
		return "", fmt.Errorf("客户端名称或标识无效")
	}
	fmt.Fprintln(&out, "# Contains plaintext credentials; keep this file private.")
	fmt.Fprintln(&out, "# Disabled rules use enabled = false; verify client compatibility before running.")
	fmt.Fprintf(&out, "# Client: %s (%s)\n", client.Name, client.ID)
	fmt.Fprintf(&out, "# Draft revision: %d\n", revision)
	if err := renderInstance(&out, client); err != nil {
		return "", fmt.Errorf("无法生成客户端连接配置")
	}
	token, err := quoteRuleExportSecret(s, client.Token, limit-out.Len())
	if err != nil {
		return "", fmt.Errorf("无法读取客户端认证信息")
	}
	fmt.Fprintf(&out, "auth.method = \"token\"\nauth.token = %s\n", token)
	secret := func(text, _ string) (string, error) {
		return quoteRuleExportSecret(s, text, limit-out.Len())
	}
	for _, rule := range rules {
		if err := renderProtocolTunnel(&out, rule, secret); err != nil {
			return "", fmt.Errorf("无法生成所选规则配置")
		}
		if !rule.Enabled {
			fmt.Fprintln(&out, "enabled = false")
		}
		if out.Len() > limit {
			return "", fmt.Errorf("导出内容超过大小限制")
		}
	}
	if out.Len() > limit {
		return "", fmt.Errorf("导出内容超过大小限制")
	}
	return out.String(), nil
}

func selectedRuleGroups(state State, wanted map[string]bool) (map[string][]Tunnel, []Tunnel, error) {
	groups := make(map[string][]Tunnel)
	selected := make([]Tunnel, 0, len(wanted))
	for _, rule := range state.Tunnels {
		if wanted[rule.ID] {
			groups[rule.InstanceID] = append(groups[rule.InstanceID], rule)
			selected = append(selected, rule)
		}
	}
	if len(selected) != len(wanted) {
		return nil, nil, fmt.Errorf("部分规则已不存在，请刷新后重新选择")
	}
	return groups, selected, nil
}

func exportSelectedRules(ctx context.Context, state State, key []byte, wanted map[string]bool, revision uint64) ([]byte, error) {
	groups, selected, err := selectedRuleGroups(state, wanted)
	if err != nil {
		return nil, err
	}
	if err := validateExportFallbacks(selected); err != nil {
		return nil, err
	}
	clients := make([]string, 0, len(groups))
	for id := range groups {
		clients = append(clients, id)
	}
	sort.Strings(clients)
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	store := &Store{key: key}
	total := 0
	for _, id := range clients {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("导出已取消")
		}
		client, ok := state.Instance(id)
		if !ok || client.Kind != "frpc" || !idPattern.MatchString(id) {
			return nil, fmt.Errorf("所选规则的客户端实例不可用")
		}
		content, err := renderRuleExport(store, client, groups[id], revision, maxRuleExportSize-total)
		if err != nil {
			return nil, err
		}
		total += len(content)
		header := &zip.FileHeader{Name: "frpc-" + id + ".toml", Method: zip.Deflate}
		header.SetMode(0600)
		entry, err := archive.CreateHeader(header)
		if err != nil {
			return nil, fmt.Errorf("无法创建导出文件")
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			return nil, fmt.Errorf("无法写入导出文件")
		}
	}
	if err := archive.Close(); err != nil || buffer.Len() > maxRuleExportSize+(4<<20) {
		return nil, fmt.Errorf("无法完成导出文件或内容过大")
	}
	return buffer.Bytes(), nil
}

// A plain TOML is only unambiguous when every selected rule belongs to one
// client. It has the same credential and fallback boundaries as ZIP export.
func exportSelectedRulesTOML(ctx context.Context, state State, key []byte, wanted map[string]bool, revision uint64) ([]byte, string, error) {
	if ctx.Err() != nil {
		return nil, "", fmt.Errorf("导出已取消")
	}
	groups, selected, err := selectedRuleGroups(state, wanted)
	if err != nil {
		return nil, "", err
	}
	if len(groups) != 1 {
		return nil, "", fmt.Errorf("单份 TOML 只能包含同一个客户端的规则")
	}
	if err := validateExportFallbacks(selected); err != nil {
		return nil, "", err
	}
	for id, rules := range groups {
		client, ok := state.Instance(id)
		if !ok || client.Kind != "frpc" || !idPattern.MatchString(id) {
			return nil, "", fmt.Errorf("所选规则的客户端实例不可用")
		}
		store := &Store{key: key}
		content, err := renderRuleExport(store, client, rules, revision, maxRuleExportSize)
		if err != nil {
			return nil, "", err
		}
		if ctx.Err() != nil {
			return nil, "", fmt.Errorf("导出已取消")
		}
		return []byte(content), id, nil
	}
	return nil, "", fmt.Errorf("请选择要导出的规则")
}

// The browser must confirm plaintext export; the route is administrator-only.
func (a *App) batchTunnelExportAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Revision  uint64   `json:"revision"`
		IDs       []string `json:"ids"`
		Confirmed bool     `json:"confirmed"`
		Format    string   `json:"format,omitempty"`
	}
	if err := decode(w, r, &req); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if !req.Confirmed || len(req.IDs) == 0 || len(req.IDs) > maxTunnelCount {
		fail(w, http.StatusBadRequest, fmt.Errorf("请确认明文导出并选择 1–512 条规则"))
		return
	}
	if req.Format != "" && req.Format != "zip" && req.Format != "toml" {
		fail(w, http.StatusBadRequest, fmt.Errorf("导出格式仅支持 zip 或 toml"))
		return
	}
	wanted := make(map[string]bool, len(req.IDs))
	for _, id := range req.IDs {
		if !idPattern.MatchString(id) || wanted[id] {
			fail(w, http.StatusBadRequest, fmt.Errorf("规则 ID 无效或重复"))
			return
		}
		wanted[id] = true
	}
	if !a.reserveBackup() {
		fail(w, http.StatusServiceUnavailable, fmt.Errorf("导出或备份正在进行，或工作台正在关闭"))
		return
	}
	defer a.releaseBackup()
	a.mu.Lock()
	if req.Revision != a.store.DB.Revision {
		a.mu.Unlock()
		fail(w, http.StatusConflict, fmt.Errorf("草稿版本已变化，请刷新后重新选择"))
		return
	}
	state := a.store.DB.State.clone()
	key := append([]byte(nil), a.store.key...)
	a.mu.Unlock()
	defer func() { clear(key) }()
	var output []byte
	var clientID string
	var err error
	if req.Format == "toml" {
		output, clientID, err = exportSelectedRulesTOML(r.Context(), state, key, wanted, req.Revision)
	} else {
		output, err = exportSelectedRules(r.Context(), state, key, wanted, req.Revision)
	}
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	defer func() { clear(output) }()
	if r.Context().Err() != nil {
		return
	}
	a.mu.Lock()
	stale := a.closing || a.store.DB.Revision != req.Revision
	a.mu.Unlock()
	if stale {
		fail(w, http.StatusConflict, fmt.Errorf("草稿版本已变化，请刷新后重新选择"))
		return
	}
	if req.Format == "toml" {
		w.Header().Set("Content-Type", "application/toml; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="frpc-`+clientID+`.toml"`)
	} else {
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="kurumi-selected-rules.zip"`)
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(output)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(output)
}
