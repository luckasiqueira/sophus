package flowengine

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

type HTTPResponseMapping struct {
	Path         string `json:"path"`
	VariableName string `json:"variableName"`
}

func httpResponseMappings(data map[string]interface{}) []HTTPResponseMapping {
	raw, ok := data["responseMappings"]
	if !ok || raw == nil {
		return nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var mappings []HTTPResponseMapping
	if json.Unmarshal(encoded, &mappings) != nil {
		return nil
	}
	return mappings
}

func applyHTTPResponseMappings(response map[string]interface{}, data map[string]interface{}) (ExecutionContext, []string) {
	mapped := ExecutionContext{}
	errorsList := []string{}
	body := response["data"]
	for _, mapping := range httpResponseMappings(data) {
		path := strings.TrimSpace(mapping.Path)
		variableName := strings.TrimSpace(mapping.VariableName)
		value, found := resolveJSONPath(body, path)
		if !found {
			errorsList = append(errorsList, fmt.Sprintf("caminho %s não encontrado", path))
			continue
		}
		if value == nil {
			mapped[variableName] = "null"
		} else {
			mapped[variableName] = value
		}
	}
	return mapped, errorsList
}

func resolveJSONPath(root interface{}, path string) (interface{}, bool) {
	tokens, err := parseJSONPath(path)
	if err != nil {
		return nil, false
	}
	current := root
	for _, token := range tokens {
		switch typed := current.(type) {
		case map[string]interface{}:
			var exists bool
			current, exists = typed[token]
			if !exists {
				return nil, false
			}
		case []interface{}:
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 || index >= len(typed) {
				return nil, false
			}
			current = typed[index]
		default:
			return nil, false
		}
	}
	return current, true
}

func parseJSONPath(path string) ([]string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("caminho vazio")
	}
	if path == "$" {
		return []string{}, nil
	}
	path = strings.TrimPrefix(path, "$")
	tokens := []string{}
	for index := 0; index < len(path); {
		if path[index] == '.' {
			index++
			continue
		}
		if path[index] == '[' {
			end := strings.IndexByte(path[index:], ']')
			if end < 0 {
				return nil, fmt.Errorf("colchete não fechado")
			}
			end += index
			token := strings.TrimSpace(path[index+1 : end])
			if len(token) >= 2 && token[0] == '"' {
				unquoted, err := strconv.Unquote(token)
				if err != nil {
					return nil, err
				}
				token = unquoted
			}
			if token == "" {
				return nil, fmt.Errorf("índice vazio")
			}
			tokens = append(tokens, token)
			index = end + 1
			continue
		}
		end := index
		for end < len(path) && path[end] != '.' && path[end] != '[' {
			end++
		}
		token := strings.TrimSpace(path[index:end])
		if token == "" {
			return nil, fmt.Errorf("segmento vazio")
		}
		tokens = append(tokens, token)
		index = end
	}
	return tokens, nil
}

func TestHTTPRequest(data map[string]interface{}, ctx ExecutionContext) (map[string]interface{}, error) {
	if err := validateHTTPRequestNode(data); err != nil {
		return nil, err
	}
	return (&Messenger{}).ExecuteHTTPRequest(data, ctx), nil
}
