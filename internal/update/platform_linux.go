//go:build linux

package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const updateRequest = "/opt/kurumifrp/runtime/update.request"
const updateScratch = "/opt/kurumifrp/private"

// The updater holds this lock until the package manager and cleanup finish.
// Purge takes the same lock; an ordinary upgrade never takes it.
func lockSystemUpdate() (*os.File, error) {
	for p := filepath.Dir(updateScratch); ; p = filepath.Dir(p) {
		parent, err := os.Lstat(p)
		if err != nil || !parent.IsDir() || parent.Mode().Perm()&0022 != 0 {
			return nil, fmt.Errorf("unsafe update parent: %s", p)
		}
		stat, ok := parent.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 {
			return nil, fmt.Errorf("untrusted update parent: %s", p)
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	info, err := os.Lstat(updateScratch)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return nil, fmt.Errorf("unsafe update directory")
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 0 {
		return nil, fmt.Errorf("update directory is not root owned")
	}
	fd, err := syscall.Open(filepath.Join(updateScratch, ".update.lock"), syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "update lock")
	info, err = f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	st, ok = info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || st.Uid != 0 || st.Nlink != 1 {
		f.Close()
		return nil, fmt.Errorf("unsafe update lock")
	}
	if err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another update or purge is active: %w", err)
	}
	return f, nil
}

func packageDistribution() string {
	output, err := exec.Command("dpkg-query", "-W", "-f=${Status}", "kurumifrp").Output()
	if err == nil {
		status := strings.Fields(string(output))
		if len(status) != 3 || status[1] != "ok" {
			return ""
		}
		switch status[0] {
		case "unknown", "install", "hold", "deinstall", "purge":
		default:
			return ""
		}
		switch status[2] {
		case "installed":
			return "linux-deb"
		case "config-files", "not-installed":
			// A selected but not fully installed package may still be in transition.
			if status[0] == "install" || status[0] == "hold" {
				return ""
			}
		default:
			return ""
		}
	} else {
		var exitErr *exec.ExitError
		// Missing dpkg or exit 1 with no output allows an RPM check.
		// Some older dpkg versions also use exit 1 for other errors.
		if !errors.Is(err, exec.ErrNotFound) &&
			!(errors.As(err, &exitErr) && exitErr.ExitCode() == 1 && len(output) == 0) {
			return ""
		}
	}
	if e := exec.Command("rpm", "-q", "kurumifrp").Run(); e == nil {
		return "linux-rpm"
	}
	return ""
}
func DetectDistribution(distribution string) string {
	if distribution == "linux-system" {
		return packageDistribution()
	}
	return distribution
}
func requestSystemUpdate(tag string) error {
	if _, e := os.Stat("/run/systemd/system"); e != nil {
		return fmt.Errorf("需要运行中的 systemd 更新服务")
	}
	if e := exec.Command("systemctl", "is-active", "--quiet", "kurumifrp-updater.path").Run(); e != nil {
		return fmt.Errorf("系统更新服务未启用")
	}
	temp, e := os.CreateTemp(filepath.Dir(updateRequest), ".update-request-")
	if e != nil {
		return e
	}
	defer os.Remove(temp.Name())
	_, e = temp.WriteString(tag)
	if e == nil {
		e = temp.Sync()
	}
	if ce := temp.Close(); e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	// Publish the complete request without replacing another pending request.
	e = unix.Renameat2(unix.AT_FDCWD, temp.Name(), unix.AT_FDCWD, updateRequest, unix.RENAME_NOREPLACE)
	if errors.Is(e, unix.ENOSYS) || errors.Is(e, unix.EINVAL) || errors.Is(e, unix.EOPNOTSUPP) {
		// Older kernels and some filesystems do not support RENAME_NOREPLACE.
		e = os.Link(temp.Name(), updateRequest)
	}
	if errors.Is(e, os.ErrExist) {
		return fmt.Errorf("已有待处理或状态未确认的系统更新请求，请先核对")
	}
	return e
}

// Executed by the root-owned systemd unit; the unprivileged request only selects
// a version. All release metadata, package bytes and hashes are obtained again.
func PerformSystemUpdate() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("system update requires root")
	}
	lock, e := lockSystemUpdate()
	if e != nil {
		return e
	}
	defer lock.Close()
	fd, e := syscall.Open(updateRequest, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return e
	}
	f := os.NewFile(uintptr(fd), updateRequest)
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return e
	}
	if !info.Mode().IsRegular() || info.Size() > 64 {
		return fmt.Errorf("invalid update request")
	}
	data, e := io.ReadAll(io.LimitReader(f, 65))
	if e != nil || len(data) > 64 {
		return fmt.Errorf("invalid update request")
	}
	if e = os.Remove(updateRequest); e != nil {
		return e
	}
	tag := strings.TrimSpace(string(data))
	distribution := packageDistribution()
	if distribution == "" {
		return fmt.Errorf("installed package not found")
	}
	m := New("", distribution)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	a, e := m.selectAsset(ctx, tag)
	if e != nil {
		return e
	}
	dir, e := os.MkdirTemp(updateScratch, "update-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(dir)
	path, e := m.download(ctx, a, dir)
	if e != nil {
		return e
	}
	// The download deadline must not terminate an in-progress package transaction.
	cancel()
	var cmd *exec.Cmd
	if distribution == "linux-deb" {
		cmd = exec.Command("apt-get", "-y", "install", path)
		cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	} else {
		cmd = exec.Command("dnf", "-y", "install", path)
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
