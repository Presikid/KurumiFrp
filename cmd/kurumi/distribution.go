package main

import "path/filepath"

// Set by native packaging. Source builds retain the explicit-directory CLI.
var distribution = "source"

func defaultPaths() (string, string, error) {
	switch distribution {
	case "linux-system":
		return "/opt/kurumifrp/runtime/data", "auto", nil
	case "windows-installed":
		root, err := windowsUserRoot()
		if err != nil {
			return "", "", err
		}
		return filepath.Join(root, "data"), "auto", nil
	}
	return "./data", "127.0.0.1:9000", nil
}
