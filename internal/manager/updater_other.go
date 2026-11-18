//go:build !windows

package manager

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Presikid/KurumiFrp/internal/version"
)

func canPlatformSelfUpdateApp(exePath string) (bool, string, string) {
	if runtime.GOOS != "linux" || exePath == "" {
		return false, "manual", "请前往 GitHub Releases 下载对应系统最新包覆盖升级"
	}
	if version.Distribution == "docker" {
		return false, "manual", "请更新 KurumiFrp 镜像并重新创建容器，保留原有数据卷"
	}
	if version.Distribution == "linux-system" {
		return false, "linux-package", "请通过安装时使用的 .deb 或 .rpm 软件包升级"
	}
	probe, err := os.CreateTemp(filepath.Dir(exePath), ".kurumi-upd-probe-*")
	if err != nil {
		return false, "linux-package", "程序目录不可写，请通过安装时使用的 .deb 或 .rpm 软件包升级"
	}
	probe.Close()
	os.Remove(probe.Name())
	return true, "linux-binary", ""
}

func executePlatformSelfUpdate(pkgPath, currentExePath, _, listenSpec, targetVersion string, origArgs []string, onShutdown func(int)) error {
	if onShutdown == nil {
		return errors.New("无法在当前运行环境内自动重启")
	}
	exitCode := 0
	var restart *exec.Cmd
	execPID := os.Getenv("SYSTEMD_EXEC_PID")
	if os.Getenv("INVOCATION_ID") != "" && (execPID == strconv.Itoa(os.Getpid()) || execPID == "" && os.Getppid() == 1) {
		exitCode = 42
	} else {
		// The old process can still own listeners and children after Shutdown
		// starts. Preserve the working directory for relative CLI arguments.
		args := []string{"-c", shellRelaunch, "kurumi-restart", strconv.Itoa(os.Getpid()), currentExePath}
		for _, arg := range origArgs {
			if arg != "-open" && arg != "--open" {
				args = append(args, arg)
			}
		}
		if listenSpec != "" {
			args = append(args, "-listen="+listenSpec)
		}
		restart = exec.Command("/bin/sh", args...)
		restart.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}
	if err := installPortableUpdate(pkgPath, currentExePath, targetVersion, restart); err != nil {
		return err
	}
	os.RemoveAll(filepath.Dir(pkgPath))
	onShutdown(exitCode)
	return nil
}

const shellRelaunch = `while kill -0 "$1" 2>/dev/null; do sleep 0.2; done; shift; exec "$@"`

func installPortableUpdate(pkgPath, exePath, targetVersion string, restart *exec.Cmd) error {
	pkg, err := os.Open(pkgPath)
	if err != nil {
		return err
	}
	defer pkg.Close()
	gz, err := gzip.NewReader(pkg)
	if err != nil {
		return err
	}
	defer gz.Close()

	dir := filepath.Dir(exePath)
	stage, err := os.MkdirTemp(dir, ".kurumi-update-*")
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			os.RemoveAll(stage)
		}
	}()
	members := map[string]os.FileMode{"kurumifrp": 0755, "bundled/frpc": 0755, "bundled/frps": 0755, "bundled/LICENSE": 0644, "bundled/manifest.json": 0644}
	tr := tar.NewReader(gz)
	extracted := 0
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(hdr.Name, "./")
		mode, wanted := members[name]
		if !wanted {
			continue
		}
		if hdr.Typeflag != tar.TypeReg || hdr.Size <= 0 || hdr.Size > maxUpdateArchiveBytes {
			return fmt.Errorf("无效的更新包文件 %s", name)
		}
		f, err := os.OpenFile(filepath.Join(stage, filepath.Base(name)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, tr)
		if err = errors.Join(err, f.Close()); err != nil {
			return err
		}
		extracted++
	}
	if extracted != len(members) {
		return errors.New("便携更新包缺少主程序或内置组件")
	}
	// Test the new executable on this CPU before changing the installation.
	next := filepath.Join(stage, "kurumifrp")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, next, "-version").Output()
	if err != nil {
		return fmt.Errorf("新版主程序运行预检失败: %w", err)
	}
	if strings.TrimSpace(string(out)) != targetVersion {
		return fmt.Errorf("新版主程序版本与发布标签 %s 不符", targetVersion)
	}
	bundled := filepath.Join(dir, "bundled")
	if err := os.MkdirAll(bundled, 0755); err != nil {
		return err
	}
	// Keep the old files until the main executable is committed. Leave
	// unrelated local files in bundled/ alone.
	var installed, backedUp []string
	commit := func() error {
		for _, name := range []string{"frpc", "frps", "LICENSE", "manifest.json"} {
			dst := filepath.Join(bundled, name)
			if info, err := os.Lstat(dst); err == nil {
				if !info.Mode().IsRegular() {
					return fmt.Errorf("内置组件 %s 不是普通文件", name)
				}
				backup := filepath.Join(stage, name+".old")
				if err := os.Link(dst, backup); err != nil {
					// Filesystems without hard links still support rename-based rollback.
					if err := os.Rename(dst, backup); err != nil {
						return err
					}
				}
				backedUp = append(backedUp, name)
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if err := os.Rename(filepath.Join(stage, name), dst); err != nil {
				return err
			}
			installed = append(installed, dst)
		}
		// The helper waits for this PID to exit. Start it while we can
		// still roll back bundled files and before switching the executable.
		if restart == nil {
			return os.Rename(next, exePath)
		}
		if err := restart.Start(); err != nil {
			return fmt.Errorf("启动新版程序的重启助手失败: %w", err)
		}
		if err := os.Rename(next, exePath); err != nil {
			restart.Process.Kill()
			restart.Wait()
			return err
		}
		restart.Process.Release()
		return nil
	}
	if err := commit(); err != nil {
		var rollback error
		for _, dst := range installed {
			rollback = errors.Join(rollback, os.Remove(dst))
		}
		for _, name := range backedUp {
			rollback = errors.Join(rollback, os.Rename(filepath.Join(stage, name+".old"), filepath.Join(bundled, name)))
		}
		if rollback != nil {
			cleanup = false
			return fmt.Errorf("升级失败 (%v)，备份保存在 %s: %w", err, stage, rollback)
		}
		return err
	}
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
