//go:build windows

package main

// Windows binaries are machine-installed and read-only. Mutable state lives
// beneath the installation directory in an ACL-protected runtime root, with
// each Windows SID receiving a private profile.
import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var advapi = syscall.NewLazyDLL("advapi32.dll")
var localFree = syscall.NewLazyDLL("kernel32.dll").NewProc("LocalFree")
var sidName = regexp.MustCompile(`^S-1-[0-9]+(?:-[0-9]+)+$`)

const machineACL = "O:BAG:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;0x1200a9;;;BU)"

// Users may create a private child directory, not write/delete existing children.
const profilesACL = "O:BAG:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;;0x1200ad;;;BU)"

type machineReceipt struct {
	Product    string `json:"product"`
	InstallDir string `json:"installDir"`
	Schema     int    `json:"schema"`
}
type profileReceipt struct {
	Product string `json:"product"`
	SID     string `json:"sid"`
	Schema  int    `json:"schema"`
}

func windowsRuntimeRoot() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	install, err := nativeRoot(exe)
	if err != nil {
		return "", err
	}
	return filepath.Join(install, "runtime"), nil
}

// legacyWindowsDataRoot resolves the old ProgramData location only for a
// one-time, ownership-checked migration from earlier machine installations.
func legacyWindowsDataRoot() (string, error) {
	guid := syscall.GUID{Data1: 0x62ab5d82, Data2: 0xfdc1, Data3: 0x4dc3, Data4: [8]byte{0xa9, 0xdd, 0x07, 0x0d, 0x1d, 0x49, 0x5d, 0x97}}
	var p *uint16
	r, _, _ := syscall.NewLazyDLL("shell32.dll").NewProc("SHGetKnownFolderPath").Call(uintptr(unsafe.Pointer(&guid)), 0, 0, uintptr(unsafe.Pointer(&p)))
	if r != 0 || p == nil {
		return "", fmt.Errorf("cannot resolve legacy ProgramData location (HRESULT 0x%x)", r)
	}
	defer syscall.NewLazyDLL("ole32.dll").NewProc("CoTaskMemFree").Call(uintptr(unsafe.Pointer(p)))
	base := syscall.UTF16ToString((*[32768]uint16)(unsafe.Pointer(p))[:])
	if !filepath.IsAbs(base) {
		return "", fmt.Errorf("invalid legacy ProgramData location")
	}
	return filepath.Join(base, "KurumiFRP"), nil
}
func currentSID() (string, error) {
	token, e := syscall.OpenCurrentProcessToken()
	if e != nil {
		return "", e
	}
	defer token.Close()
	u, e := token.GetTokenUser()
	if e != nil {
		return "", e
	}
	s, e := u.User.Sid.String()
	if e != nil || !sidName.MatchString(s) {
		return "", fmt.Errorf("invalid Windows user SID")
	}
	return s, nil
}
func windowsUserRoot() (string, error) {
	root, e := windowsRuntimeRoot()
	if e != nil {
		return "", e
	}
	sid, e := currentSID()
	if e != nil {
		return "", e
	}
	return filepath.Join(root, "profiles", sid), nil
}
func profileACL(sid string) string {
	return "O:" + sid + "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;" + sid + ")"
}

func securityDescriptor(sddl string) (uintptr, error) {
	p, e := syscall.UTF16PtrFromString(sddl)
	if e != nil {
		return 0, e
	}
	var sd uintptr
	r, _, e := advapi.NewProc("ConvertStringSecurityDescriptorToSecurityDescriptorW").Call(uintptr(unsafe.Pointer(p)), 1, uintptr(unsafe.Pointer(&sd)), 0)
	if r == 0 {
		return 0, e
	}
	return sd, nil
}
func securityText(sd uintptr) (string, error) {
	var p *uint16
	// Compare OWNER and DACL; ignore group and auto-inheritance bookkeeping.
	r, _, e := advapi.NewProc("ConvertSecurityDescriptorToStringSecurityDescriptorW").Call(sd, 1, 5, uintptr(unsafe.Pointer(&p)), 0)
	if r == 0 {
		return "", e
	}
	defer localFree.Call(uintptr(unsafe.Pointer(p)))
	s := syscall.UTF16ToString((*[32768]uint16)(unsafe.Pointer(p))[:])
	s = strings.ReplaceAll(s, "D:PAI", "D:P")
	return s, nil
}
func windowsPlainPath(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for p := abs; ; p = filepath.Dir(p) {
		st, e := os.Lstat(p)
		if e != nil {
			return e
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing linked path: %s", p)
		}
		if data, ok := st.Sys().(*syscall.Win32FileAttributeData); ok && data.FileAttributes&0x400 != 0 {
			return fmt.Errorf("refusing reparse-point path: %s", p)
		}
		if parent := filepath.Dir(p); parent == p {
			break
		}
	}
	return nil
}

func verifyDirectoryACL(path, sddl string) error {
	if e := windowsPlainPath(path); e != nil {
		return e
	}
	st, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if !st.IsDir() {
		return fmt.Errorf("not a plain directory: %s", path)
	}
	p, e := syscall.UTF16PtrFromString(path)
	if e != nil {
		return e
	}
	var sd uintptr
	r, _, _ := advapi.NewProc("GetNamedSecurityInfoW").Call(uintptr(unsafe.Pointer(p)), 1, 5, 0, 0, 0, 0, uintptr(unsafe.Pointer(&sd)))
	if r != 0 {
		return fmt.Errorf("cannot inspect directory permissions: %d", r)
	}
	defer localFree.Call(sd)
	desired, e := securityDescriptor(sddl)
	if e != nil {
		return e
	}
	defer localFree.Call(desired)
	actual, e := securityText(sd)
	if e != nil {
		return e
	}
	want, e := securityText(desired)
	if e != nil {
		return e
	}
	if actual != want {
		return fmt.Errorf("directory ownership/permissions differ; refusing to adopt %s", path)
	}
	return nil
}
func createPrivateDirectory(path, sddl string) error {
	if e := windowsPlainPath(filepath.Dir(path)); e != nil {
		return e
	}
	sd, e := securityDescriptor(sddl)
	if e != nil {
		return e
	}
	defer localFree.Call(sd)
	sa := syscall.SecurityAttributes{Length: uint32(unsafe.Sizeof(syscall.SecurityAttributes{})), SecurityDescriptor: sd}
	p, e := syscall.UTF16PtrFromString(path)
	if e != nil {
		return e
	}
	if e = syscall.CreateDirectory(p, &sa); e != nil && e != syscall.ERROR_ALREADY_EXISTS {
		return e
	}
	return verifyDirectoryACL(path, sddl)
}
func readBoundedJSON(path string, target any) error {
	if e := windowsPlainPath(path); e != nil {
		return e
	}
	st, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if !st.Mode().IsRegular() || st.Size() > 4096 {
		return fmt.Errorf("unsafe receipt")
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	return json.Unmarshal(b, target)
}
func exclusiveJSON(path string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	_, e = f.Write(b)
	ce := f.Close()
	if e != nil {
		return e
	}
	return ce
}
func verifyMachine(root, install string) error {
	if e := verifyDirectoryACL(root, machineACL); e != nil {
		return e
	}
	if e := verifyDirectoryACL(filepath.Join(root, "profiles"), profilesACL); e != nil {
		return e
	}
	for _, p := range []string{filepath.Join(root, "machine-install.json"), filepath.Join(install, "native-install.json")} {
		var r machineReceipt
		if e := readBoundedJSON(p, &r); e != nil {
			return e
		}
		if r.Product != productID || r.Schema != 2 || !strings.EqualFold(r.InstallDir, install) {
			return fmt.Errorf("machine installation receipt mismatch")
		}
	}
	return nil
}
const (
	maxLegacyProfileEntries = 100000
	maxLegacyProfileBytes  = int64(8 << 30)
)

func profileTreeDigest(root string) (string, error) {
	if err := windowsPlainPath(root); err != nil {
		return "", err
	}
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return "", err
	}
	if !rootInfo.IsDir() {
		return "", fmt.Errorf("legacy profile is not a directory")
	}

	hash := sha256.New()
	var entries int
	var total int64
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := windowsPlainPath(path); err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		entries++
		if entries > maxLegacyProfileEntries {
			return fmt.Errorf("legacy profile contains too many entries")
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if info.IsDir() {
			_, _ = io.WriteString(hash, "D\x00"+rel+"\x00")
			return nil
		}
		if !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > maxLegacyProfileBytes-total {
			return fmt.Errorf("legacy profile contains an unsupported or oversized file: %s", rel)
		}
		total += info.Size()
		_, _ = io.WriteString(hash, "F\x00"+rel+"\x00")
		_, _ = io.WriteString(hash, fmt.Sprintf("%d\x00", info.Size()))
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		n, copyErr := io.Copy(hash, io.LimitReader(file, info.Size()+1))
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if n != info.Size() {
			return fmt.Errorf("legacy profile changed while being inspected: %s", rel)
		}
		_, _ = io.WriteString(hash, "\x00")
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func copyLegacyProfile(source, target string) error {
	var entries int
	var total int64
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := windowsPlainPath(path); err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if path == source {
			return nil
		}
		entries++
		if entries > maxLegacyProfileEntries {
			return fmt.Errorf("legacy profile contains too many entries")
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, rel)
		if info.IsDir() {
			return os.Mkdir(destination, 0700)
		}
		if !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > maxLegacyProfileBytes-total {
			return fmt.Errorf("legacy profile contains an unsupported or oversized file: %s", rel)
		}
		total += info.Size()
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			_ = input.Close()
			return err
		}
		n, copyErr := io.Copy(output, io.LimitReader(input, info.Size()+1))
		inputErr := input.Close()
		syncErr := output.Sync()
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if inputErr != nil {
			return inputErr
		}
		if syncErr != nil {
			return syncErr
		}
		if closeErr != nil {
			return closeErr
		}
		if n != info.Size() {
			return fmt.Errorf("legacy profile changed while being copied: %s", rel)
		}
		return nil
	})
}

func verifyLegacyProfile(path, sid string) (string, error) {
	if err := verifyDirectoryACL(path, profileACL(sid)); err != nil {
		return "", err
	}
	var receipt profileReceipt
	if err := readBoundedJSON(filepath.Join(path, "profile.json"), &receipt); err != nil {
		return "", err
	}
	if receipt.Product != productID || receipt.SID != sid || receipt.Schema != 1 {
		return "", fmt.Errorf("unrecognized legacy profile: %s", sid)
	}
	return profileTreeDigest(path)
}

type legacyProfile struct {
	sid    string
	source string
	target string
	digest string
}

func migrateLegacyWindowsProfiles(legacyRoot, runtimeRoot, install string) error {
	legacyRoot = filepath.Clean(legacyRoot)
	runtimeRoot = filepath.Clean(runtimeRoot)
	if strings.EqualFold(legacyRoot, runtimeRoot) {
		return nil
	}
	if err := windowsPlainPath(legacyRoot); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("cannot inspect old ProgramData directory: %w", err)
	}
	rootInfo, err := os.Lstat(legacyRoot)
	if err != nil {
		return err
	}
	if !rootInfo.IsDir() {
		return fmt.Errorf("old ProgramData path is not a directory; it was retained")
	}
	if err = verifyMachine(legacyRoot, install); err != nil {
		return fmt.Errorf("old ProgramData installation ownership or permissions could not be verified; old data was retained: %w", err)
	}
	marker := filepath.Join(legacyRoot, ".uninstalling")
	if err = windowsPlainPath(marker); err == nil {
		b, readErr := os.ReadFile(marker)
		if readErr != nil {
			return readErr
		}
		if string(b) != productID {
			return fmt.Errorf("unknown old installation maintenance marker; old data was retained")
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	oldProfiles := filepath.Join(legacyRoot, "profiles")
	entries, err := os.ReadDir(oldProfiles)
	if err != nil {
		return err
	}
	profiles := make([]legacyProfile, 0, len(entries))
	for _, entry := range entries {
		sid := entry.Name()
		if !sidName.MatchString(sid) {
			return fmt.Errorf("unknown old profile entry %q; all old data was retained", sid)
		}
		source := filepath.Join(oldProfiles, sid)
		digest, verifyErr := verifyLegacyProfile(source, sid)
		if verifyErr != nil {
			return fmt.Errorf("old profile %s failed validation and was retained: %w", sid, verifyErr)
		}
		if err = stopExisting(filepath.Join(source, "data"), 45*time.Second); err != nil {
			return fmt.Errorf("old profile %s is still running; data was retained: %w", sid, err)
		}
		target := filepath.Join(runtimeRoot, "profiles", sid)
		if _, statErr := os.Lstat(target); statErr == nil {
			targetDigest, targetErr := verifyLegacyProfile(target, sid)
			if targetErr != nil || targetDigest != digest {
				return fmt.Errorf("destination profile %s already exists with different or untrusted data; both copies were retained", sid)
			}
		} else if !os.IsNotExist(statErr) {
			return statErr
		}
		profiles = append(profiles, legacyProfile{sid: sid, source: source, target: target, digest: digest})
	}

	for _, profile := range profiles {
		if _, err = os.Lstat(profile.target); err == nil {
			if err = os.RemoveAll(profile.source); err != nil {
				return fmt.Errorf("migrated profile %s but could not remove its verified old copy: %w", profile.sid, err)
			}
			continue
		} else if !os.IsNotExist(err) {
			return err
		}

		if err = os.Rename(profile.source, profile.target); err == nil {
			movedDigest, digestErr := verifyLegacyProfile(profile.target, profile.sid)
			if digestErr == nil && movedDigest == profile.digest {
				continue
			}
			_ = os.Rename(profile.target, profile.source)
			if digestErr != nil {
				return fmt.Errorf("moved profile %s failed post-move validation: %w", profile.sid, digestErr)
			}
			return fmt.Errorf("profile %s changed during migration; original data was restored when possible", profile.sid)
		}

		tempPath := filepath.Join(filepath.Dir(profile.target), fmt.Sprintf(".migration-%s-%d", profile.sid, time.Now().UnixNano()))
		if err = createPrivateDirectory(tempPath, profileACL(profile.sid)); err != nil {
			return fmt.Errorf("cannot prepare a private migration directory for %s: %w", profile.sid, err)
		}
		cleanupTemp := true
		func() {
			defer func() {
				if cleanupTemp {
					_ = os.RemoveAll(tempPath)
				}
			}()
			if err = copyLegacyProfile(profile.source, tempPath); err != nil {
				return
			}
			sourceDigest, sourceErr := profileTreeDigest(profile.source)
			if sourceErr != nil {
				err = sourceErr
				return
			}
			targetDigest, targetErr := verifyLegacyProfile(tempPath, profile.sid)
			if targetErr != nil {
				err = targetErr
				return
			}
			if sourceDigest != profile.digest || targetDigest != profile.digest {
				err = fmt.Errorf("profile %s changed during cross-volume migration", profile.sid)
				return
			}
			err = os.Rename(tempPath, profile.target)
			if err == nil {
				cleanupTemp = false
			}
		}()
		if err != nil {
			return fmt.Errorf("could not copy old profile %s; original data was retained: %w", profile.sid, err)
		}
		if err = os.RemoveAll(profile.source); err != nil {
			return fmt.Errorf("copied profile %s but could not remove its verified old copy: %w", profile.sid, err)
		}
	}

	if err = os.Remove(oldProfiles); err != nil {
		return fmt.Errorf("old profiles directory is not empty; old data was retained: %w", err)
	}
	if err = os.Remove(filepath.Join(legacyRoot, "machine-install.json")); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err = os.Remove(marker); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err = os.Remove(legacyRoot); err != nil {
		return fmt.Errorf("profiles were migrated but old directory contains unrecognized files; retained at %s: %w", legacyRoot, err)
	}
	return nil
}

func initMachine(install string) error {
	root, e := windowsRuntimeRoot()
	if e != nil {
		return e
	}
	if e = createPrivateDirectory(root, machineACL); e != nil {
		return e
	}
	if e = createPrivateDirectory(filepath.Join(root, "profiles"), profilesACL); e != nil {
		return e
	}
	for _, p := range []string{filepath.Join(root, "machine-install.json"), filepath.Join(install, "native-install.json")} {
		if _, e = os.Lstat(p); os.IsNotExist(e) {
			if e = exclusiveJSON(p, machineReceipt{productID, install, 2}); e != nil {
				return e
			}
		} else if e != nil {
			return e
		}
	}
	if e = verifyMachine(root, install); e != nil {
		return e
	}
	legacyRoot, e := legacyWindowsDataRoot()
	if e != nil {
		return e
	}
	if e = migrateLegacyWindowsProfiles(legacyRoot, root, install); e != nil {
		return e
	}
	if e = os.Remove(filepath.Join(root, ".uninstalling")); e != nil && !os.IsNotExist(e) {
		return e
	}
	return nil
}
func prepareWindowsProfile(create bool) error {
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	install := filepath.Dir(exe)
	root, e := windowsRuntimeRoot()
	if e != nil {
		return e
	}
	if e = verifyMachine(root, install); e != nil {
		return e
	}
	if _, e = os.Lstat(filepath.Join(root, ".uninstalling")); !os.IsNotExist(e) {
		return fmt.Errorf("machine installation maintenance in progress")
	}
	sid, e := currentSID()
	if e != nil {
		return e
	}
	p := filepath.Join(root, "profiles", sid)
	if create {
		e = createPrivateDirectory(p, profileACL(sid))
	} else {
		e = verifyDirectoryACL(p, profileACL(sid))
	}
	if e != nil {
		return e
	}
	receipt := filepath.Join(p, "profile.json")
	if create {
		if _, e = os.Lstat(receipt); os.IsNotExist(e) {
			if e = exclusiveJSON(receipt, profileReceipt{productID, sid, 1}); e != nil {
				return e
			}
		}
	}
	var r profileReceipt
	if e = readBoundedJSON(receipt, &r); e != nil {
		return e
	}
	if r.Product != productID || r.SID != sid || r.Schema != 1 {
		return fmt.Errorf("unrecognized private profile")
	}
	return nil
}
func stopMachine(install string, purge bool) (err error) {
	root, err := windowsRuntimeRoot()
	if err != nil {
		return err
	}
	if err = verifyMachine(root, install); err != nil {
		return err
	}
	marker := filepath.Join(root, ".uninstalling")
	if b, e := os.ReadFile(marker); e == nil {
		if string(b) != productID {
			return fmt.Errorf("unknown maintenance marker")
		}
	} else if os.IsNotExist(e) {
		if e = os.WriteFile(marker, []byte(productID), 0644); e != nil {
			return e
		}
	} else {
		return e
	}
	defer func() {
		if err != nil {
			_ = os.Remove(marker)
		}
	}()
	entries, err := os.ReadDir(filepath.Join(root, "profiles"))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !sidName.MatchString(entry.Name()) {
			return fmt.Errorf("unknown profile directory retained: %s", entry.Name())
		}
		path := filepath.Join(root, "profiles", entry.Name())
		if err = verifyDirectoryACL(path, profileACL(entry.Name())); err != nil {
			return err
		}
		var r profileReceipt
		if err = readBoundedJSON(filepath.Join(path, "profile.json"), &r); err != nil {
			return err
		}
		if r.Product != productID || r.SID != entry.Name() || r.Schema != 1 {
			return fmt.Errorf("unrecognized private profile")
		}
		if err = stopExisting(filepath.Join(path, "data"), 45*time.Second); err != nil {
			return err
		}
	}
	if !purge {
		return nil
	} // Keep the gate until the new installer finishes init.
	for _, entry := range entries {
		if err = os.RemoveAll(filepath.Join(root, "profiles", entry.Name())); err != nil {
			return err
		}
	}
	if err = os.Remove(filepath.Join(root, "profiles")); err != nil {
		return err
	}
	if err = os.Remove(marker); err != nil {
		return err
	}
	if err = os.Remove(filepath.Join(root, "machine-install.json")); err != nil {
		return err
	}
	return os.Remove(root)
}
func installedOperation(exe, operation string) error {
	root, e := nativeRoot(exe)
	if e != nil {
		return e
	}
	switch operation {
	case "init":
		return initMachine(root)
	case "stop":
		return stopMachine(root, false)
	case "purge":
		return stopMachine(root, true)
	default:
		return fmt.Errorf("invalid native operation")
	}
}
func prepareInstalled(create bool) error {
	if distribution == "windows-installed" {
		return prepareWindowsProfile(create)
	}
	return nil
}
func checkMachineGate() error {
	if distribution != "windows-installed" {
		return nil
	}
	root, e := windowsRuntimeRoot()
	if e != nil {
		return e
	}
	if _, e = os.Lstat(filepath.Join(root, ".uninstalling")); os.IsNotExist(e) {
		return nil
	}
	return fmt.Errorf("machine installation maintenance in progress")
}
