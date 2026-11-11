//go:build windows

package manager

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func lockDataFile(file *os.File) error {
	var overlap windows.Overlapped
	err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlap)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return errDataDirectoryLocked
	}
	return err
}

func unlockDataFile(file *os.File) error {
	var overlap windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlap)
}
