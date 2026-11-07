//go:build !windows

package update
import "fmt"
func launchInstaller(string) error {return fmt.Errorf("当前系统不支持安装此程序")}
