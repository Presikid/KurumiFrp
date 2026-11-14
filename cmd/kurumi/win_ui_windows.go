//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

var (
	shell32           = syscall.NewLazyDLL("shell32.dll")
	procShellExecuteW = shell32.NewProc("ShellExecuteW")
	user32            = syscall.NewLazyDLL("user32.dll")
	procMessageBoxW   = user32.NewProc("MessageBoxW")
)

func platformOpenURL(u string) error {
	verb, err := syscall.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	target, err := syscall.UTF16PtrFromString(u)
	if err != nil {
		return err
	}
	r, _, callErr := procShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(target)),
		0,
		0,
		1, // SW_SHOWNORMAL
	)
	if r <= 32 {
		return callErr
	}
	return nil
}

func platformShowFatalError(msg string) {
	title, _ := syscall.UTF16PtrFromString("KurumiFrp 启动失败")
	text, _ := syscall.UTF16PtrFromString(msg)
	procMessageBoxW.Call(
		0,
		uintptr(unsafe.Pointer(text)),
		uintptr(unsafe.Pointer(title)),
		0x00000010, // MB_ICONERROR | MB_OK
	)
}
