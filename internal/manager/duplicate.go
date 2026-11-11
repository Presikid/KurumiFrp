package manager

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

type duplicateRequest struct {
	Revision         uint64 `json:"revision"`
	Kind             string `json:"kind"`
	ID               string `json:"id"`
	IDs              []string `json:"ids,omitempty"`
	TargetInstanceID string `json:"targetInstanceId,omitempty"`
	IncludeTunnels   bool   `json:"includeTunnels,omitempty"`
}

// duplicateAPI is called under the application lock, after the normal
// loopback, origin and authentication checks. Duplicating never starts frp.
func (a *App) duplicateAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req duplicateRequest
	if err := decode(w, r, &req); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if req.Revision != a.store.DB.Revision {
		fail(w, http.StatusConflict, fmt.Errorf("配置已在其他窗口更新，请刷新后重试"))
		return
	}
	if req.Kind != "tunnels" && len(req.IDs) != 0 {
		fail(w, http.StatusBadRequest, fmt.Errorf("仅批量复制支持多个规则 ID"))
		return
	}
	var (
		id    string
		count int
		err   error
		ids   []string
	)
	switch req.Kind {
	case "instance":
		if req.TargetInstanceID != "" {
			fail(w, http.StatusBadRequest, fmt.Errorf("复制实例时不能指定目标实例"))
			return
		}
		id, count, err = a.duplicateInstance(req.ID, req.IncludeTunnels)
	case "tunnel":
		if req.IncludeTunnels {
			fail(w, http.StatusBadRequest, fmt.Errorf("复制单条规则时不能包含其他规则"))
			return
		}
		id, err = a.duplicateTunnel(req.ID, req.TargetInstanceID)
	case "tunnels":
		if req.ID != "" || req.IncludeTunnels {
			fail(w, http.StatusBadRequest, fmt.Errorf("批量复制请使用规则 ID 列表"))
			return
		}
		ids, err = a.duplicateTunnels(req.IDs, req.TargetInstanceID)
		count = len(ids)
	default:
		fail(w, http.StatusBadRequest, fmt.Errorf("只能复制实例或连接规则"))
		return
	}
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	result := map[string]any{
		"ok": true, "id": id, "rulesCopied": count,
		"revision": a.store.DB.Revision,
	}
	if req.Kind == "tunnels" {
		result["ids"] = ids
	}
	reply(w, http.StatusOK, result)
}

func duplicateInstanceName(st State, name string) (string, error) {
	used := make(map[string]bool, len(st.Instances))
	for _, instance := range st.Instances {
		used[instance.Name] = true
	}
	runes := []rune(name)
	for n := 1; n <= 10000; n++ {
		suffix := " 副本"
		if n > 1 {
			suffix += " " + strconv.Itoa(n)
		}
		maxBase := 80 - len([]rune(suffix))
		if maxBase <= 0 {
			break
		}
		base := runes
		if len(base) > maxBase {
			base = base[:maxBase]
		}
		candidate := strings.TrimSpace(string(base)) + suffix
		if !used[candidate] && labelOK(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("无法为实例分配不重复的副本名称")
}

func duplicateTunnelName(st State, instanceID, name string) (string, error) {
	used := make(map[string]bool)
	for _, tunnel := range st.Tunnels {
		if tunnel.InstanceID == instanceID {
			used[tunnel.Name] = true
		}
	}
	for n := 1; n <= 10000; n++ {
		suffix := "-copy"
		if n > 1 {
			suffix += "-" + strconv.Itoa(n)
		}
		maxBase := 64 - len(suffix)
		if maxBase <= 0 {
			break
		}
		base := name
		if len(base) > maxBase {
			base = base[:maxBase]
		}
		candidate := base + suffix
		if !used[candidate] && namePattern.MatchString(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("无法为规则分配不重复的副本名称")
}

// Select only a currently unclaimed configured TCP listener port. This does
// not assert that the operating system or another program has the port free.
func duplicateServerPort(st State, sourcePort int) (int, error) {
	used := make(map[int]bool)
	for _, instance := range st.Instances {
		if instance.Kind != "frps" {
			continue
		}
		used[instance.Port] = true
		if instance.HTTPPort != 0 {
			used[instance.HTTPPort] = true
		}
		if instance.HTTPSPort != 0 {
			used[instance.HTTPSPort] = true
		}
		if instance.TCPMuxHTTPConnectPort != 0 {
			used[instance.TCPMuxHTTPConnectPort] = true
		}
	}
	for _, tunnel := range st.Tunnels {
		if isVisitor(tunnel.Type) && visitorNetwork(tunnel.Type) == "tcp" && tunnel.Enabled && tunnel.LocalPort > 0 {
			used[tunnel.LocalPort] = true
		}
	}
	port := sourcePort + 1
	if port < 1024 || port > 65535 {
		port = 1024
	}
	for tries := 0; tries <= 65535-1024; tries++ {
		if !used[port] {
			return port, nil
		}
		port++
		if port > 65535 {
			port = 1024
		}
	}
	return 0, fmt.Errorf("没有可分配的服务端监听端口")
}

// Explicitly copy supported fields, so future execution and auto-start
// policies cannot accidentally be inherited from a source instance.
func duplicatedInstance(src Instance, id, name string) Instance {
	return Instance{
		ID: id, Name: name, Kind: src.Kind, Host: src.Host, Port: src.Port,
		HTTPPort: src.HTTPPort, HTTPSPort: src.HTTPSPort, Token: src.Token,
		AuthHeartbeats: src.AuthHeartbeats, AuthNewWorkConns: src.AuthNewWorkConns,
		User: src.User, NatHoleSTUNServer: src.NatHoleSTUNServer, DNSServer: src.DNSServer, ClientTransport: src.ClientTransport,
		ClientBindIP: src.ClientBindIP, ClientDialTimeout: src.ClientDialTimeout,
		TCPKeepalive: src.TCPKeepalive, MuxKeepalive: src.MuxKeepalive,
		LoginFailExit: src.LoginFailExit, ClientTLSDisabled: src.ClientTLSDisabled, ServerAllowPlaintext: src.ServerAllowPlaintext,
		TCPMuxHTTPConnectPort: src.TCPMuxHTTPConnectPort, TCPMuxPassthrough: src.TCPMuxPassthrough,
		SubDomainHost: src.SubDomainHost, MaxPortsPerClient: src.MaxPortsPerClient, ServerAllowedPorts: src.ServerAllowedPorts, ProxyBindAddr: src.ProxyBindAddr,
		ServerAccessMode: src.ServerAccessMode, ServerAccessList: src.ServerAccessList,
		VhostHTTPTimeout: src.VhostHTTPTimeout, UserConnTimeout: src.UserConnTimeout,
		ClientPoolCount: src.ClientPoolCount, ServerMaxPoolCount: src.ServerMaxPoolCount, TransportMuxDisabled: src.TransportMuxDisabled,
	}
}

// Sensitive tokens remain encrypted using the existing local state key.
// A cloned rule is disabled until the operator reviews its destination.
func duplicatedTunnel(src Tunnel, id, instanceID, name string) Tunnel {
	return Tunnel{
		ID: id, InstanceID: instanceID, Name: name, Type: src.Type,
		LocalHost: src.LocalHost, LocalPort: src.LocalPort, UnixSocketPath: src.UnixSocketPath,
		PluginType: src.PluginType, PluginUser: src.PluginUser, PluginPassword: src.PluginPassword,
		RemotePort: src.RemotePort, Domains: append([]string(nil), src.Domains...), Locations: append([]string(nil), src.Locations...),
		ProbePath: src.ProbePath, ProbeMethod: src.ProbeMethod,
		HealthCheckType: src.HealthCheckType, HealthCheckPath: src.HealthCheckPath,
		HealthCheckTimeout: src.HealthCheckTimeout, HealthCheckInterval: src.HealthCheckInterval,
		HealthCheckMaxFailed: src.HealthCheckMaxFailed,
		ServerName:           src.ServerName, Secret: src.Secret,
		BandwidthKB: src.BandwidthKB, BandwidthMode: src.BandwidthMode,
		ProxyProtocolVersion: src.ProxyProtocolVersion, Enabled: false,
		AllowUsers: append([]string(nil), src.AllowUsers...), ServerUser: src.ServerUser,
		UseEncryption: src.UseEncryption, UseCompression: src.UseCompression,
		Protocol: src.Protocol, KeepTunnelOpen: src.KeepTunnelOpen,
		MaxRetriesAnHour: src.MaxRetriesAnHour, MinRetryInterval: src.MinRetryInterval,
		FallbackTo: src.FallbackTo, FallbackTimeoutMs: src.FallbackTimeoutMs,
		DisableAssistedAddrs: src.DisableAssistedAddrs,
		Subdomain:            src.Subdomain, HostHeaderRewrite: src.HostHeaderRewrite, Multiplexer: src.Multiplexer, HTTPAuthEnabled: src.HTTPAuthEnabled, HTTPRouteByUser: src.HTTPRouteByUser, HTTPUser: src.HTTPUser,
		HTTPPassword: src.HTTPPassword, RouteByHTTPUser: src.RouteByHTTPUser,
		RequestHeaders: append([]HTTPHeader(nil), src.RequestHeaders...),
		ResponseHeaders: append([]HTTPHeader(nil), src.ResponseHeaders...),
	}
}

func (a *App) duplicateInstance(sourceID string, includeTunnels bool) (string, int, error) {
	st := a.store.DB.State.clone()
	source, ok := st.Instance(sourceID)
	if !ok {
		return "", 0, fmt.Errorf("源实例不存在")
	}
	name, err := duplicateInstanceName(st, source.Name)
	if err != nil {
		return "", 0, err
	}
	id := randomHex(8)
	copied := duplicatedInstance(source, id, name)
	if source.Kind == "frps" {
		// A standby server must not duplicate the original TCP listeners.
		copied.Port, err = duplicateServerPort(st, source.Port)
		if err != nil {
			return "", 0, err
		}
		copied.HTTPPort = 0
		copied.HTTPSPort = 0
		copied.TCPMuxHTTPConnectPort = 0
		copied.TCPMuxPassthrough = false
		copied.KCPBindPort = 0
		copied.QUICBindPort = 0
	}
	st.Instances = append(st.Instances, copied)
	count := 0
	if includeTunnels {
		for _, tunnel := range a.store.DB.State.Tunnels {
			if tunnel.InstanceID != sourceID {
				continue
			}
			st.Tunnels = append(st.Tunnels, duplicatedTunnel(tunnel, randomHex(8), id, tunnel.Name))
			count++
		}
	}
	if err = a.store.commit(st, "复制实例 "+source.Name+" 为 "+name); err != nil {
		return "", 0, err
	}
	return id, count, nil
}

func (a *App) duplicateTunnel(sourceID, targetInstanceID string) (string, error) {
	st := a.store.DB.State.clone()
	source, ok := st.Tunnel(sourceID)
	if !ok {
		return "", fmt.Errorf("源规则不存在")
	}
	if targetInstanceID == "" {
		targetInstanceID = source.InstanceID
	}
	target, ok := st.Instance(targetInstanceID)
	if !ok || target.Kind != "frpc" {
		return "", fmt.Errorf("目标必须是已保存的 frpc 客户端实例")
	}
	name, err := duplicateTunnelName(st, targetInstanceID, source.Name)
	if err != nil {
		return "", err
	}
	id := randomHex(8)
	clone := duplicatedTunnel(source, id, targetInstanceID, name)
	if err := prepareVisitorTransfer(st, source, &clone); err != nil {
		return "", err
	}
	st.Tunnels = append(st.Tunnels, clone)
	if err := a.store.commit(st, "复制规则 "+source.Name+" 到 "+target.Name); err != nil {
		return "", err
	}
	return id, nil
}

// The caller holds App.mu. Resolve the complete selection before adding any
// copies, then publish all disabled rules in one validated state commit.
func (a *App) duplicateTunnels(sourceIDs []string, targetInstanceID string) ([]string, error) {
	if len(sourceIDs) == 0 || len(sourceIDs) > maxTunnelCount {
		return nil, fmt.Errorf("请选择 1–512 条规则")
	}
	st := a.store.DB.State.clone()
	target, ok := st.Instance(targetInstanceID)
	if !ok || target.Kind != "frpc" {
		return nil, fmt.Errorf("目标必须是已保存的 frpc 客户端实例")
	}
	if len(st.Tunnels)+len(sourceIDs) > maxTunnelCount {
		return nil, fmt.Errorf("复制后规则总数不能超过 %d 条", maxTunnelCount)
	}
	wanted := make(map[string]bool, len(sourceIDs))
	sources := make([]Tunnel, 0, len(sourceIDs))
	for _, id := range sourceIDs {
		if !idPattern.MatchString(id) || wanted[id] {
			return nil, fmt.Errorf("规则 ID 无效或重复")
		}
		wanted[id] = true
		source, exists := st.Tunnel(id)
		if !exists {
			return nil, fmt.Errorf("部分规则已不存在，请刷新后重新选择")
		}
		sources = append(sources, source)
	}
	// Both export and this operation require the full fallback chain, even for
	// disabled drafts. Reject missing, invalid or cyclic links before copying.
	if err := validateExportFallbacks(sources); err != nil {
		return nil, fmt.Errorf("批量复制的回退依赖不完整或无效：%w", err)
	}
	start := len(st.Tunnels)
	ids := make([]string, 0, len(sources))
	names := make(map[string]string, len(sources))
	for _, source := range sources {
		name, err := duplicateTunnelName(st, targetInstanceID, source.Name)
		if err != nil {
			return nil, err
		}
		id := randomHex(8)
		clone := duplicatedTunnel(source, id, targetInstanceID, name)
		// The ordinary single-rule transfer forbids an unresolved fallback.
		// This batch has verified its full chain and remaps it after all names
		// exist; still apply the normal visitor namespace checks here.
		transfer := source
		if transfer.Type == "xtcp-visitor" && transfer.FallbackTo != "" {
			transfer.FallbackTo = ""
			clone.FallbackTo = ""
		}
		if err := prepareVisitorTransfer(st, transfer, &clone); err != nil {
			return nil, err
		}
		st.Tunnels = append(st.Tunnels, clone)
		ids = append(ids, id)
		names[source.InstanceID+"\x00"+source.Name] = name
	}
	for index, source := range sources {
		if source.Type == "xtcp-visitor" && source.FallbackTo != "" {
			name, exists := names[source.InstanceID+"\x00"+source.FallbackTo]
			if !exists {
				return nil, fmt.Errorf("规则 %q 的回退目标未选中", source.Name)
			}
			st.Tunnels[start+index].FallbackTo = name
		}
	}
	if err := a.store.commit(st, fmt.Sprintf("批量复制 %d 条连接规则到 %s", len(ids), target.Name)); err != nil {
		return nil, err
	}
	return ids, nil
}
