package manager

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
	"unicode/utf8"
)

// This endpoint has a separate control credential; a read-only grant can never
// operate instances. The preview and reservation are scoped to one instance.
type nodeControlPreview struct {
	Protocol int    `json:"protocol"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	State    string `json:"state"`
	Version  string `json:"version"`
}

// a.mu is held. A process exit has its own mutex, and a new process can only
// be published under a.mu. RunID prevents an old running instance matching a
// different process with the same displayed state.
func (a *App) nodeControlPreviewLocked(id string) (nodeControlPreview, error) {
	instance, exists := a.store.DB.State.Instance(id)
	if !exists {
		return nodeControlPreview{}, fmt.Errorf("实例不存在")
	}
	if a.closing || a.operations[id] != nil {
		return nodeControlPreview{}, fmt.Errorf("实例正在执行其他操作，请稍后重新检查")
	}
	state, runID := "stopped", ""
	if process := a.procs[id]; process != nil {
		status := process.view()
		state, runID = status.State, status.RunID
	}
	// Protocol 1 has no transition states; fail closed until this generation
	// has reached running or completed cleanup.
	if state == "starting" || state == "stopping" {
		return nodeControlPreview{}, fmt.Errorf("实例正在启动或停止，请稍后重新检查")
	}
	if instance.AutoStart && a.startupErrors[id] != "" && state != "running" {
		state = "autostart-failed"
	}
	if state != "running" && state != "stopped" && state != "exited" && state != "autostart-failed" {
		return nodeControlPreview{}, fmt.Errorf("实例状态未知")
	}
	value, _ := json.Marshal(struct {
		ID        string `json:"id"`
		Revision  uint64 `json:"revision"`
		State     string `json:"state"`
		RunID     string `json:"runId"`
		Epoch     string `json:"epoch"`
		Operation string `json:"operation"`
	}{id, a.store.DB.Revision, state, runID, a.nodeControlEpoch, a.nodeOperationVersions[id]})
	sum := sha256.Sum256(value)
	return nodeControlPreview{Protocol: 1, ID: id, Name: instance.Name, Kind: instance.Kind, State: state, Version: hex.EncodeToString(sum[:])}, nil
}

func validNodeControlRequest(id, action, version string, confirmed bool) bool {
	if !idPattern.MatchString(id) {
		return false
	}
	switch action {
	case "preview":
		return version == "" && !confirmed
	case "start", "stop", "restart":
		return confirmed && nodeTokenPattern.MatchString(version)
	default:
		return false
	}
}

func (a *App) nodeControlAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.RawQuery != "" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	header := r.Header.Get("Authorization")
	token := ""
	if len(header) > len("Bearer ") && header[:len("Bearer ")] == "Bearer " {
		token = header[len("Bearer "):]
	}
	hash := sha256.Sum256([]byte(token))
	candidate := hex.EncodeToString(hash[:])
	// Check before accepting a body, then check again under the revocation
	// gate before reserving work. No gate is held while the peer uploads.
	a.mu.Lock()
	cfg := a.store.DB.Nodes
	allowed := cfg != nil && cfg.ControlHash != "" && nodeTokenPattern.MatchString(token) && subtle.ConstantTimeCompare([]byte(candidate), []byte(cfg.ControlHash)) == 1
	a.mu.Unlock()
	if !allowed {
		fail(w, 401, fmt.Errorf("控制授权无效或已撤销"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	var req struct {
		Action    string `json:"action"`
		ID        string `json:"id"`
		Version   string `json:"version"`
		Confirmed bool   `json:"confirmed"`
	}
	if err := decode(w, r, &req); err != nil {
		fail(w, 400, fmt.Errorf("请求无效"))
		return
	}
	if !validNodeControlRequest(req.ID, req.Action, req.Version, req.Confirmed) {
		fail(w, 400, fmt.Errorf("请求无效"))
		return
	}
	var controller *http.ResponseController
	if req.Action == "preview" {
		controller = http.NewResponseController(w)
		if err := controller.SetWriteDeadline(time.Now().Add(2 * time.Second)); err != nil {
			fail(w, 503, fmt.Errorf("节点状态暂不可用"))
			return
		}
	}
	a.nodeControlGate.RLock()
	a.mu.Lock()
	cfg = a.store.DB.Nodes
	if cfg == nil || cfg.ControlHash == "" || !nodeTokenPattern.MatchString(token) || subtle.ConstantTimeCompare([]byte(candidate), []byte(cfg.ControlHash)) != 1 {
		a.mu.Unlock()
		a.nodeControlGate.RUnlock()
		fail(w, 401, fmt.Errorf("控制授权无效或已撤销"))
		return
	}
	preview, err := a.nodeControlPreviewLocked(req.ID)
	if err != nil {
		a.mu.Unlock()
		a.nodeControlGate.RUnlock()
		fail(w, 409, fmt.Errorf("实例不可用，请重新检查"))
		return
	}
	if req.Action == "preview" {
		a.mu.Unlock()
		defer a.nodeControlGate.RUnlock()
		reply(w, 200, preview)
		_ = controller.Flush()
		return
	}
	if preview.Version != req.Version || (req.Action == "start" && preview.State == "running") || ((req.Action == "stop" || req.Action == "restart") && preview.State != "running") {
		a.mu.Unlock()
		a.nodeControlGate.RUnlock()
		fail(w, 409, fmt.Errorf("实例状态或配置已变化，请重新检查"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
	defer cancel()
	runtimeReq := runtimeRequest{Revision: a.store.DB.Revision, ID: req.ID, Action: req.Action}
	snapshot, op, status, err := a.beginRuntimeOperationLocked(ctx, runtimeReq, false)
	a.mu.Unlock()
	a.nodeControlGate.RUnlock()
	if err != nil {
		if (req.Action == "start" || req.Action == "restart") && a.webhooks != nil {
			a.webhooks.emitInstance("instance.start_failed", snapshot.instance.ID, snapshot.instance.Kind)
		}
		fail(w, status, fmt.Errorf("实例操作未完成，请在目标工作台查看详情"))
		return
	}
	// Accepted operations may complete after a later revocation; no new
	// operation can reserve after revocation publishes the new hash.
	a.runRuntimeOperation(w, runtimeReq, false, principal{Role: "operator"}, snapshot, op)
}

func validControlPreview(preview nodeControlPreview, id string) bool {
	if preview.Protocol != 1 || preview.ID != id || !idPattern.MatchString(preview.ID) || !utf8.ValidString(preview.Name) || !labelOK(preview.Name) || (preview.Kind != "frpc" && preview.Kind != "frps") || !nodeTokenPattern.MatchString(preview.Version) {
		return false
	}
	switch preview.State {
	case "running", "stopped", "exited", "autostart-failed":
		return true
	}
	return false
}

// Local administrators use this proxy. DNS is checked against the approved
// addresses and the dialer never re-resolves; TLS verifies the original host.
func (a *App) nodeControlProxyAPI(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID         string `json:"id"`
		Stamp      string `json:"stamp"`
		InstanceID string `json:"instanceId"`
		Action     string `json:"action"`
		Version    string `json:"version"`
		Confirmed  bool   `json:"confirmed"`
	}
	if err := decode(w, r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	if !validNodeControlRequest(req.InstanceID, req.Action, req.Version, req.Confirmed) {
		fail(w, 400, fmt.Errorf("节点操作请求无效"))
		return
	}
	target, _, code, err := a.reserveNodeLookup(req.ID, req.Stamp)
	if err != nil {
		fail(w, code, err)
		return
	}
	defer a.releaseNodeLookup()
	if target.ControlCredential == "" {
		fail(w, 409, fmt.Errorf("请先设置目标节点控制授权"))
		return
	}
	if len(target.Addresses) == 0 {
		fail(w, 409, fmt.Errorf("请先核对节点解析地址"))
		return
	}
	timeout := 5 * time.Second
	if req.Action != "preview" {
		timeout = 40 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	dnsCtx, dnsCancel := context.WithTimeout(ctx, 5*time.Second)
	resolved, err := resolveNodeAddresses(dnsCtx, target.URL)
	dnsCancel()
	if err != nil {
		fail(w, 502, err)
		return
	}
	if !sameNodeAddresses(resolved, target.Addresses) {
		fail(w, 409, fmt.Errorf("节点解析地址已变化，请先核对"))
		return
	}
	dial, err := pinnedNodeDialer(target.URL, target.Addresses)
	if err != nil {
		fail(w, 502, fmt.Errorf("节点地址不可用"))
		return
	}
	a.mu.Lock()
	_, current := a.nodeWithStampLocked(req.ID, req.Stamp)
	if !current || a.closing || ctx.Err() != nil {
		a.mu.Unlock()
		fail(w, 409, fmt.Errorf("节点设置已变化或请求已取消"))
		return
	}
	credential, err := a.store.decrypt(target.ControlCredential)
	a.mu.Unlock()
	if err != nil || !nodeTokenPattern.MatchString(credential) {
		fail(w, 500, fmt.Errorf("无法读取控制授权"))
		return
	}
	payload, err := json.Marshal(struct {
		Action    string `json:"action"`
		ID        string `json:"id"`
		Version   string `json:"version"`
		Confirmed bool   `json:"confirmed"`
	}{req.Action, req.InstanceID, req.Version, req.Confirmed})
	if err != nil {
		fail(w, 500, fmt.Errorf("无法准备节点请求"))
		return
	}
	remote, err := http.NewRequestWithContext(ctx, http.MethodPost, target.URL+"/api/node/control", bytes.NewReader(payload))
	if err != nil {
		fail(w, 502, fmt.Errorf("节点地址不可用"))
		return
	}
	remote.Header.Set("Authorization", "Bearer "+credential)
	remote.Header.Set("Content-Type", "application/json")
	remote.Header.Set("Accept", "application/json")
	transport := &http.Transport{Proxy: nil, DialContext: dial, DisableKeepAlives: true, TLSHandshakeTimeout: 3 * time.Second, MaxResponseHeaderBytes: 16 << 10}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(remote)
	if err != nil {
		fail(w, 502, fmt.Errorf("节点连接失败；操作若已提交，结果可能未知，请先核对目标"))
		return
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		if req.Action == "preview" && (response.StatusCode == 401 || response.StatusCode == 403) {
			fail(w, 502, fmt.Errorf("目标拒绝控制授权"))
			return
		}
		if req.Action == "preview" && (response.StatusCode == 404 || response.StatusCode == 405) {
			fail(w, 502, fmt.Errorf("目标未提供远程控制，请检查目标版本和 HTTPS 转发"))
			return
		}
		fail(w, 502, fmt.Errorf("目标未确认操作，请先核对目标状态"))
		return
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (16<<10)+1))
	if err != nil || len(body) > 16<<10 {
		fail(w, 502, fmt.Errorf("目标响应无效，请核对目标状态"))
		return
	}
	a.mu.Lock()
	_, current = a.nodeWithStampLocked(req.ID, req.Stamp)
	a.mu.Unlock()
	if !current {
		fail(w, 409, fmt.Errorf("节点设置已变化；操作结果可能未知，请先核对目标"))
		return
	}
	if req.Action == "preview" {
		var preview nodeControlPreview
		if err = decodeStrictJSON(body, &preview); err != nil || !validControlPreview(preview, req.InstanceID) {
			fail(w, 502, fmt.Errorf("目标状态格式无效"))
			return
		}
		reply(w, 200, map[string]any{"id": target.ID, "stamp": req.Stamp, "preview": preview})
		return
	}
	var result struct {
		OK       bool   `json:"ok"`
		Revision uint64 `json:"revision"`
	}
	if err = decodeStrictJSON(body, &result); err != nil || !result.OK {
		fail(w, 502, fmt.Errorf("目标结果未确认，请先核对目标状态"))
		return
	}
	reply(w, 200, map[string]any{"ok": true, "id": target.ID, "stamp": req.Stamp, "instanceId": req.InstanceID})
}
