//go:build linux || darwin

package manager

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func lockDataFile(file *os.File) error {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return errDataDirectoryLocked
	}
	return err
}

func unlockDataFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
