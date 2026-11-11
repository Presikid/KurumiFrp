//go:build !windows && !linux && !darwin

package manager

import (
	"fmt"
	"os"
	"runtime"
)

func lockDataFile(*os.File) error {
	return fmt.Errorf("当前操作系统 %s 不支持数据目录独占锁", runtime.GOOS)
}

func unlockDataFile(*os.File) error { return nil }
