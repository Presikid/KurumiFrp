package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type nativeReceipt struct {
	Product string `json:"product"`
	Root    string `json:"root"`
	Schema  int    `json:"schema"`
}

func nativeRoot(exe string) (string, error) {
	root, err := filepath.Abs(filepath.Dir(exe))
	if err != nil {
		return "", err
	}
	if err := plainPath(root); err != nil {
		return "", err
	}
	if root == filepath.VolumeName(root)+string(os.PathSeparator) {
		return "", fmt.Errorf("refusing filesystem root")
	}
	return root, nil
}

func readNativeReceipt(root string) error {
	p := filepath.Join(root, "native-install.json")
	if err := plainPath(p); err != nil {
		return err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	var r nativeReceipt
	if len(b) > 4096 || json.Unmarshal(b, &r) != nil || r.Product != productID || r.Root != root || r.Schema != 1 {
		return fmt.Errorf("native installation ownership was not verified")
	}
	return nil
}

func initNative(root string) error {
	p := filepath.Join(root, "native-install.json")
	if _, err := os.Lstat(p); err == nil {
		return readNativeReceipt(root)
	} else if !os.IsNotExist(err) {
		return err
	}
	// Do not adopt an unrelated existing directory or an old portable installation.
	for _, name := range []string{"data", "cores", "install-receipt.json", "install.receipt"} {
		if _, err := os.Lstat(filepath.Join(root, name)); err == nil {
			return fmt.Errorf("existing unmanaged data retained; select an empty installation directory")
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	b, _ := json.Marshal(nativeReceipt{productID, root, 1})
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

// Only mutable directories owned by this native installation are purged. The
// installer engine owns executable files, shortcuts, ARP registration and itself.
func purgeNative(root string) error {
	if err := readNativeReceipt(root); err != nil {
		return err
	}
	marker := filepath.Join(root, ".uninstalling")
	f, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("maintenance already in progress: %w", err)
	}
	f.Close()
	complete := false
	defer func() {
		if !complete {
			_ = os.Remove(marker)
		}
	}()
	if err := stopExisting(filepath.Join(root, "data"), 45*time.Second); err != nil {
		return err
	}
	for _, name := range []string{"data", "cores"} {
		// RemoveAll removes symlinks/junctions themselves; it never follows their targets.
		if err := os.RemoveAll(filepath.Join(root, name)); err != nil {
			return err
		}
	}
	complete = true
	return nil
}

// Called only after Setup reports successful installation and local API readiness.
// Windows locks the running Setup EXE; retry its exact verified path after exit.
// No folder scan, process-name killing, event-log or package-cache deletion.
func removeSetup(path, digest, self string, timeout time.Duration) error {
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	self, err = filepath.Abs(self)
	if err != nil {
		return err
	}
	if !hashPattern.MatchString(digest) || strings.EqualFold(path, self) || !strings.EqualFold(filepath.Ext(path), ".exe") {
		return fmt.Errorf("invalid installer cleanup request")
	}
	if err := plainPath(path); err != nil {
		return err
	}
	until := time.Now().Add(timeout)
	for {
		got, err := fileHash(path)
		if err != nil {
			return err
		}
		if got != digest {
			return fmt.Errorf("installer changed; file retained")
		}
		if err = os.Remove(path); err == nil {
			return nil
		}
		if time.Now().After(until) {
			return fmt.Errorf("installer is still locked; retained at %s", path)
		}
		time.Sleep(250 * time.Millisecond)
	}
}
