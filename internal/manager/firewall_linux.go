//go:build linux

package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"os"
	"os/exec"
	"sort"
	"strconv"
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

func firewallNFTPath() string {
	for _, candidate := range []string{"/usr/sbin/nft", "/sbin/nft", "/usr/bin/nft", "/bin/nft"} {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate
		}
	}
	return ""
}

func applyFirewall(parent context.Context, rules string) error {
	path := firewallNFTPath()
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

// nft output is local but may still be modified by another system administrator.
// Keep both memory and the JSON response bounded even if a table is replaced.
type firewallReadOutput struct {
	data     bytes.Buffer
	exceeded bool
}

func (b *firewallReadOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := (2 << 20) - b.data.Len()
	if len(p) > remaining {
		p = p[:remaining]
		b.exceeded = true
	}
	_, _ = b.data.Write(p)
	return n, nil
}

func readFirewallSeen(parent context.Context, table string) ([]firewallSeen, bool, error) {
	path := firewallNFTPath()
	if path == "" {
		return nil, false, fmt.Errorf("nft unavailable")
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "-j", "-n", "list", "table", "inet", table)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	var output firewallReadOutput
	cmd.Stdout = &output
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil || output.exceeded {
		return nil, false, fmt.Errorf("nft record read failed")
	}
	var document struct {
		Items []json.RawMessage `json:"nftables"`
	}
	if err := json.Unmarshal(output.data.Bytes(), &document); err != nil {
		return nil, false, fmt.Errorf("nft record response invalid")
	}
	seenSets := map[string]bool{}
	records := make([]firewallSeen, 0)
	partial := false
	type nftSet struct {
		Family   string          `json:"family"`
		Table    string          `json:"table"`
		Name     string          `json:"name"`
		Elements json.RawMessage `json:"elem"`
	}
	for _, raw := range document.Items {
		var item struct {
			Set     *nftSet `json:"set"`
			Element *nftSet `json:"element"`
		}
		if json.Unmarshal(raw, &item) != nil {
			return nil, false, fmt.Errorf("nft record response invalid")
		}
		set := item.Set
		if set == nil {
			set = item.Element
		}
		if set == nil || (set.Name != "seen4" && set.Name != "seen6") {
			continue
		}
		if set.Family != "inet" || set.Table != table || (item.Set != nil && seenSets[set.Name]) {
			return nil, false, fmt.Errorf("nft record set invalid")
		}
		if item.Set != nil {
			seenSets[set.Name] = true
		}
		var entries []json.RawMessage
		elements := bytes.TrimSpace(set.Elements)
		if len(elements) > 0 && !bytes.Equal(elements, []byte("null")) {
			if elements[0] == '[' {
				if json.Unmarshal(elements, &entries) != nil {
					return nil, false, fmt.Errorf("nft record elements invalid")
				}
			} else {
				entries = []json.RawMessage{elements}
			}
		}
		for _, entry := range entries {
			if len(records) >= 2048 {
				partial = true
				break
			}
			record, ok := parseFirewallSeen(entry, set.Name == "seen4")
			if !ok {
				partial = true
				continue
			}
			records = append(records, record)
		}
	}
	if !seenSets["seen4"] || !seenSets["seen6"] {
		return nil, false, fmt.Errorf("nft record sets missing")
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].Source != records[j].Source {
			return records[i].Source < records[j].Source
		}
		if records[i].Protocol != records[j].Protocol {
			return records[i].Protocol < records[j].Protocol
		}
		return records[i].Port < records[j].Port
	})
	return records, partial, nil
}

func parseFirewallSeen(raw json.RawMessage, ipv4 bool) (firewallSeen, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return firewallSeen{}, false
	}
	value := raw
	if inner, ok := fields["elem"]; ok {
		value = inner
		fields = nil
		if json.Unmarshal(inner, &fields) != nil {
			return firewallSeen{}, false
		}
	}
	if inner, ok := fields["val"]; ok {
		value = inner
	}
	var tuple struct {
		Concat []json.RawMessage `json:"concat"`
	}
	if json.Unmarshal(value, &tuple) != nil || len(tuple.Concat) != 3 {
		return firewallSeen{}, false
	}
	var source, protocol string
	if json.Unmarshal(tuple.Concat[0], &source) != nil {
		return firewallSeen{}, false
	}
	ip, err := netip.ParseAddr(source)
	if err != nil || ip.Zone() != "" || ip.Is4() != ipv4 {
		return firewallSeen{}, false
	}
	if json.Unmarshal(tuple.Concat[1], &protocol) != nil {
		var number int
		if json.Unmarshal(tuple.Concat[1], &number) != nil {
			return firewallSeen{}, false
		}
		protocol = strconv.Itoa(number)
	}
	switch strings.ToLower(protocol) {
	case "tcp", "6":
		protocol = "tcp"
	case "udp", "17":
		protocol = "udp"
	default:
		return firewallSeen{}, false
	}
	var port int
	if json.Unmarshal(tuple.Concat[2], &port) != nil || port < 1 || port > 65535 {
		return firewallSeen{}, false
	}
	return firewallSeen{Source: ip.String(), Protocol: protocol, Port: port}, true
}
