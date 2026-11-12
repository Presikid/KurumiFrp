package manager

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Presikid/KurumiFrp/internal/version"
)

// Node settings are local authority, not portable configuration or snapshot data.
const maxRemoteNodes = 32
const maxNodeChecks = 4
const maxNodeResponse = 64 << 10

var nodeTokenPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var nodeDNSLabel = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

type remoteNode struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	URL               string   `json:"url"`
	Credential        string   `json:"credential"`
	ControlCredential string   `json:"controlCredential,omitempty"`
	Addresses         []string `json:"addresses,omitempty"`
	MonitorEnabled    bool     `json:"monitorEnabled,omitempty"`
}

type nodeSettings struct {
	ReaderHash   string       `json:"readerHash,omitempty"`
	ControlHash  string       `json:"controlHash,omitempty"`
	ShareTraffic bool         `json:"shareTraffic,omitempty"`
	Nodes        []remoteNode `json:"nodes,omitempty"`
	Reporter     *outboundAgentConfig `json:"reporter,omitempty"`
}

type nodeInstanceSummary struct {
	ID         string       `json:"id"`
	Name       string       `json:"name"`
	Kind       string       `json:"kind"`
	State      string       `json:"state"`
	Binding    string       `json:"binding,omitempty"`
	CanControl bool         `json:"canControl,omitempty"`
	Traffic    *nodeTraffic `json:"traffic,omitempty"`
}

// Counters describe the target's current run, not a durable usage total.
type nodeTraffic struct {
	SampledAt     time.Time `json:"sampledAt"`
	ReceivedBytes *uint64   `json:"receivedBytes,string"`
	SentBytes     *uint64   `json:"sentBytes,string"`
}

// This non-secret binding changes when the target instance is replaced, even
// if an imported configuration reuses its public instance ID.
func nodeInstanceBinding(localID string) string {
	if !validLocalTrafficID(localID) {
		return ""
	}
	sum := sha256.Sum256([]byte("kurumifrp-node-instance-v1:" + localID))
	return hex.EncodeToString(sum[:])
}

type nodeSummary struct {
	Protocol  int                   `json:"protocol"`
	Version   string                `json:"version"`
	Instances []nodeInstanceSummary `json:"instances"`
}

func nodeNameOK(name string) bool {
	if !utf8.ValidString(name) || name == "" || len(name) > 256 || strings.TrimSpace(name) != name || utf8.RuneCountInString(name) > 64 {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// Only an HTTPS origin is accepted. No user info, paths, query or fragment can
// become a credential-bearing request to an unexpected endpoint.
func normalizeNodeURL(raw string) (string, error) {
	if len(raw) > 512 || strings.TrimSpace(raw) != raw {
		return "", fmt.Errorf("节点地址无效")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil || u.Host == "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.RawPath != "" || (u.Path != "" && u.Path != "/") {
		return "", fmt.Errorf("节点地址必须是 HTTPS 域名或 IP，不含路径、账号或参数")
	}
	host := u.Hostname()
	if host == "" || strings.Contains(host, "%") {
		return "", fmt.Errorf("节点地址无效")
	}
	if net.ParseIP(host) == nil {
		dns := strings.TrimSuffix(host, ".")
		if len(dns) > 253 || dns == "" {
			return "", fmt.Errorf("节点域名无效")
		}
		for _, label := range strings.Split(dns, ".") {
			if !nodeDNSLabel.MatchString(label) {
				return "", fmt.Errorf("节点域名无效")
			}
		}
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("节点端口无效")
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return "", fmt.Errorf("节点端口无效")
	}
	u.Host = strings.ToLower(u.Host)
	u.Path = ""
	return u.String(), nil
}

func validateStoredNodes(s *Store, cfg *nodeSettings) error {
	if cfg == nil {
		return nil
	}
	if (cfg.ReaderHash != "" && !nodeTokenPattern.MatchString(cfg.ReaderHash)) ||
		(cfg.ControlHash != "" && (!nodeTokenPattern.MatchString(cfg.ControlHash) || cfg.ControlHash == cfg.ReaderHash)) {
		return fmt.Errorf("invalid node authorization")
	}
	if cfg.ShareTraffic && cfg.ReaderHash == "" {
		return fmt.Errorf("node traffic sharing requires read authorization")
	}
	if err := validateStoredOutboundAgent(s, cfg.Reporter); err != nil {
		return err
	}
	if len(cfg.Nodes) > maxRemoteNodes {
		return fmt.Errorf("too many remote nodes")
	}
	ids, origins := map[string]bool{}, map[string]bool{}
	for _, n := range cfg.Nodes {
		origin, err := normalizeNodeURL(n.URL)
		if err != nil || origin != n.URL || !idPattern.MatchString(n.ID) || !nodeNameOK(n.Name) || !validPinnedAddresses(n.Addresses, true) || ids[n.ID] || origins[n.URL] {
			return fmt.Errorf("invalid node settings")
		}
		plain, err := s.decrypt(n.Credential)
		if err != nil || !nodeTokenPattern.MatchString(plain) {
			return fmt.Errorf("invalid node credential")
		}
		if n.ControlCredential != "" {
			control, err := s.decrypt(n.ControlCredential)
			if err != nil || !nodeTokenPattern.MatchString(control) {
				return fmt.Errorf("invalid node control credential")
			}
		}
		ids[n.ID], origins[n.URL] = true, true
	}
	return nil
}

func cloneNodeSettings(cfg *nodeSettings) nodeSettings {
	if cfg == nil {
		return nodeSettings{}
	}
	copy := nodeSettings{ReaderHash: cfg.ReaderHash, ControlHash: cfg.ControlHash, ShareTraffic: cfg.ShareTraffic, Nodes: append([]remoteNode(nil), cfg.Nodes...), Reporter: cloneOutboundAgentConfig(cfg.Reporter)}
	for i := range copy.Nodes {
		copy.Nodes[i].Addresses = append([]string(nil), copy.Nodes[i].Addresses...)
	}
	return copy
}

// Rewrap local credentials whenever a configuration import replaces the key.
func rewrapNodeSettings(oldStore, newStore *Store, cfg *nodeSettings) (*nodeSettings, error) {
	if cfg == nil {
		return nil, nil
	}
	if err := validateStoredNodes(oldStore, cfg); err != nil {
		return nil, err
	}
	next := cloneNodeSettings(cfg)
	if next.Reporter != nil {
		plain, err := oldStore.decrypt(next.Reporter.Credential)
		if err != nil {
			return nil, err
		}
		next.Reporter.Credential, err = newStore.encrypt(plain)
		if err != nil {
			return nil, err
		}
	}
	for i := range next.Nodes {
		plain, err := oldStore.decrypt(next.Nodes[i].Credential)
		if err != nil {
			return nil, err
		}
		next.Nodes[i].Credential, err = newStore.encrypt(plain)
		if err != nil {
			return nil, err
		}
		if next.Nodes[i].ControlCredential != "" {
			control, err := oldStore.decrypt(next.Nodes[i].ControlCredential)
			if err != nil {
				return nil, err
			}
			next.Nodes[i].ControlCredential, err = newStore.encrypt(control)
			if err != nil {
				return nil, err
			}
		}
	}
	return &next, nil
}

// A digest of the complete local record identifies exactly what a browser saw.
// It contains no plaintext credential; encrypted values are already randomized.
func nodeStamp(node remoteNode) string {
	encoded, _ := json.Marshal(node) // A struct of strings and slices cannot fail.
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func (a *App) nodesAPI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/nodes/control" {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		a.nodeControlProxyAPI(w, r)
		return
	}
	if r.URL.Path == "/api/nodes/resolve" || r.URL.Path == "/api/nodes/confirm" {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path == "/api/nodes/resolve" {
			a.nodeResolveAPI(w, r)
		} else {
			a.nodeConfirmAPI(w, r)
		}
		return
	}
	if r.URL.Path == "/api/nodes/check" {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		a.nodeCheckAPI(w, r)
		return
	}
	if r.Method == http.MethodGet {
		a.mu.Lock()
		cfg, revision := cloneNodeSettings(a.store.DB.Nodes), a.store.DB.Revision
		monitors := make(map[string]nodeMonitorView)
		for _, node := range cfg.Nodes {
			if observation, ok := a.nodeMonitors[node.ID]; ok && observation.stamp == nodeStamp(node) && node.MonitorEnabled {
				monitors[node.ID] = nodeMonitorView{State: observation.state, Reason: observation.reason, CheckedAt: observation.checkedAt, LastSuccessAt: observation.lastSuccessAt}
			}
		}
		a.mu.Unlock()
		principal := requestPrincipal(r)
		if !principal.admin() {
			type sharedNode struct {
				ID string `json:"id"`
				Name string `json:"name"`
				Stamp string `json:"stamp"`
				Ready bool `json:"ready"`
			}
			items := make([]sharedNode, 0)
			for _, n := range cfg.Nodes {
				if principal.canViewRemoteNode(n.ID) {
					items = append(items, sharedNode{ID: n.ID, Name: n.Name, Stamp: nodeStamp(n), Ready: len(n.Addresses) != 0})
				}
			}
			reply(w, http.StatusOK, map[string]any{"nodes": items})
			return
		}
		type view struct {
			ID         string   `json:"id"`
			Name       string   `json:"name"`
			URL        string   `json:"url"`
			Stamp      string   `json:"stamp"`
			Addresses  []string `json:"addresses"`
			HasControl bool     `json:"hasControl"`
			MonitorEnabled bool `json:"monitorEnabled"`
			MonitorStatus *nodeMonitorView `json:"monitorStatus,omitempty"`
		}
		items := make([]view, 0, len(cfg.Nodes))
		for _, n := range cfg.Nodes {
			item := view{ID: n.ID, Name: n.Name, URL: n.URL, Stamp: nodeStamp(n), Addresses: append([]string{}, n.Addresses...), HasControl: n.ControlCredential != "", MonitorEnabled: n.MonitorEnabled}
			if observation, ok := monitors[n.ID]; ok {
				item.MonitorStatus = &observation
			}
			items = append(items, item)
		}
		reply(w, http.StatusOK, map[string]any{"revision": revision, "sharing": cfg.ReaderHash != "", "shareTraffic": cfg.ShareTraffic, "controlSharing": cfg.ControlHash != "", "nodes": items, "reporter": a.outboundAgentView(cfg.Reporter)})
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Revision          uint64 `json:"revision"`
		Action            string `json:"action"`
		ID                string `json:"id"`
		Name              string `json:"name"`
		URL               string `json:"url"`
		Credential        string `json:"credential"`
		ControlCredential string `json:"controlCredential"`
		ClearControl      bool   `json:"clearControl"`
		Enabled           *bool  `json:"enabled"`
		NodeID            string `json:"nodeId"`
		Stamp             string `json:"stamp"`
		Addresses         []string `json:"addresses"`
		ShareTraffic      *bool `json:"shareTraffic"`
	}
	if err := decode(w, r, &req); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	switch req.Action {
	case "reporter-save", "reporter-resolve", "reporter-confirm", "reporter-enable", "reporter-delete":
		a.outboundAgentAPI(w, r, outboundAgentAPIRequest{Revision: req.Revision, Action: req.Action, URL: req.URL, NodeID: req.NodeID, Credential: req.Credential, Stamp: req.Stamp, Addresses: req.Addresses, ShareTraffic: req.ShareTraffic, Enabled: req.Enabled})
		return
	}
	// The gate orders changes to the inbound grant after all admitted summary
	// writes. Always acquire it before mu and release both before writing back.
	grantChange := req.Action == "issue" || req.Action == "revoke" || req.Action == "share-traffic"
	controlChange := req.Action == "issue-control" || req.Action == "revoke-control"
	if grantChange {
		a.nodeAccessGate.Lock()
	}
	if controlChange {
		a.nodeControlGate.Lock()
	}
	a.mu.Lock()
	locked := true
	unlock := func() {
		if locked {
			a.mu.Unlock()
			if controlChange {
				a.nodeControlGate.Unlock()
			}
			if grantChange {
				a.nodeAccessGate.Unlock()
			}
			locked = false
		}
	}
	defer unlock()
	reject := func(code int, err error) { unlock(); fail(w, code, err) }
	if a.closing {
		reject(503, fmt.Errorf("工作台正在关闭"))
		return
	}
	if req.Revision != a.store.DB.Revision {
		reject(409, fmt.Errorf("配置已变化，请刷新后重试"))
		return
	}
	cfg := cloneNodeSettings(a.store.DB.Nodes)
	issued := ""
	switch req.Action {
	case "issue":
		issued = randomHex(32)
		hash := sha256.Sum256([]byte(issued))
		cfg.ReaderHash = hex.EncodeToString(hash[:])
		if cfg.ReaderHash == cfg.ControlHash {
			reject(500, fmt.Errorf("无法生成授权，请重试"))
			return
		}
	case "revoke":
		cfg.ReaderHash = ""
		cfg.ShareTraffic = false
	case "share-traffic":
		if req.Enabled == nil || cfg.ReaderHash == "" {
			reject(400, fmt.Errorf("请先启用只读授权并指定流量共享状态"))
			return
		}
		cfg.ShareTraffic = *req.Enabled
	case "issue-control":
		issued = randomHex(32)
		hash := sha256.Sum256([]byte(issued))
		cfg.ControlHash = hex.EncodeToString(hash[:])
		if cfg.ControlHash == cfg.ReaderHash {
			reject(500, fmt.Errorf("无法生成授权，请重试"))
			return
		}
	case "revoke-control":
		cfg.ControlHash = ""
	case "save":
		origin, err := normalizeNodeURL(req.URL)
		if err != nil {
			reject(400, err)
			return
		}
		if !nodeNameOK(req.Name) {
			reject(400, fmt.Errorf("名称须为 1 至 64 个非控制字符，且首尾不能有空格"))
			return
		}
		index := -1
		for i, n := range cfg.Nodes {
			if n.ID == req.ID {
				index = i
			}
			if n.URL == origin && n.ID != req.ID {
				reject(400, fmt.Errorf("该地址已经添加"))
				return
			}
		}
		if req.ID != "" && index < 0 {
			reject(404, fmt.Errorf("节点不存在"))
			return
		}
		if index < 0 && len(cfg.Nodes) >= maxRemoteNodes {
			reject(400, fmt.Errorf("最多添加 32 个节点"))
			return
		}
		if index < 0 || (index >= 0 && cfg.Nodes[index].URL != origin) {
			if req.Credential == "" {
				reject(400, fmt.Errorf("新增或更改地址时需要重新输入目标节点授权"))
				return
			}
		}
		if req.Credential != "" && !nodeTokenPattern.MatchString(req.Credential) {
			reject(400, fmt.Errorf("节点授权格式无效"))
			return
		}
		if req.ClearControl && req.ControlCredential != "" {
			reject(400, fmt.Errorf("不能同时设置和清除控制授权"))
			return
		}
		if req.ControlCredential != "" && !nodeTokenPattern.MatchString(req.ControlCredential) {
			reject(400, fmt.Errorf("控制授权格式无效"))
			return
		}
		credential, controlCredential := "", ""
		monitorEnabled := false
		var addresses []string
		if index >= 0 {
			credential = cfg.Nodes[index].Credential
			if cfg.Nodes[index].URL == origin {
				addresses = append([]string(nil), cfg.Nodes[index].Addresses...)
				controlCredential = cfg.Nodes[index].ControlCredential
				monitorEnabled = cfg.Nodes[index].MonitorEnabled
			}
		}
		if req.ClearControl {
			controlCredential = ""
		}
		if req.ControlCredential != "" {
			controlCredential, err = a.store.encrypt(req.ControlCredential)
			if err != nil {
				reject(500, fmt.Errorf("无法保存控制授权"))
				return
			}
		}
		if req.Credential != "" {
			credential, err = a.store.encrypt(req.Credential)
			if err != nil {
				reject(500, fmt.Errorf("无法保存节点授权"))
				return
			}
		}
		if index < 0 {
			id := randomHex(8)
			for _, n := range cfg.Nodes {
				if n.ID == id {
					reject(500, fmt.Errorf("无法分配节点标识，请重试"))
					return
				}
			}
			cfg.Nodes = append(cfg.Nodes, remoteNode{ID: id, Name: req.Name, URL: origin, Credential: credential, ControlCredential: controlCredential})
		} else {
			cfg.Nodes[index] = remoteNode{ID: req.ID, Name: req.Name, URL: origin, Credential: credential, ControlCredential: controlCredential, Addresses: addresses, MonitorEnabled: monitorEnabled}
		}
	case "monitor":
		if req.Enabled == nil {
			reject(400, fmt.Errorf("请指定后台检查状态"))
			return
		}
		index := -1
		for i, node := range cfg.Nodes {
			if node.ID == req.ID {
				index = i
				break
			}
		}
		if index < 0 {
			reject(404, fmt.Errorf("节点不存在"))
			return
		}
		if *req.Enabled && len(cfg.Nodes[index].Addresses) == 0 {
			reject(409, fmt.Errorf("请先核对并确认节点解析地址"))
			return
		}
		cfg.Nodes[index].MonitorEnabled = *req.Enabled
	case "delete":
		index := -1
		for i, n := range cfg.Nodes {
			if n.ID == req.ID {
				index = i
				break
			}
		}
		if index < 0 {
			reject(404, fmt.Errorf("节点不存在"))
			return
		}
		cfg.Nodes = append(cfg.Nodes[:index], cfg.Nodes[index+1:]...)
	default:
		reject(400, fmt.Errorf("未知节点操作"))
		return
	}
	db := a.store.DB
	if cfg.ReaderHash == "" && cfg.ControlHash == "" && len(cfg.Nodes) == 0 && cfg.Reporter == nil {
		db.Nodes = nil
	} else {
		db.Nodes = &cfg
	}
	if err := incrementRevision(&db); err != nil {
		reject(409, err)
		return
	}
	if err := a.store.write(db); err != nil {
		reject(500, fmt.Errorf("无法保存节点设置"))
		return
	}
	a.store.DB = db
	if req.Action == "save" || req.Action == "monitor" || req.Action == "delete" {
		delete(a.nodeMonitors, req.ID)
	}
	unlock()
	result := map[string]any{"ok": true, "revision": db.Revision}
	if issued != "" {
		result["credential"] = issued
	}
	reply(w, 200, result)
}

// This endpoint accepts ONLY the separate read-only credential, never a local
// session or management token. It remains behind the existing loopback listener.
func (a *App) nodeSummaryAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.RawQuery != "" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	// Bound slow readers before taking the gate. Flush before releasing it so
	// revocation cannot complete while a previously authorized write is pending.
	controller := http.NewResponseController(w)
	if err := controller.SetWriteDeadline(time.Now().Add(2 * time.Second)); err != nil {
		fail(w, 503, fmt.Errorf("节点状态暂不可用"))
		return
	}
	// Keep the deadline in force for the server’s final flush as well.
	a.nodeAccessGate.RLock()
	defer a.nodeAccessGate.RUnlock()
	credentialHeader := r.Header.Get("Authorization")
	token := strings.TrimPrefix(credentialHeader, "Bearer ")
	sum := sha256.Sum256([]byte(token))
	candidate := hex.EncodeToString(sum[:])
	a.mu.Lock()
	cfg := a.store.DB.Nodes
	if cfg == nil || cfg.ReaderHash == "" || !strings.HasPrefix(credentialHeader, "Bearer ") || !nodeTokenPattern.MatchString(token) || subtle.ConstantTimeCompare([]byte(candidate), []byte(cfg.ReaderHash)) != 1 {
		a.mu.Unlock()
		fail(w, http.StatusUnauthorized, fmt.Errorf("节点授权无效或已撤销"))
		return
	}
	if a.closing {
		a.mu.Unlock()
		fail(w, 503, fmt.Errorf("节点不可用"))
		return
	}
	type trafficSource struct {
		index int
		proc  *process
		runID string
	}
	instances := make([]nodeInstanceSummary, 0, len(a.store.DB.State.Instances))
	sources := make([]trafficSource, 0)
	revision := a.store.DB.Revision
	for _, i := range a.store.DB.State.Instances {
		state := "stopped"
		p := a.procs[i.ID]
		status := RuntimeStatus{}
		if p != nil {
			status = p.view()
			state = status.State
		}
		// Protocol 1 readers accept a fixed state set. Do not present a
		// transition as stopped or send an enum old readers reject.
		if state == "starting" || state == "stopping" {
			a.mu.Unlock()
			fail(w, http.StatusServiceUnavailable, fmt.Errorf("实例正在启动或停止，请稍后重试"))
			_ = controller.Flush()
			return
		}
		if i.AutoStart && a.startupErrors[i.ID] != "" && state != "running" {
			state = "autostart-failed"
		}
		instances = append(instances, nodeInstanceSummary{ID: i.ID, Name: i.Name, Kind: i.Kind, State: state, Binding: nodeInstanceBinding(a.store.DB.LocalTrafficIDs[i.ID])})
		if cfg.ShareTraffic && state == "running" && p != nil && status.RunID != "" {
			sources = append(sources, trafficSource{index: len(instances) - 1, proc: p, runID: status.RunID})
		}
	}
	a.mu.Unlock()
	// Readers may lock a runtime and copy counters. Never hold App.mu while
	// calling them, and reject a result if its run or configuration changed.
	for _, source := range sources {
		var at time.Time
		var received, sent uint64
		available := false
		if instances[source.index].Kind == "frpc" {
			if reader, ok := source.proc.instance.(RuntimeTrafficReader); ok {
				snapshot := reader.ReadTraffic()
				at, received, sent, available = snapshot.SampledAt, snapshot.ReceivedBytes, snapshot.SentBytes, snapshot.Available
			}
		} else if reader, ok := source.proc.instance.(RuntimeServerTrafficReader); ok {
			snapshot := reader.ReadServerTraffic()
			at, received, sent, available = snapshot.SampledAt, snapshot.ReceivedBytes, snapshot.SentBytes, snapshot.Available
		}
		now := time.Now()
		if available && received != ^uint64(0) && sent != ^uint64(0) && !at.IsZero() && !at.After(now.Add(trafficFutureSkew)) && now.Sub(at) <= trafficFreshness {
			instances[source.index].Traffic = &nodeTraffic{SampledAt: at.UTC(), ReceivedBytes: &received, SentBytes: &sent}
		}
	}
	if len(sources) != 0 {
		a.mu.Lock()
		valid := !a.closing && revision == a.store.DB.Revision
		for _, source := range sources {
			if !valid || a.procs[instances[source.index].ID] != source.proc {
				valid = false
				break
			}
			status := source.proc.view()
			if status.State != "running" || status.RunID != source.runID {
				valid = false
				break
			}
		}
		a.mu.Unlock()
		if !valid {
			fail(w, http.StatusServiceUnavailable, fmt.Errorf("实例状态已变化，请稍后重试"))
			_ = controller.Flush()
			return
		}
	}
	reply(w, 200, nodeSummary{Protocol: 1, Version: version.Current, Instances: instances})
	_ = controller.Flush()
}

func validNodeSummary(s nodeSummary) bool {
	return validNodeSummaryWithTransitions(s, false)
}

func validNodeSummaryWithTransitions(s nodeSummary, allowTransitions bool) bool {
	if s.Protocol != 1 || s.Version == "" || len(s.Version) > 80 || !utf8.ValidString(s.Version) || len(s.Instances) > 64 {
		return false
	}
	for _, r := range s.Version {
		if unicode.IsControl(r) {
			return false
		}
	}
	ids := map[string]bool{}
	for _, i := range s.Instances {
		if !idPattern.MatchString(i.ID) || ids[i.ID] || !utf8.ValidString(i.Name) || !labelOK(i.Name) || (i.Kind != "frpc" && i.Kind != "frps") {
			return false
		}
		if i.Binding != "" && !nodeTokenPattern.MatchString(i.Binding) {
			return false
		}
		if i.Traffic != nil {
			if i.State != "running" || i.Traffic.SampledAt.IsZero() || i.Traffic.ReceivedBytes == nil || i.Traffic.SentBytes == nil || *i.Traffic.ReceivedBytes == ^uint64(0) || *i.Traffic.SentBytes == ^uint64(0) {
				return false
			}
		}
		switch i.State {
		case "running", "stopped", "exited", "autostart-failed":
		case "starting", "stopping":
			if !allowTransitions {
				return false
			}
		default:
			return false
		}
		ids[i.ID] = true
	}
	return true
}

func (a *App) nodeCheckAPI(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID    string `json:"id"`
		Stamp string `json:"stamp"`
	}
	if err := decode(w, r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	principal := requestPrincipal(r)
	if !principal.canViewRemoteNode(req.ID) {
		fail(w, http.StatusForbidden, fmt.Errorf("没有此远程节点的查看权限"))
		return
	}
	target, summary, code, err := a.checkRemoteNode(r.Context(), req.ID, req.Stamp)
	if err != nil {
		fail(w, code, err)
		return
	}
	if !principal.admin() {
		visible := make([]nodeInstanceSummary, 0)
		for _, instance := range summary.Instances {
			if principal.canViewRemoteInstance(req.ID, instance.ID, instance.Binding) {
				_, instance.CanControl = principal.remoteControlGrant(req.ID, instance.ID)
				instance.CanControl = instance.CanControl && principal.canPerformRemoteAction() && target.ControlCredential != ""
				if !principal.canReadTraffic() {
					instance.Traffic = nil
				}
				visible = append(visible, instance)
			}
		}
		summary.Instances = visible
	}
	reply(w, 200, map[string]any{"id": target.ID, "stamp": req.Stamp, "checkedAt": now(), "summary": summary})
}
