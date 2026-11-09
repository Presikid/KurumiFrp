//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"github.com/Presikid/KurumiFrp/internal/version"
)

// This deliberately accepts only simple, locally protected installation paths.
// Unknown owners, ACE types and permissions fail closed; profile data has its
// own ACL checks and is not part of this check.
func trustedOwner(s string) bool {
	switch s {
	case "BA", "SY", "S-1-5-32-544", "S-1-5-18", "S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464":
		return true
	}
	return false
}

func unsafeAccess(rights string, volumeRoot bool) bool {
	var mask uint64
	if strings.HasPrefix(rights, "0x") {
		var err error
		mask, err = strconv.ParseUint(rights[2:], 16, 32)
		if err != nil {
			return true
		}
	} else {
		if len(rights)%2 != 0 || rights == "" {
			return true
		}
		for len(rights) > 0 {
			switch rights[:2] {
			case "FR", "FX", "GR", "GX", "RC":
			case "FW", "FA", "GW", "GA", "SD", "WD", "WO":
				return true
			case "CC":
				mask |= 1
			case "DC":
				mask |= 2
			case "LC":
				mask |= 4
			case "SW":
				mask |= 8
			case "RP":
				mask |= 0x10
			case "WP":
				mask |= 0x20
			case "DT":
				mask |= 0x40
			case "LO":
				mask |= 0x80
			case "CR":
				mask |= 0x100
			default:
				return true
			}
			rights = rights[2:]
		}
	}
	const dangerous = uint64(0x10000000 | 0x40000000 | 0x02000000 | 0x000d0156)
	if volumeRoot {
		// A normal volume root can allow creation of new children. It must not
		// permit deleting or changing the protected child or its security.
		return mask&(0x10000000|0x40000000|0x02000000|0x000d0040) != 0
	}
	return mask&dangerous != 0
}

func protectedInstallObject(path string, inherit, volumeRoot bool) error {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	var sd uintptr
	r, _, _ := advapi.NewProc("GetNamedSecurityInfoW").Call(uintptr(unsafe.Pointer(p)), 1, 5, 0, 0, 0, 0, uintptr(unsafe.Pointer(&sd)))
	if r != 0 {
		return fmt.Errorf("cannot inspect installation permissions: %d", r)
	}
	defer localFree.Call(sd)
	s, err := securityText(sd)
	if err != nil {
		return err
	}
	parts := strings.SplitN(s, "D:", 2)
	if len(parts) != 2 || !strings.HasPrefix(parts[0], "O:") || !trustedOwner(strings.TrimPrefix(parts[0], "O:")) {
		return fmt.Errorf("untrusted installation owner: %s", path)
	}
	acl := parts[1]
	i := strings.IndexByte(acl, '(')
	if i < 0 || strings.Trim(acl[:i], "PAIR") != "" {
		return fmt.Errorf("unsupported installation ACL: %s", path)
	}
	acl = acl[i:]
	for acl != "" {
		end := strings.IndexByte(acl, ')')
		if acl[0] != '(' || end < 0 {
			return fmt.Errorf("unsupported installation ACL: %s", path)
		}
		ace := strings.Split(acl[1:end], ";")
		if len(ace) != 6 || ace[3] != "" || ace[4] != "" || (ace[0] != "A" && ace[0] != "D") {
			return fmt.Errorf("unsupported installation ACE: %s", path)
		}
		if ace[0] == "A" && !trustedOwner(ace[5]) && !(ace[5] == "CO" && strings.Contains(ace[1], "IO")) &&
			(inherit || !strings.Contains(ace[1], "IO")) && unsafeAccess(ace[2], volumeRoot) {
			return fmt.Errorf("installation is writable by an untrusted principal: %s", path)
		}
		acl = acl[end+1:]
	}
	return nil
}

func checkInstallPath(path string) error {
	missing := true
	for p := path; ; p = filepath.Dir(p) {
		_, err := os.Lstat(p)
		if err == nil {
			if err = windowsPlainPath(p); err != nil {
				return err
			}
			parent := filepath.Dir(p)
			if err = protectedInstallObject(p, missing, parent == p && !missing); err != nil {
				return err
			}
			missing = false
		} else if !os.IsNotExist(err) {
			return err
		}
		if filepath.Dir(p) == p {
			return nil
		}
	}
}

func validateMaintenanceTarget(install string) error {
	if distribution != "windows-installed" || !filepath.IsAbs(install) || len(filepath.VolumeName(install)) != 2 {
		return fmt.Errorf("a local installation path is required")
	}
	install = filepath.Clean(install)
	volume := filepath.VolumeName(install) + `\`
	if strings.EqualFold(install, volume) {
		return fmt.Errorf("refusing filesystem root")
	}
	p, err := syscall.UTF16PtrFromString(volume)
	if err != nil {
		return err
	}
	kernel := syscall.NewLazyDLL("kernel32.dll")
	drive, err := syscall.UTF16PtrFromString(filepath.VolumeName(install))
	if err != nil {
		return err
	}
	var mapping [1024]uint16
	n, _, _ := kernel.NewProc("QueryDosDeviceW").Call(uintptr(unsafe.Pointer(drive)), uintptr(unsafe.Pointer(&mapping[0])), uintptr(len(mapping)))
	device := strings.ToLower(syscall.UTF16ToString(mapping[:]))
	const prefix = `\device\harddiskvolume`
	if n == 0 || !strings.HasPrefix(device, prefix) || strings.Trim(device[len(prefix):], "0123456789") != "" || len(device) == len(prefix) {
		return fmt.Errorf("installation requires a direct local volume, not a substituted drive")
	}
	typeID, _, _ := kernel.NewProc("GetDriveTypeW").Call(uintptr(unsafe.Pointer(p)))
	var flags uint32
	ok, _, _ := kernel.NewProc("GetVolumeInformationW").Call(uintptr(unsafe.Pointer(p)), 0, 0, 0, 0, uintptr(unsafe.Pointer(&flags)), 0, 0)
	if (typeID != 2 && typeID != 3) || ok == 0 || flags&8 == 0 {
		return fmt.Errorf("installation requires a local volume with persistent ACLs")
	}
	paths := []string{install, filepath.Join(install, "kurumi.exe"), filepath.Join(install, "native-install.json"),
		filepath.Join(install, "INSTALL.md"), filepath.Join(install, "kurumi.ico"), filepath.Join(install, "bundled"),
		filepath.Join(install, "bundled", version.Current)}
	for _, name := range []string{"frpc.exe", "frps.exe", "LICENSE", "manifest.json"} {
		paths = append(paths, filepath.Join(install, "bundled", version.Current, name))
	}
	entries, err := os.ReadDir(install)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, entry := range entries {
		if strings.HasPrefix(strings.ToLower(entry.Name()), "unins") {
			paths = append(paths, filepath.Join(install, entry.Name()))
		}
	}
	for _, path := range paths {
		if err = checkInstallPath(path); err != nil {
			return err
		}
	}
	return nil
}
