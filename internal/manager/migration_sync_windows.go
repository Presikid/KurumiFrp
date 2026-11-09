//go:build windows

package manager

// The file itself is synced before the rename. Windows directory-entry
// durability is not guaranteed by the standard library's file operations.
func syncMigrationDir(string) error { return nil }
