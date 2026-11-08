//go:build !windows && !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package childprocess

import (
	"os"
	"os/exec"
)

// Platforms without POSIX process groups retain direct-child management.
func Configure(*exec.Cmd) {}
func Terminate(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return os.ErrProcessDone
	}
	return cmd.Process.Signal(os.Interrupt)
}
func Kill(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return os.ErrProcessDone
	}
	return cmd.Process.Kill()
}
