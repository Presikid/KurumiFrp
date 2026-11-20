package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Presikid/KurumiFrp/internal/manager"
	"github.com/Presikid/KurumiFrp/internal/version"
)

// distribution is set via -ldflags "-X main.distribution=..." during native packaging.
// Values: "source" (default), "windows-installed", "linux-system".
var distribution = "source"

func main() {
	var (
		dataFlag        = flag.String("data", "", "数据与配置文件存储目录（默认保存在软件目录内 data 文件夹）")
		listenFlag      = flag.String("listen", "", "WebUI 监听地址（例如 127.0.0.1:9000 或 0.0.0.0:9000，默认读取面板设置或 auto）")
		defaultHostFlag = flag.String("default-host", "", "未自定义面板监听地址时的默认监听网卡（如 0.0.0.0）")
		openFlag        = flag.Bool("open", false, "启动后自动在系统默认浏览器中打开 WebUI")
		headlessFlag    = flag.Bool("headless", false, "后台静默启动（供开机自启使用，不自动弹出浏览器）")
		stopFlag        = flag.Bool("stop", false, "优雅停止当前正在运行的 KurumiFrp 后台进程")
		checkRunning    = flag.Bool("check-running", false, "检查后台服务是否正在运行")
		pathsFlag       = flag.Bool("paths", false, "以 JSON 输出当前软件目录与数据目录路径")
		versionFlag     = flag.Bool("version", false, "打印版本号")
		nativeMaint     = flag.String("native-maintenance", "", "安装包生命周期维护命令 (preflight|preflight-existing|init|stop|finish-stop|purge)")
		installDirFlag  = flag.String("install-dir", "", "安装包目标安装目录")
	)
	flag.Parse()

	if *versionFlag {
		fmt.Println(version.Current)
		return
	}

	if *nativeMaint != "" {
		if err := handleNativeMaintenance(*nativeMaint, *installDirFlag); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	shouldOpenBrowser := *openFlag || (runtime.GOOS == "windows" && !*headlessFlag)

	dataDir := resolveDataDir(*dataFlag)
	if *pathsFlag {
		exe, _ := os.Executable()
		_ = json.NewEncoder(os.Stdout).Encode(map[string]string{
			"distribution": distribution,
			"executable":   exe,
			"dataDir":      dataDir,
		})
		return
	}

	if *checkRunning {
		if addr, ok := probeExistingServer(dataDir); ok {
			fmt.Println(addr)
			return
		}
		os.Exit(1)
	}

	if *stopFlag {
		if err := stopExistingServer(dataDir); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if existingAddr, ok := probeExistingServer(dataDir); ok {
		if shouldOpenBrowser {
			openLocalWebUI(existingAddr)
		}
		return
	}

	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	app, err := manager.NewApp(dataDir, stopSignals)
	if err != nil {
		msg := fmt.Sprintf("初始化数据目录 (%s) 失败: %v", dataDir, err)
		fmt.Fprintln(os.Stderr, msg)
		if shouldOpenBrowser {
			platformShowFatalError(msg)
		}
		os.Exit(1)
	}
	defer app.Close()

	ln, actualCallbackAddr, browserBaseURL, err := bindInitialListener(*listenFlag, *defaultHostFlag, dataDir)
	if err != nil {
		msg := fmt.Sprintf("绑定本地 WebUI 端口失败: %v", err)
		fmt.Fprintln(os.Stderr, msg)
		if shouldOpenBrowser {
			platformShowFatalError(msg)
		}
		os.Exit(1)
	}

	_ = os.WriteFile(filepath.Join(dataDir, "listen.addr"), []byte(browserBaseURL+"\n"), 0o600)
	app.SetRuntimeMeta(ln.Addr().String(), os.Args[1:])
	app.SetListenAddr(actualCallbackAddr)

	var (
		srvMu      sync.Mutex
		activeLn   = ln
		activeSrv  = newPanelHTTPServer(app)
		fatalErrCh = make(chan error, 1)
	)

	startServing := func(s *http.Server, l net.Listener) {
		go func() {
			if serveErr := s.Serve(l); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
				srvMu.Lock()
				isCurrent := (s == activeSrv)
				srvMu.Unlock()
				if isCurrent {
					select {
					case fatalErrCh <- serveErr:
					default:
					}
				}
			}
		}()
	}

	// Register live zero-downtime panel port hot-rebinding callback
	app.SetPanelRebindHandler(ln.Addr().String(), func(bindHost string, newPort int) (string, string, int, error) {
		srvMu.Lock()
		defer srvMu.Unlock()

		targetSpec := net.JoinHostPort(bindHost, strconv.Itoa(newPort))
		newLn, bindErr := net.Listen("tcp", targetSpec)
		if bindErr != nil {
			return "", "", 0, bindErr
		}
		tcpAddr := newLn.Addr().(*net.TCPAddr)
		boundHost, _, _ := net.SplitHostPort(tcpAddr.String())
		boundPort := tcpAddr.Port
		newBrowserURL := buildLocalBrowserURL(tcpAddr)
		newCallbackAddr := fmt.Sprintf("127.0.0.1:%d", boundPort)

		newSrv := newPanelHTTPServer(app)
		oldSrv := activeSrv
		activeLn = newLn
		activeSrv = newSrv
		startServing(newSrv, newLn)

		_ = os.WriteFile(filepath.Join(dataDir, "listen.addr"), []byte(newBrowserURL+"\n"), 0o600)
		app.SetRuntimeMeta(newLn.Addr().String(), filterListenFromArgs(os.Args[1:]))
		app.UpdateCallbackAddr(newCallbackAddr)

		// Gracefully retire old HTTP listener after current response completes
		if oldSrv != nil {
			go func(retiring *http.Server) {
				time.Sleep(750 * time.Millisecond)
				shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = retiring.Shutdown(shutCtx)
			}(oldSrv)
		}
		return newLn.Addr().String(), boundHost, boundPort, nil
	})

	startServing(activeSrv, activeLn)

	fmt.Printf("KurumiFrp v%s WebUI 已启动: %s (监听: %s, 数据目录: %s)\n", version.Current, browserBaseURL, ln.Addr().String(), dataDir)
	if shouldOpenBrowser {
		go func() {
			time.Sleep(200 * time.Millisecond)
			openLocalWebUI(browserBaseURL)
		}()
	}

	select {
	case <-ctx.Done():
		srvMu.Lock()
		curSrv := activeSrv
		curLn := activeLn
		srvMu.Unlock()
		if curSrv != nil {
			shutCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			_ = curSrv.Shutdown(shutCtx)
			cancel()
		}
		if curLn != nil {
			_ = curLn.Close()
		}
	case serveErr := <-fatalErrCh:
		msg := fmt.Sprintf("WebUI 服务异常退出: %v", serveErr)
		fmt.Fprintln(os.Stderr, msg)
		if shouldOpenBrowser {
			platformShowFatalError(msg)
		}
		os.Exit(1)
	}

	if code := app.ExitCode(); code != 0 {
		app.Close()
		os.Exit(code)
	}
}

func newPanelHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
}

func filterListenFromArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-listen" || a == "--listen" {
			i++
			continue
		}
		if strings.HasPrefix(a, "-listen=") || strings.HasPrefix(a, "--listen=") {
			continue
		}
		out = append(out, a)
	}
	return out
}

func resolveDataDir(explicit string) string {
	if strings.TrimSpace(explicit) != "" {
		if abs, err := filepath.Abs(strings.TrimSpace(explicit)); err == nil {
			return abs
		}
		return explicit
	}
	if distribution == "linux-system" {
		return "/opt/kurumifrp/data"
	}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		if distribution == "windows-installed" || dirExists(filepath.Join(exeDir, "bundled")) {
			preferred := filepath.Join(exeDir, "data")
			if ensureDirWritable(preferred) {
				return preferred
			}
			if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
				fallback := filepath.Join(localAppData, "KurumiFRP", "data")
				if ensureDirWritable(fallback) {
					return fallback
				}
			}
			return preferred
		}
	}
	if abs, err := filepath.Abs("./data"); err == nil {
		return abs
	}
	return "./data"
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func ensureDirWritable(dir string) bool {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false
	}
	probe := filepath.Join(dir, ".write-probe")
	if err := os.WriteFile(probe, []byte("ok"), 0o600); err != nil {
		return false
	}
	_ = os.Remove(probe)
	return true
}

func buildLocalBrowserURL(tcpAddr *net.TCPAddr) string {
	hostForURL := tcpAddr.IP.String()
	if tcpAddr.IP.IsUnspecified() || hostForURL == "" {
		hostForURL = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s", net.JoinHostPort(hostForURL, strconv.Itoa(tcpAddr.Port)))
}

func bindInitialListener(requested, defaultHost, dataDir string) (net.Listener, string, string, error) {
	req := strings.TrimSpace(requested)
	panelCfg := manager.LoadPanelSettings(dataDir)

	fallbackHost := strings.TrimSpace(defaultHost)
	if fallbackHost == "" {
		if distribution == "linux-system" {
			fallbackHost = "0.0.0.0"
		} else {
			fallbackHost = "127.0.0.1"
		}
	}

	// Honor WebUI-saved panel_settings.json when -listen is auto/empty OR legacy systemd default "0.0.0.0:9000"
	if panelCfg.Customized && panelCfg.Port > 0 && (req == "" || req == "auto" || req == "0.0.0.0:9000") {
		host := panelCfg.BindHost
		if host == "" {
			if req == "0.0.0.0:9000" {
				host = "0.0.0.0"
			} else {
				host = fallbackHost
			}
		}
		req = net.JoinHostPort(host, strconv.Itoa(panelCfg.Port))
	}

	if req != "" && req != "auto" {
		ln, err := net.Listen("tcp", req)
		if err != nil {
			return nil, "", "", err
		}
		tcpAddr := ln.Addr().(*net.TCPAddr)
		callbackAddr := fmt.Sprintf("127.0.0.1:%d", tcpAddr.Port)
		return ln, callbackAddr, buildLocalBrowserURL(tcpAddr), nil
	}

	for port := 9000; port <= 9020; port++ {
		addr := net.JoinHostPort(fallbackHost, strconv.Itoa(port))
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			tcpAddr := ln.Addr().(*net.TCPAddr)
			cb := fmt.Sprintf("127.0.0.1:%d", tcpAddr.Port)
			return ln, cb, buildLocalBrowserURL(tcpAddr), nil
		}
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(fallbackHost, "0"))
	if err != nil {
		return nil, "", "", err
	}
	tcpAddr := ln.Addr().(*net.TCPAddr)
	cb := fmt.Sprintf("127.0.0.1:%d", tcpAddr.Port)
	return ln, cb, buildLocalBrowserURL(tcpAddr), nil
}

func newLocalLoopbackClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{DisableKeepAlives: true},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func isValidLocalPanelURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "http" || u.User != nil || u.Path != "" && u.Path != "/" {
		return false
	}
	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		return false
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.IsUnspecified() {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if ipNet, ok := a.(*net.IPNet); ok && ipNet.IP.Equal(ip) {
			return true
		}
	}
	return false
}

func probeExistingServer(dataDir string) (string, bool) {
	rawAddr, err := os.ReadFile(filepath.Join(dataDir, "listen.addr"))
	if err != nil {
		return "", false
	}
	baseURL := strings.TrimRight(strings.TrimSpace(string(rawAddr)), "/")
	if !isValidLocalPanelURL(baseURL) {
		return "", false
	}
	client := newLocalLoopbackClient(1200 * time.Millisecond)
	resp, err := client.Get(baseURL + "/api/session")
	if err != nil {
		return "", false
	}
	var body struct {
		Version         string `json:"version"`
		DefaultUsername string `json:"defaultUsername"`
	}
	decErr := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body)
	resp.Body.Close()
	return baseURL, resp.StatusCode == http.StatusOK && decErr == nil && body.Version != "" && body.DefaultUsername != ""
}

func stopExistingServer(dataDir string) error {
	baseURL, ok := probeExistingServer(dataDir)
	if !ok {
		return nil
	}
	tokBytes, err := os.ReadFile(filepath.Join(dataDir, "access.token"))
	if err != nil {
		return fmt.Errorf("读取本地维护令牌失败: %w", err)
	}
	client := newLocalLoopbackClient(5 * time.Second)
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/quit", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(tokBytes)))
	if resp, err := client.Do(req); err == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, alive := probeExistingServer(dataDir); !alive {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return errors.New("等待后台服务退出超时")
}

func openLocalWebUI(baseURL string) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if !isValidLocalPanelURL(baseURL) {
		return
	}
	_ = platformOpenURL(baseURL)
}

func handleNativeMaintenance(action, installDir string) error {
	dir := strings.TrimSpace(installDir)
	if dir == "" {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		dir = filepath.Dir(exe)
	}
	dataDir := filepath.Join(dir, "data")

	switch action {
	case "preflight", "preflight-existing":
		if info, err := os.Lstat(dir); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return errors.New("不支持符号链接安装目录")
		}
		return nil
	case "init":
		if err := os.MkdirAll(filepath.Join(dataDir, "configs"), 0o777); err != nil {
			return err
		}
		marker := map[string]string{
			"version":   version.Current,
			"updatedAt": time.Now().UTC().Format(time.RFC3339),
		}
		b, _ := json.MarshalIndent(marker, "", "  ")
		return os.WriteFile(filepath.Join(dir, "native-install.json"), append(b, '\n'), 0o644)
	case "stop":
		_ = stopExistingServer(dataDir)
		if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
			_ = stopExistingServer(filepath.Join(localAppData, "KurumiFRP", "data"))
		}
		return nil
	case "finish-stop":
		return nil
	case "purge":
		_ = stopExistingServer(dataDir)
		_ = os.RemoveAll(dataDir)
		if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
			_ = stopExistingServer(filepath.Join(localAppData, "KurumiFRP", "data"))
			_ = os.RemoveAll(filepath.Join(localAppData, "KurumiFRP"))
		}
		_ = os.Remove(filepath.Join(dir, "native-install.json"))
		return nil
	default:
		return fmt.Errorf("未知维护命令: %s", action)
	}
}
