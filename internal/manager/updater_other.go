//go:build !windows

package manager

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

func canPlatformSelfUpdateApp(exePath string) (bool, string, string) {
	if runtime.GOOS != "linux" || exePath == "" {
		return false, "manual", "请前往 GitHub Releases 下载对应系统最新包覆盖升级"
	}
	exeDir := filepath.Dir(exePath)
	probe := filepath.Join(exeDir, ".kurumi-upd-probe")
	if err := os.WriteFile(probe, []byte("1"), 0o600); err != nil {
		return false, "linux-package", "当前以受限系统服务账户运行（/opt/kurumifrp 只读），FRP 核心可直接一键热更；本体升级请下载最新 .deb / .rpm 包执行 sudo apt install ./kurumifrp_*.deb"
	}
	_ = os.Remove(probe)
	return true, "linux-binary", ""
}

func executePlatformSelfUpdate(pkgPath, currentExePath, _ string, origArgs []string, onShutdown func()) error {
	if !strings.HasSuffix(pkgPath, ".tar.gz") {
		return errors.New("当前 Linux 模式需要 .tar.gz 便携更新包")
	}
	raw, err := os.ReadFile(pkgPath)
	if err != nil {
		return err
	}
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("解压更新包失败: %w", err)
	}
	defer gz.Close()

	exeDir := filepath.Dir(currentExePath)
	bundledDir := filepath.Join(exeDir, "bundled")
	_ = os.MkdirAll(bundledDir, 0o755)

	var newMainBin []byte
	bundledFiles := make(map[string][]byte)

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		cleanName := strings.TrimPrefix(hdr.Name, "./")
		b, err := io.ReadAll(io.LimitReader(tr, maxUpdateArchiveBytes))
		if err != nil {
			return err
		}
		if cleanName == "kurumifrp" {
			newMainBin = b
		} else if strings.HasPrefix(cleanName, "bundled/") {
			bundledFiles[path.Base(cleanName)] = b
		}
	}
	if len(newMainBin) == 0 {
		return errors.New("更新包中未找到 kurumifrp 主程序")
	}

	tmpExe := filepath.Join(exeDir, ".kurumifrp.new")
	if err := os.WriteFile(tmpExe, newMainBin, 0o755); err != nil {
		return err
	}
	for name, content := range bundledFiles {
		mode := os.FileMode(0o755)
		if name == "LICENSE" || name == "manifest.json" {
			mode = 0o644
		}
		tmpB := filepath.Join(bundledDir, "."+name+".new")
		if err := os.WriteFile(tmpB, content, mode); err == nil {
			_ = os.Rename(tmpB, filepath.Join(bundledDir, name))
		}
	}
	if err := os.Rename(tmpExe, currentExePath); err != nil {
		_ = os.Remove(tmpExe)
		return err
	}
	_ = os.RemoveAll(filepath.Dir(pkgPath))

	// Restart smoothly: under systemd (INVOCATION_ID set with Restart=always), graceful shutdown triggers automatic systemd restart;
	// otherwise spawn a detached shell that waits 800ms for port release and execs the updated binary.
	go func() {
		time.Sleep(350 * time.Millisecond)
		if os.Getenv("INVOCATION_ID") == "" {
			var filtered []string
			for _, a := range origArgs {
				if a != "-open" && a != "--open" {
					filtered = append(filtered, a)
				}
			}
			cmdArgs := append([]string{"-c", `sleep 0.8; exec "$0" "$@"`, currentExePath}, filtered...)
			re := exec.Command("/bin/sh", cmdArgs...)
			re.Dir = exeDir
			re.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			_ = re.Start()
		}
		if onShutdown != nil {
			onShutdown()
		}
	}()
	return nil
}
