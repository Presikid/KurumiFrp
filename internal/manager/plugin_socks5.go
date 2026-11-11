package manager

import (
	"bytes"
	"fmt"
	"unicode"
	"unicode/utf8"
)

const socks5PluginType = "socks5"

// encoding/json replaces malformed UTF-8 and unpaired surrogates. Check raw
// credential strings first so the saved password is never silently changed.
func validPluginJSONText(raw []byte) bool {
	if len(raw) == 0 {
		return true
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' || !utf8.Valid(raw) {
		return false
	}
	hex4 := func(data []byte) (uint16, bool) {
		if len(data) != 4 {
			return 0, false
		}
		var value uint16
		for _, c := range data {
			value <<= 4
			switch {
			case c >= '0' && c <= '9':
				value += uint16(c - '0')
			case c >= 'a' && c <= 'f':
				value += uint16(c-'a') + 10
			case c >= 'A' && c <= 'F':
				value += uint16(c-'A') + 10
			default:
				return 0, false
			}
		}
		return value, true
	}
	end := len(raw) - 1
	for i := 1; i < end; i++ {
		if raw[i] != '\\' {
			continue
		}
		i++
		if i >= end {
			return false
		}
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= end {
			return false
		}
		value, ok := hex4(raw[i+1 : i+5])
		if !ok {
			return false
		}
		i += 4
		if value >= 0xd800 && value <= 0xdbff {
			if i+6 >= end || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, ok := hex4(raw[i+3 : i+7])
			if !ok || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		} else if value >= 0xdc00 && value <= 0xdfff {
			return false
		}
	}
	return true
}

// SOCKS5 username/password fields have one-byte lengths on the wire. The
// workbench accepts UTF-8 text and deliberately excludes control characters.
func validPluginCredential(value string) bool {
	if !utf8.ValidString(value) || len(value) < 1 || len(value) > 255 {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func validatePluginTarget(t Tunnel) error {
	if t.HasPluginPassword {
		return fmt.Errorf("插件密码状态标记不能写入配置")
	}
	switch t.PluginType {
	case "":
		if t.PluginUser != "" || t.PluginPassword != "" {
			return fmt.Errorf("未启用插件时不能保留插件认证信息")
		}
		return nil
	case socks5PluginType:
		if t.Type != "tcp" {
			return fmt.Errorf("SOCKS5 插件仅适用于 TCP 规则")
		}
		if t.LocalHost != "" || t.LocalPort != 0 || t.UnixSocketPath != "" {
			return fmt.Errorf("SOCKS5 插件不能同时指定本地地址、端口或 Unix 套接字")
		}
		if t.HealthCheckType != "" || t.HealthCheckPath != "" || t.HealthCheckTimeout != 0 || t.HealthCheckInterval != 0 || t.HealthCheckMaxFailed != 0 || t.ProxyProtocolVersion != "" || t.ProbePath != "" || t.ProbeMethod != "" {
			return fmt.Errorf("SOCKS5 插件不支持本地目标检测、代理健康检查或 PROXY 协议")
		}
		if t.BandwidthKB > 0 && (t.BandwidthMode == "" || t.BandwidthMode == "client") {
			return fmt.Errorf("SOCKS5 插件暂不支持客户端限速；请关闭限速或选择服务端限速")
		}
		if !validPluginCredential(t.PluginUser) || t.PluginPassword == "" || len(t.PluginPassword) > 512 {
			return fmt.Errorf("SOCKS5 用户名和密码须为 1–255 字节的有效文本，不能包含控制字符")
		}
		return nil
	default:
		return fmt.Errorf("不支持的客户端插件")
	}
}

func (s *Store) encryptPluginPassword(value string) (string, error) {
	if !validPluginCredential(value) {
		return "", fmt.Errorf("SOCKS5 密码须为 1–255 字节的有效文本，不能包含控制字符")
	}
	return s.encryptValue(value)
}

func (s *Store) validatePluginSecret(t Tunnel) error {
	if t.PluginPassword == "" {
		return nil
	}
	value, err := s.decrypt(t.PluginPassword)
	if err != nil || !validPluginCredential(value) {
		return fmt.Errorf("已保存的插件密码无法解密或无效")
	}
	return nil
}
