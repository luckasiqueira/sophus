package flowengine

import (
	"strconv"
	"strings"
)

const (
	menuActionPrevious = "previous"
	menuActionMain     = "main"
	menuRowIDPrevious  = "__flow_back_previous"
	menuRowIDMain      = "__flow_back_main"
	maxMenuHistory     = 50
)

type menuNavigationOption struct {
	Action string
	RowID  string
	Title  string
}

func menuHistory(ctx ExecutionContext) []string {
	raw := ctx["_menuHistory"]
	history := []string{}
	switch values := raw.(type) {
	case []string:
		history = append(history, values...)
	case []interface{}:
		for _, value := range values {
			if id := strings.TrimSpace(stringValue(value)); id != "" {
				history = append(history, id)
			}
		}
	}
	return history
}

func menuHistoryWithCurrent(ctx ExecutionContext, nodeID string) []string {
	history := menuHistory(ctx)
	if len(history) > 0 && history[len(history)-1] == nodeID {
		return history
	}
	if len(history) >= maxMenuHistory {
		history = append(history[:1], history[len(history)-(maxMenuHistory-2):]...)
	}
	return append(history, nodeID)
}

func menuNavigationOptions(data map[string]interface{}, ctx ExecutionContext) []menuNavigationOption {
	if len(menuHistory(ctx)) < 2 {
		return nil
	}
	options := []menuNavigationOption{}
	if boolVal(data, "allowBackPrevious") {
		options = append(options, menuNavigationOption{
			Action: menuActionPrevious,
			RowID:  menuRowIDPrevious,
			Title:  "Voltar ao menu anterior",
		})
	}
	if boolVal(data, "allowBackMain") {
		options = append(options, menuNavigationOption{
			Action: menuActionMain,
			RowID:  menuRowIDMain,
			Title:  "Voltar ao menu principal",
		})
	}
	return options
}

func menuNavigationTarget(action string, ctx ExecutionContext) (string, []string) {
	history := menuHistory(ctx)
	if len(history) < 2 {
		return "", history
	}
	switch action {
	case menuActionPrevious:
		history = history[:len(history)-1]
		return history[len(history)-1], history
	case menuActionMain:
		return history[0], history[:1]
	default:
		return "", history
	}
}

func menuResponseResult(data map[string]interface{}, response string, ctx ExecutionContext) (string, string) {
	sections, _ := data["sections"].([]interface{})
	response = strings.TrimSpace(response)
	option := 1
	for _, rawSection := range sections {
		section, _ := rawSection.(map[string]interface{})
		rows, _ := section["rows"].([]interface{})
		for _, rawRow := range rows {
			row, _ := rawRow.(map[string]interface{})
			rowID := ReplaceVariables(stringVal(row, "rowId"), ctx)
			if response != strconv.Itoa(option) && response != strings.TrimSpace(rowID) {
				option++
				continue
			}
			handleID := stringVal(row, "_handleId")
			if handleID == "" {
				handleID = stringVal(row, "rowId")
			}
			return "menu-" + handleID, ""
		}
	}
	for _, navigation := range menuNavigationOptions(data, ctx) {
		if response == strconv.Itoa(option) || strings.EqualFold(response, navigation.RowID) {
			return "", navigation.Action
		}
		option++
	}
	return "fallback", ""
}

func menuResponseHandle(data map[string]interface{}, response string, ctx ExecutionContext) string {
	handle, _ := menuResponseResult(data, response, ctx)
	return handle
}

func stringValue(value interface{}) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}
