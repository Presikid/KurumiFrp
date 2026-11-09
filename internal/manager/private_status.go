package manager

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// privateStatus is bound to one frpc process. Its certificate authenticates
// the local listener; Basic authentication alone does not prevent a different
// process from taking the port and stealing the password.
type privateStatus struct {
	port     int
	password string
	cert     *x509.Certificate
	dir      string
	baseText string
	metadata statusMetadata
}

type observedProxy struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Status     string `json:"status"`
	Error      string `json:"err"`
	RemoteAddr string `json:"remote_addr"`
}

func newPrivateStatus(parent string) (_ *privateStatus, err error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err = listener.Close(); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(parent, "status-")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dir)
		}
	}()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "KurumiFRP local status"}, NotBefore: now.Add(-time.Hour), NotAfter: now.AddDate(10, 0, 0), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	if err = os.WriteFile(filepath.Join(dir, "cert.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		return nil, err
	}
	if err = os.WriteFile(filepath.Join(dir, "key.pem"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0600); err != nil {
		return nil, err
	}
	metadata := statusMetadata{Version: 1, OwnerPID: os.Getpid(), Identity: statusSystemIdentity()}
	data, err := json.Marshal(metadata)
	if err != nil {
		return nil, err
	}
	if err = os.WriteFile(filepath.Join(dir, "owner.json"), data, 0600); err != nil {
		return nil, err
	}
	return &privateStatus{port: port, password: randomHex(32), cert: cert, dir: dir, metadata: metadata}, nil
}

// Until the child PID has been durably recorded, recovery leaves the directory
// untouched: a crash between process creation and recording is ambiguous.
func (s *privateStatus) markStarted(pid int) {
	if s == nil || pid <= 1 {
		return
	}
	metadata := s.metadata
	metadata.ChildPID = pid
	data, err := json.Marshal(metadata)
	if err != nil {
		return
	}
	if err = atomicWrite(filepath.Join(s.dir, "owner.json"), data); err == nil {
		s.metadata = metadata
	}
}

func (s *privateStatus) cleanup() {
	if s != nil && s.dir != "" {
		_ = os.RemoveAll(s.dir)
	}
}

// Monitoring is optional: an unsupported core or unavailable status setup
// must not prevent a valid user configuration from starting.
func (a *App) preparePrivateStatus(ctx context.Context, binary string, instance Instance, base Rendered) (Rendered, *privateStatus, error) {
	if instance.Kind != "frpc" {
		return base, nil, nil
	}
	s, err := newPrivateStatus(a.store.dir)
	if err != nil {
		base.MonitorReason = "无法建立本机安全状态接口"
		return base, nil, nil
	}
	runtime := s.runtimeConfig(base)
	if _, err := a.verifyBinary(ctx, binary, runtime); err != nil {
		s.cleanup()
		if ctx.Err() != nil {
			return base, nil, ctx.Err()
		}
		base.MonitorReason = "当前核心未通过安全状态接口配置验证"
		return base, nil, nil
	}
	return runtime, s, nil
}

// runtimeConfig retains the original digest/preview so the ephemeral
// credentials and paths cannot masquerade as user changes.
func (s *privateStatus) runtimeConfig(r Rendered) Rendered {
	s.baseText = r.Text
	// The randomly generated value is hex only. Literal TOML quotes and a
	// hex-only template value work with both frp's older html/template and its
	// newer text/template engines without exposing the credential in the file.
	prefix := fmt.Sprintf("webServer.addr = \"127.0.0.1\"\nwebServer.port = %d\nwebServer.user = \"kurumi\"\nwebServer.password = \"{{ .Envs.KURUMI_STATUS_PASSWORD }}\"\nwebServer.tls.certFile = \"{{ .Envs.KURUMI_STATUS_CERT }}\"\nwebServer.tls.keyFile = \"{{ .Envs.KURUMI_STATUS_KEY }}\"\n", s.port)
	r.Text = prefix + r.Text
	r.Env = append(append([]string(nil), r.Env...), "KURUMI_STATUS_PASSWORD="+s.password, "KURUMI_STATUS_CERT="+templateCredential(filepath.ToSlash(filepath.Join(s.dir, "cert.pem"))), "KURUMI_STATUS_KEY="+templateCredential(filepath.ToSlash(filepath.Join(s.dir, "key.pem"))))
	r.Secrets = append(append([]string(nil), r.Secrets...), s.password)
	return r
}

func (s *privateStatus) read(ctx context.Context) (map[string]observedProxy, error) {
	if s == nil || s.cert == nil || s.port <= 0 || s.password == "" {
		return nil, fmt.Errorf("本机状态接口不可用")
	}
	pool := x509.NewCertPool()
	pool.AddCert(s.cert)
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(s.port))
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool, ServerName: "127.0.0.1"}, DialContext: (&net.Dialer{Timeout: time.Second}).DialContext}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+address+"/api/status", nil)
	if err != nil {
		return nil, fmt.Errorf("状态请求无效")
	}
	req.SetBasicAuth("kurumi", s.password)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("本机状态接口不可用或身份验证失败")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("本机状态接口返回 HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, fmt.Errorf("状态数据过大或读取失败")
	}
	groups := map[string][]observedProxy{}
	if err = json.Unmarshal(data, &groups); err != nil {
		return nil, fmt.Errorf("状态数据格式无效")
	}
	out := map[string]observedProxy{}
	for kind, entries := range groups {
		for _, entry := range entries {
			if len(out) >= 512 {
				return nil, fmt.Errorf("状态数据条目过多")
			}
			if entry.Type == kind && entry.Name != "" {
				out[kind+"\x00"+entry.Name] = entry
			}
		}
	}
	return out, nil
}
