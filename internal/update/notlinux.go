//go:build !linux

package update
import "fmt"
func DetectDistribution(distribution string) string {return distribution}
func PerformSystemUpdate() error {return fmt.Errorf("system updates require Linux")}
