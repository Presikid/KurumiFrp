//go:build !linux && !windows

package update
import "fmt"
func requestSystemUpdate(string) error {return fmt.Errorf("当前系统不支持此更新方式")}
