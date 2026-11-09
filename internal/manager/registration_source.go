package manager

import (
	"context"
	"fmt"
)

// ProxyRegistrationStatus is the small portion of a client's proxy status
// that the manager needs. In particular, it never contains an frp configurer.
// RemoteAddr is reported by the source and may be only a port, not proof of
// reachability.
type ProxyRegistrationStatus struct {
	Name       string
	Type       string
	Phase      string
	Error      string
	RemoteAddr string
}

// ProxyRegistrationReader belongs to one client run. Implementations must be
// safe for concurrent calls and return promptly when ctx is cancelled. A
// missing proxy is reported as found=false, not as an error. Only proxy names,
// never visitor names, are passed to this interface.
//
// An in-process frp adapter can project Name, Type, Phase, Err and RemoteAddr
// from Service.StatusExporter().GetProxyStatus(name) into this value. It must
// not retain or return WorkingStatus.Cfg or other upstream configuration.
type ProxyRegistrationReader interface {
	ReadProxyRegistration(ctx context.Context, name string) (status ProxyRegistrationStatus, found bool, err error)
}

// ProxyRegistrationReaderFunc adapts a per-proxy function to the reader.
type ProxyRegistrationReaderFunc func(context.Context, string) (ProxyRegistrationStatus, bool, error)

func (f ProxyRegistrationReaderFunc) ReadProxyRegistration(ctx context.Context, name string) (ProxyRegistrationStatus, bool, error) {
	if f == nil {
		return ProxyRegistrationStatus{}, false, fmt.Errorf("代理状态接口不可用")
	}
	if err := ctx.Err(); err != nil {
		return ProxyRegistrationStatus{}, false, err
	}
	return f(ctx, name)
}

type registrationQuery struct {
	name string
	kind string
}

func (q registrationQuery) key() string { return q.kind + "\x00" + q.name }

func registrationQueryFor(t Tunnel) (registrationQuery, bool) {
	if !t.Enabled || isVisitor(t.Type) {
		return registrationQuery{}, false
	}
	return registrationQuery{name: t.Name, kind: t.Type}, true
}

func registrationQueries(tunnels []Tunnel) []registrationQuery {
	queries := make([]registrationQuery, 0, len(tunnels))
	for _, t := range tunnels {
		if query, ok := registrationQueryFor(t); ok {
			queries = append(queries, query)
		}
	}
	return queries
}

type proxyObservation struct {
	status ProxyRegistrationStatus
	found  bool
	err    error
}

type registrationReadings struct {
	proxies map[string]proxyObservation
	err     error
}

func (r registrationReadings) forTunnel(t Tunnel) proxyObservation {
	if r.err != nil {
		return proxyObservation{err: r.err}
	}
	return r.proxies[(registrationQuery{name: t.Name, kind: t.Type}).key()]
}

// readRegistrations uses the in-process reader when present. The private HTTP
// source is retained for the current external-process runtime and is fetched
// once per client, rather than once per proxy. Neither source's own error is
// returned to the API caller: upstream errors can contain sensitive material.
func readRegistrations(ctx context.Context, reader ProxyRegistrationReader, legacy *privateStatus, queries []registrationQuery) registrationReadings {
	out := registrationReadings{proxies: make(map[string]proxyObservation, len(queries))}
	if len(queries) == 0 {
		return out
	}
	if reader == nil {
		values, err := legacy.read(ctx)
		if err != nil {
			out.err = err
			return out
		}
		for _, q := range queries {
			if value, found := values[q.key()]; found {
				out.proxies[q.key()] = proxyObservation{found: true, status: ProxyRegistrationStatus{
					Name: value.Name, Type: value.Type, Phase: value.Status, Error: value.Error, RemoteAddr: value.RemoteAddr,
				}}
			}
		}
		return out
	}
	for _, q := range queries {
		if err := ctx.Err(); err != nil {
			out.err = err
			return out
		}
		status, found, err := reader.ReadProxyRegistration(ctx, q.name)
		if err != nil {
			out.proxies[q.key()] = proxyObservation{err: err}
			continue
		}
		if !found {
			continue
		}
		if status.Name != q.name || status.Type != q.kind {
			out.proxies[q.key()] = proxyObservation{err: fmt.Errorf("代理状态身份不匹配")}
			continue
		}
		out.proxies[q.key()] = proxyObservation{status: status, found: true}
	}
	if err := ctx.Err(); err != nil {
		out.err = err
	}
	return out
}
