//go:build !windows

package main

import "fmt"

func windowsUserRoot() (string, error) {
	return "", fmt.Errorf("Windows installed layout is only available on Windows")
}
func prepareInstalled(bool) error { return nil }
func checkMachineGate() error     { return nil }
func installedOperation(exe, operation string) error {
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
