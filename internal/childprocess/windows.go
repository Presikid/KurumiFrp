//go:build windows

package childprocess

import (
	"os"
	"os/exec"
	"syscall"
)

// Preserve captured pipes without creating a new console for the GUI installation.
func Configure(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= 0x08000000 // CREATE_NO_WINDOW
}

// CREATE_NO_WINDOW has no console to receive CTRL_BREAK_EVENT. Do not claim
// graceful shutdown by adding CREATE_NEW_PROCESS_GROUP; keep the bounded direct
// termination fallback until a supported authenticated frp shutdown API is used.
func Terminate(cmd *exec.Cmd) error { return Kill(cmd) }
func Kill(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return os.ErrProcessDone
	}
	return cmd.Process.Kill()
}
