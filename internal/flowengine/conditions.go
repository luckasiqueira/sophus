package flowengine

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var stringMethodPattern = regexp.MustCompile(`\.(includes|startsWith|endsWith|toLowerCase|toUpperCase|test|match)\(`)
var regexTestPattern = regexp.MustCompile(`/[^/]+/\w*\.test\(`)

func (e *Engine) EvaluateCondition(condition string, ctx ExecutionContext) bool {
	condition = strings.TrimSpace(condition)
	if condition == "" {
		return true
	}
	if strings.HasPrefix(condition, "__hasLabel:") || strings.HasPrefix(condition, "__notHasLabel:") {
		// Labels não implementadas no Sophus ainda
		return false
	}
	if isStringCondition(condition) {
		return evaluateStringCondition(condition, ctx)
	}
	return evaluateMathCondition(condition, ctx)
}

func isStringCondition(condition string) bool {
	if stringMethodPattern.MatchString(condition) || regexTestPattern.MatchString(condition) {
		return true
	}
	return varPattern.MatchString(condition)
}

func evaluateStringCondition(condition string, ctx ExecutionContext) bool {
	resolved := varPattern.ReplaceAllStringFunc(condition, func(match string) string {
		sub := varPattern.FindStringSubmatch(match)
		if len(sub) < 2 {
			return `""`
		}
		path := sub[1]
		if strings.HasPrefix(path, "_") {
			return `""`
		}
		value := resolvePath(ctx, path)
		if value == nil {
			return `""`
		}
		switch v := value.(type) {
		case string:
			return strconv.Quote(v)
		case float64, int, int64, bool:
			return fmt.Sprint(v)
		default:
			b, _ := json.Marshal(v)
			return strconv.Quote(string(b))
		}
	})

	lower := strings.ToLower(resolved)
	if strings.Contains(lower, ".includes(") {
		return evalIncludes(resolved)
	}
	if strings.Contains(lower, ".startswith(") {
		return evalStartsWith(resolved)
	}
	if strings.Contains(lower, ".endswith(") {
		return evalEndsWith(resolved)
	}
	if strings.Contains(resolved, "===") {
		parts := strings.SplitN(resolved, "===", 2)
		return strings.TrimSpace(parts[0]) == strings.TrimSpace(parts[1])
	}
	if strings.Contains(resolved, "==") {
		parts := strings.SplitN(resolved, "==", 2)
		return strings.TrimSpace(parts[0]) == strings.TrimSpace(parts[1])
	}
	if strings.Contains(resolved, "!=") {
		parts := strings.SplitN(resolved, "!=", 2)
		return strings.TrimSpace(parts[0]) != strings.TrimSpace(parts[1])
	}
	return false
}

func evalIncludes(expr string) bool {
	re := regexp.MustCompile(`(?i)^(.+?)\.includes\((.+)\)$`)
	m := re.FindStringSubmatch(strings.TrimSpace(expr))
	if len(m) < 3 {
		return false
	}
	left := unquote(m[1])
	right := unquote(m[2])
	return strings.Contains(strings.ToLower(left), strings.ToLower(right))
}

func evalStartsWith(expr string) bool {
	re := regexp.MustCompile(`(?i)^(.+?)\.startsWith\((.+)\)$`)
	m := re.FindStringSubmatch(strings.TrimSpace(expr))
	if len(m) < 3 {
		return false
	}
	return strings.HasPrefix(unquote(m[1]), unquote(m[2]))
}

func evalEndsWith(expr string) bool {
	re := regexp.MustCompile(`(?i)^(.+?)\.endsWith\((.+)\)$`)
	m := re.FindStringSubmatch(strings.TrimSpace(expr))
	if len(m) < 3 {
		return false
	}
	return strings.HasSuffix(unquote(m[1]), unquote(m[2]))
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && ((s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'')) {
		u, err := strconv.Unquote(s)
		if err == nil {
			return u
		}
		return s[1 : len(s)-1]
	}
	return s
}

func evaluateMathCondition(condition string, ctx ExecutionContext) bool {
	cond := condition
	for key, val := range ctx {
		if strings.HasPrefix(key, "_") {
			continue
		}
		placeholder := fmt.Sprintf("{{%s}}", key)
		if strings.Contains(cond, placeholder) {
			cond = strings.ReplaceAll(cond, placeholder, fmt.Sprint(val))
		}
	}
	re := regexp.MustCompile(`([a-zA-Z_]\w*)\s*(==|!=|>=|<=|>|<)\s*(.+)`)
	m := re.FindStringSubmatch(strings.TrimSpace(cond))
	if len(m) == 4 {
		left := parseComparable(m[1], ctx)
		right := parseComparable(strings.TrimSpace(m[3]), ctx)
		switch m[2] {
		case "==":
			return left == right
		case "!=":
			return left != right
		case ">":
			return toFloat(left) > toFloat(right)
		case "<":
			return toFloat(left) < toFloat(right)
		case ">=":
			return toFloat(left) >= toFloat(right)
		case "<=":
			return toFloat(left) <= toFloat(right)
		}
	}
	return strings.EqualFold(cond, "true")
}

func parseComparable(token string, ctx ExecutionContext) string {
	token = strings.TrimSpace(token)
	token = strings.Trim(token, `"'`)
	if v, ok := ctx[token]; ok {
		return fmt.Sprint(v)
	}
	return token
}

func toFloat(v string) float64 {
	f, _ := strconv.ParseFloat(v, 64)
	return f
}

func (e *Engine) EvaluateSwitch(cases []map[string]interface{}, ctx ExecutionContext) int {
	if len(cases) == 0 {
		return -1
	}
	for i, c := range cases {
		condition := stringVal(c, "condition")
		if condition == "" {
			continue
		}
		if e.EvaluateCondition(condition, ctx) {
			return i
		}
	}
	return -1
}
