//go:build linux

package manager

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"unsafe"
)

var statusDirectoryName = regexp.MustCompile(`^status-[0-9]+$`)

func statusSystemIdentity() string {
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	ns, err := os.Readlink("/proc/self/ns/pid")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(boot)) + ":" + ns
}

func definitelyDead(pid int) bool {
	return pid > 1 && syscall.Kill(pid, 0) == syscall.ESRCH
}

func privateOwned(fd int, directory bool) bool {
	var st syscall.Stat_t
	if syscall.Fstat(fd, &st) != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&0077 != 0 {
		return false
	}
	if directory {
		return st.Mode&syscall.S_IFMT == syscall.S_IFDIR
	}
	return st.Mode&syscall.S_IFMT == syscall.S_IFREG && st.Nlink == 1
}

// Open relative to the verified directory with O_NOFOLLOW; refuse unknown
// files, hard links, insecure permissions and uncertain process ownership.
func readPrivateAt(fd int, name string, limit int64) ([]byte, bool) {
	f, err := syscall.Openat(fd, name, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, false
	}
	file := os.NewFile(uintptr(f), name)
	defer file.Close()
	if !privateOwned(f, false) {
		return nil, false
	}
	b, err := io.ReadAll(io.LimitReader(file, limit+1))
	return b, err == nil && int64(len(b)) <= limit
}

func openPlainAbsolute(path string) (int, error) {
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return -1, syscall.EINVAL
	}
	fd, err := syscall.Open("/", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if part == "" {
			continue
		}
		next, err := syscall.Openat(fd, part, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		syscall.Close(fd)
		if err != nil {
			return -1, err
		}
		fd = next
	}
	return fd, nil
}

func reapPrivateStatuses(parent string) {
	identity := statusSystemIdentity()
	if identity == "" {
		return
	}
	root, err := openPlainAbsolute(parent)
	if err != nil {
		return
	}
	rf := os.NewFile(uintptr(root), parent)
	defer rf.Close()
	if !privateOwned(root, true) {
		return
	}
	entries, err := rf.ReadDir(10000)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if !statusDirectoryName.MatchString(name) {
			continue
		}
		fd, err := syscall.Openat(root, name, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		if err != nil {
			continue
		}
		func() {
			dir := os.NewFile(uintptr(fd), name)
			defer dir.Close()
			if !privateOwned(fd, true) {
				return
			}
			children, err := dir.ReadDir(4)
			if err != nil || len(children) != 3 {
				return
			}
			names := map[string]bool{}
			for _, child := range children {
				names[child.Name()] = true
			}
			if !names["cert.pem"] || !names["key.pem"] || !names["owner.json"] {
				return
			}
			metaBytes, ok := readPrivateAt(fd, "owner.json", 4096)
			if !ok {
				return
			}
			var meta statusMetadata
			if json.Unmarshal(metaBytes, &meta) != nil || meta.Version != 1 || meta.Identity != identity || meta.OwnerPID == meta.ChildPID || !definitelyDead(meta.OwnerPID) || !definitelyDead(meta.ChildPID) {
				return
			}
			cert, ok := readPrivateAt(fd, "cert.pem", 32768)
			if !ok {
				return
			}
			key, ok := readPrivateAt(fd, "key.pem", 32768)
			if !ok {
				return
			}
			pair, err := tls.X509KeyPair(cert, key)
			if err != nil || len(pair.Certificate) != 1 {
				return
			}
			leaf, err := x509.ParseCertificate(pair.Certificate[0])
			if err != nil || leaf.Subject.CommonName != "KurumiFRP local status" || leaf.CheckSignature(leaf.SignatureAlgorithm, leaf.RawTBSCertificate, leaf.Signature) != nil {
				return
			}
			if !definitelyDead(meta.OwnerPID) || !definitelyDead(meta.ChildPID) {
				return
			}
			// Unlink relative to the opened private directory, so a replacement
			// symlink cannot redirect cleanup into another directory.
			for _, file := range []string{"key.pem", "cert.pem", "owner.json"} {
				if syscall.Unlinkat(fd, file) != nil {
					return
				}
			}
			// Remove the now-empty directory only if the name still refers to
			// the exact inode we inspected. Never follow a replacement link.
			check, err := syscall.Openat(root, name, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
			if err != nil {
				return
			}
			defer syscall.Close(check)
			var a, b syscall.Stat_t
			if syscall.Fstat(fd, &a) != nil || syscall.Fstat(check, &b) != nil || a.Ino != b.Ino || a.Dev != b.Dev {
				return
			}
			ptr, err := syscall.BytePtrFromString(name)
			if err != nil {
				return
			}
			_, _, _ = syscall.Syscall(syscall.SYS_UNLINKAT, uintptr(root), uintptr(unsafe.Pointer(ptr)), uintptr(0x200))
		}()
	}
}
