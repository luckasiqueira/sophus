package flowengine

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var stringMethodPattern = regexp.MustCompile(`\.(includes|startsWith|endsWith|toLowerCase|toUpperCase|test|match)\(`)
var regexTestPattern = regexp.MustCompile(`/[^/]+/\w*\.test\(`)

var structuredConditionSources = map[string]bool{
	"contactName": true, "contactNumber": true, "conversationTag": true, "contactEmail": true,
	"contactMessage": true, "department": true, "time": true, "variable": true, "connectionType": true,
}

var structuredConditionOperators = map[string]bool{
	"equals": true, "notEquals": true, "contains": true, "notContains": true,
	"greaterThan": true, "lessThan": true, "true": true, "false": true,
}

func (e *Engine) EvaluateNodeCondition(data map[string]interface{}, ctx ExecutionContext) bool {
	source := strings.TrimSpace(stringVal(data, "conditionSource"))
	if source == "" || source == "legacy" {
		return e.EvaluateCondition(stringVal(data, "condition"), ctx)
	}
	if !structuredConditionSources[source] {
		return false
	}
	operator := strings.TrimSpace(stringVal(data, "conditionOperator"))
	if !structuredConditionOperators[operator] {
		return false
	}
	actual := structuredConditionValue(source, stringVal(data, "conditionVariable"), ctx)
	expected := ReplaceVariables(stringVal(data, "conditionValue"), ctx)
	return compareStructuredCondition(actual, operator, expected)
}

func structuredConditionValue(source, variable string, ctx ExecutionContext) interface{} {
	switch source {
	case "contactName":
		return ctx["contactName"]
	case "contactNumber":
		return ctx["contactNumber"]
	case "conversationTag":
		return ctx["conversationTags"]
	case "contactEmail":
		return ctx["contactEmail"]
	case "contactMessage":
		return ctx["message"]
	case "department":
		return ctx["department"]
	case "time":
		if current, ok := ctx["currentTime"].(string); ok && current != "" {
			return current
		}
		return time.Now().Format("15:04")
	case "variable":
		variable = strings.TrimSpace(variable)
		if variable == "" || strings.HasPrefix(variable, "_") {
			return nil
		}
		return resolvePath(ctx, variable)
	case "connectionType":
		return ctx["connectionType"]
	default:
		return nil
	}
}

func compareStructuredCondition(actual interface{}, operator, expected string) bool {
	switch operator {
	case "true":
		return conditionTruthy(actual)
	case "false":
		return !conditionTruthy(actual)
	case "equals":
		return conditionEquals(actual, expected)
	case "notEquals":
		return !conditionEquals(actual, expected)
	case "contains":
		return conditionContains(actual, expected)
	case "notContains":
		return !conditionContains(actual, expected)
	case "greaterThan":
		return compareConditionOrder(actual, expected) > 0
	case "lessThan":
		return compareConditionOrder(actual, expected) < 0
	default:
		return false
	}
}

func conditionValues(value interface{}) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []interface{}:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			values = append(values, fmt.Sprint(item))
		}
		return values
	case nil:
		return []string{""}
	default:
		return []string{fmt.Sprint(typed)}
	}
}

func conditionEquals(actual interface{}, expected string) bool {
	expected = strings.TrimSpace(expected)
	for _, value := range conditionValues(actual) {
		if strings.EqualFold(strings.TrimSpace(value), expected) {
			return true
		}
	}
	return false
}

func conditionContains(actual interface{}, expected string) bool {
	expected = strings.ToLower(strings.TrimSpace(expected))
	if expected == "" {
		return false
	}
	for _, value := range conditionValues(actual) {
		if strings.Contains(strings.ToLower(value), expected) {
			return true
		}
	}
	return false
}

func conditionTruthy(value interface{}) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case float64:
		return typed != 0
	case int:
		return typed != 0
	case int64:
		return typed != 0
	case []string:
		return len(typed) > 0
	case []interface{}:
		return len(typed) > 0
	case nil:
		return false
	default:
		text := strings.ToLower(strings.TrimSpace(fmt.Sprint(typed)))
		switch text {
		case "", "0", "false", "falso", "não", "nao", "no":
			return false
		case "1", "true", "verdadeiro", "sim", "yes":
			return true
		default:
			return true
		}
	}
}

func compareConditionOrder(actual interface{}, expected string) int {
	actualText := strings.TrimSpace(fmt.Sprint(actual))
	expected = strings.TrimSpace(expected)
	if actualTime, err := time.Parse("15:04", actualText); err == nil {
		if expectedTime, expectedErr := time.Parse("15:04", expected); expectedErr == nil {
			actualMinutes := actualTime.Hour()*60 + actualTime.Minute()
			expectedMinutes := expectedTime.Hour()*60 + expectedTime.Minute()
			return compareNumbers(float64(actualMinutes), float64(expectedMinutes))
		}
	}
	if actualNumber, err := strconv.ParseFloat(actualText, 64); err == nil {
		if expectedNumber, expectedErr := strconv.ParseFloat(expected, 64); expectedErr == nil {
			return compareNumbers(actualNumber, expectedNumber)
		}
	}
	return strings.Compare(strings.ToLower(actualText), strings.ToLower(expected))
}

func compareNumbers(left, right float64) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

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
