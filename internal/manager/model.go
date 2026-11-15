package manager

// ConfigDocument represents an independent workspace configuration file stored inside data/configs/.
type ConfigDocument struct {
	Version int             `json:"version"`
	Name    string          `json:"name"`
	Updated string          `json:"updated"`
	Servers []ServerProfile `json:"servers"`
	Clients []ClientProfile `json:"clients"`
}

// ConfigFileSummary describes a selectable config file in App Control.
type ConfigFileSummary struct {
	FileName    string `json:"fileName"`
	DisplayName string `json:"displayName"`
	Active      bool   `json:"active"`
	ServerCount int    `json:"serverCount"`
	ClientCount int    `json:"clientCount"`
	Updated     string `json:"updated"`
	SizeBytes   int64  `json:"sizeBytes"`
}

// HTTPPluginSpec models a user-defined [[httpPlugins]] webhook on an frps server.
type HTTPPluginSpec struct {
	Name      string   `json:"name"`
	Addr      string   `json:"addr"`
	Path      string   `json:"path"`
	Ops       []string `json:"ops"`
	TLSVerify bool     `json:"tlsVerify,omitempty"`
}

// ServerProfile models an independent frps configuration profile with 100% official fatedier/frp v1 coverage.
type ServerProfile struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	CoverAsset string `json:"coverAsset,omitempty"`
	Note       string `json:"note,omitempty"`
	AutoStart  bool   `json:"autoStart"`

	// Bind & Listeners
	BindAddr      string `json:"bindAddr"`
	BindPort      int    `json:"bindPort"`
	KCPBindPort   int    `json:"kcpBindPort,omitempty"`
	QUICBindPort  int    `json:"quicBindPort,omitempty"`
	ProxyBindAddr string `json:"proxyBindAddr,omitempty"`

	// Vhost HTTP / HTTPS / TCPMux
	VhostHTTPPort         int    `json:"vhostHTTPPort,omitempty"`
	VhostHTTPSPort        int    `json:"vhostHTTPSPort,omitempty"`
	VhostHTTPTimeout      int64  `json:"vhostHTTPTimeout,omitempty"`
	TCPMuxHTTPConnectPort int    `json:"tcpmuxHTTPConnectPort,omitempty"`
	TCPMuxPassthrough     bool   `json:"tcpmuxPassthrough,omitempty"`
	SubDomainHost         string `json:"subDomainHost,omitempty"`
	Custom404Page         string `json:"custom404Page,omitempty"`

	// Authentication (token & oidc)
	AuthMethod           string   `json:"authMethod,omitempty"` // "token" or "oidc"
	AuthToken            string   `json:"authToken,omitempty"`
	AuthTokenSourceFile  string   `json:"authTokenSourceFile,omitempty"`
	AuthAdditionalScopes []string `json:"authAdditionalScopes,omitempty"` // HeartBeats, NewWorkConns
	OIDCIssuer           string   `json:"oidcIssuer,omitempty"`
	OIDCAudience         string   `json:"oidcAudience,omitempty"`
	OIDCSkipExpiryCheck  bool     `json:"oidcSkipExpiryCheck,omitempty"`
	OIDCSkipIssuerCheck  bool     `json:"oidcSkipIssuerCheck,omitempty"`

	// Transport, Multiplexing, QUIC & TLS
	MaxPoolCount            int    `json:"maxPoolCount,omitempty"`
	TCPMux                  *bool  `json:"tcpMux,omitempty"`
	TCPMuxKeepaliveInterval int64  `json:"tcpMuxKeepaliveInterval,omitempty"`
	TCPKeepalive            int64  `json:"tcpKeepalive,omitempty"`
	HeartbeatTimeout        int64  `json:"heartbeatTimeout,omitempty"`
	QUICKeepalivePeriod     int    `json:"quicKeepalivePeriod,omitempty"`
	QUICMaxIdleTimeout      int    `json:"quicMaxIdleTimeout,omitempty"`
	QUICMaxIncomingStreams  int    `json:"quicMaxIncomingStreams,omitempty"`
	TLSForce                bool   `json:"tlsForce,omitempty"`
	TLSCertFile             string `json:"tlsCertFile,omitempty"`
	TLSKeyFile              string `json:"tlsKeyFile,omitempty"`
	TLSTrustedCaFile        string `json:"tlsTrustedCaFile,omitempty"`

	// SSH Tunnel Gateway (frp v0.53+)
	SSHGatewayBindPort       int    `json:"sshGatewayBindPort,omitempty"`
	SSHPrivateKeyFile        string `json:"sshPrivateKeyFile,omitempty"`
	SSHAutoGenPrivateKeyPath string `json:"sshAutoGenPrivateKeyPath,omitempty"`
	SSHAuthorizedKeysFile    string `json:"sshAuthorizedKeysFile,omitempty"`

	// Quotas, Port Whitelist & NatHole Analysis
	MaxPortsPerClient               int    `json:"maxPortsPerClient,omitempty"`
	AllowPorts                      string `json:"allowPorts,omitempty"`
	UserConnTimeout                 int64  `json:"userConnTimeout,omitempty"`
	UDPPacketSize                   int64  `json:"udpPacketSize,omitempty"`
	NatHoleAnalysisDataReserveHours int64  `json:"natholeAnalysisDataReserveHours,omitempty"`
	DetailedErrorsToClient          *bool  `json:"detailedErrorsToClient,omitempty"`

	// Logging & Custom HTTP Server Plugins
	LogLevel          string           `json:"logLevel,omitempty"`
	LogMaxDays        int              `json:"logMaxDays,omitempty"`
	DisablePrintColor bool             `json:"disablePrintColor,omitempty"`
	HTTPPlugins       []HTTPPluginSpec `json:"httpPlugins,omitempty"`

	// Extra custom TOML & Connection Dashboard policies
	ExtraTOML    string                `json:"extraToml,omitempty"`
	ConnPolicies map[string]ConnPolicy `json:"connPolicies,omitempty"`
}

// ConnPolicy stores Connection Dashboard controls for a single proxy on an frps server:
// traffic stats baseline, data cap (限流), bandwidth speed limit (限速), expiration time (限时), and on/off toggle (开关).
type ConnPolicy struct {
	ProxyName        string `json:"proxyName"`
	Enabled          bool   `json:"enabled"`
	TrafficLimitMB   int64  `json:"trafficLimitMB,omitempty"`
	SpeedLimitKBps   int64  `json:"speedLimitKBps,omitempty"`
	ExpiresAt        string `json:"expiresAt,omitempty"`
	RecordedInBytes  int64  `json:"recordedInBytes,omitempty"`
	RecordedOutBytes int64  `json:"recordedOutBytes,omitempty"`
	Note             string `json:"note,omitempty"`
}

// ConnLiveItem represents a real-time entry on the Server Connection Dashboard.
type ConnLiveItem struct {
	ProxyName      string `json:"proxyName"`
	ProxyType      string `json:"proxyType"`
	RemoteAddr     string `json:"remoteAddr,omitempty"`
	ClientVersion  string `json:"clientVersion,omitempty"`
	Status         string `json:"status"`
	BlockReason    string `json:"blockReason,omitempty"`
	CurConns       int64  `json:"curConns"`
	TotalInBytes   int64  `json:"totalInBytes"`
	TotalOutBytes  int64  `json:"totalOutBytes"`
	SpeedInBps     int64  `json:"speedInBps"`
	SpeedOutBps    int64  `json:"speedOutBps"`
	Enabled        bool   `json:"enabled"`
	TrafficLimitMB int64  `json:"trafficLimitMB"`
	SpeedLimitKBps int64  `json:"speedLimitKBps"`
	ExpiresAt      string `json:"expiresAt,omitempty"`
	Note           string `json:"note,omitempty"`
	LastSyncAt     string `json:"lastSyncAt,omitempty"`
}

// RemoteClientSummary represents a connected KurumiFrp client (frpc) instance managed over its active frps link.
type RemoteClientSummary struct {
	ServerID      string `json:"serverId"`
	ClientID      string `json:"clientId"`
	ClientName    string `json:"clientName"`
	FRPUser       string `json:"frpUser,omitempty"`
	Hostname      string `json:"hostname,omitempty"`
	OS            string `json:"os,omitempty"`
	Arch          string `json:"arch,omitempty"`
	AppVersion    string `json:"appVersion,omitempty"`
	FRPVersion    string `json:"frpVersion,omitempty"`
	RemoteAddr    string `json:"remoteAddr,omitempty"`
	RunID         string `json:"runId,omitempty"`
	ConnectedAt   string `json:"connectedAt"`
	LastPingAt    string `json:"lastPingAt"`
	CtrlProxyName string `json:"ctrlProxyName"`
	CtrlReady     bool   `json:"ctrlReady"`
	Online        bool   `json:"online"`
	ProxyCount    int    `json:"proxyCount"`
}

// ClientProfile models an independent frpc configuration profile with 100% official fatedier/frp v1 coverage.
type ClientProfile struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	CoverAsset string `json:"coverAsset,omitempty"`
	Note       string `json:"note,omitempty"`
	AutoStart  bool   `json:"autoStart"`

	// Instance-to-Instance Remote Control Plane credentials (strictly connection-scoped)
	RemoteCtrlEnabled *bool  `json:"remoteCtrlEnabled,omitempty"` // default true when nil
	CtrlSecret        string `json:"ctrlSecret,omitempty"`
	AgentToken        string `json:"agentToken,omitempty"`

	// Common Upstream & Runtime Options
	ServerAddr           string            `json:"serverAddr"`
	ServerPort           int               `json:"serverPort"`
	User                 string            `json:"user,omitempty"`
	ClientID             string            `json:"clientId,omitempty"`
	LoginFailExit        bool              `json:"loginFailExit,omitempty"`
	StartProxies         []string          `json:"startProxies,omitempty"`
	Includes             []string          `json:"includes,omitempty"`
	DNSServer            string            `json:"dnsServer,omitempty"`
	NatHoleSTUNServer    string            `json:"natHoleStunServer,omitempty"`
	ConnectServerLocalIP string            `json:"connectServerLocalIP,omitempty"`
	ProxyURL             string            `json:"proxyURL,omitempty"`
	UDPPacketSize        int64             `json:"udpPacketSize,omitempty"`
	VirtualNetAddr       string            `json:"virtualNetAddr,omitempty"`
	Metadatas            map[string]string `json:"metadatas,omitempty"`

	// Logging
	LogLevel          string `json:"logLevel,omitempty"`
	LogMaxDays        int    `json:"logMaxDays,omitempty"`
	DisablePrintColor bool   `json:"disablePrintColor,omitempty"`

	// Authentication (token & oidc)
	AuthMethod           string            `json:"authMethod,omitempty"`
	AuthToken            string            `json:"authToken,omitempty"`
	AuthTokenSourceFile  string            `json:"authTokenSourceFile,omitempty"`
	AuthAdditionalScopes []string          `json:"authAdditionalScopes,omitempty"`
	OIDCClientID         string            `json:"oidcClientId,omitempty"`
	OIDCClientSecret     string            `json:"oidcClientSecret,omitempty"`
	OIDCAudience         string            `json:"oidcAudience,omitempty"`
	OIDCScope            string            `json:"oidcScope,omitempty"`
	OIDCTokenEndpointURL string            `json:"oidcTokenEndpointURL,omitempty"`
	OIDCAdditionalParams map[string]string `json:"oidcAdditionalParams,omitempty"`

	// Transport, Multiplexing, QUIC & TLS
	Protocol                  string `json:"protocol,omitempty"` // tcp, kcp, quic, websocket, wss
	PoolCount                 int    `json:"poolCount,omitempty"`
	DialServerTimeout         int64  `json:"dialServerTimeout,omitempty"`
	DialServerKeepAlive       int64  `json:"dialServerKeepAlive,omitempty"`
	TCPMux                    *bool  `json:"tcpMux,omitempty"`
	TCPMuxKeepaliveInterval   int64  `json:"tcpMuxKeepaliveInterval,omitempty"`
	HeartbeatInterval         int64  `json:"heartbeatInterval,omitempty"`
	HeartbeatTimeout          int64  `json:"heartbeatTimeout,omitempty"`
	QUICKeepalivePeriod       int    `json:"quicKeepalivePeriod,omitempty"`
	QUICMaxIdleTimeout        int    `json:"quicMaxIdleTimeout,omitempty"`
	QUICMaxIncomingStreams    int    `json:"quicMaxIncomingStreams,omitempty"`
	TLSEnable                 bool   `json:"tlsEnable"`
	TLSDisableCustomFirstByte bool   `json:"tlsDisableCustomFirstByte,omitempty"`
	TLSServerName             string `json:"tlsServerName,omitempty"`
	TLSCertFile               string `json:"tlsCertFile,omitempty"`
	TLSKeyFile                string `json:"tlsKeyFile,omitempty"`
	TLSTrustedCaFile          string `json:"tlsTrustedCaFile,omitempty"`

	ExtraTOML string       `json:"extraToml,omitempty"`
	Tunnels   []TunnelRule `json:"tunnels"`
}

// TunnelRule models a single client [[proxies]] or [[visitors]] rule with full official FRP v1 capabilities.
type TunnelRule struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Mode    string `json:"mode,omitempty"` // "proxy" (default) or "visitor"
	Type    string `json:"type"`           // tcp, udp, http, https, tcpmux, stcp, sudp, xtcp
	Enabled bool   `json:"enabled"`
	Note    string `json:"note,omitempty"`

	// Local & Remote Endpoints + Port-Range Batch Mapping
	LocalIP         string `json:"localIP,omitempty"`
	LocalPort       int    `json:"localPort,omitempty"`
	RemotePort      int    `json:"remotePort,omitempty"`
	PortRangeLocal  string `json:"portRangeLocal,omitempty"`  // e.g. "6000-6010,6012"
	PortRangeRemote string `json:"portRangeRemote,omitempty"` // e.g. "16000-16010,16012"

	// HTTP / HTTPS / TCPMux Routing & Rewriting
	CustomDomains     []string          `json:"customDomains,omitempty"`
	SubDomain         string            `json:"subDomain,omitempty"`
	Locations         []string          `json:"locations,omitempty"`
	HostHeaderRewrite string            `json:"hostHeaderRewrite,omitempty"`
	HTTPUser          string            `json:"httpUser,omitempty"`
	HTTPPassword      string            `json:"httpPassword,omitempty"`
	RouteByHTTPUser   string            `json:"routeByHTTPUser,omitempty"`
	Multiplexer       string            `json:"multiplexer,omitempty"` // "httpconnect"
	RequestHeaders    map[string]string `json:"requestHeaders,omitempty"`
	ResponseHeaders   map[string]string `json:"responseHeaders,omitempty"`

	// STCP / SUDP / XTCP & Visitor Orchestration
	SecretKey         string   `json:"secretKey,omitempty"`
	AllowUsers        []string `json:"allowUsers,omitempty"`
	ServerName        string   `json:"serverName,omitempty"`
	ServerUser        string   `json:"serverUser,omitempty"`
	VisitorProtocol   string   `json:"visitorProtocol,omitempty"` // "quic" or "kcp" for xtcp visitor
	KeepTunnelOpen    bool     `json:"keepTunnelOpen,omitempty"`
	MaxRetriesAnHour  int      `json:"maxRetriesAnHour,omitempty"`
	MinRetryInterval  int      `json:"minRetryInterval,omitempty"`
	FallbackTo        string   `json:"fallbackTo,omitempty"`
	FallbackTimeoutMs int      `json:"fallbackTimeoutMs,omitempty"`

	// Proxy-Level Transport, Rate Limit & Proxy Protocol
	BandwidthLimit       string `json:"bandwidthLimit,omitempty"`     // e.g. "2MB", "512KB"
	BandwidthLimitKB     int    `json:"bandwidthLimitKB,omitempty"`   // legacy/numeric KB helper
	BandwidthLimitMode   string `json:"bandwidthLimitMode,omitempty"` // "client" or "server"
	UseEncryption        bool   `json:"useEncryption,omitempty"`
	UseCompression       bool   `json:"useCompression,omitempty"`
	ProxyProtocolVersion string `json:"proxyProtocolVersion,omitempty"` // "", "v1", "v2"

	// Load Balancer ([proxies.loadBalancer])
	LoadBalancerGroup    string `json:"loadBalancerGroup,omitempty"`
	LoadBalancerGroupKey string `json:"loadBalancerGroupKey,omitempty"`

	// Health Check ([proxies.healthCheck])
	HealthCheckType            string            `json:"healthCheckType,omitempty"` // "", "tcp", "http"
	HealthCheckTimeoutSeconds  int               `json:"healthCheckTimeoutSeconds,omitempty"`
	HealthCheckMaxFailed       int               `json:"healthCheckMaxFailed,omitempty"`
	HealthCheckIntervalSeconds int               `json:"healthCheckIntervalSeconds,omitempty"`
	HealthCheckPath            string            `json:"healthCheckPath,omitempty"`
	HealthCheckHeaders         map[string]string `json:"healthCheckHeaders,omitempty"`

	// Per-Tunnel Metadatas & Annotations
	Metadatas   map[string]string `json:"metadatas,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`

	// Built-in Client Plugins ([proxies.plugin]: static_file, http_proxy, socks5, https2http, https2https, http2https, http2http, tls2raw, unix_domain_socket, virtual_net)
	Plugin                  string            `json:"plugin,omitempty"`
	PluginLocalPath         string            `json:"pluginLocalPath,omitempty"`
	PluginStripPrefix       string            `json:"pluginStripPrefix,omitempty"`
	PluginUser              string            `json:"pluginUser,omitempty"`
	PluginPassword          string            `json:"pluginPassword,omitempty"`
	PluginLocalAddr         string            `json:"pluginLocalAddr,omitempty"`
	PluginCrtPath           string            `json:"pluginCrtPath,omitempty"`
	PluginKeyPath           string            `json:"pluginKeyPath,omitempty"`
	PluginHostHeaderRewrite string            `json:"pluginHostHeaderRewrite,omitempty"`
	PluginEnableHTTP2       *bool             `json:"pluginEnableHTTP2,omitempty"`
	PluginHeaders           map[string]string `json:"pluginHeaders,omitempty"`

	ExtraTOML string `json:"extraToml,omitempty"`
}

// TunnelLiveStatus reports real-time status from official frpc local Admin API (/api/status).
type TunnelLiveStatus struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Status     string `json:"status"`
	LocalAddr  string `json:"localAddr,omitempty"`
	RemoteAddr string `json:"remoteAddr,omitempty"`
	Plugin     string `json:"plugin,omitempty"`
	Err        string `json:"err,omitempty"`
}

// ProcessRuntimeView exposes live process status for a ServerProfile or ClientProfile.
type ProcessRuntimeView struct {
	ID            string                `json:"id"`
	Kind          string                `json:"kind"` // "frps" or "frpc"
	Running       bool                  `json:"running"`
	State         string                `json:"state"` // "stopped", "starting", "running", "error"
	PID           int                   `json:"pid,omitempty"`
	StartedAt     string                `json:"startedAt,omitempty"`
	LastError     string                `json:"lastError,omitempty"`
	AdminPort     int                   `json:"adminPort,omitempty"`
	Connections   []ConnLiveItem        `json:"connections,omitempty"`
	RemoteClients []RemoteClientSummary `json:"remoteClients,omitempty"`
	TunnelStats   []TunnelLiveStatus    `json:"tunnelStats,omitempty"`
}

// CoreBinaryInfo describes the resolved official frpc and frps binaries.
type CoreBinaryInfo struct {
	FrpcPath    string `json:"frpcPath"`
	FrpcVersion string `json:"frpcVersion"`
	FrpcFound   bool   `json:"frpcFound"`
	FrpsPath    string `json:"frpsPath"`
	FrpsVersion string `json:"frpsVersion"`
	FrpsFound   bool   `json:"frpsFound"`
	BundledDir  string `json:"bundledDir"`
	CoreSource  string `json:"coreSource,omitempty"`
}
