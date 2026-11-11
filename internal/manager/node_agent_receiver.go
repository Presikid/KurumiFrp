package manager

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	inboundAgentFormat         = 1
	inboundAgentMaxNodes       = 32
	inboundAgentMaxRetired     = 16384
	inboundAgentMaxFile        = 1 << 20
	inboundAgentMaxRequest     = 96 << 10
	inboundAgentInterval       = 30
	inboundAgentOfflineAfter   = 90 * time.Second
	inboundAgentMaxSequence    = ^uint64(0) - 1
)

// The durable file is local authority. It contains neither bearer credentials
// nor observations, and must not be included in portable configuration data.
type inboundAgentGrant struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Hash string `json:"hash,omitempty"`
}

type inboundAgentDisk struct {
	Format   int                 `json:"format"`
	Revision uint64              `json:"revision"`
	Nodes    []inboundAgentGrant `json:"nodes"`
	Retired  []string            `json:"retired"`
}

type inboundAgentSession struct {
	token      string
	sequence   uint64
	digest     [sha256.Size]byte
	summary    nodeSummary
	openedAt   time.Time
	lastSeen   time.Time
	hasReport  bool
}

// One registry belongs to one running manager. The surrounding application
// must serialize access to the data directory across manager processes.
type inboundAgentRegistry struct {
	mu       sync.Mutex
	path     string
	disk     inboundAgentDisk
	sessions map[string]*inboundAgentSession
	slots    chan struct{}
	closed   bool
}

type inboundAgentView struct {
	ID       string       `json:"id"`
	Name     string       `json:"name"`
	State    string       `json:"state"`
	LastSeen string       `json:"lastSeen,omitempty"`
	Summary  *nodeSummary `json:"summary,omitempty"`
}

type inboundAgentList struct {
	Revision uint64             `json:"revision"`
	Nodes    []inboundAgentView `json:"nodes"`
}

type inboundAgentAdminRequest struct {
	Action   string `json:"action"`
	Revision uint64 `json:"revision"`
	ID       string `json:"id"`
	Name     string `json:"name"`
}

type inboundAgentAdminResponse struct {
	OK         bool                `json:"ok"`
	Revision   uint64              `json:"revision"`
	Node       *inboundAgentLabel  `json:"node,omitempty"`
	Credential string              `json:"credential,omitempty"`
}

type inboundAgentLabel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Loading a malformed file fails closed. A missing file has no authorized
// nodes; it is created only when an administrator issues the first grant.
func newInboundAgentRegistry(store *Store) (*inboundAgentRegistry, error) {
	if store == nil || store.dir == "" {
		return nil, fmt.Errorf("主动节点存储不可用")
	}
	path := filepath.Join(store.dir, "node-agents.json")
	disk := inboundAgentDisk{Format: inboundAgentFormat, Revision: 1, Nodes: []inboundAgentGrant{}, Retired: []string{}}
	data, err := privateFileLimit(path, inboundAgentMaxFile)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("无法读取主动节点授权，原文件已保留")
	}
	if err == nil {
		var stored inboundAgentDisk
		if decodeStrictJSON(data, &stored) != nil || !validInboundAgentDisk(stored) {
			return nil, fmt.Errorf("主动节点授权文件无效，原文件已保留")
		}
		disk = stored
	}
	return &inboundAgentRegistry{path: path, disk: disk, sessions: make(map[string]*inboundAgentSession), slots: make(chan struct{}, 4)}, nil
}

func validInboundAgentDisk(disk inboundAgentDisk) bool {
	if disk.Format != inboundAgentFormat || disk.Revision == 0 || disk.Revision > maxJSONRevision || disk.Nodes == nil || disk.Retired == nil ||
		len(disk.Nodes) > inboundAgentMaxNodes || len(disk.Retired)+len(disk.Nodes) > inboundAgentMaxRetired {
		return false
	}
	ids := make(map[string]bool, len(disk.Nodes)+len(disk.Retired))
	hashes := make(map[string]bool, len(disk.Nodes))
	for _, id := range disk.Retired {
		if !idPattern.MatchString(id) || ids[id] {
			return false
		}
		ids[id] = true
	}
	for _, node := range disk.Nodes {
		if !idPattern.MatchString(node.ID) || ids[node.ID] || !nodeNameOK(node.Name) {
			return false
		}
		if node.Hash != "" && (!nodeTokenPattern.MatchString(node.Hash) || hashes[node.Hash]) {
			return false
		}
		ids[node.ID] = true
		if node.Hash != "" {
			hashes[node.Hash] = true
		}
	}
	return true
}

func cloneInboundAgentDisk(disk inboundAgentDisk) inboundAgentDisk {
	disk.Nodes = append([]inboundAgentGrant{}, disk.Nodes...)
	disk.Retired = append([]string{}, disk.Retired...)
	return disk
}

func inboundAgentRandomHex(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

func inboundAgentHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Caller holds mu. No bearer secret is retained after a successful save.
func (registry *inboundAgentRegistry) newCredentialLocked() (string, string, error) {
	for attempts := 0; attempts < 16; attempts++ {
		token, err := inboundAgentRandomHex(32)
		if err != nil {
			return "", "", err
		}
		hash := inboundAgentHash(token)
		unique := true
		for _, node := range registry.disk.Nodes {
			if node.Hash == hash {
				unique = false
				break
			}
		}
		if unique {
			return token, hash, nil
		}
	}
	return "", "", fmt.Errorf("unable to create unique credential")
}

func (registry *inboundAgentRegistry) newIDLocked() (string, error) {
	used := make(map[string]bool, len(registry.disk.Nodes)+len(registry.disk.Retired))
	for _, node := range registry.disk.Nodes {
		used[node.ID] = true
	}
	for _, id := range registry.disk.Retired {
		used[id] = true
	}
	for attempts := 0; attempts < 16; attempts++ {
		id, err := inboundAgentRandomHex(8)
		if err != nil {
			return "", err
		}
		if !used[id] {
			return id, nil
		}
	}
	return "", fmt.Errorf("unable to create unique node ID")
}

func (registry *inboundAgentRegistry) saveLocked(next inboundAgentDisk) error {
	if !validInboundAgentDisk(next) {
		return fmt.Errorf("invalid active node state")
	}
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil || len(data) > inboundAgentMaxFile {
		return fmt.Errorf("invalid active node state")
	}
	if _, err = writeIfChanged(registry.path, data); err != nil {
		return err
	}
	registry.disk = next
	return nil
}

// applyAdmin performs only the durable operation. The eventual HTTP route
// must authenticate an administrator, decode the request, and write the reply
// after this method has returned. Revision is independent of configuration.
func (registry *inboundAgentRegistry) applyAdmin(req inboundAgentAdminRequest) (inboundAgentAdminResponse, int, error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.closed {
		return inboundAgentAdminResponse{}, http.StatusServiceUnavailable, fmt.Errorf("工作台正在关闭")
	}
	if req.Revision != registry.disk.Revision {
		return inboundAgentAdminResponse{}, http.StatusConflict, fmt.Errorf("主动节点设置已变化，请刷新后重试")
	}
	if registry.disk.Revision >= maxJSONRevision {
		return inboundAgentAdminResponse{}, http.StatusConflict, fmt.Errorf("主动节点版本号已达上限")
	}
	next := cloneInboundAgentDisk(registry.disk)
	result := inboundAgentAdminResponse{OK: true}
	invalid := func() (inboundAgentAdminResponse, int, error) {
		return inboundAgentAdminResponse{}, http.StatusBadRequest, fmt.Errorf("主动节点请求无效")
	}
	index := -1
	for i := range next.Nodes {
		if next.Nodes[i].ID == req.ID {
			index = i
			break
		}
	}
	switch req.Action {
	case "issue":
		if req.ID != "" || !nodeNameOK(req.Name) {
			return invalid()
		}
		// Reserve one retired-ID slot for every live node, so deletion never
		// needs to forget an identity when the historical limit is reached.
		if len(next.Nodes) >= inboundAgentMaxNodes || len(next.Nodes)+len(next.Retired) >= inboundAgentMaxRetired {
			return inboundAgentAdminResponse{}, http.StatusConflict, fmt.Errorf("主动节点数量或历史身份已达上限")
		}
		id, err := registry.newIDLocked()
		if err != nil {
			return inboundAgentAdminResponse{}, http.StatusInternalServerError, fmt.Errorf("无法创建节点身份")
		}
		token, hash, err := registry.newCredentialLocked()
		if err != nil {
			return inboundAgentAdminResponse{}, http.StatusInternalServerError, fmt.Errorf("无法生成主动节点凭据")
		}
		next.Nodes = append(next.Nodes, inboundAgentGrant{ID: id, Name: req.Name, Hash: hash})
		result.Node = &inboundAgentLabel{ID: id, Name: req.Name}
		result.Credential = token
	case "rotate", "revoke", "delete":
		if !idPattern.MatchString(req.ID) || req.Name != "" {
			return invalid()
		}
		if index < 0 {
			return inboundAgentAdminResponse{}, http.StatusNotFound, fmt.Errorf("主动节点不存在")
		}
		switch req.Action {
		case "rotate":
			token, hash, err := registry.newCredentialLocked()
			if err != nil {
				return inboundAgentAdminResponse{}, http.StatusInternalServerError, fmt.Errorf("无法生成主动节点凭据")
			}
			next.Nodes[index].Hash = hash
			result.Node = &inboundAgentLabel{ID: next.Nodes[index].ID, Name: next.Nodes[index].Name}
			result.Credential = token
		case "revoke":
			next.Nodes[index].Hash = ""
		case "delete":
			next.Retired = append(next.Retired, req.ID)
			next.Nodes = append(next.Nodes[:index], next.Nodes[index+1:]...)
		}
	default:
		return invalid()
	}
	next.Revision++
	if err := registry.saveLocked(next); err != nil {
		return inboundAgentAdminResponse{}, http.StatusInternalServerError, fmt.Errorf("无法保存主动节点授权")
	}
	if req.Action != "issue" {
		delete(registry.sessions, req.ID)
	}
	result.Revision = next.Revision
	return result, http.StatusOK, nil
}

// close prevents subsequent admissions or administrative changes. In-flight
// uploads must still be interrupted or joined by the owning HTTP server.
func (registry *inboundAgentRegistry) close() {
	registry.mu.Lock()
	registry.closed = true
	registry.sessions = make(map[string]*inboundAgentSession)
	registry.mu.Unlock()
}

func cloneInboundAgentSummary(summary nodeSummary) nodeSummary {
	summary.Instances = append([]nodeInstanceSummary{}, summary.Instances...)
	for i := range summary.Instances {
		item := &summary.Instances[i]
		// A reporting credential never confers remote-control permission.
		item.CanControl = false
		if item.Traffic != nil {
			traffic := *item.Traffic
			if traffic.ReceivedBytes != nil {
				value := *traffic.ReceivedBytes
				traffic.ReceivedBytes = &value
			}
			if traffic.SentBytes != nil {
				value := *traffic.SentBytes
				traffic.SentBytes = &value
			}
			item.Traffic = &traffic
		}
	}
	return summary
}

// list is a point-in-time administrator view. Only a newly accepted report
// updates lastSeen; duplicate retries do not keep an unreachable node online.
func (registry *inboundAgentRegistry) list() inboundAgentList {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	result := inboundAgentList{Revision: registry.disk.Revision, Nodes: make([]inboundAgentView, 0, len(registry.disk.Nodes))}
	current := time.Now()
	for _, node := range registry.disk.Nodes {
		view := inboundAgentView{ID: node.ID, Name: node.Name, State: "pending"}
		if node.Hash == "" {
			view.State = "revoked"
		} else if session := registry.sessions[node.ID]; session != nil {
			if session.hasReport {
				view.State = "online"
				if current.Sub(session.lastSeen) >= inboundAgentOfflineAfter {
					view.State = "offline"
				}
				view.LastSeen = session.lastSeen.UTC().Format(time.RFC3339)
				summary := cloneInboundAgentSummary(session.summary)
				view.Summary = &summary
			} else if current.Sub(session.openedAt) >= inboundAgentOfflineAfter {
				view.State = "offline"
			}
		}
		result.Nodes = append(result.Nodes, view)
	}
	return result
}

// inboundAgentAPI is available only after the local administrator check.
func (a *App) inboundAgentAPI(w http.ResponseWriter, r *http.Request) {
	if !requestPrincipal(r).admin() {
		fail(w, http.StatusForbidden, fmt.Errorf("仅管理员可管理主动节点"))
		return
	}
	if a.agentReceiver == nil {
		fail(w, http.StatusServiceUnavailable, fmt.Errorf("主动节点授权无法读取，原文件已保留；检查后重新启动"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		reply(w, http.StatusOK, a.agentReceiver.list())
	case http.MethodPost:
		var request inboundAgentAdminRequest
		if err := decode(w, r, &request); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		result, status, err := a.agentReceiver.applyAdmin(request)
		if err != nil {
			fail(w, status, err)
			return
		}
		reply(w, status, result)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func inboundAgentBearer(r *http.Request) string {
	values := r.Header.Values("Authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
		return ""
	}
	token := strings.TrimPrefix(values[0], "Bearer ")
	if !nodeTokenPattern.MatchString(token) {
		return ""
	}
	return token
}

// Caller holds mu. An identity is selected only by its distinct credential,
// never by a node ID or other data provided in an incoming report.
func (registry *inboundAgentRegistry) authenticateLocked(token string) string {
	if token == "" {
		return ""
	}
	hash := inboundAgentHash(token)
	id := ""
	for _, node := range registry.disk.Nodes {
		if node.Hash != "" && subtle.ConstantTimeCompare([]byte(hash), []byte(node.Hash)) == 1 {
			id = node.ID
		}
	}
	return id
}

func inboundAgentReadBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return nil, fmt.Errorf("请求内容类型无效")
	}
	if r.ContentLength > inboundAgentMaxRequest {
		return nil, fmt.Errorf("请求过大")
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, inboundAgentMaxRequest))
	if err != nil {
		return nil, fmt.Errorf("请求无效或过大")
	}
	return data, nil
}

type inboundAgentSessionRequest struct {
	Protocol int `json:"protocol"`
}

type inboundAgentSessionResponse struct {
	Protocol        int    `json:"protocol"`
	NodeID          string `json:"nodeId"`
	Session         string `json:"session"`
	NextSequence    string `json:"nextSequence"`
	IntervalSeconds int    `json:"intervalSeconds"`
}

type inboundAgentReportRequest struct {
	Protocol int         `json:"protocol"`
	Session  string      `json:"session"`
	Sequence string      `json:"sequence"`
	Summary  nodeSummary `json:"summary"`
}

type inboundAgentReportResponse struct {
	Protocol        int    `json:"protocol"`
	Sequence        string `json:"sequence"`
	NextSequence    string `json:"nextSequence"`
	IntervalSeconds int    `json:"intervalSeconds"`
}

// serveAgent is intended only for the two exact node-agent paths, behind the
// existing loopback Host checks and an administrator-managed HTTPS proxy.
// Local login cookies and the existing node read/control tokens do not apply.
func (registry *inboundAgentRegistry) serveAgent(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/node-agent/session" && r.URL.Path != "/api/node-agent/report" {
		fail(w, http.StatusNotFound, fmt.Errorf("接口不存在"))
		return
	}
	if r.Method != http.MethodPost || r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.RawPath != "" {
		fail(w, http.StatusMethodNotAllowed, fmt.Errorf("请求方法或路径无效"))
		return
	}
	token := inboundAgentBearer(r)
	registry.mu.Lock()
	closed := registry.closed
	authorized := !closed && registry.authenticateLocked(token) != ""
	registry.mu.Unlock()
	if closed {
		fail(w, http.StatusServiceUnavailable, fmt.Errorf("工作台正在关闭"))
		return
	}
	if !authorized {
		fail(w, http.StatusUnauthorized, fmt.Errorf("主动节点凭据无效或已撤销"))
		return
	}
	select {
	case registry.slots <- struct{}{}:
		defer func() { <-registry.slots }()
	default:
		fail(w, http.StatusServiceUnavailable, fmt.Errorf("主动节点请求繁忙，请稍后重试"))
		return
	}
	body, err := inboundAgentReadBody(w, r)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if r.URL.Path == "/api/node-agent/session" {
		var request inboundAgentSessionRequest
		if decodeStrictJSON(body, &request) != nil || request.Protocol != 1 {
			fail(w, http.StatusBadRequest, fmt.Errorf("会话请求无效"))
			return
		}
		session, err := inboundAgentRandomHex(32)
		if err != nil {
			fail(w, http.StatusServiceUnavailable, fmt.Errorf("无法创建节点会话"))
			return
		}
		registry.mu.Lock()
		if registry.closed {
			registry.mu.Unlock()
			fail(w, http.StatusServiceUnavailable, fmt.Errorf("工作台正在关闭"))
			return
		}
		id := registry.authenticateLocked(token)
		if id == "" {
			registry.mu.Unlock()
			fail(w, http.StatusUnauthorized, fmt.Errorf("主动节点凭据无效或已撤销"))
			return
		}
		if r.Context().Err() != nil {
			registry.mu.Unlock()
			fail(w, http.StatusServiceUnavailable, fmt.Errorf("节点会话请求已取消"))
			return
		}
		registry.sessions[id] = &inboundAgentSession{token: session, openedAt: time.Now()}
		registry.mu.Unlock()
		reply(w, http.StatusOK, inboundAgentSessionResponse{Protocol: 1, NodeID: id, Session: session, NextSequence: "1", IntervalSeconds: inboundAgentInterval})
		return
	}
	var request inboundAgentReportRequest
	if decodeStrictJSON(body, &request) != nil || request.Protocol != 1 || !nodeTokenPattern.MatchString(request.Session) || !validNodeSummary(request.Summary) {
		fail(w, http.StatusBadRequest, fmt.Errorf("节点上报格式无效"))
		return
	}
	sequence, err := strconv.ParseUint(request.Sequence, 10, 64)
	if err != nil || sequence == 0 || sequence > inboundAgentMaxSequence || strconv.FormatUint(sequence, 10) != request.Sequence {
		fail(w, http.StatusBadRequest, fmt.Errorf("节点上报序号无效"))
		return
	}
	digest := sha256.Sum256(body)
	registry.mu.Lock()
	if registry.closed {
		registry.mu.Unlock()
		fail(w, http.StatusServiceUnavailable, fmt.Errorf("工作台正在关闭"))
		return
	}
	id := registry.authenticateLocked(token)
	if id == "" {
		registry.mu.Unlock()
		fail(w, http.StatusUnauthorized, fmt.Errorf("主动节点凭据无效或已撤销"))
		return
	}
	session := registry.sessions[id]
	if session == nil || subtle.ConstantTimeCompare([]byte(session.token), []byte(request.Session)) != 1 || r.Context().Err() != nil {
		registry.mu.Unlock()
		fail(w, http.StatusConflict, fmt.Errorf("节点会话已失效，请重新建立"))
		return
	}
	receivedAt := time.Now()
	freshSince := session.openedAt
	if session.hasReport {
		freshSince = session.lastSeen
	}
	if receivedAt.Sub(freshSince) >= inboundAgentOfflineAfter {
		registry.mu.Unlock()
		fail(w, http.StatusConflict, fmt.Errorf("节点会话已失效，请重新建立"))
		return
	}
	if sequence == session.sequence && session.hasReport && subtle.ConstantTimeCompare(digest[:], session.digest[:]) == 1 {
		// A retransmission is acknowledged without extending the online lease.
	} else if sequence == session.sequence+1 {
		session.sequence = sequence
		session.digest = digest
		session.summary = cloneInboundAgentSummary(request.Summary)
		session.lastSeen = receivedAt
		session.hasReport = true
	} else {
		registry.mu.Unlock()
		fail(w, http.StatusConflict, fmt.Errorf("节点上报序号冲突，请重新建立会话"))
		return
	}
	registry.mu.Unlock()
	reply(w, http.StatusOK, inboundAgentReportResponse{Protocol: 1, Sequence: request.Sequence, NextSequence: strconv.FormatUint(sequence+1, 10), IntervalSeconds: inboundAgentInterval})
}
