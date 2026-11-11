package manager

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

type outboundAgentAPIRequest struct {
	Revision uint64
	Action string
	URL string
	NodeID string
	Credential string
	Stamp string
	Addresses []string
	ShareTraffic *bool
	Enabled *bool
}

type outboundAgentView struct {
	Configured bool `json:"configured"`
	URL string `json:"url,omitempty"`
	NodeID string `json:"nodeId,omitempty"`
	HasCredential bool `json:"hasCredential"`
	Addresses []string `json:"addresses"`
	Enabled bool `json:"enabled"`
	ShareTraffic bool `json:"shareTraffic"`
	Stamp string `json:"stamp,omitempty"`
	Status outboundAgentStatus `json:"status"`
}

func (a *App) outboundAgentView(cfg *outboundAgentConfig) outboundAgentView {
	view := outboundAgentView{Addresses: []string{}, Status: outboundAgentStatus{State: "disabled"}}
	if cfg == nil {
		return view
	}
	view.Configured, view.URL, view.NodeID = true, cfg.URL, cfg.NodeID
	view.HasCredential, view.Enabled, view.ShareTraffic = cfg.Credential != "", cfg.Enabled, cfg.ShareTraffic
	view.Addresses, view.Stamp = append([]string{}, cfg.Addresses...), cfg.Stamp
	if a.agentReporter != nil {
		view.Status = a.agentReporter.view(cfg.Stamp, cfg.Enabled)
	} else if cfg.Enabled {
		view.Status = outboundAgentStatus{State: "blocked", Message: "上报工作线程不可用。"}
	}
	return view
}

// App.mu is held. The revision and complete configuration token must both
// match the admin's view, including when no settings exist yet.
func (a *App) outboundAgentCheckLocked(revision uint64, stamp string) (*outboundAgentConfig, int, error) {
	if a.closing {
		return nil, http.StatusServiceUnavailable, fmt.Errorf("工作台正在关闭")
	}
	if revision != a.store.DB.Revision {
		return nil, http.StatusConflict, fmt.Errorf("配置已变化，请刷新后重试")
	}
	var cfg *outboundAgentConfig
	if a.store.DB.Nodes != nil {
		cfg = cloneOutboundAgentConfig(a.store.DB.Nodes.Reporter)
	}
	if (cfg == nil && stamp != "") || (cfg != nil && cfg.Stamp != stamp) {
		return nil, http.StatusConflict, fmt.Errorf("上报设置已变化，请刷新后重试")
	}
	return cfg, http.StatusOK, nil
}

// App.mu is held. No worker is started or stopped until after it is released.
func (a *App) outboundAgentCommitLocked(next *outboundAgentConfig) (uint64, error) {
	cfg := cloneNodeSettings(a.store.DB.Nodes)
	cfg.Reporter = cloneOutboundAgentConfig(next)
	db := a.store.DB
	if cfg.ReaderHash == "" && cfg.ControlHash == "" && len(cfg.Nodes) == 0 && cfg.Reporter == nil {
		db.Nodes = nil
	} else {
		db.Nodes = &cfg
	}
	if err := incrementRevision(&db); err != nil {
		return 0, err
	}
	if err := a.store.write(db); err != nil {
		return 0, err
	}
	a.store.DB = db
	return db.Revision, nil
}

func nextOutboundAgentStamp(previous string) (string, error) {
	stamp := randomHex(32)
	if !nodeTokenPattern.MatchString(stamp) || stamp == previous {
		return "", fmt.Errorf("无法分配上报设置标识")
	}
	return stamp, nil
}

func (a *App) outboundAgentAPI(w http.ResponseWriter, r *http.Request, req outboundAgentAPIRequest) {
	if a.agentReporter == nil {
		fail(w, http.StatusServiceUnavailable, fmt.Errorf("节点上报不可用"))
		return
	}
	if req.Action == "reporter-resolve" || req.Action == "reporter-confirm" {
		a.outboundAgentAddressesAPI(w, r, req)
		return
	}
	m := a.agentReporter
	m.gate.Lock()
	defer m.gate.Unlock()
	a.mu.Lock()
	current, code, err := a.outboundAgentCheckLocked(req.Revision, req.Stamp)
	if err != nil {
		a.mu.Unlock()
		fail(w, code, err)
		return
	}
	var next *outboundAgentConfig
	switch req.Action {
	case "reporter-save":
		origin, err := normalizeNodeURL(req.URL)
		if err != nil || !idPattern.MatchString(req.NodeID) || req.ShareTraffic == nil {
			a.mu.Unlock()
			fail(w, http.StatusBadRequest, fmt.Errorf("上报地址、节点 ID 或流量共享选项无效"))
			return
		}
		if req.Credential != "" && !nodeTokenPattern.MatchString(req.Credential) {
			a.mu.Unlock()
			fail(w, http.StatusBadRequest, fmt.Errorf("上报授权格式无效"))
			return
		}
		if req.Credential == "" && (current == nil || current.URL != origin || current.NodeID != req.NodeID) {
			a.mu.Unlock()
			fail(w, http.StatusBadRequest, fmt.Errorf("新增或更换目标时需要上报授权"))
			return
		}
		credential := ""
		var addresses []string
		if current != nil {
			if current.URL == origin {
				addresses = append([]string(nil), current.Addresses...)
			}
			if current.URL == origin && current.NodeID == req.NodeID {
				credential = current.Credential
			}
		}
		if req.Credential != "" {
			credential, err = a.store.encrypt(req.Credential)
			if err != nil {
				a.mu.Unlock()
				fail(w, http.StatusInternalServerError, fmt.Errorf("无法保存上报授权"))
				return
			}
		}
		previous := ""
		if current != nil {
			previous = current.Stamp
		}
		stamp, err := nextOutboundAgentStamp(previous)
		if err != nil {
			a.mu.Unlock()
			fail(w, http.StatusInternalServerError, err)
			return
		}
		next = &outboundAgentConfig{URL: origin, NodeID: req.NodeID, Credential: credential, Addresses: addresses, ShareTraffic: *req.ShareTraffic, Stamp: stamp}
	case "reporter-enable":
		if current == nil || req.Enabled == nil {
			a.mu.Unlock()
			fail(w, http.StatusBadRequest, fmt.Errorf("请先保存上报设置并指定启用状态"))
			return
		}
		if *req.Enabled && len(current.Addresses) == 0 {
			a.mu.Unlock()
			fail(w, http.StatusConflict, fmt.Errorf("请先核对并确认目标解析地址"))
			return
		}
		next = current
		next.Enabled = *req.Enabled
		next.Stamp, err = nextOutboundAgentStamp(current.Stamp)
		if err != nil {
			a.mu.Unlock()
			fail(w, http.StatusInternalServerError, err)
			return
		}
	case "reporter-delete":
		if current == nil {
			a.mu.Unlock()
			fail(w, http.StatusConflict, fmt.Errorf("尚未设置节点上报"))
			return
		}
	default:
		a.mu.Unlock()
		fail(w, http.StatusBadRequest, fmt.Errorf("未知节点上报操作"))
		return
	}
	revision, err := a.outboundAgentCommitLocked(next)
	a.mu.Unlock()
	if err != nil {
		fail(w, http.StatusInternalServerError, fmt.Errorf("无法确认上报设置的保存结果，请刷新核对"))
		return
	}
	// Cancellation and the join happen after releasing App.mu. Data already
	// delivered to the network or receiver cannot be recalled.
	m.restartLocked(a)
	reply(w, http.StatusOK, map[string]any{"ok": true, "revision": revision})
}

func (a *App) outboundAgentAddressesAPI(w http.ResponseWriter, r *http.Request, req outboundAgentAPIRequest) {
	a.mu.Lock()
	current, code, err := a.outboundAgentCheckLocked(req.Revision, req.Stamp)
	if err == nil && current == nil {
		code, err = http.StatusConflict, fmt.Errorf("请先保存上报设置")
	}
	if err == nil && req.Action == "reporter-confirm" && current.Enabled {
		code, err = http.StatusConflict, fmt.Errorf("请先停用节点上报")
	}
	a.mu.Unlock()
	if err != nil {
		fail(w, code, err)
		return
	}
	if req.Action == "reporter-confirm" && !validPinnedAddresses(req.Addresses, false) {
		fail(w, http.StatusBadRequest, fmt.Errorf("待确认的解析地址无效"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	addresses, err := resolveNodeAddresses(ctx, current.URL)
	if err != nil {
		fail(w, http.StatusBadGateway, fmt.Errorf("无法解析上报目标，请检查网络和 DNS"))
		return
	}
	if req.Action == "reporter-resolve" {
		a.mu.Lock()
		_, code, err = a.outboundAgentCheckLocked(req.Revision, req.Stamp)
		if ctx.Err() != nil && err == nil {
			code, err = http.StatusConflict, fmt.Errorf("解析已取消，请重新操作")
		}
		a.mu.Unlock()
		if err != nil {
			fail(w, code, err)
			return
		}
		reply(w, http.StatusOK, map[string]any{"revision": req.Revision, "stamp": req.Stamp, "addresses": addresses})
		return
	}
	if !sameNodeAddresses(addresses, req.Addresses) {
		fail(w, http.StatusConflict, fmt.Errorf("解析结果已变化，请重新核对"))
		return
	}
	m := a.agentReporter
	m.gate.Lock()
	defer m.gate.Unlock()
	a.mu.Lock()
	current, code, err = a.outboundAgentCheckLocked(req.Revision, req.Stamp)
	if err == nil && (current == nil || current.Enabled || ctx.Err() != nil) {
		code, err = http.StatusConflict, fmt.Errorf("上报设置已变化，请重新核对")
	}
	if err != nil {
		a.mu.Unlock()
		fail(w, code, err)
		return
	}
	current.Addresses = append([]string(nil), addresses...)
	current.Stamp, err = nextOutboundAgentStamp(current.Stamp)
	if err != nil {
		a.mu.Unlock()
		fail(w, http.StatusInternalServerError, err)
		return
	}
	revision, err := a.outboundAgentCommitLocked(current)
	a.mu.Unlock()
	if err != nil {
		fail(w, http.StatusInternalServerError, fmt.Errorf("无法确认地址保存结果，请刷新核对"))
		return
	}
	m.restartLocked(a)
	reply(w, http.StatusOK, map[string]any{"ok": true, "revision": revision})
}
