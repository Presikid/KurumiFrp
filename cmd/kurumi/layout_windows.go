//go:build windows

package main

// Windows binaries are machine-installed and read-only. Mutable state lives
// beneath the installation directory in an ACL-protected runtime root, with
// each Windows SID receiving a private profile.
import (
	"encoding/json"
	"fmt"
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
func verifyDirectoryACL(path, sddl string) error {
	if e := plainPath(path); e != nil {
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
	if e := plainPath(filepath.Dir(path)); e != nil {
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
	if e := plainPath(path); e != nil {
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
