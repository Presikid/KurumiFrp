package manager

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// A batch is expanded into ordinary tunnels, so existing rendering, history,
// editing, exports and runtime status do not need a new persisted schema.
type tunnelBatchRequest struct {
	Revision             uint64 `json:"revision"`
	Name                 string `json:"name"`
	InstanceID           string `json:"instanceId"`
	Protocol             string `json:"protocol"`
	LocalHost            string `json:"localHost"`
	LocalPorts           string `json:"localPorts"`
	RemotePorts          string `json:"remotePorts"`
	BandwidthKB          int    `json:"bandwidthKB"`
	BandwidthMode        string `json:"bandwidthMode,omitempty"`
	ProxyProtocolVersion string `json:"proxyProtocolVersion,omitempty"`
	Enabled              bool   `json:"enabled"`
}

// expandPortList preserves the order supplied by the user. Restrict both input
// length and expansion before iterating so large or overflowing ranges cannot
// consume unbounded work, even when the request did not come from the UI.
func expandPortList(raw, label string) ([]int, error) {
	if len(raw) > 4096 {
		return nil, fmt.Errorf("%s列表过长", label)
	}
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("请填写%s", label)
	}
	parsePort := func(s string) (int, error) {
		s = strings.TrimSpace(s)
		if s == "" {
			return 0, fmt.Errorf("%s须为 1-65535 的整数", label)
		}
		for _, c := range s {
			if c < '0' || c > '9' {
				return 0, fmt.Errorf("%s须为 1-65535 的整数", label)
			}
		}
		p, err := strconv.Atoi(s)
		if err != nil || !portOK(p) {
			return 0, fmt.Errorf("%s须为 1-65535 的整数", label)
		}
		return p, nil
	}
	ports := make([]int, 0)
	seen := make(map[int]bool)
	for _, part := range strings.Split(raw, ",") {
		bounds := strings.Split(strings.TrimSpace(part), "-")
		if len(bounds) > 2 {
			return nil, fmt.Errorf("%s格式无效，请使用单个端口或起始-结束", label)
		}
		first, err := parsePort(bounds[0])
		if err != nil {
			return nil, err
		}
		last := first
		if len(bounds) == 2 {
			last, err = parsePort(bounds[1])
			if err != nil {
				return nil, err
			}
			if last < first {
				return nil, fmt.Errorf("%s范围的结束端口不能小于起始端口", label)
			}
		}
		if last-first+1 > maxTunnelCount-len(ports) {
			return nil, fmt.Errorf("%s最多包含 %d 个端口", label, maxTunnelCount)
		}
		for p := first; p <= last; p++ {
			if seen[p] {
				return nil, fmt.Errorf("%s %d 重复，请检查重叠的范围", label, p)
			}
			seen[p] = true
			ports = append(ports, p)
		}
	}
	return ports, nil
}

// Called with the same application lock and authentication checks as /api/save.
func (a *App) createTunnelBatchAPI(w http.ResponseWriter, r *http.Request) {
	var req tunnelBatchRequest
	if err := decode(w, r, &req); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if req.Revision != a.store.DB.Revision {
		fail(w, http.StatusConflict, fmt.Errorf("配置已在其他窗口更新，请刷新后重试"))
		return
	}
	count, err := a.createTunnelBatch(req)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	reply(w, http.StatusOK, map[string]any{"ok": true, "created": count, "revision": a.store.DB.Revision})
}

func (a *App) createTunnelBatch(req tunnelBatchRequest) (int, error) {
	st := a.store.DB.State.clone()
	instance, ok := st.Instance(req.InstanceID)
	if !ok || instance.Kind != "frpc" {
		return 0, fmt.Errorf("请选择有效的 frpc 实例")
	}
	if !namePattern.MatchString(req.Name) {
		return 0, fmt.Errorf("规则名称限 1-64 位英文、数字、点、横线或下划线")
	}
	if !hostOK(req.LocalHost) {
		return 0, fmt.Errorf("本地目标地址无效")
	}
	if req.BandwidthKB < 0 || req.BandwidthKB > 10000000 {
		return 0, fmt.Errorf("带宽限制须为 0-10000000 KB/s")
	}
	if req.BandwidthMode != "" && req.BandwidthMode != "client" && req.BandwidthMode != "server" {
		return 0, fmt.Errorf("带宽限制位置仅支持客户端或服务端")
	}
	if req.ProxyProtocolVersion != "" && req.ProxyProtocolVersion != "v1" && req.ProxyProtocolVersion != "v2" {
		return 0, fmt.Errorf("PROXY 协议版本仅支持 v1 或 v2")
	}
	if (req.Protocol == "udp" || req.Protocol == "tcp+udp") && req.ProxyProtocolVersion != "" {
		return 0, fmt.Errorf("当前包含 UDP 规则的批量创建暂不支持 PROXY 协议")
	}
	var protocols []string
	switch req.Protocol {
	case "tcp", "udp":
		protocols = []string{req.Protocol}
	case "tcp+udp":
		protocols = []string{"tcp", "udp"}
	default:
		return 0, fmt.Errorf("批量创建仅支持 TCP、UDP 或 TCP+UDP")
	}
	local, err := expandPortList(req.LocalPorts, "本地端口")
	if err != nil {
		return 0, err
	}
	count := len(local) * len(protocols)
	if count > maxTunnelCount-len(st.Tunnels) {
		return 0, fmt.Errorf("规则总数最多为 %d 条，当前已有 %d 条，本次需要 %d 条", maxTunnelCount, len(st.Tunnels), count)
	}
	dynamic := strings.TrimSpace(req.RemotePorts) == "0"
	if dynamic && len(protocols) > 1 {
		return 0, fmt.Errorf("TCP+UDP 成对创建需要指定相同的固定远端端口；动态分配无法保证两者相同")
	}
	var remote []int
	if !dynamic {
		remote, err = expandPortList(req.RemotePorts, "远端端口")
		if err != nil {
			return 0, err
		}
		if len(remote) != len(local) {
			return 0, fmt.Errorf("本地端口有 %d 个，远端端口有 %d 个；两边数量须相同并按顺序配对", len(local), len(remote))
		}
	}
	for index, localPort := range local {
		name := req.Name
		if len(local) > 1 {
			name += "-" + strconv.Itoa(localPort)
		}
		remotePort := 0
		if !dynamic {
			remotePort = remote[index]
		}
		for _, protocol := range protocols {
			generatedName := name
			if len(protocols) > 1 {
				generatedName += "-" + protocol
			}
			if !namePattern.MatchString(generatedName) {
				return 0, fmt.Errorf("生成的规则名称 %q 超过 64 位；请缩短名称前缀", generatedName)
			}
			st.Tunnels = append(st.Tunnels, Tunnel{
				ID: randomHex(8), InstanceID: req.InstanceID, Name: generatedName,
				Type: protocol, LocalHost: req.LocalHost, LocalPort: localPort,
				RemotePort: remotePort, Domains: []string{}, BandwidthKB: req.BandwidthKB, BandwidthMode: req.BandwidthMode,
				ProxyProtocolVersion: req.ProxyProtocolVersion, Enabled: req.Enabled,
			})
		}
	}
	// commit validates the complete expanded state before writing anything and
	// increments the revision once. Any validation or write failure leaves DB intact.
	if err := a.store.commit(st, fmt.Sprintf("批量创建 %d 条连接规则", count)); err != nil {
		return 0, err
	}
	return count, nil
}
