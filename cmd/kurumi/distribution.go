package main

import "path/filepath"

// Set by native packaging. Source builds retain the explicit-directory CLI.
var distribution = "source"

func defaultPaths() (string, string, string, error) {
	switch distribution {
	case "linux-system":
		return "/opt/kurumifrp/runtime/data", "/opt/kurumifrp/runtime/cores", "auto", nil
	case "windows-installed":
		root, err := windowsUserRoot()
		if err != nil {
			return "", "", "", err
		}
		return filepath.Join(root, "data"), filepath.Join(root, "cores"), "auto", nil
	}
	return "./data", "./frp", "127.0.0.1:9000", nil
}
