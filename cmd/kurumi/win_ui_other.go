//go:build !windows

package main

import (
	"os/exec"
	"runtime"
)

func platformOpenURL(u string) error {
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		cmd = exec.Command("open", u)
	} else {
		cmd = exec.Command("xdg-open", u)
	}
	return cmd.Start()
}

func platformShowFatalError(_ string) {}
