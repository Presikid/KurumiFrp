//go:build windows

package manager

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

var (
	updShell32            = syscall.NewLazyDLL("shell32.dll")
	updProcShellExecuteW  = updShell32.NewProc("ShellExecuteW")
	updAdvapi32           = syscall.NewLazyDLL("advapi32.dll")
	updProcRegOpenKeyExW  = updAdvapi32.NewProc("RegOpenKeyExW")
	updProcRegQueryValExW = updAdvapi32.NewProc("RegQueryValueExW")
	updProcRegCloseKey    = updAdvapi32.NewProc("RegCloseKey")
)

func canPlatformSelfUpdateApp(exePath string) (bool, string, string) {
	// Inno records a single installation for our AppId. A copied or renamed
	// binary must not replace that installation or redirect its uninstall entry.
	installDir := registeredWindowsInstallDir()
	if installDir == "" || !strings.EqualFold(filepath.Clean(exePath), filepath.Join(installDir, "kurumi.exe")) {
		return false, "manual", "当前程序不在 Windows 安装器登记的位置，请从发布页下载安装包手动升级"
	}
	return true, "windows-installer", ""
}

func registeredWindowsInstallDir() string {
	keyName, _ := syscall.UTF16PtrFromString("Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\io.github.Presikid.KurumiFRP_is1")
	var key syscall.Handle
	if err := syscall.RegOpenKeyEx(syscall.HKEY_LOCAL_MACHINE, keyName, 0, syscall.KEY_QUERY_VALUE|syscall.KEY_WOW64_64KEY, &key); err != nil {
		return ""
	}
	defer syscall.RegCloseKey(key)
	value, _ := syscall.UTF16PtrFromString("InstallLocation")
	var dir [32768]uint16
	size := uint32(len(dir) * 2)
	var kind uint32
	if err := syscall.RegQueryValueEx(key, value, nil, &kind, (*byte)(unsafe.Pointer(&dir[0])), &size); err != nil || kind != syscall.REG_SZ {
		return ""
	}
	return syscall.UTF16ToString(dir[:size/2])
}

func executePlatformSelfUpdate(pkgPath, currentExePath, dataDir, listenSpec, _ string, _ []string, _ func(exitCode int)) error {
	if ok, _, hint := canPlatformSelfUpdateApp(currentExePath); !ok {
		return errors.New(hint)
	}
	f, err := os.Open(pkgPath)
	if err != nil {
		return err
	}
	var head [64]byte
	_, err = io.ReadFull(f, head[:])
	f.Close()
	if err != nil || head[0] != 'M' || head[1] != 'Z' {
		return errors.New("下载的 Windows 安装包文件头校验无效")
	}
	absData, err := filepath.Abs(dataDir)
	if err != nil {
		return err
	}
	args := "/SP- /VERYSILENT /SUPPRESSMSGBOXES /NORESTART /NORESTARTAPPLICATIONS /RESTARTDAEMON=1"
	args += " " + syscall.EscapeArg("/DIR="+filepath.Dir(currentExePath))
	args += " " + syscall.EscapeArg("/DATADIR="+absData)
	if listenSpec != "" {
		args += " " + syscall.EscapeArg("/LISTENADDR="+listenSpec)
	}
	// SetupLdr requests elevation itself and retains the original user for
	// the daemon restart. A declined UAC prompt must not prompt a second time.
	return winShellExec("open", pkgPath, args, filepath.Dir(pkgPath))
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
		if r == 5 || errors.Is(callErr, syscall.Errno(1223)) {
			return errors.New("未授予 Windows 管理员权限，已取消安装升级")
		}
		return fmt.Errorf("启动静默升级程序失败 (code=%d): %v", r, callErr)
	}
	return nil
}

// detectPlatformSystemProxy dynamically reads Windows current-user system proxy (set by NekoBox, Clash, v2rayN, or Windows Settings).
func detectPlatformSystemProxy() string {
	subKey, err := syscall.UTF16PtrFromString(`Software\Microsoft\Windows\CurrentVersion\Internet Settings`)
	if err != nil {
		return ""
	}
	var hKey uintptr
	const keyQueryValue = 0x0001
	r0, _, _ := updProcRegOpenKeyExW.Call(
		uintptr(syscall.HKEY_CURRENT_USER),
		uintptr(unsafe.Pointer(subKey)),
		0,
		keyQueryValue,
		uintptr(unsafe.Pointer(&hKey)),
	)
	if r0 != 0 || hKey == 0 {
		return ""
	}
	defer updProcRegCloseKey.Call(hKey)

	enableName, _ := syscall.UTF16PtrFromString("ProxyEnable")
	var valType uint32
	var enableVal uint32
	dwSize := uint32(4)
	r1, _, _ := updProcRegQueryValExW.Call(
		hKey,
		uintptr(unsafe.Pointer(enableName)),
		0,
		uintptr(unsafe.Pointer(&valType)),
		uintptr(unsafe.Pointer(&enableVal)),
		uintptr(unsafe.Pointer(&dwSize)),
	)
	if r1 != 0 || enableVal == 0 {
		return ""
	}

	srvName, _ := syscall.UTF16PtrFromString("ProxyServer")
	var byteLen uint32
	r2, _, _ := updProcRegQueryValExW.Call(
		hKey,
		uintptr(unsafe.Pointer(srvName)),
		0,
		uintptr(unsafe.Pointer(&valType)),
		0,
		uintptr(unsafe.Pointer(&byteLen)),
	)
	if r2 != 0 || byteLen == 0 || byteLen > 4096 {
		return ""
	}
	u16Buf := make([]uint16, (byteLen+1)/2+1)
	r3, _, _ := updProcRegQueryValExW.Call(
		hKey,
		uintptr(unsafe.Pointer(srvName)),
		0,
		uintptr(unsafe.Pointer(&valType)),
		uintptr(unsafe.Pointer(&u16Buf[0])),
		uintptr(unsafe.Pointer(&byteLen)),
	)
	if r3 != 0 {
		return ""
	}
	return parseWinINETProxyString(strings.TrimSpace(syscall.UTF16ToString(u16Buf)))
}
