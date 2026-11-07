//go:build windows

package main

import (
	"os"
	"syscall"
	"unsafe"
)

func reportDesktopError(err error) {
	for _, arg := range os.Args[1:] {
		if arg == "--open" {
			title, _ := syscall.UTF16PtrFromString("KurumiFRP")
			message, _ := syscall.UTF16PtrFromString(err.Error())
			syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(message)), uintptr(unsafe.Pointer(title)), 0x10)
			return
		}
	}
}
