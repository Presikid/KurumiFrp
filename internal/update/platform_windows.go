//go:build windows

package update

import (
	"fmt"
	"syscall"
	"unsafe"
)

func launchInstaller(path string) error {
	verb, _ := syscall.UTF16PtrFromString("runas")
	file, e := syscall.UTF16PtrFromString(path)
	if e != nil {
		return e
	}
	args, _ := syscall.UTF16PtrFromString("/VERYSILENT /SUPPRESSMSGBOXES /NORESTART")
	result, _, callErr := syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteW").Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(file)), uintptr(unsafe.Pointer(args)), 0, 1)
	if result <= 32 {
		return fmt.Errorf("无法启动安装程序或管理员授权被取消：%v", callErr)
	}
	return nil
}
func requestSystemUpdate(string) error { return fmt.Errorf("当前系统不支持此更新方式") }
