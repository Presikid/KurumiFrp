package manager

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

const maxWebhookTemplateSize = 8 << 10
const maxWebhookTemplateDepth = 16
const maxWebhookTemplateBody = 64 << 10

// Templates are parsed once per target configuration and remain immutable.
// Placeholders are only interpreted in string values, never in object keys.
type webhookJSONTemplate struct {
	value any
}

func parseWebhookJSONTemplate(raw string) (*webhookJSONTemplate, error) {
	if len(raw) == 0 || len(raw) > maxWebhookTemplateSize || !utf8.ValidString(raw) {
		return nil, fmt.Errorf("JSON 模板须为不超过 8192 字节的有效 UTF-8 文本")
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("JSON 模板无效")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("JSON 模板须包含且只包含一个 JSON 值")
	}
	if err := validateWebhookTemplateValue(value, 1); err != nil {
		return nil, err
	}
	return &webhookJSONTemplate{value: value}, nil
}

func validateWebhookTemplateValue(value any, depth int) error {
	if depth > maxWebhookTemplateDepth {
		return fmt.Errorf("JSON 模板层级不能超过 16 层")
	}
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			if strings.Contains(key, "{{") || strings.Contains(key, "}}") {
				return fmt.Errorf("JSON 模板的对象键不能包含占位符")
			}
			if err := validateWebhookTemplateValue(child, depth+1); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range value {
			if err := validateWebhookTemplateValue(child, depth+1); err != nil {
				return err
			}
		}
	case string:
		if !validWebhookTemplatePlaceholders(value) {
			return fmt.Errorf("JSON 模板包含无效占位符")
		}
	}
	return nil
}

func validWebhookTemplatePlaceholders(value string) bool {
	for {
		start, closing := strings.Index(value, "{{"), strings.Index(value, "}}")
		if start < 0 {
			return closing < 0
		}
		if closing >= 0 && closing < start {
			return false
		}
		value = value[start+2:]
		end := strings.Index(value, "}}")
		if end < 0 {
			return false
		}
		switch value[:end] {
		case "event", "at", "instanceId", "kind", "revision", "sourceIp", "version":
		default:
			return false
		}
		value = value[end+2:]
	}
}

func (template *webhookJSONTemplate) render(event webhookEvent) ([]byte, error) {
	revision := ""
	if event.Revision != 0 {
		revision = strconv.FormatUint(event.Revision, 10)
	}
	replacer := strings.NewReplacer(
		"{{event}}", event.Event,
		"{{at}}", event.At,
		"{{instanceId}}", event.InstanceID,
		"{{kind}}", event.Kind,
		"{{revision}}", revision,
		"{{sourceIp}}", event.SourceIP,
		"{{version}}", event.Version,
	)
	body, err := json.Marshal(expandWebhookTemplateValue(template.value, replacer))
	if err != nil || len(body) > maxWebhookTemplateBody {
		return nil, fmt.Errorf("通知生成失败")
	}
	return body, nil
}

func expandWebhookTemplateValue(value any, replacer *strings.Replacer) any {
	switch value := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(value))
		for key, child := range value {
			result[key] = expandWebhookTemplateValue(child, replacer)
		}
		return result
	case []any:
		result := make([]any, len(value))
		for i, child := range value {
			result[i] = expandWebhookTemplateValue(child, replacer)
		}
		return result
	case string:
		return replacer.Replace(value)
	default:
		return value
	}
}
