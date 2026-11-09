package manager

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/pelletier/go-toml/v2"
)

// The importer consumes known fields and reports every remaining value.
// Parsing itself uses the same maintained TOML library as the frp fork.
type tomlTable struct{ values map[string]any }

func parseImportTOML(text string) (*tomlTable, error) {
	if !utf8.ValidString(text) {
		return nil, fmt.Errorf("TOML 必须是有效 UTF-8")
	}
	if strings.Contains(text, "{{") {
		return nil, fmt.Errorf("配置含 frp 模板语法；导入不会展开模板")
	}
	var root map[string]any
	if err := toml.Unmarshal([]byte(strings.TrimPrefix(text, "\ufeff")), &root); err != nil {
		// Parser diagnostics may contain input values; do not echo them.
		return nil, fmt.Errorf("TOML 语法无效，请检查配置文件")
	}
	items := 0
	value, err := importTOMLValue(root, 0, &items)
	if err != nil {
		return nil, err
	}
	return value.(*tomlTable), nil
}

func importTOMLValue(value any, depth int, items *int) (any, error) {
	(*items)++
	if depth > 32 || *items > 20000 {
		return nil, fmt.Errorf("TOML 配置层级或条目过多")
	}
	switch item := value.(type) {
	case map[string]any:
		table := &tomlTable{values: make(map[string]any, len(item))}
		for key, child := range item {
			converted, err := importTOMLValue(child, depth+1, items)
			if err != nil {
				return nil, err
			}
			table.values[key] = converted
		}
		return table, nil
	case []any:
		result := make([]any, 0, len(item))
		for _, child := range item {
			converted, err := importTOMLValue(child, depth+1, items)
			if err != nil {
				return nil, err
			}
			result = append(result, converted)
		}
		return result, nil
	default:
		return value, nil
	}
}
