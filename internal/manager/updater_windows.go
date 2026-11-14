//go:build windows

package manager

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

var (
	updShell32           = syscall.NewLazyDLL("shell32.dll")
	updProcShellExecuteW = updShell32.NewProc("ShellExecuteW")
)

func canPlatformSelfUpdateApp(_ string) (bool, string, string) {
	return true, "windows-installer", ""
}

func executePlatformSelfUpdate(pkgPath, currentExePath, listenSpec string, _ []string, _ func()) error {
	head, err := os.ReadFile(pkgPath)
	if err != nil || len(head) < 64 || head[0] != 'M' || head[1] != 'Z' {
		return errors.New("下载的 Windows 安装包文件头校验无效")
	}

	args := "/SP- /VERYSILENT /SUPPRESSMSGBOXES /NORESTART /RESTARTDAEMON=1"
	if listenSpec != "" {
		args += " /LISTENADDR=" + listenSpec
	}
	if currentExePath != "" {
		exeDir := filepath.Dir(currentExePath)
		if _, err := os.Stat(filepath.Join(exeDir, "native-install.json")); err == nil {
			args += fmt.Sprintf(` /DIR="%s"`, exeDir)
		}
	}

	// Launch Inno Setup stub via "open" first so SetupLdr preserves the original non-elevated user session for runasoriginaluser;
	// fall back to "runas" if shell policy requires explicit elevation verb.
	if err := winShellExec("open", pkgPath, args, filepath.Dir(pkgPath)); err != nil {
		if err2 := winShellExec("runas", pkgPath, args, filepath.Dir(pkgPath)); err2 != nil {
			return err2
		}
	}
	return nil
}

func winShellExec(verbStr, fileStr, paramsStr, dirStr string) error {
	verb, err := syscall.UTF16PtrFromString(verbStr)
	if err != nil {
		return err
	}
	file, err := syscall.UTF16PtrFromString(fileStr)
	if err != nil {
		return err
	}
	params, err := syscall.UTF16PtrFromString(paramsStr)
	if err != nil {
		return err
	}
	dir, err := syscall.UTF16PtrFromString(dirStr)
	if err != nil {
		return err
	}
	r, _, callErr := updProcShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(file)),
		uintptr(unsafe.Pointer(params)),
		uintptr(unsafe.Pointer(dir)),
		1, // SW_SHOWNORMAL
	)
	if r <= 32 {
		if r == 5 || r == 1223 {
			return errors.New("未授予 Windows 管理员权限，已取消安装升级")
		}
		return fmt.Errorf("启动静默升级程序失败 (code=%d): %v", r, callErr)
	}
	return nil
}
