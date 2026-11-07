package main

import (
	"os"
	"path/filepath"
)

// Set by the native package build. Source builds retain the explicit-directory CLI.
var distribution = "source"

func defaultPaths() (string, string, string) {
	switch distribution {
	case "linux-system":
		return "/var/lib/kurumifrp/data", "/var/lib/kurumifrp/cores", "auto"
	case "windows-installed":
		exe, err := os.Executable()
		if err == nil {
			root := filepath.Dir(exe)
			return filepath.Join(root, "data"), filepath.Join(root, "cores"), "auto"
		}
	}
	return "./data", "./frp", "127.0.0.1:9000"
}
