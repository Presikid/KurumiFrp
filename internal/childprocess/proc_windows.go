//go:build windows

package childprocess

import (
	"os/exec"
	"syscall"
)

const (
	createNewProcessGroup = 0x00000200
	createNoWindow        = 0x08000000
	ctrlBreakEvent        = 1
)

var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	procGenerateConsoleEvent = kernel32.NewProc("GenerateConsoleCtrlEvent")
)

// Configure hides console windows and creates a dedicated process group on Windows.
func Configure(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow | createNewProcessGroup,
	}
}

// Terminate sends CTRL_BREAK_EVENT so frpc/frps can release remote ports cleanly before fallback Kill.
func Terminate(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	r1, _, _ := procGenerateConsoleEvent.Call(uintptr(ctrlBreakEvent), uintptr(uint32(cmd.Process.Pid)))
	if r1 != 0 {
		return nil
	}
	return cmd.Process.Kill()
}

// Kill forcefully terminates the child process on Windows.
func Kill(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
