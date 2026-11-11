package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Do not redirect an external browser's temporary files into our managed session.
var browserEnvironment = os.Environ()

func openBrowser(address string) error {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "http" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "::1") {
		return fmt.Errorf("invalid local browser URL")
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", address)
	case "darwin":
		cmd = exec.Command("open", address)
	default:
		cmd = exec.Command("xdg-open", address)
	}
	cmd.Env = browserEnvironment
	if err = cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}
func openExisting(addr, token string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid local manager address")
	}
	n, err := strconv.Atoi(port)
	if err != nil || (host != "127.0.0.1" && host != "::1") || n < 1024 || n > 65535 {
		return fmt.Errorf("invalid local manager address")
	}
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	req, err := http.NewRequest("POST", "http://"+addr+"/api/browser-ticket", bytes.NewBufferString("{}"))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("existing manager rejected local credentials")
	}
	var result struct {
		URL string `json:"url"`
	}
	if err = json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 4096)).Decode(&result); err != nil {
		return err
	}
	address := "http://" + addr + "/"
	if result.URL != address {
		return fmt.Errorf("unexpected browser address response")
	}
	return openBrowser(address)
}

// Installation launchers use auto; explicit --listen retains its original behavior.
func autoListen(dir string) (string, error) {
	saved, err := os.ReadFile(filepath.Join(dir, "listen.addr"))
	preferred := strings.TrimSpace(string(saved))
	if err == nil {
		host, port, e := net.SplitHostPort(preferred)
		n, _ := strconv.Atoi(port)
		if e != nil || host != "127.0.0.1" || n < 9000 || n > 9010 {
			return "", fmt.Errorf("invalid saved management address")
		}
		if _, e := os.Stat(filepath.Join(dir, "manager.lock")); e == nil {
			return preferred, nil
		}
	}
	choices := []string{}
	if preferred != "" {
		choices = append(choices, preferred)
	}
	for port := 9000; port <= 9010; port++ {
		choices = append(choices, fmt.Sprintf("127.0.0.1:%d", port))
	}
	for _, candidate := range choices {
		l, e := net.Listen("tcp", candidate)
		if e == nil {
			l.Close()
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no available loopback port in 9000..9010; existing services were not changed")
}
func saveListen(dir, addr string) error {
	f, err := os.CreateTemp(dir, ".listen-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.WriteString(addr); err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err == nil {
		err = ce
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dir, "listen.addr"))
}
