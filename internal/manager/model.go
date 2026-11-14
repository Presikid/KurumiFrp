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

// ServerProfile models an independent frps configuration profile (A, B, C...).
type ServerProfile struct {
	ID                     string                `json:"id"`
	Name                   string                `json:"name"`
	AutoStart              bool                  `json:"autoStart"`
	BindAddr               string                `json:"bindAddr"`
	BindPort               int                   `json:"bindPort"`
	KCPBindPort            int                   `json:"kcpBindPort,omitempty"`
	QUICBindPort           int                   `json:"quicBindPort,omitempty"`
	VhostHTTPPort          int                   `json:"vhostHTTPPort,omitempty"`
	VhostHTTPSPort         int                   `json:"vhostHTTPSPort,omitempty"`
	TCPMuxHTTPConnectPort  int                   `json:"tcpmuxHTTPConnectPort,omitempty"`
	SubDomainHost          string                `json:"subDomainHost,omitempty"`
	AuthToken              string                `json:"authToken,omitempty"`
	MaxPoolCount           int                   `json:"maxPoolCount,omitempty"`
	MaxPortsPerClient      int                   `json:"maxPortsPerClient,omitempty"`
	AllowPorts             string                `json:"allowPorts,omitempty"`
	TLSForce               bool                  `json:"tlsForce,omitempty"`
	DetailedErrorsToClient *bool                 `json:"detailedErrorsToClient,omitempty"`
	ExtraTOML              string                `json:"extraToml,omitempty"`
	ConnPolicies           map[string]ConnPolicy `json:"connPolicies,omitempty"`
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

// ClientProfile models an independent frpc configuration profile (A, B, C, D, E, F...).
type ClientProfile struct {
	ID                string       `json:"id"`
	Name              string       `json:"name"`
	AutoStart         bool         `json:"autoStart"`
	ServerAddr        string       `json:"serverAddr"`
	ServerPort        int          `json:"serverPort"`
	User              string       `json:"user,omitempty"`
	AuthToken         string       `json:"authToken,omitempty"`
	Protocol          string       `json:"protocol,omitempty"`
	PoolCount         int          `json:"poolCount,omitempty"`
	TLSEnable         bool         `json:"tlsEnable"`
	TLSServerName     string       `json:"tlsServerName,omitempty"`
	DNSServer         string       `json:"dnsServer,omitempty"`
	NatHoleSTUNServer string       `json:"natHoleStunServer,omitempty"`
	LoginFailExit     bool         `json:"loginFailExit,omitempty"`
	ExtraTOML         string       `json:"extraToml,omitempty"`
	Tunnels           []TunnelRule `json:"tunnels"`
}

// TunnelRule models a single client proxy or visitor rule inside an frpc profile.
type TunnelRule struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Mode              string   `json:"mode,omitempty"` // "proxy" (default) or "visitor"
	Type              string   `json:"type"`           // tcp, udp, http, https, stcp, xtcp, sudp, tcpmux
	Enabled           bool     `json:"enabled"`
	LocalIP           string   `json:"localIP,omitempty"`
	LocalPort         int      `json:"localPort,omitempty"`
	RemotePort        int      `json:"remotePort,omitempty"`
	CustomDomains     []string `json:"customDomains,omitempty"`
	SubDomain         string   `json:"subDomain,omitempty"`
	Locations         []string `json:"locations,omitempty"`
	HostHeaderRewrite string   `json:"hostHeaderRewrite,omitempty"`
	HTTPUser          string   `json:"httpUser,omitempty"`
	HTTPPassword      string   `json:"httpPassword,omitempty"`
	SecretKey         string   `json:"secretKey,omitempty"`
	ServerName        string   `json:"serverName,omitempty"`
	ServerUser        string   `json:"serverUser,omitempty"`
	KeepTunnelOpen    bool     `json:"keepTunnelOpen,omitempty"`
	BandwidthLimitKB  int      `json:"bandwidthLimitKB,omitempty"`
	UseEncryption     bool     `json:"useEncryption,omitempty"`
	UseCompression    bool     `json:"useCompression,omitempty"`
	Plugin            string   `json:"plugin,omitempty"`
	PluginLocalPath   string   `json:"pluginLocalPath,omitempty"`
	PluginStripPrefix string   `json:"pluginStripPrefix,omitempty"`
	PluginUser        string   `json:"pluginUser,omitempty"`
	PluginPassword    string   `json:"pluginPassword,omitempty"`
	PluginLocalAddr   string   `json:"pluginLocalAddr,omitempty"`
	ExtraTOML         string   `json:"extraToml,omitempty"`
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
	ID          string             `json:"id"`
	Kind        string             `json:"kind"` // "frps" or "frpc"
	Running     bool               `json:"running"`
	State       string             `json:"state"` // "stopped", "starting", "running", "error"
	PID         int                `json:"pid,omitempty"`
	StartedAt   string             `json:"startedAt,omitempty"`
	LastError   string             `json:"lastError,omitempty"`
	AdminPort   int                `json:"adminPort,omitempty"`
	Connections []ConnLiveItem     `json:"connections,omitempty"`
	TunnelStats []TunnelLiveStatus `json:"tunnelStats,omitempty"`
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
