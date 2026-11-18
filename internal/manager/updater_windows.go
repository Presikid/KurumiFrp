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

func canPlatformSelfUpdateApp(_ string) (bool, string, string) {
	return true, "windows-installer", ""
}

func executePlatformSelfUpdate(pkgPath, currentExePath, dataDir, listenSpec string, _ []string, _ func(exitCode int)) error {
	f, err := os.Open(pkgPath)
	if err != nil {
		return errors.New("打开下载的 Windows 安装包失败")
	}
	var head [64]byte
	_, rErr := io.ReadFull(f, head[:])
	_ = f.Close()
	if rErr != nil || head[0] != 'M' || head[1] != 'Z' {
		return errors.New("下载的 Windows 安装包文件头校验无效")
	}

	args := "/SP- /VERYSILENT /SUPPRESSMSGBOXES /NORESTART /NORESTARTAPPLICATIONS /RESTARTDAEMON=1"
	if strings.TrimSpace(listenSpec) != "" {
		args += fmt.Sprintf(` /LISTENADDR="%s"`, strings.TrimSpace(listenSpec))
	}
	if strings.TrimSpace(dataDir) != "" {
		args += fmt.Sprintf(` /DATADIR="%s"`, strings.TrimSpace(dataDir))
	}
	if currentExePath != "" {
		exeDir := filepath.Dir(currentExePath)
		if _, err := os.Stat(filepath.Join(exeDir, "native-install.json")); err == nil {
			args += fmt.Sprintf(` /DIR="%s"`, exeDir)
		}
	}

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
		if r == 5 || r == 1223 {
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
