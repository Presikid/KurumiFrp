//go:build linux

package manager

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode"
)

func firewallSupported() bool { return true }

// Avoid an unbounded error buffer if the local executable emits excessive data.
type firewallErrorOutput struct{ data bytes.Buffer }

func (b *firewallErrorOutput) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := 4096 - b.data.Len(); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = b.data.Write(p)
	}
	return n, nil
}

func applyFirewall(parent context.Context, rules string) error {
	path := ""
	for _, candidate := range []string{"/usr/sbin/nft", "/sbin/nft", "/usr/bin/nft", "/bin/nft"} {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			path = candidate
			break
		}
	}
	if path == "" {
		return fmt.Errorf("未找到 nft 程序")
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "-f", "-")
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	cmd.Stdin = strings.NewReader(rules)
	cmd.Stdout = io.Discard
	var output firewallErrorOutput
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("操作中断或超时，请独立核对内核规则")
		}
		message := strings.TrimSpace(strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return ' '
			}
			return r
		}, output.data.String()))
		if message == "" {
			message = "请检查 nftables 和网络管理权限"
		}
		return fmt.Errorf("nftables 未确认：%s", message)
	}
	return nil
}
