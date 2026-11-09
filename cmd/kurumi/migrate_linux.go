//go:build linux

package main

import (
	"bufio"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Mount IDs in mountinfo, rather than device numbers, identify bind mounts
// which can expose unrelated data on the same filesystem.
func mountTargetsInTrees(r io.Reader, roots ...string) error {
	cleanRoots := make([]string, 0, len(roots))
	for _, root := range roots {
		if !filepath.IsAbs(root) || filepath.Clean(root) == "/" {
			return fmt.Errorf("invalid mount-check root")
		}
		cleanRoots = append(cleanRoots, filepath.Clean(root))
	}
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 4096), 1024*1024)
	sawRoot := false
	for s.Scan() {
		fields := strings.Fields(s.Text())
		if len(fields) < 10 {
			return fmt.Errorf("malformed mountinfo")
		}
		var target strings.Builder
		for i := 0; i < len(fields[4]); i++ {
			if fields[4][i] != '\\' {
				target.WriteByte(fields[4][i])
				continue
			}
			if i+3 >= len(fields[4]) {
				return fmt.Errorf("invalid mountinfo escape")
			}
			esc := fields[4][i+1 : i+4]
			switch esc {
			case "040":
				target.WriteByte(' ')
			case "011":
				target.WriteByte('\t')
			case "012":
				target.WriteByte('\n')
			case "134":
				target.WriteByte('\\')
			default:
				return fmt.Errorf("invalid mountinfo escape")
			}
			i += 3
		}
		path := filepath.Clean(target.String())
		if !filepath.IsAbs(path) {
			return fmt.Errorf("invalid mount target")
		}
		if path == "/" {
			sawRoot = true
		}
		for _, root := range cleanRoots {
			if path == root || strings.HasPrefix(path, root+string(os.PathSeparator)) {
				return fmt.Errorf("mount inside migration tree: %s", path)
			}
		}
	}
	if err := s.Err(); err != nil {
		return err
	}
	if !sawRoot {
		return fmt.Errorf("mountinfo root unavailable")
	}
	return nil
}

func ensureNoRuntimeMounts(roots ...string) error {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return fmt.Errorf("cannot inspect mounts: %w", err)
	}
	defer f.Close()
	return mountTargetsInTrees(f, roots...)
}

func legacyLinuxMigration() error {
	if distribution != "linux-system" || os.Geteuid() != 0 {
		return fmt.Errorf("legacy migration requires an installed Linux build and root")
	}
	const oldRoot = "/var/lib/kurumifrp"
	const oldMeta = "/var/lib/kurumifrp-package"
	const dest = "/opt/kurumifrp/runtime"
	meta, err := os.Lstat(oldMeta)
	if err != nil || !meta.IsDir() || meta.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("legacy receipt directory invalid")
	}
	st, ok := meta.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 0 {
		return fmt.Errorf("legacy receipt directory not root owned")
	}
	receipt := filepath.Join(oldMeta, "uid")
	info, err := os.Lstat(receipt)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("legacy receipt invalid")
	}
	st, ok = info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 0 || st.Nlink != 1 {
		return fmt.Errorf("legacy receipt ownership invalid")
	}
	b, err := os.ReadFile(receipt)
	if err != nil || len(b) > 32 {
		return fmt.Errorf("legacy receipt unreadable")
	}
	uid64, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 32)
	if err != nil || uid64 == 0 {
		return fmt.Errorf("legacy receipt uid invalid")
	}
	account, err := user.Lookup("kurumifrp")
	if err != nil || account.Uid != strconv.FormatUint(uid64, 10) {
		return fmt.Errorf("legacy service account changed")
	}
	if err := ensureNoAccountProcesses(uint32(uid64)); err != nil {
		return err
	}
	if err := ensureNoRuntimeMounts(oldRoot, dest); err != nil {
		return err
	}
	if _, err = os.Lstat(dest); os.IsNotExist(err) {
		if err = copyLegacyRuntimeGuarded(oldRoot, dest, uint32(uid64), func() error { return ensureNoAccountProcesses(uint32(uid64)) }); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err = ensureNoAccountProcesses(uint32(uid64)); err != nil {
		return err
	}
	if err = compareLegacyRuntime(oldRoot, dest, uint32(uid64)); err != nil {
		return err
	}
	return ensureNoAccountProcesses(uint32(uid64))
}

func ensureNoAccountProcesses(uid uint32) error {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return fmt.Errorf("cannot inspect processes: %w", err)
	}
	for _, entry := range entries {
		if _, err := strconv.ParseUint(entry.Name(), 10, 32); err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "status"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("cannot inspect process %s: %w", entry.Name(), err)
		}
		found := false
		for _, line := range strings.Split(string(b), "\n") {
			if !strings.HasPrefix(line, "Uid:") {
				continue
			}
			found = true
			for _, value := range strings.Fields(strings.TrimPrefix(line, "Uid:")) {
				n, err := strconv.ParseUint(value, 10, 32)
				if err != nil {
					return fmt.Errorf("invalid process uid")
				}
				if uint32(n) == uid {
					return fmt.Errorf("service account still has live processes")
				}
			}
			break
		}
		if !found {
			return fmt.Errorf("process uid unavailable: %s", entry.Name())
		}
	}
	return nil
}

type migrationEntry struct {
	mode     os.FileMode
	uid, gid uint32
	size     int64
	hash     [32]byte
}

// Legacy installations also allow an administrator to place root-owned frp
// binaries in cores/. Do not generalize this exception to other files: state,
// snapshots and metadata remain private to the service account.
func safeRuntimeEntry(rel string, info os.FileInfo, st *syscall.Stat_t, uid uint32) bool {
	if info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	if rel == "cores/frpc" || rel == "cores/frps" {
		return (st.Uid == 0 || st.Uid == uid) && st.Nlink == 1 && info.Mode().IsRegular() && info.Mode().Perm()&0022 == 0 && info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) == 0
	}
	return st.Uid == uid && info.Mode().Perm()&0077 == 0
}

func runtimeManifest(root string, uid uint32) (map[string]migrationEntry, error) {
	if err := ensureNoRuntimeMounts(root); err != nil {
		return nil, err
	}
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	rootStat, ok := rootInfo.Sys().(*syscall.Stat_t)
	if !ok || !rootInfo.IsDir() {
		return nil, fmt.Errorf("invalid runtime root")
	}
	entries := make(map[string]migrationEntry)
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ensureNoRuntimeMounts(root); err != nil {
			return err
		}
		info, e := os.Lstat(path)
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(root, path)
		if e != nil {
			return e
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Dev != rootStat.Dev || !safeRuntimeEntry(rel, info, st, uid) {
			return fmt.Errorf("unsafe runtime entry: %s", path)
		}
		v := migrationEntry{mode: info.Mode(), uid: st.Uid, gid: st.Gid, size: info.Size()}
		if info.IsDir() {
			v.size = 0
		} else {
			if !info.Mode().IsRegular() || st.Nlink != 1 {
				return fmt.Errorf("unsafe runtime file: %s", path)
			}
			fd, e := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
			if e != nil {
				return e
			}
			f := os.NewFile(uintptr(fd), path)
			h := sha256.New()
			_, e = io.Copy(h, f)
			closeErr := f.Close()
			if e != nil {
				return e
			}
			if closeErr != nil {
				return closeErr
			}
			copy(v.hash[:], h.Sum(nil))
		}
		entries[rel] = v
		return nil
	})
	return entries, err
}

func compareLegacyRuntime(source, dest string, uid uint32) error {
	if err := ensureNoRuntimeMounts(source, dest); err != nil {
		return err
	}
	a, err := runtimeManifest(source, uid)
	if err != nil {
		return err
	}
	b, err := runtimeManifest(dest, uid)
	if err != nil {
		return err
	}
	if len(a) != len(b) {
		return fmt.Errorf("migration copies differ")
	}
	for name, v := range a {
		if other, ok := b[name]; !ok || other != v {
			return fmt.Errorf("migration copies differ: %s", name)
		}
	}
	return ensureNoRuntimeMounts(source, dest)
}

// copyLegacyRuntime is an isolated migration primitive. Its caller must stop
// the old service and all processes of uid and protect the destination parent
// from writes by uid before calling it. It does not remove the source.
func copyLegacyRuntime(source, destination string, uid uint32) (err error) {
	return copyLegacyRuntimeGuarded(source, destination, uid, nil)
}

func copyLegacyRuntimeGuarded(source, destination string, uid uint32, guard func() error) (err error) {
	if !filepath.IsAbs(source) || !filepath.IsAbs(destination) || filepath.Clean(source) == filepath.Clean(destination) {
		return fmt.Errorf("invalid migration paths")
	}
	if err = ensureNoRuntimeMounts(source, destination); err != nil {
		return err
	}
	relDest, relErr := filepath.Rel(source, destination)
	if relErr != nil || relDest == "." || (relDest != ".." && !strings.HasPrefix(relDest, ".."+string(os.PathSeparator))) {
		return fmt.Errorf("destination must not be inside source")
	}
	for _, path := range []string{source, filepath.Dir(destination)} {
		for p := filepath.Clean(path); ; p = filepath.Dir(p) {
			info, e := os.Lstat(p)
			if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("unsafe path component: %s", p)
			}
			if p == filepath.Dir(p) {
				break
			}
		}
	}
	sourceInfo, err := os.Lstat(source)
	if err != nil {
		return err
	}
	sourceStat, ok := sourceInfo.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("unsupported source filesystem")
	}
	if _, err = os.Lstat(destination); err == nil || !os.IsNotExist(err) {
		return fmt.Errorf("destination already exists or is inaccessible")
	}
	parent := filepath.Dir(destination)
	parentInfo, err := os.Lstat(parent)
	if err != nil || !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("unsafe destination parent")
	}
	parentStat, ok := parentInfo.Sys().(*syscall.Stat_t)
	if !ok || parentStat.Uid != uint32(os.Geteuid()) || parentInfo.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("destination parent is not protected")
	}
	stage, err := os.MkdirTemp(parent, ".kurumi-migration-")
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			// Never remove a staging tree if a mount is present or cannot be checked.
			if ensureNoRuntimeMounts(stage) == nil {
				_ = os.RemoveAll(stage)
			}
		}
	}()
	if err = ensureNoRuntimeMounts(source, stage, destination); err != nil {
		return err
	}
	var dirs []string
	err = filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if e := ensureNoRuntimeMounts(source, stage, destination); e != nil {
			return e
		}
		info, e := os.Lstat(path)
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(source, path)
		if e != nil {
			return e
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Dev != sourceStat.Dev || !safeRuntimeEntry(rel, info, stat, uid) {
			return fmt.Errorf("unsafe legacy entry: %s", path)
		}
		target := filepath.Join(stage, rel)
		if info.IsDir() {
			if rel != "." {
				if e = os.Mkdir(target, 0700); e != nil {
					return e
				}
			}
			dirs = append(dirs, target)
			return nil
		}
		if !info.Mode().IsRegular() || stat.Nlink != 1 {
			return fmt.Errorf("unsafe legacy file: %s", path)
		}
		fd, e := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if e != nil {
			return e
		}
		input := os.NewFile(uintptr(fd), path)
		defer input.Close()
		if e = ensureNoRuntimeMounts(source, stage, destination); e != nil {
			return e
		}
		actual, e := input.Stat()
		if e != nil || !os.SameFile(info, actual) {
			return fmt.Errorf("legacy file changed during migration: %s", path)
		}
		openedStat, valid := actual.Sys().(*syscall.Stat_t)
		if !valid || openedStat.Dev != sourceStat.Dev || openedStat.Nlink != 1 || !actual.Mode().IsRegular() || !safeRuntimeEntry(rel, actual, openedStat, uid) {
			return fmt.Errorf("legacy file metadata changed: %s", path)
		}
		output, e := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return e
		}
		hash := sha256.New()
		_, e = io.Copy(io.MultiWriter(output, hash), input)
		if e == nil {
			after, statErr := input.Stat()
			if statErr != nil || after.Size() != actual.Size() || !after.ModTime().Equal(actual.ModTime()) {
				e = fmt.Errorf("legacy file changed during copy: %s", path)
			}
		}
		if e == nil {
			e = output.Chown(int(stat.Uid), int(stat.Gid))
		}
		if e == nil {
			e = output.Chmod(info.Mode().Perm())
		}
		if e == nil {
			e = output.Sync()
		}
		if closeErr := output.Close(); e == nil {
			e = closeErr
		}
		if e != nil {
			return e
		}
		check, e := os.Open(target)
		if e != nil {
			return e
		}
		writtenHash := sha256.New()
		_, e = io.Copy(writtenHash, check)
		check.Close()
		if e != nil || string(hash.Sum(nil)) != string(writtenHash.Sum(nil)) {
			return fmt.Errorf("copy verification failed: %s", path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		rel, e := filepath.Rel(stage, dirs[i])
		if e != nil {
			return e
		}
		original, e := os.Lstat(filepath.Join(source, rel))
		if e != nil {
			return e
		}
		st := original.Sys().(*syscall.Stat_t)
		if e = os.Chown(dirs[i], int(st.Uid), int(st.Gid)); e != nil {
			return e
		}
		if e = os.Chmod(dirs[i], original.Mode().Perm()); e != nil {
			return e
		}
		f, e := os.Open(dirs[i])
		if e != nil {
			return e
		}
		e = f.Sync()
		f.Close()
		if e != nil {
			return e
		}
	}
	if err = ensureNoRuntimeMounts(source, stage, destination); err != nil {
		return err
	}
	if guard != nil {
		if err = guard(); err != nil {
			return err
		}
	}
	if err = os.Rename(stage, destination); err != nil {
		return err
	}
	committed = true
	d, err := os.Open(parent)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
