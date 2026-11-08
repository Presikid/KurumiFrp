//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package childprocess

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// Configure gives a managed command its own group, isolating it from terminal
// signals and making cancellation include children which inherit that group.
func Configure(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.SysProcAttr.Pgid = 0
	if cmd.Cancel != nil {
		// CommandContext otherwise kills only the immediate child on timeout.
		cmd.Cancel = func() error { return Kill(cmd) }
	}
}

func Terminate(cmd *exec.Cmd) error { return signalGroup(cmd, syscall.SIGINT) }
func Kill(cmd *exec.Cmd) error      { return signalGroup(cmd, syscall.SIGKILL) }

func signalGroup(cmd *exec.Cmd, signal syscall.Signal) error {
	if cmd == nil || cmd.Process == nil || cmd.Process.Pid <= 0 {
		return os.ErrProcessDone
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid || cmd.SysProcAttr.Pgid != 0 {
		return cmd.Process.Signal(signal)
	}
	// Only groups created by Configure may be addressed with a negative PID.
	// A group may still contain descendants after the leader has been reaped.
	err := syscall.Kill(-cmd.Process.Pid, signal)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}
