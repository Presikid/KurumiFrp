//go:build !linux

package manager

import (
	"context"
	"fmt"
)

func firewallSupported() bool { return false }

func applyFirewall(context.Context, string) error {
	return fmt.Errorf("当前系统不支持 Linux nftables")
}
