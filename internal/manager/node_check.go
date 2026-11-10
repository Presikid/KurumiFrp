package manager

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// checkRemoteNode is shared by explicit checks and background monitoring.
// It never holds the application lock during DNS, TLS or response reads.
func (a *App) checkRemoteNode(parent context.Context, id, stamp string) (remoteNode, nodeSummary, int, error) {
	var summary nodeSummary
	target, _, code, err := a.reserveNodeLookup(id, stamp)
	if err != nil {
		return target, summary, code, err
	}
	defer a.releaseNodeLookup()
	if len(target.Addresses) == 0 {
		return target, summary, http.StatusConflict, fmt.Errorf("请先核对并确认节点解析地址")
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	resolved, err := resolveNodeAddresses(ctx, target.URL)
	if err != nil {
		return target, summary, http.StatusBadGateway, err
	}
	if !sameNodeAddresses(resolved, target.Addresses) {
		return target, summary, http.StatusConflict, fmt.Errorf("节点解析地址已变化，请先核对并确认")
	}
	dial, err := pinnedNodeDialer(target.URL, target.Addresses)
	if err != nil {
		return target, summary, http.StatusBadGateway, fmt.Errorf("节点地址不可用")
	}
	// The exact record must still be current before decrypting its credential.
	a.mu.Lock()
	_, current := a.nodeWithStampLocked(id, stamp)
	if !current || a.closing || ctx.Err() != nil {
		a.mu.Unlock()
		return target, summary, http.StatusConflict, fmt.Errorf("节点设置已变化或检查已取消")
	}
	credential, err := a.store.decrypt(target.Credential)
	a.mu.Unlock()
	if err != nil || !nodeTokenPattern.MatchString(credential) {
		return target, summary, http.StatusInternalServerError, fmt.Errorf("无法读取节点授权")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.URL+"/api/node/summary", nil)
	if err != nil {
		return target, summary, http.StatusBadGateway, fmt.Errorf("节点地址不可用")
	}
	request.Header.Set("Authorization", "Bearer "+credential)
	request.Header.Set("Accept", "application/json")
	transport := &http.Transport{Proxy: nil, DialContext: dial, DisableKeepAlives: true, TLSHandshakeTimeout: 3 * time.Second, ResponseHeaderTimeout: 4 * time.Second, MaxResponseHeaderBytes: 16 << 10}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return target, summary, http.StatusBadGateway, fmt.Errorf("节点不可用或 HTTPS 证书不受信任，请检查地址和网络")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return target, summary, http.StatusBadGateway, fmt.Errorf("目标拒绝授权，可能已撤销或替换")
	}
	if response.StatusCode != http.StatusOK {
		return target, summary, http.StatusBadGateway, fmt.Errorf("节点未返回可用状态（HTTP %d）", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxNodeResponse+1))
	if err != nil || len(body) > maxNodeResponse {
		return target, summary, http.StatusBadGateway, fmt.Errorf("节点响应无效或过大")
	}
	if err = decodeStrictJSON(body, &summary); err != nil || !validNodeSummary(summary) {
		return target, summary, http.StatusBadGateway, fmt.Errorf("节点状态格式不受支持")
	}
	a.mu.Lock()
	_, current = a.nodeWithStampLocked(id, stamp)
	current = current && !a.closing && ctx.Err() == nil
	a.mu.Unlock()
	if !current {
		return target, nodeSummary{}, http.StatusConflict, fmt.Errorf("节点设置已变化或检查已取消")
	}
	return target, summary, http.StatusOK, nil
}
