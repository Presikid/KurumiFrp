//go:build !windows

package manager

import "os"

// Persist the source-copy directory entry before replacing state.json.
func syncMigrationDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = f.Sync()
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return err
}
