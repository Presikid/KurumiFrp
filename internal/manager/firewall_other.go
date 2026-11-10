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

func readFirewallSeen(context.Context, string) ([]firewallSeen, bool, error) {
	return nil, false, fmt.Errorf("当前系统不支持 Linux nftables")
}
