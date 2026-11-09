//go:build !linux

package manager

// No automatic cleanup is attempted where process liveness and filesystem
// ownership cannot yet be verified with the same guarantees as Linux.
func statusSystemIdentity() string { return "" }
func reapPrivateStatuses(string)   {}
