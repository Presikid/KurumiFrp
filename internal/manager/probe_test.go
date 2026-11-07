package manager
import ("net"; "strconv"; "testing")
func TestProbeTarget(t *testing.T) {
 listener, err := net.Listen("tcp", "127.0.0.1:0"); if err != nil { t.Fatal(err) }; defer listener.Close()
 _, portText, _ := net.SplitHostPort(listener.Addr().String()); port, _ := strconv.Atoi(portText)
 got := probeTarget(Tunnel{Type:"tcp", LocalHost:"127.0.0.1", LocalPort:port})
 if got.Status != "reachable" || got.Address == "" { t.Fatalf("expected reachable target, got %#v", got) }
}
func TestProbeTargetUnsupportedUDP(t *testing.T) { got:=probeTarget(Tunnel{Type:"udp",LocalHost:"127.0.0.1",LocalPort:53}); if got.Status!="unsupported" { t.Fatalf("expected unsupported, got %#v",got) } }
func TestProbeTargetInvalidPort(t *testing.T) { got:=probeTarget(Tunnel{Type:"tcp",LocalHost:"127.0.0.1",LocalPort:0}); if got.Status!="invalid" { t.Fatalf("expected invalid, got %#v",got) } }
