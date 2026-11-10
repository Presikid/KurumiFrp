package manager

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"time"
)

const maxNodeAddresses = 16

// Preserve all returned addresses, including private/loopback addresses, and
// reject oversized sets instead of silently selecting a different destination.
func validPinnedAddresses(addresses []string, allowEmpty bool) bool {
	if len(addresses) == 0 {
		return allowEmpty
	}
	if len(addresses) > maxNodeAddresses {
		return false
	}
	for i, value := range addresses {
		ip, err := netip.ParseAddr(value)
		if err != nil || !ip.IsValid() || ip.Zone() != "" || ip.IsUnspecified() || ip.IsMulticast() || ip.Unmap().String() != value || (i > 0 && addresses[i-1] >= value) {
			return false
		}
	}
	return true
}

func sameNodeAddresses(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func resolveNodeAddresses(ctx context.Context, origin string) ([]string, error) {
	u, err := url.Parse(origin)
	if err != nil {
		return nil, fmt.Errorf("节点地址无效")
	}
	var ips []netip.Addr
	if ip, parseErr := netip.ParseAddr(u.Hostname()); parseErr == nil {
		ips = []netip.Addr{ip}
	} else {
		ips, err = net.DefaultResolver.LookupNetIP(ctx, "ip", u.Hostname())
		if err != nil {
			return nil, fmt.Errorf("无法解析节点地址，请检查网络和 DNS")
		}
	}
	unique := make(map[string]bool, len(ips))
	for _, ip := range ips {
		ip = ip.Unmap()
		if !ip.IsValid() || ip.Zone() != "" || ip.IsUnspecified() || ip.IsMulticast() {
			return nil, fmt.Errorf("节点解析包含不可用地址")
		}
		unique[ip.String()] = true
		if len(unique) > maxNodeAddresses {
			return nil, fmt.Errorf("节点解析地址过多，无法安全确认")
		}
	}
	addresses := make([]string, 0, len(unique))
	for address := range unique {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	if !validPinnedAddresses(addresses, false) {
		return nil, fmt.Errorf("节点没有可用解析地址")
	}
	return addresses, nil
}

// Caller holds a.mu. Nodes and their address slices are never mutated in place.
func (a *App) nodeWithStampLocked(id, stamp string) (remoteNode, bool) {
	if !idPattern.MatchString(id) || !nodeTokenPattern.MatchString(stamp) || a.store.DB.Nodes == nil {
		return remoteNode{}, false
	}
	for _, node := range a.store.DB.Nodes.Nodes {
		if node.ID == id && nodeStamp(node) == stamp {
			return node, true
		}
	}
	return remoteNode{}, false
}

// Reserve a bounded slot and take a snapshot before doing DNS or network I/O.
func (a *App) reserveNodeLookup(id, stamp string) (remoteNode, uint64, int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	node, ok := a.nodeWithStampLocked(id, stamp)
	if !ok {
		return remoteNode{}, 0, 409, fmt.Errorf("节点设置已变化，请刷新列表后重试")
	}
	if a.closing || a.activeNodeChecks >= maxNodeChecks {
		return remoteNode{}, 0, 503, fmt.Errorf("检查过多或工作台正在关闭，请稍后再试")
	}
	node.Addresses = append([]string(nil), node.Addresses...)
	a.activeNodeChecks++
	return node, a.store.DB.Revision, 0, nil
}

func (a *App) releaseNodeLookup() {
	a.mu.Lock()
	a.activeNodeChecks--
	a.mu.Unlock()
}

func (a *App) nodeResolveAPI(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID    string `json:"id"`
		Stamp string `json:"stamp"`
	}
	if err := decode(w, r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	node, _, code, err := a.reserveNodeLookup(req.ID, req.Stamp)
	if err != nil {
		fail(w, code, err)
		return
	}
	defer a.releaseNodeLookup()
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	addresses, err := resolveNodeAddresses(ctx, node.URL)
	if err != nil {
		fail(w, 502, err)
		return
	}
	a.mu.Lock()
	_, current := a.nodeWithStampLocked(req.ID, req.Stamp)
	revision, closing := a.store.DB.Revision, a.closing
	a.mu.Unlock()
	if !current || closing {
		fail(w, 409, fmt.Errorf("节点设置已变化，请刷新列表后重试"))
		return
	}
	reply(w, 200, map[string]any{"id": node.ID, "stamp": req.Stamp, "revision": revision, "addresses": addresses})
}

func (a *App) nodeConfirmAPI(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID        string   `json:"id"`
		Stamp     string   `json:"stamp"`
		Revision  uint64   `json:"revision"`
		Addresses []string `json:"addresses"`
	}
	if err := decode(w, r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	if !validPinnedAddresses(req.Addresses, false) {
		fail(w, 400, fmt.Errorf("待确认的解析地址无效"))
		return
	}
	node, revision, code, err := a.reserveNodeLookup(req.ID, req.Stamp)
	if err != nil {
		fail(w, code, err)
		return
	}
	defer a.releaseNodeLookup()
	if revision != req.Revision {
		fail(w, 409, fmt.Errorf("配置已变化，请重新核对地址"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	addresses, err := resolveNodeAddresses(ctx, node.URL)
	if err != nil {
		fail(w, 502, err)
		return
	}
	if !sameNodeAddresses(addresses, req.Addresses) {
		fail(w, 409, fmt.Errorf("解析结果已变化，请重新核对"))
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.nodeWithStampLocked(req.ID, req.Stamp); !ok || a.closing || a.store.DB.Revision != req.Revision || r.Context().Err() != nil {
		fail(w, 409, fmt.Errorf("节点或配置已变化，请重新核对地址"))
		return
	}
	cfg := cloneNodeSettings(a.store.DB.Nodes)
	for i := range cfg.Nodes {
		if cfg.Nodes[i].ID == req.ID {
			cfg.Nodes[i].Addresses = append([]string(nil), addresses...)
			break
		}
	}
	db := a.store.DB
	db.Nodes = &cfg
	if err := incrementRevision(&db); err != nil {
		fail(w, 409, err)
		return
	}
	if err := a.store.write(db); err != nil {
		fail(w, 500, fmt.Errorf("无法保存已确认地址"))
		return
	}
	a.store.DB = db
	reply(w, 200, map[string]any{"ok": true, "revision": db.Revision})
}

// Dial only the exact addresses the user approved. TLS still verifies the
// original URL hostname, even if DNS changes between lookup and connection.
func pinnedNodeDialer(origin string, addresses []string) (func(context.Context, string, string) (net.Conn, error), error) {
	u, err := url.Parse(origin)
	if err != nil || !validPinnedAddresses(addresses, false) {
		return nil, fmt.Errorf("节点地址无效")
	}
	host, port := u.Hostname(), u.Port()
	if port == "" {
		port = "443"
	}
	pinned := append([]string(nil), addresses...)
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		requestedHost, requestedPort, err := net.SplitHostPort(address)
		if err != nil || network != "tcp" || !strings.EqualFold(requestedHost, host) || requestedPort != port {
			return nil, fmt.Errorf("节点连接目标不匹配")
		}
		dialer := net.Dialer{Timeout: 2 * time.Second}
		var last error
		for _, ip := range pinned {
			conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip, port))
			if err == nil {
				return conn, nil
			}
			last = err
			if ctx.Err() != nil {
				break
			}
		}
		return nil, last
	}, nil
}
