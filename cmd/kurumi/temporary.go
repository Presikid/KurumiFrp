package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Called only after holding the manager's data-directory lock. Temporary paths
// apply to this process and its frp children, never to the user's global settings.
func prepareTemporary(data string) (func(), error) {
	root := temporaryRoot(data)
	if e := os.MkdirAll(root, 0700); e != nil {
		return nil, e
	}
	if e := plainPath(root); e != nil {
		return nil, e
	}
	entries, e := os.ReadDir(root)
	if e != nil {
		return nil, e
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "session-") || !entry.IsDir() {
			continue
		}
		p := filepath.Join(root, entry.Name())
		if e = plainPath(p); e != nil {
			return nil, e
		}
		marker := filepath.Join(p, ".kurumi-temp")
		st, err := os.Lstat(marker)
		if err != nil || !st.Mode().IsRegular() {
			continue
		}
		b, err := os.ReadFile(marker)
		if err != nil || string(b) != productID {
			continue
		}
		if e = os.RemoveAll(p); e != nil {
			return nil, e
		}
	}
	session, e := os.MkdirTemp(root, "session-")
	if e != nil {
		return nil, e
	}
	if e = os.WriteFile(filepath.Join(session, ".kurumi-temp"), []byte(productID), 0600); e != nil {
		os.RemoveAll(session)
		return nil, e
	}
	before := map[string]*string{}
	restore := func() {
		for key, value := range before {
			if value == nil {
				_ = os.Unsetenv(key)
			} else {
				_ = os.Setenv(key, *value)
			}
		}
		_ = os.RemoveAll(session)
	}
	for _, key := range []string{"TMPDIR", "TEMP", "TMP"} {
		if value, ok := os.LookupEnv(key); ok {
			v := value
			before[key] = &v
		} else {
			before[key] = nil
		}
		if e = os.Setenv(key, session); e != nil {
			restore()
			return nil, fmt.Errorf("configure temporary directory: %w", e)
		}
	}
	return restore, nil
}

func temporaryRoot(data string) string {
	if distribution == "linux-system" && os.Getenv("RUNTIME_DIRECTORY") == "/run/kurumifrp" {
		return "/run/kurumifrp"
	}
	if distribution == "windows-installed" || distribution == "linux-system" {
		return filepath.Join(filepath.Dir(data), "tmp")
	}
	return filepath.Join(data, "tmp")
}
