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
