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
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
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
		dataFlag       = flag.String("data", "", "数据与配置文件存储目录（默认保存在软件目录内 data 文件夹）")
		listenFlag     = flag.String("listen", "", "WebUI 监听地址（例如 127.0.0.1:9000 或 0.0.0.0:9000，默认 auto）")
		openFlag       = flag.Bool("open", false, "启动后自动在系统默认浏览器中打开 WebUI")
		headlessFlag   = flag.Bool("headless", false, "后台静默启动（供开机自启使用，不自动弹出浏览器）")
		stopFlag       = flag.Bool("stop", false, "优雅停止当前正在运行的 KurumiFrp 后台进程")
		checkRunning   = flag.Bool("check-running", false, "检查后台服务是否正在运行")
		showToken      = flag.Bool("show-token", false, "输出本地 CLI 维护令牌")
		pathsFlag      = flag.Bool("paths", false, "以 JSON 输出当前软件目录与数据目录路径")
		versionFlag    = flag.Bool("version", false, "打印版本号")
		nativeMaint    = flag.String("native-maintenance", "", "安装包生命周期维护命令 (preflight|preflight-existing|init|stop|finish-stop|purge)")
		installDirFlag = flag.String("install-dir", "", "安装包目标安装目录")
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

	// On Windows desktop builds, double-clicking kurumi.exe or any shortcut should ALWAYS open the WebUI
	// unless explicitly launched in background mode via -headless.
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

	if *showToken {
		raw, err := os.ReadFile(filepath.Join(dataDir, "access.token"))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(strings.TrimSpace(string(raw)))
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

	// If an instance is already running, immediately open the browser WebUI for the user!
	if existingAddr, ok := probeExistingServer(dataDir); ok {
		if shouldOpenBrowser {
			openWebUIWithTicket(dataDir, existingAddr)
		}
		return
	}
	// Fallback check: even if dataDir token wasn't readable (e.g. legacy elevated instance), check default loopback ports
	if shouldOpenBrowser {
		if fallbackURL, ok := probePublicSessionOnDefaultPorts(); ok {
			_ = platformOpenURL(fallbackURL)
			return
		}
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

	ln, actualAddr, browserBaseURL, err := bindListener(*listenFlag)
	if err != nil {
		msg := fmt.Sprintf("绑定本地 WebUI 端口失败: %v", err)
		fmt.Fprintln(os.Stderr, msg)
		if shouldOpenBrowser {
			platformShowFatalError(msg)
		}
		os.Exit(1)
	}
	defer ln.Close()

	_ = os.WriteFile(filepath.Join(dataDir, "listen.addr"), []byte(browserBaseURL+"\n"), 0o600)
	app.SetListenAddr(actualAddr)

	srv := &http.Server{
		Handler:           app,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	fmt.Printf("KurumiFrp v%s WebUI 已启动: %s (数据目录: %s)\n", version.Current, browserBaseURL, dataDir)
	if shouldOpenBrowser {
		go func() {
			time.Sleep(200 * time.Millisecond)
			openWebUIWithTicket(dataDir, browserBaseURL)
		}()
	}

	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		msg := fmt.Sprintf("WebUI 服务异常退出: %v", err)
		fmt.Fprintln(os.Stderr, msg)
		if shouldOpenBrowser {
			platformShowFatalError(msg)
		}
		os.Exit(1)
	}
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
			// Fallback if installed into a locked folder without Users write ACL
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

func bindListener(requested string) (net.Listener, string, string, error) {
	req := strings.TrimSpace(requested)
	if req != "" && req != "auto" {
		ln, err := net.Listen("tcp", req)
		if err != nil {
			return nil, "", "", err
		}
		tcpAddr := ln.Addr().(*net.TCPAddr)
		callbackAddr := fmt.Sprintf("127.0.0.1:%d", tcpAddr.Port)
		hostForURL := tcpAddr.IP.String()
		if tcpAddr.IP.IsUnspecified() || hostForURL == "" {
			hostForURL = "127.0.0.1"
		}
		return ln, callbackAddr, fmt.Sprintf("http://%s", net.JoinHostPort(hostForURL, fmt.Sprintf("%d", tcpAddr.Port))), nil
	}

	for port := 9000; port <= 9020; port++ {
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			return ln, addr, "http://" + addr, nil
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", "", err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	return ln, addr, "http://" + addr, nil
}

func probeExistingServer(dataDir string) (string, bool) {
	rawAddr, err := os.ReadFile(filepath.Join(dataDir, "listen.addr"))
	if err != nil {
		return "", false
	}
	baseURL := strings.TrimSpace(string(rawAddr))
	if !strings.HasPrefix(baseURL, "http://") {
		return "", false
	}
	client := &http.Client{Timeout: 1200 * time.Millisecond}
	resp, err := client.Get(baseURL + "/api/session")
	if err != nil {
		return "", false
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return baseURL, resp.StatusCode == http.StatusOK
}

func probePublicSessionOnDefaultPorts() (string, bool) {
	client := &http.Client{Timeout: 400 * time.Millisecond}
	for port := 9000; port <= 9005; port++ {
		base := fmt.Sprintf("http://127.0.0.1:%d", port)
		resp, err := client.Get(base + "/api/session")
		if err != nil {
			continue
		}
		var body struct {
			Version         string `json:"version"`
			DefaultUsername string `json:"defaultUsername"`
		}
		err = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if err == nil && resp.StatusCode == http.StatusOK && body.Version != "" && body.DefaultUsername != "" {
			return base, true
		}
	}
	return "", false
}

func stopExistingServer(dataDir string) error {
	baseURL, ok := probeExistingServer(dataDir)
	if !ok {
		return nil
	}
	tokBytes, err := os.ReadFile(filepath.Join(dataDir, "access.token"))
	if err != nil {
		return nil
	}
	client := &http.Client{Timeout: 5 * time.Second}
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

func openWebUIWithTicket(dataDir, baseURL string) {
	targetURL := baseURL
	if tokBytes, err := os.ReadFile(filepath.Join(dataDir, "access.token")); err == nil {
		client := &http.Client{Timeout: 2 * time.Second}
		req, err := http.NewRequest(http.MethodPost, baseURL+"/api/browser-ticket", nil)
		if err == nil {
			req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(tokBytes)))
			if resp, err := client.Do(req); err == nil {
				var out struct {
					Ticket string `json:"ticket"`
				}
				_ = json.NewDecoder(resp.Body).Decode(&out)
				resp.Body.Close()
				if out.Ticket != "" {
					targetURL = baseURL + "/launch?ticket=" + out.Ticket
				}
			}
		}
	}
	if !strings.HasPrefix(targetURL, "http://127.0.0.1:") && !strings.HasPrefix(targetURL, "http://[::1]:") {
		return
	}
	_ = platformOpenURL(targetURL)
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
