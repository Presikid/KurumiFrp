package manager

import (
 "context"
 "fmt"
 "net"
 "strconv"
 "time"
)

// ProbeResult reports only local target reachability, never remote frp registration.
type ProbeResult struct {
 Status string `json:"status"`
 Address string `json:"address,omitempty"`
 LatencyMS int64 `json:"latencyMs,omitempty"`
 Message string `json:"message"`
}

func probeTarget(t Tunnel) ProbeResult {
 if t.Type == "udp" { return ProbeResult{Status:"unsupported", Message:"UDP 不支持 TCP 连通性探测"} }
 if t.LocalHost == "" || t.LocalPort < 1 || t.LocalPort > 65535 { return ProbeResult{Status:"invalid", Message:"本地目标地址或端口无效"} }
 address := net.JoinHostPort(t.LocalHost, strconv.Itoa(t.LocalPort))
 ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
 defer cancel()
 started := time.Now()
 conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
 if err != nil { return ProbeResult{Status:"unreachable", Address:address, Message:fmt.Sprintf("本地目标不可连接：%v", err)} }
 _ = conn.Close()
 return ProbeResult{Status:"reachable", Address:address, LatencyMS:time.Since(started).Milliseconds(), Message:"本地目标 TCP 端口可连接；不代表 frp 远端隧道已注册"}
}
