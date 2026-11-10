package manager

import (
	"fmt"
	"strings"
)

func renderProtocolInstance(b *strings.Builder, i Instance) error {
	if i.Kind == "frpc" {
		if i.ClientTransport != "" && i.ClientTransport != "tcp" {
			fmt.Fprintf(b, "transport.protocol = %s\n", quote(i.ClientTransport))
		}
		if i.User != "" {
			fmt.Fprintf(b, "user = %s\n", quote(i.User))
		}
		if i.NatHoleSTUNServer != "" {
			fmt.Fprintf(b, "natHoleStunServer = %s\n", quote(i.NatHoleSTUNServer))
		}
		if i.DNSServer != "" {
			address, ok := dnsServerAddress(i.DNSServer)
			if !ok {
				return fmt.Errorf("DNS 服务器须为 IP 地址，可选端口")
			}
			fmt.Fprintf(b, "dnsServer = %s\n", quote(address))
		}
		return nil
	}
	if i.KCPBindPort > 0 {
		fmt.Fprintf(b, "kcpBindPort = %d\n", i.KCPBindPort)
	}
	if i.QUICBindPort > 0 {
		fmt.Fprintf(b, "quicBindPort = %d\n", i.QUICBindPort)
	}
	if i.TCPMuxHTTPConnectPort > 0 {
		fmt.Fprintf(b, "tcpmuxHTTPConnectPort = %d\n", i.TCPMuxHTTPConnectPort)
	}
	if i.TCPMuxPassthrough {
		fmt.Fprintln(b, "tcpmuxPassthrough = true")
	}
	if i.SubDomainHost != "" {
		fmt.Fprintf(b, "subDomainHost = %s\n", quote(i.SubDomainHost))
	}
	return nil
}

func renderStringArray(b *strings.Builder, key string, values []string) {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, quote(value))
	}
	fmt.Fprintf(b, "%s = [%s]\n", key, strings.Join(quoted, ", "))
}

func renderProtocolTunnel(b *strings.Builder, t Tunnel, secret func(string, string) (string, error)) error {
	if isVisitor(t.Type) {
		kind := strings.TrimSuffix(t.Type, "-visitor")
		fmt.Fprintf(b, "\n[[visitors]]\nname = %s\ntype = %s\nserverName = %s\nbindAddr = %s\nbindPort = %d\n", quote(t.Name), quote(kind), quote(t.ServerName), quote(t.LocalHost), t.LocalPort)
		if t.ServerUser != "" {
			fmt.Fprintf(b, "serverUser = %s\n", quote(t.ServerUser))
		}
		if t.Type == "xtcp-visitor" {
			if t.Protocol != "" {
				fmt.Fprintf(b, "protocol = %s\n", quote(t.Protocol))
			}
			if t.KeepTunnelOpen {
				fmt.Fprintln(b, "keepTunnelOpen = true")
			}
			if t.MaxRetriesAnHour > 0 {
				fmt.Fprintf(b, "maxRetriesAnHour = %d\n", t.MaxRetriesAnHour)
			}
			if t.MinRetryInterval > 0 {
				fmt.Fprintf(b, "minRetryInterval = %d\n", t.MinRetryInterval)
			}
			if t.FallbackTo != "" {
				fmt.Fprintf(b, "fallbackTo = %s\n", quote(t.FallbackTo))
			}
			if t.FallbackTimeoutMs > 0 {
				fmt.Fprintf(b, "fallbackTimeoutMs = %d\n", t.FallbackTimeoutMs)
			}
		}
	} else {
		fmt.Fprintf(b, "\n[[proxies]]\nname = %s\ntype = %s\nlocalIP = %s\nlocalPort = %d\n", quote(t.Name), quote(t.Type), quote(t.LocalHost), t.LocalPort)
		if t.Type == "tcp" || t.Type == "udp" {
			fmt.Fprintf(b, "remotePort = %d\n", t.RemotePort)
		}
		if (t.Type == "http" || t.Type == "https" || t.Type == "tcpmux") && len(t.Domains) > 0 {
			renderStringArray(b, "customDomains", t.Domains)
		}
		if (t.Type == "http" || t.Type == "https" || t.Type == "tcpmux") && t.Subdomain != "" {
			fmt.Fprintf(b, "subdomain = %s\n", quote(t.Subdomain))
		}
		if t.Type == "http" && t.HostHeaderRewrite != "" {
			fmt.Fprintf(b, "hostHeaderRewrite = %s\n", quote(t.HostHeaderRewrite))
		}
		if t.Type == "http" && len(t.Locations) > 0 {
			renderStringArray(b, "locations", t.Locations)
		}
		if t.Type == "tcpmux" {
			mux := t.Multiplexer
			if mux == "" {
				mux = "httpconnect"
			}
			fmt.Fprintf(b, "multiplexer = %s\n", quote(mux))
			if t.HTTPUser != "" {
				fmt.Fprintf(b, "httpUser = %s\n", quote(t.HTTPUser))
			}
			if t.RouteByHTTPUser != "" {
				fmt.Fprintf(b, "routeByHTTPUser = %s\n", quote(t.RouteByHTTPUser))
			}
			if t.HTTPPassword != "" {
				value, err := secret(t.HTTPPassword, "KURUMI_HTTP_PASSWORD_"+t.ID)
				if err != nil {
					return err
				}
				fmt.Fprintf(b, "httpPassword = %s\n", value)
			}
		}
		if usesPairingSecret(t.Type) && len(t.AllowUsers) > 0 {
			renderStringArray(b, "allowUsers", t.AllowUsers)
		}
		if t.BandwidthKB > 0 {
			mode := t.BandwidthMode
			if mode == "" {
				mode = "client"
			}
			fmt.Fprintf(b, "transport.bandwidthLimit = \"%dKB\"\ntransport.bandwidthLimitMode = %s\n", t.BandwidthKB, quote(mode))
		}
		if t.HealthCheckType != "" {
			fmt.Fprintf(b, "healthCheck.type = %s\n", quote(t.HealthCheckType))
			if t.HealthCheckType == "http" {
				fmt.Fprintf(b, "healthCheck.path = %s\n", quote(t.HealthCheckPath))
			}
			if t.HealthCheckTimeout > 0 {
				fmt.Fprintf(b, "healthCheck.timeoutSeconds = %d\n", t.HealthCheckTimeout)
			}
			if t.HealthCheckInterval > 0 {
				fmt.Fprintf(b, "healthCheck.intervalSeconds = %d\n", t.HealthCheckInterval)
			}
			if t.HealthCheckMaxFailed > 0 {
				fmt.Fprintf(b, "healthCheck.maxFailed = %d\n", t.HealthCheckMaxFailed)
			}
		}
		if t.ProxyProtocolVersion != "" {
			fmt.Fprintf(b, "transport.proxyProtocolVersion = %s\n", quote(t.ProxyProtocolVersion))
		}
	}
	if t.UseEncryption {
		fmt.Fprintln(b, "transport.useEncryption = true")
	}
	if t.UseCompression {
		fmt.Fprintln(b, "transport.useCompression = true")
	}
	if (t.Type == "xtcp" || t.Type == "xtcp-visitor") && t.DisableAssistedAddrs {
		fmt.Fprintln(b, "natTraversal.disableAssistedAddrs = true")
	}
	if usesPairingSecret(t.Type) {
		value, err := secret(t.Secret, "KURUMI_SECRET_"+t.ID)
		if err != nil {
			return err
		}
		fmt.Fprintf(b, "secretKey = %s\n", value)
	}
	return nil
}
