// Modified by KurumiFRP: isolate metrics per server instance.

package metrics

type ServerMetrics interface {
	NewClient()
	CloseClient()
	NewProxy(name string, proxyType string, user string, clientID string)
	CloseProxy(name string, proxyType string)
	OpenConnection(name string, proxyType string)
	CloseConnection(name string, proxyType string)
	AddTrafficIn(name string, proxyType string, trafficBytes int64)
	AddTrafficOut(name string, proxyType string, trafficBytes int64)
}

// Noop returns a collector that discards metrics when none is supplied.
func Noop() ServerMetrics {
	return noopServerMetrics{}
}

type noopServerMetrics struct{}

func (noopServerMetrics) NewClient()                              {}
func (noopServerMetrics) CloseClient()                            {}
func (noopServerMetrics) NewProxy(string, string, string, string) {}
func (noopServerMetrics) CloseProxy(string, string)               {}
func (noopServerMetrics) OpenConnection(string, string)           {}
func (noopServerMetrics) CloseConnection(string, string)          {}
func (noopServerMetrics) AddTrafficIn(string, string, int64)      {}
func (noopServerMetrics) AddTrafficOut(string, string, int64)     {}
