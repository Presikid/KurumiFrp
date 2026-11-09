//go:build !linux

package main

import "fmt"

func legacyLinuxMigration() error {
	return fmt.Errorf("Linux migration is unavailable on this platform")
}
