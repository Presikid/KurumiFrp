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
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return false, "manual", "当前运行于容器环境，请拉取最新 Docker 镜像并重建容器完成升级"
	}
	cleanExe := filepath.Clean(exePath)
	if strings.HasPrefix(cleanExe, "/opt/kurumifrp/") || strings.HasPrefix(cleanExe, "/usr/") {
		return false, "linux-package", "当前为系统软件包安装（DEB/RPM），FRP 核心可直接热更新；软件本体请使用 apt/dnf 安装最新 .deb 或 .rpm 包"
	}
	exeDir := filepath.Dir(cleanExe)
	probe, err := os.CreateTemp(exeDir, ".kurumi-upd-probe-*")
	if err != nil {
		return false, "linux-package", "当前运行目录只读，FRP 核心可直接一键热更；本体升级请使用包管理器或有写权限的账户执行"
	}
	probeName := probe.Name()
	_ = probe.Close()
	_ = os.Remove(probeName)
	return true, "linux-binary", ""
}

func executePlatformSelfUpdate(pkgPath, currentExePath, _ string, _ string, origArgs []string, onShutdown func(exitCode int)) error {
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

	tmpExe := filepath.Join(exeDir, fmt.Sprintf(".kurumifrp.new.%d", os.Getpid()))
	if err := os.WriteFile(tmpExe, newMainBin, 0o755); err != nil {
		return err
	}
	if out, vErr := exec.Command(tmpExe, "-version").CombinedOutput(); vErr != nil || len(bytes.TrimSpace(out)) == 0 {
		_ = os.Remove(tmpExe)
		return fmt.Errorf("新版主程序在当前系统架构下自检失败: %v", vErr)
	}

	type bundledBackup struct {
		existed bool
		data    []byte
		mode    os.FileMode
	}
	backups := make(map[string]bundledBackup, len(bundledFiles))
	restoreBundled := func() {
		for name, bk := range backups {
			dst := filepath.Join(bundledDir, name)
			if bk.existed {
				_ = os.WriteFile(dst, bk.data, bk.mode)
			} else {
				_ = os.Remove(dst)
			}
		}
	}

	for name, content := range bundledFiles {
		dst := filepath.Join(bundledDir, name)
		if st, sErr := os.Stat(dst); sErr == nil {
			oldBytes, _ := os.ReadFile(dst)
			backups[name] = bundledBackup{existed: true, data: oldBytes, mode: st.Mode()}
		} else {
			backups[name] = bundledBackup{existed: false}
		}
		mode := os.FileMode(0o755)
		if name == "LICENSE" || name == "manifest.json" {
			mode = 0o644
		}
		tmpB := filepath.Join(bundledDir, fmt.Sprintf(".%s.new.%d", name, os.Getpid()))
		if err := os.WriteFile(tmpB, content, mode); err != nil {
			_ = os.Remove(tmpExe)
			restoreBundled()
			return err
		}
		if err := os.Rename(tmpB, dst); err != nil {
			_ = os.Remove(tmpB)
			_ = os.Remove(tmpExe)
			restoreBundled()
			return err
		}
	}

	var restartCmd *exec.Cmd
	if os.Getenv("INVOCATION_ID") == "" {
		var filtered []string
		for _, a := range origArgs {
			if a != "-open" && a != "--open" {
				filtered = append(filtered, a)
			}
		}
		waitScript := `pid="$1"; shift; i=0; while kill -0 "$pid" 2>/dev/null && [ "$i" -lt 75 ]; do sleep 0.2; i=$((i+1)); done; exec "$@"`
		cmdArgs := append([]string{"-c", waitScript, "sh", fmt.Sprintf("%d", os.Getpid()), currentExePath}, filtered...)
		restartCmd = exec.Command("/bin/sh", cmdArgs...)
		restartCmd.Dir = exeDir
		restartCmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := restartCmd.Start(); err != nil {
			_ = os.Remove(tmpExe)
			restoreBundled()
			return fmt.Errorf("启动便携版重启守护进程失败: %w", err)
		}
	}

	if err := os.Rename(tmpExe, currentExePath); err != nil {
		_ = os.Remove(tmpExe)
		restoreBundled()
		if restartCmd != nil && restartCmd.Process != nil {
			_ = restartCmd.Process.Kill()
		}
		return err
	}
	_ = os.RemoveAll(filepath.Dir(pkgPath))

	go func() {
		time.Sleep(300 * time.Millisecond)
		if onShutdown != nil {
			onShutdown(0)
		}
	}()
	return nil
}

func detectPlatformSystemProxy() string {
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "HTTP_PROXY", "http_proxy"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			if norm, err := normalizeProxyURL(v); err == nil {
				return norm
			}
		}
	}
	return ""
}
