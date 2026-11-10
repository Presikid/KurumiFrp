package manager

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/http/httpguts"
)

const (
	maxHTTPHeadersPerDirection = 32
	maxHTTPHeadersPerInstance  = 128
	maxHTTPHeaderNameBytes     = 128
	maxHTTPHeaderValueBytes    = 1024
)

// HTTPHeader stores an encrypted value. HasValue is used only in public views;
// an encrypted empty value is distinct from a missing header.
type HTTPHeader struct {
	Name     string `json:"name"`
	Value    string `json:"value,omitempty"`
	HasValue bool   `json:"hasValue,omitempty"`
}

type httpHeaderEdit struct {
	Name     string  `json:"name"`
	Value    *string `json:"value,omitempty"`
	HasValue bool    `json:"hasValue,omitempty"`
}

func validHTTPHeaderName(name string) bool {
	return len(name) <= maxHTTPHeaderNameBytes && httpguts.ValidHeaderFieldName(name)
}

func validHTTPHeaderValue(value string) bool {
	return len(value) <= maxHTTPHeaderValueBytes && utf8.ValidString(value) && httpguts.ValidHeaderFieldValue(value)
}

func validateHTTPHeaders(t Tunnel) error {
	if t.Type != "http" && (len(t.RequestHeaders) != 0 || len(t.ResponseHeaders) != 0) {
		return fmt.Errorf("自定义请求头和响应头仅适用于 HTTP；更换协议前请先删除头项")
	}
	for _, headers := range [][]HTTPHeader{t.RequestHeaders, t.ResponseHeaders} {
		if len(headers) > maxHTTPHeadersPerDirection {
			return fmt.Errorf("每种方向最多支持 32 个自定义 HTTP 头")
		}
		seen := make(map[string]bool, len(headers))
		for _, header := range headers {
			if !validHTTPHeaderName(header.Name) || header.Value == "" || header.HasValue {
				return fmt.Errorf("自定义 HTTP 头名称或已保存的值无效")
			}
			key := strings.ToLower(header.Name)
			if seen[key] {
				return fmt.Errorf("同一方向的 HTTP 头名称不能重复（不区分大小写）")
			}
			seen[key] = true
		}
	}
	return nil
}

// Empty and short header values use the same encrypted envelope as other local
// credentials, but have HTTP-specific validation instead of token length rules.
func (s *Store) encryptHTTPHeaderValue(value string) (string, error) {
	if !validHTTPHeaderValue(value) {
		return "", fmt.Errorf("HTTP 头值无效或超过 1024 字节")
	}
	return s.encryptValue(value)
}

func (s *Store) validateStoredHTTPHeaders(t Tunnel) error {
	for _, headers := range [][]HTTPHeader{t.RequestHeaders, t.ResponseHeaders} {
		for _, header := range headers {
			value, err := s.decrypt(header.Value)
			if err != nil || !validHTTPHeaderValue(value) {
				return fmt.Errorf("已保存的 HTTP 头无法解密或值无效")
			}
		}
	}
	return nil
}

// Missing arrays preserve old entries. A present array replaces that direction:
// a nil value keeps the same case-insensitive name, and a pointer to an empty
// string explicitly sets an empty value. Omitting an entry deletes it.
func (s *Store) mergeHTTPHeaders(raw json.RawMessage, previous []HTTPHeader) ([]HTTPHeader, error) {
	if len(raw) == 0 {
		return append([]HTTPHeader(nil), previous...), nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, fmt.Errorf("请用数组明确指定 HTTP 头；空数组表示删除")
	}
	var edits []httpHeaderEdit
	if err := decodeStrictJSON(raw, &edits); err != nil || len(edits) > maxHTTPHeadersPerDirection {
		return nil, fmt.Errorf("HTTP 头格式无效或数量超过 32")
	}
	old := make(map[string]HTTPHeader, len(previous))
	for _, header := range previous {
		old[strings.ToLower(header.Name)] = header
	}
	seen := make(map[string]bool, len(edits))
	result := make([]HTTPHeader, 0, len(edits))
	for _, edit := range edits {
		if !validHTTPHeaderName(edit.Name) {
			return nil, fmt.Errorf("HTTP 头名称无效或超过 128 字节")
		}
		key := strings.ToLower(edit.Name)
		if seen[key] {
			return nil, fmt.Errorf("同一方向的 HTTP 头名称不能重复（不区分大小写）")
		}
		seen[key] = true
		prior, exists := old[key]
		header := HTTPHeader{Name: edit.Name}
		if edit.Value == nil {
			if !exists {
				return nil, fmt.Errorf("新增或重命名 HTTP 头时请提供值；可显式设置空值")
			}
			header.Value = prior.Value
		} else {
			if !validHTTPHeaderValue(*edit.Value) {
				return nil, fmt.Errorf("HTTP 头值无效或超过 1024 字节")
			}
			if exists {
				value, err := s.decrypt(prior.Value)
				if err != nil {
					return nil, fmt.Errorf("已保存的 HTTP 头无法解密")
				}
				if value == *edit.Value {
					header.Value = prior.Value
				}
			}
			if header.Value == "" {
				var err error
				header.Value, err = s.encryptHTTPHeaderValue(*edit.Value)
				if err != nil {
					return nil, fmt.Errorf("HTTP 头无法保存")
				}
			}
		}
		result = append(result, header)
	}
	return result, nil
}
