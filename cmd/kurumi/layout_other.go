//go:build !windows

package main

import "fmt"

func windowsUserRoot() (string, error) {
	return "", fmt.Errorf("Windows installed layout is only available on Windows")
}
func prepareInstalled(bool) error { return nil }
func checkMachineGate() error     { return nil }
func validateMaintenanceTarget(string) error {
	return fmt.Errorf("installer target is only supported on Windows")
}
func installedOperation(exe, operation, target string) error {
	if target != "" {
		return fmt.Errorf("installer target is only supported on Windows")
	}
	root, e := nativeRoot(exe)
	if e != nil {
		return e
	}
	switch operation {
	case "init":
		return initNative(root)
	case "purge":
		return purgeNative(root)
	default:
		return fmt.Errorf("invalid native operation")
	}
}
