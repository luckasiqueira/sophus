package flowengine

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var varPattern = regexp.MustCompile(`\{\{([\w.]+)\}\}`)

func ReplaceVariables(text string, ctx ExecutionContext) string {
	if text == "" {
		return text
	}
	return varPattern.ReplaceAllStringFunc(text, func(match string) string {
		sub := varPattern.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		path := sub[1]
		if strings.HasPrefix(path, "_") {
			return match
		}
		value := resolvePath(ctx, path)
		if value == nil {
			return match
		}
		switch v := value.(type) {
		case string:
			return v
		case float64, int, int64, bool:
			return fmt.Sprint(v)
		default:
			b, _ := json.Marshal(v)
			return string(b)
		}
	})
}

func replaceVariablesLimited(text string, ctx ExecutionContext, maxBytes int) (string, error) {
	if len(text) > maxBytes {
		return "", fmt.Errorf("valor expandido excede o limite de %d bytes", maxBytes)
	}
	var result strings.Builder
	cursor := 0
	appendValue := func(value string) error {
		if result.Len()+len(value) > maxBytes {
			return fmt.Errorf("valor expandido excede o limite de %d bytes", maxBytes)
		}
		result.WriteString(value)
		return nil
	}
	for _, indexes := range varPattern.FindAllStringSubmatchIndex(text, -1) {
		if err := appendValue(text[cursor:indexes[0]]); err != nil {
			return "", err
		}
		match := text[indexes[0]:indexes[1]]
		path := text[indexes[2]:indexes[3]]
		value := match
		if !strings.HasPrefix(path, "_") {
			if resolved := resolvePath(ctx, path); resolved != nil {
				value = flowVariableText(resolved)
			}
		}
		if err := appendValue(value); err != nil {
			return "", err
		}
		cursor = indexes[1]
	}
	if err := appendValue(text[cursor:]); err != nil {
		return "", err
	}
	return result.String(), nil
}

func flowVariableText(value interface{}) string {
	switch typed := value.(type) {
	case string:
		return typed
	case float64, int, int64, bool:
		return fmt.Sprint(typed)
	default:
		encoded, _ := json.Marshal(typed)
		return string(encoded)
	}
}

func resolvePath(ctx ExecutionContext, path string) interface{} {
	parts := strings.Split(path, ".")
	var current interface{} = ctx
	for _, part := range parts {
		m, ok := current.(map[string]interface{})
		if !ok {
			if ec, ok := current.(ExecutionContext); ok {
				current = ec[part]
				continue
			}
			return nil
		}
		current = m[part]
		if current == nil {
			return nil
		}
	}
	return current
}
