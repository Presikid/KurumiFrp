package manager

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// The lock file is deliberately retained on disk. Removing it while a process
// holds the lock would let a second process lock a different file at that path.
const dataDirectoryLockName = "manager.oslock"

var errDataDirectoryLocked = errors.New("数据目录正被另一个工作台使用")

type dataDirectoryLock struct {
	file *os.File
	dir  os.FileInfo
}

// Keep every acquired file reachable until it is explicitly released. If
// cleanup is uncertain, the OS must retain its lock until process exit.
var heldDataDirectoryLocks = struct {
	sync.Mutex
	locks map[*dataDirectoryLock]struct{}
}{locks: make(map[*dataDirectoryLock]struct{})}

// Resolve ancestor aliases once so later reads and the lock use the same path.
// Match the store's existing rule that the final directory is not a symlink.
func canonicalDataDirectory(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(abs, 0700); err != nil {
		return "", err
	}
	info, err := os.Lstat(abs)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("数据目录不可用或不是安全的目录")
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("无法解析数据目录路径: %w", err)
	}
	if !filepath.IsAbs(resolved) {
		return "", fmt.Errorf("数据目录路径必须为绝对路径")
	}
	return resolved, nil
}

func acquireDataDirectoryLock(abs string) (*dataDirectoryLock, error) {
	if !filepath.IsAbs(abs) {
		return nil, fmt.Errorf("数据目录路径必须为绝对路径")
	}
	if err := os.MkdirAll(abs, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(abs)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("数据目录不可用或不是安全的目录")
	}

	// Some platform locks are process-scoped. Check directory identity as well
	// so a second App in this process cannot use an alias of the same directory.
	heldDataDirectoryLocks.Lock()
	defer heldDataDirectoryLocks.Unlock()
	for held := range heldDataDirectoryLocks.locks {
		if os.SameFile(info, held.dir) {
			return nil, errDataDirectoryLocked
		}
	}

	path := filepath.Join(abs, dataDirectoryLockName)
	if existing, statErr := os.Lstat(path); statErr == nil {
		if !existing.Mode().IsRegular() || existing.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("数据目录锁文件无效")
		}
	} else if !os.IsNotExist(statErr) {
		return nil, fmt.Errorf("无法检查数据目录锁文件: %w", statErr)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("无法打开数据目录锁文件: %w", err)
	}
	closeOnError := func(err error) (*dataDirectoryLock, error) {
		_ = file.Close()
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() {
		return closeOnError(fmt.Errorf("数据目录锁文件无效"))
	}
	if err = lockDataFile(file); err != nil {
		if errors.Is(err, errDataDirectoryLocked) {
			return closeOnError(errDataDirectoryLocked)
		}
		return closeOnError(fmt.Errorf("无法锁定数据目录: %w", err))
	}
	current, err := os.Lstat(path)
	if err != nil || !current.Mode().IsRegular() || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, current) {
		_ = unlockDataFile(file)
		return closeOnError(fmt.Errorf("数据目录锁文件已变化"))
	}
	lock := &dataDirectoryLock{file: file, dir: info}
	heldDataDirectoryLocks.locks[lock] = struct{}{}
	return lock, nil
}

func (lock *dataDirectoryLock) release() error {
	if lock == nil {
		return nil
	}
	heldDataDirectoryLocks.Lock()
	defer heldDataDirectoryLocks.Unlock()
	if _, held := heldDataDirectoryLocks.locks[lock]; !held {
		return nil
	}
	if err := unlockDataFile(lock.file); err != nil {
		return fmt.Errorf("无法释放数据目录锁: %w", err)
	}
	delete(heldDataDirectoryLocks.locks, lock)
	return lock.file.Close()
}
