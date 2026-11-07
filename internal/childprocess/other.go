//go:build !windows

package childprocess

import "os/exec"

func Configure(*exec.Cmd) {}
