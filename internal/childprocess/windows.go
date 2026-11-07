//go:build windows

package childprocess

import (
	"os/exec"
	"syscall"
)

// Preserve captured pipes without creating a new console for the GUI installation.
func Configure(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
