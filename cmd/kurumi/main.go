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
	"os/exec"
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

	// If an instance is already running for this dataDir, open browser directly if --open was passed.
	if existingAddr, ok := probeExistingServer(dataDir); ok {
		if *openFlag {
			openWebUIWithTicket(dataDir, existingAddr)
		}
		return
	}

	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	app, err := manager.NewApp(dataDir, stopSignals)
	if err != nil {
		fmt.Fprintf(os.Stderr, "初始化 KurumiFrp 失败: %v\n", err)
		os.Exit(1)
	}
	defer app.Close()

	ln, actualAddr, browserBaseURL, err := bindListener(*listenFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "绑定 WebUI 端口失败: %v\n", err)
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
	if *openFlag {
		go func() {
			time.Sleep(150 * time.Millisecond)
			openWebUIWithTicket(dataDir, browserBaseURL)
		}()
	}

	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(os.Stderr, "HTTP 服务异常退出: %v\n", err)
		os.Exit(1)
	}
}

func resolveDataDir(explicit string) string {
	if strings.TrimSpace(explicit) != "" {
		abs, err := filepath.Abs(strings.TrimSpace(explicit))
		if err == nil {
			return abs
		}
		return explicit
	}
	if distribution == "linux-system" {
		return "/opt/kurumifrp/data"
	}
	exe, err := os.Executable()
	if err == nil {
		exeDir := filepath.Dir(exe)
		if distribution == "windows-installed" {
			return filepath.Join(exeDir, "data")
		}
		// Portable or source build: keep configs inside ./data or next to binary if writable
		if st, err := os.Stat(filepath.Join(exeDir, "bundled")); err == nil && st.IsDir() {
			return filepath.Join(exeDir, "data")
		}
	}
	abs, err := filepath.Abs("./data")
	if err == nil {
		return abs
	}
	return "./data"
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

	// Atomic port scan in 9000..9020 without TOCTOU close-reopen race
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
	tokBytes, err := os.ReadFile(filepath.Join(dataDir, "access.token"))
	if err != nil {
		return "", false
	}
	tok := strings.TrimSpace(string(tokBytes))

	client := &http.Client{Timeout: 1500 * time.Millisecond}
	req, err := http.NewRequest(http.MethodGet, baseURL+"/api/state", nil)
	if err != nil {
		return "", false
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := client.Do(req)
	if err != nil {
		return "", false
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return baseURL, resp.StatusCode == http.StatusOK
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
	openSystemBrowser(targetURL)
}

func openSystemBrowser(u string) {
	if !strings.HasPrefix(u, "http://127.0.0.1:") && !strings.HasPrefix(u, "http://[::1]:") {
		return
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	case "darwin":
		cmd = exec.Command("open", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	_ = cmd.Start()
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
		// Allow any valid non-symlink local directory chosen by the user (e.g., C:\Program Files\KurumiFRP or D:\KurumiFRP).
		if info, err := os.Lstat(dir); err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return errors.New("不支持符号链接安装目录")
			}
		}
		return nil
	case "init":
		if err := os.MkdirAll(filepath.Join(dataDir, "configs"), 0o700); err != nil {
			return err
		}
		marker := map[string]string{
			"version":   version.Current,
			"updatedAt": time.Now().UTC().Format(time.RFC3339),
		}
		b, _ := json.MarshalIndent(marker, "", "  ")
		return os.WriteFile(filepath.Join(dir, "native-install.json"), append(b, '\n'), 0o644)
	case "stop":
		return stopExistingServer(dataDir)
	case "finish-stop":
		return nil
	case "purge":
		_ = stopExistingServer(dataDir)
		_ = os.RemoveAll(dataDir)
		_ = os.Remove(filepath.Join(dir, "native-install.json"))
		return nil
	default:
		return fmt.Errorf("未知维护命令: %s", action)
	}
}
