package manager

import (
	"fmt"
	"net"
	"strings"
)

// candidateServerForClient matches configured numeric or localhost addresses,
// not running process identity. A stopped managed frps may be replaced by
// another server. A wildcard does not identify arbitrary remote hosts.
func candidateServerForClient(s State, client Instance) (Instance, bool) {
	target := net.ParseIP(client.Host)
	localName := strings.EqualFold(client.Host, "localhost")
	if (target == nil && !localName) || (target != nil && target.IsUnspecified()) {
		return Instance{}, false
	}
	var found Instance
	matched := false
	for _, server := range s.Instances {
		if server.Kind != "frps" || server.Port != client.Port {
			continue
		}
		bind := net.ParseIP(server.Host)
		if bind == nil {
			continue
		}
		same := localName && (bind.IsLoopback() || bind.IsUnspecified())
		if target != nil {
			same = bind.Equal(target) || (bind.IsUnspecified() && target.IsLoopback() && (bind.To4() != nil) == (target.To4() != nil))
		}
		if !same {
			continue
		}
		if matched {
			return Instance{}, false
		}
		found, matched = server, true
	}
	return found, matched
}

type localBinding struct {
	host, network, name, id string
	port                    int
}

// These are warnings only: configured addresses do not prove which frps is
// running, and hostnames can resolve to different servers.
func possibleProxyConflicts(s State) map[string]string {
	var listeners []localBinding
	instances := make(map[string]Instance, len(s.Instances))
	for _, server := range s.Instances {
		instances[server.ID] = server
		if server.Kind != "frps" {
			continue
		}
		for _, port := range []int{server.Port, server.HTTPPort, server.HTTPSPort, server.TCPMuxHTTPConnectPort} {
			if port > 0 {
				listeners = append(listeners, localBinding{server.Host, "tcp", server.Name, server.ID, port})
			}
		}
	}
	for _, visitor := range s.Tunnels {
		if visitor.Enabled && isVisitor(visitor.Type) && visitor.LocalPort > 0 {
			listeners = append(listeners, localBinding{visitor.LocalHost, visitorNetwork(visitor.Type), visitor.Name, visitor.ID, visitor.LocalPort})
		}
	}
	conflicts := map[string]string{}
	for _, tunnel := range s.Tunnels {
		if !tunnel.Enabled || tunnel.RemotePort == 0 || (tunnel.Type != "tcp" && tunnel.Type != "udp") {
			continue
		}
		client, ok := instances[tunnel.InstanceID]
		if !ok {
			continue
		}
		server, ok := candidateServerForClient(s, client)
		if !ok {
			continue
		}
		for _, binding := range listeners {
			if binding.network != tunnel.Type || binding.port != tunnel.RemotePort || !listenerHostsOverlap(binding.host, server.Host) {
				continue
			}
			a, b := binding.id, tunnel.ID
			if a > b {
				a, b = b, a
			}
			key := fmt.Sprintf("%s/%s/%s/%d", a, b, tunnel.Type, tunnel.RemotePort)
			conflicts[key] = fmt.Sprintf("若客户端连接到本机受管服务端，%s 端口 %d 可能与监听冲突：%s / %s", strings.ToUpper(tunnel.Type), tunnel.RemotePort, binding.name, tunnel.Name)
			if len(conflicts) >= 11 {
				return conflicts
			}
		}
		listeners = append(listeners, localBinding{server.Host, tunnel.Type, tunnel.Name, tunnel.ID, tunnel.RemotePort})
	}
	loopback := func(host string) bool {
		if strings.EqualFold(host, "localhost") {
			return true
		}
		ip := net.ParseIP(host)
		return ip != nil && ip.IsLoopback()
	}
	for n, left := range s.Tunnels {
		if !left.Enabled || left.RemotePort == 0 || (left.Type != "tcp" && left.Type != "udp") {
			continue
		}
		leftClient, ok := instances[left.InstanceID]
		if !ok || !loopback(leftClient.Host) {
			continue
		}
		for _, right := range s.Tunnels[n+1:] {
			if !right.Enabled || right.Type != left.Type || right.RemotePort != left.RemotePort {
				continue
			}
			rightClient, ok := instances[right.InstanceID]
			if !ok || rightClient.Port != leftClient.Port || !loopback(rightClient.Host) || strings.EqualFold(rightClient.Host, leftClient.Host) {
				continue
			}
			a, b := left.ID, right.ID
			if a > b {
				a, b = b, a
			}
			key := fmt.Sprintf("%s/%s/%s/%d", a, b, left.Type, left.RemotePort)
			if _, exists := conflicts[key]; !exists {
				conflicts[key] = fmt.Sprintf("回环地址可能指向同一服务端，%s 端口 %d 可能冲突：%s / %s", strings.ToUpper(left.Type), left.RemotePort, left.Name, right.Name)
			}
			if len(conflicts) >= 11 {
				return conflicts
			}
		}
	}
	return conflicts
}
