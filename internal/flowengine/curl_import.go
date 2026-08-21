package flowengine

import (
	"encoding/base64"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const maxCURLImportBytes = 64 << 10

type CURLImportField struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	ContentType string `json:"contentType,omitempty"`
}

type CURLImportConfig struct {
	Method          string            `json:"method"`
	URL             string            `json:"url"`
	HeaderMode      string            `json:"headerMode"`
	HeaderFields    []CURLImportField `json:"headerFields"`
	BodyMode        string            `json:"bodyMode"`
	Body            string            `json:"body,omitempty"`
	BodyFields      []CURLImportField `json:"bodyFields"`
	Timeout         int               `json:"timeout,omitempty"`
	ConnectTimeout  int               `json:"connectTimeout,omitempty"`
	FollowRedirects bool              `json:"followRedirects"`
}

type curlDataPart struct {
	value     string
	urlEncode bool
}

func ParseCURL(command string) (CURLImportConfig, error) {
	if strings.TrimSpace(command) == "" {
		return CURLImportConfig{}, fmt.Errorf("informe um comando cURL")
	}
	if len(command) > maxCURLImportBytes {
		return CURLImportConfig{}, fmt.Errorf("o comando cURL excede o limite de %d bytes", maxCURLImportBytes)
	}
	tokens, err := tokenizeCURL(command)
	if err != nil {
		return CURLImportConfig{}, err
	}
	if len(tokens) == 0 || !isCURLCommand(tokens[0]) {
		return CURLImportConfig{}, fmt.Errorf("o comando deve começar com curl")
	}

	config := CURLImportConfig{HeaderMode: "none", HeaderFields: []CURLImportField{}, BodyMode: "none", BodyFields: []CURLImportField{}}
	var dataParts []curlDataPart
	var formFields []CURLImportField
	var explicitMethod, useQuery, jsonMode bool

	for index := 1; index < len(tokens); index++ {
		token := tokens[index]
		option, inlineValue, hasInlineValue := splitLongOption(token)
		nextValue := func() (string, error) {
			if hasInlineValue {
				return inlineValue, nil
			}
			if index+1 >= len(tokens) {
				return "", fmt.Errorf("a opção %s requer um valor", option)
			}
			index++
			return tokens[index], nil
		}

		switch option {
		case "-X", "--request":
			value, valueErr := nextValue()
			if valueErr != nil {
				return CURLImportConfig{}, valueErr
			}
			config.Method = strings.ToUpper(strings.TrimSpace(value))
			explicitMethod = true
		case "-H", "--header":
			value, valueErr := nextValue()
			if valueErr != nil {
				return CURLImportConfig{}, valueErr
			}
			if err := addCURLHeader(&config, value); err != nil {
				return CURLImportConfig{}, err
			}
		case "-d", "--data", "--data-raw", "--data-binary":
			value, valueErr := nextValue()
			if valueErr != nil {
				return CURLImportConfig{}, valueErr
			}
			if option != "--data-raw" && strings.HasPrefix(value, "@") {
				return CURLImportConfig{}, fmt.Errorf("arquivos locais em %s não podem ser importados", option)
			}
			dataParts = append(dataParts, curlDataPart{value: value})
		case "--data-urlencode":
			value, valueErr := nextValue()
			if valueErr != nil {
				return CURLImportConfig{}, valueErr
			}
			if strings.HasPrefix(value, "@") || strings.Contains(value, "@") && !strings.Contains(value, "=") {
				return CURLImportConfig{}, fmt.Errorf("arquivos locais em --data-urlencode não podem ser importados")
			}
			dataParts = append(dataParts, curlDataPart{value: value, urlEncode: true})
		case "--json":
			value, valueErr := nextValue()
			if valueErr != nil {
				return CURLImportConfig{}, valueErr
			}
			if strings.HasPrefix(value, "@") {
				return CURLImportConfig{}, fmt.Errorf("arquivos JSON locais não podem ser importados")
			}
			dataParts = append(dataParts, curlDataPart{value: value})
			jsonMode = true
		case "-G", "--get":
			useQuery = true
		case "--url":
			value, valueErr := nextValue()
			if valueErr != nil {
				return CURLImportConfig{}, valueErr
			}
			config.URL = value
		case "-u", "--user":
			value, valueErr := nextValue()
			if valueErr != nil {
				return CURLImportConfig{}, valueErr
			}
			if err := addCURLHeader(&config, "Authorization: Basic "+base64.StdEncoding.EncodeToString([]byte(value))); err != nil {
				return CURLImportConfig{}, err
			}
		case "-A", "--user-agent":
			value, valueErr := nextValue()
			if valueErr != nil {
				return CURLImportConfig{}, valueErr
			}
			if err := addCURLHeader(&config, "User-Agent: "+value); err != nil {
				return CURLImportConfig{}, err
			}
		case "-e", "--referer":
			value, valueErr := nextValue()
			if valueErr != nil {
				return CURLImportConfig{}, valueErr
			}
			if err := addCURLHeader(&config, "Referer: "+value); err != nil {
				return CURLImportConfig{}, err
			}
		case "-b", "--cookie":
			value, valueErr := nextValue()
			if valueErr != nil {
				return CURLImportConfig{}, valueErr
			}
			if strings.HasPrefix(value, "@") {
				return CURLImportConfig{}, fmt.Errorf("arquivos locais de cookies não podem ser importados")
			}
			if err := addCURLHeader(&config, "Cookie: "+value); err != nil {
				return CURLImportConfig{}, err
			}
		case "-m", "--max-time", "--connect-timeout":
			value, valueErr := nextValue()
			if valueErr != nil {
				return CURLImportConfig{}, valueErr
			}
			seconds, parseErr := strconv.ParseFloat(strings.TrimSpace(value), 64)
			if parseErr != nil || seconds <= 0 {
				return CURLImportConfig{}, fmt.Errorf("timeout inválido: %s", value)
			}
			milliseconds := int(seconds * 1000)
			if option == "--connect-timeout" {
				config.ConnectTimeout = milliseconds
			} else {
				config.Timeout = milliseconds
			}
		case "-F", "--form", "--form-string":
			value, valueErr := nextValue()
			if valueErr != nil {
				return CURLImportConfig{}, valueErr
			}
			field, parseErr := parseCURLFormField(value, option == "--form-string")
			if parseErr != nil {
				return CURLImportConfig{}, parseErr
			}
			formFields = append(formFields, field)
		case "-I", "--head":
			return CURLImportConfig{}, fmt.Errorf("o método HEAD não é suportado pelo bloco HTTP Request")
		case "-L", "--location":
			config.FollowRedirects = true
		case "-k", "--insecure":
			return CURLImportConfig{}, fmt.Errorf("--insecure não é suportado; o bloco exige certificados TLS válidos")
		case "--location-trusted":
			return CURLImportConfig{}, fmt.Errorf("--location-trusted não é suportado por segurança")
		case "-fsSL", "-sSL":
			config.FollowRedirects = true
		case "--compressed", "-s", "--silent", "-S", "--show-error", "--fail", "--fail-with-body", "--globoff", "--http1.1", "--http2", "-i", "--include", "-sS", "-fsS":
			// These cURL transport/output flags do not change the imported request fields.
		case "--":
			if index+1 >= len(tokens) {
				return CURLImportConfig{}, fmt.Errorf("URL não informada")
			}
			index++
			config.URL = tokens[index]
		case "":
			if config.URL != "" {
				return CURLImportConfig{}, fmt.Errorf("argumento inesperado: %s", token)
			}
			config.URL = token
		default:
			return CURLImportConfig{}, fmt.Errorf("opção cURL não suportada: %s", option)
		}
	}

	if strings.TrimSpace(config.URL) == "" {
		return CURLImportConfig{}, fmt.Errorf("URL não informada no comando cURL")
	}
	if len(dataParts) > 0 && len(formFields) > 0 {
		return CURLImportConfig{}, fmt.Errorf("não é possível combinar --data e --form no bloco HTTP Request")
	}
	if useQuery && len(formFields) > 0 {
		return CURLImportConfig{}, fmt.Errorf("não é possível combinar -G e --form no bloco HTTP Request")
	}
	hasData := len(dataParts) > 0 || len(formFields) > 0
	body := curlDataBody(dataParts)
	if useQuery && body != "" {
		config.URL = appendCURLQuery(config.URL, body)
		body = ""
	}
	if config.Method == "" {
		if useQuery || !hasData {
			config.Method = http.MethodGet
		} else {
			config.Method = http.MethodPost
		}
	}
	if !allowedHTTPMethod(config.Method) {
		return CURLImportConfig{}, fmt.Errorf("método HTTP não suportado: %s", config.Method)
	}
	if explicitMethod && config.Method == http.MethodGet && hasData && !useQuery {
		return CURLImportConfig{}, fmt.Errorf("o bloco não envia corpo com GET; use -G para importar os dados na query string")
	}

	if jsonMode {
		if len(formFields) > 0 {
			return CURLImportConfig{}, fmt.Errorf("não é possível combinar --json e --form no bloco HTTP Request")
		}
		if !hasCURLHeader(config.HeaderFields, "Content-Type") {
			if err := addCURLHeader(&config, "Content-Type: application/json"); err != nil {
				return CURLImportConfig{}, err
			}
		}
		if !hasCURLHeader(config.HeaderFields, "Accept") {
			if err := addCURLHeader(&config, "Accept: application/json"); err != nil {
				return CURLImportConfig{}, err
			}
		}
	}
	if len(formFields) > 0 {
		config.BodyMode = "multipart"
		config.BodyFields = formFields
	} else if hasData && !useQuery {
		contentType := curlHeaderValue(config.HeaderFields, "Content-Type")
		if contentType == "" && !hasCURLHeader(config.HeaderFields, "Content-Type") {
			contentType = "application/x-www-form-urlencoded"
			if err := addCURLHeader(&config, "Content-Type: "+contentType); err != nil {
				return CURLImportConfig{}, err
			}
		}
		if isJSONMediaType(contentType) {
			config.BodyMode = "rawJSON"
		} else {
			config.BodyMode = "raw"
		}
		config.Body = body
	}
	if len(config.HeaderFields) > 0 {
		config.HeaderMode = "fields"
	}
	if config.Timeout != 0 && (config.Timeout < 100 || config.Timeout > int(maxHTTPRequestTimeout/time.Millisecond)) {
		return CURLImportConfig{}, fmt.Errorf("timeout deve estar entre 0.1 e 60 segundos")
	}
	if config.ConnectTimeout != 0 && (config.ConnectTimeout < 100 || config.ConnectTimeout > int(maxHTTPRequestTimeout/time.Millisecond)) {
		return CURLImportConfig{}, fmt.Errorf("timeout de conexão deve estar entre 0.1 e 60 segundos")
	}
	if err := validateHTTPRequestNode(config.nodeData()); err != nil {
		return CURLImportConfig{}, err
	}
	return config, nil
}

func (config CURLImportConfig) nodeData() map[string]interface{} {
	headerFields := make([]interface{}, 0, len(config.HeaderFields))
	for _, field := range config.HeaderFields {
		headerFields = append(headerFields, map[string]interface{}{"key": field.Key, "value": field.Value})
	}
	bodyFields := make([]interface{}, 0, len(config.BodyFields))
	for _, field := range config.BodyFields {
		bodyFields = append(bodyFields, map[string]interface{}{
			"key": field.Key, "value": field.Value, "contentType": field.ContentType,
		})
	}
	data := map[string]interface{}{
		"method": config.Method, "url": config.URL, "headerMode": config.HeaderMode,
		"headerFields": headerFields, "bodyMode": config.BodyMode, "body": config.Body,
		"bodyFields": bodyFields, "followRedirects": config.FollowRedirects, "connectTimeout": config.ConnectTimeout,
	}
	if config.Timeout != 0 {
		data["timeout"] = config.Timeout
	}
	return data
}

func parseCURLFormField(raw string, literal bool) (CURLImportField, error) {
	separator := strings.IndexByte(raw, '=')
	if separator <= 0 {
		return CURLImportField{}, fmt.Errorf("campo form-data inválido: %s", raw)
	}
	field := CURLImportField{Key: strings.TrimSpace(raw[:separator]), Value: raw[separator+1:]}
	if field.Key == "" || strings.ContainsAny(field.Key, "\r\n") {
		return CURLImportField{}, fmt.Errorf("nome de campo form-data inválido")
	}
	if literal {
		return field, nil
	}
	if modifier := curlFormUnsupportedModifier(field.Value); modifier != "" {
		return CURLImportField{}, fmt.Errorf("o modificador multipart %s não é suportado no campo %s", modifier, field.Key)
	}
	if modifier := curlFormTypeModifier(field.Value); modifier >= 0 {
		field.ContentType = trimCURLFormQuotes(strings.TrimSpace(field.Value[modifier+6:]))
		field.Value = field.Value[:modifier]
		lowerContentType := strings.ToLower(field.ContentType)
		if strings.Contains(lowerContentType, ";headers=") || strings.Contains(lowerContentType, ";filename=") || strings.Contains(lowerContentType, ";encoder=") {
			return CURLImportField{}, fmt.Errorf("modificadores multipart adicionais não são suportados no campo %s", field.Key)
		}
	}
	field.Value = trimCURLFormQuotes(field.Value)
	if strings.HasPrefix(field.Value, "@") || strings.HasPrefix(field.Value, "<") {
		return CURLImportField{}, fmt.Errorf("o arquivo local do campo %s não pode ser importado; envie o conteúdo como texto ou use --form-string", field.Key)
	}
	if field.ContentType != "" {
		if _, _, err := mime.ParseMediaType(field.ContentType); err != nil {
			return CURLImportField{}, fmt.Errorf("Content-Type multipart inválido para %s", field.Key)
		}
	}
	return field, nil
}

func curlFormUnsupportedModifier(value string) string {
	modifiers := []string{"headers", "filename", "encoder"}
	var quote byte
	for index := 0; index < len(value); index++ {
		if value[index] == '\\' && quote != 0 && index+1 < len(value) {
			index++
			continue
		}
		if quote != 0 {
			if value[index] == quote {
				quote = 0
			}
			continue
		}
		if value[index] == '\'' || value[index] == '"' {
			quote = value[index]
			continue
		}
		for _, modifier := range modifiers {
			if strings.HasPrefix(strings.ToLower(value[index:]), ";"+modifier+"=") {
				return modifier
			}
		}
	}
	return ""
}

func curlFormTypeModifier(value string) int {
	var quote byte
	for index := 0; index < len(value); index++ {
		if value[index] == '\\' && quote != 0 && index+1 < len(value) {
			index++
			continue
		}
		if quote != 0 {
			if value[index] == quote {
				quote = 0
			}
			continue
		}
		if value[index] == '\'' || value[index] == '"' {
			quote = value[index]
			continue
		}
		if strings.HasPrefix(strings.ToLower(value[index:]), ";type=") {
			return index
		}
	}
	return -1
}

func trimCURLFormQuotes(value string) string {
	if len(value) >= 2 && (value[0] == '"' && value[len(value)-1] == '"' || value[0] == '\'' && value[len(value)-1] == '\'') {
		return value[1 : len(value)-1]
	}
	return value
}

func addCURLHeader(config *CURLImportConfig, raw string) error {
	separator := strings.IndexByte(raw, ':')
	if separator <= 0 {
		return fmt.Errorf("header cURL inválido: %s", raw)
	}
	name := http.CanonicalHeaderKey(strings.TrimSpace(raw[:separator]))
	value := strings.TrimSpace(raw[separator+1:])
	if !validHTTPHeaderName(name) {
		return fmt.Errorf("nome de header inválido: %s", name)
	}
	if blockedHTTPHeader(name) {
		return fmt.Errorf("header não permitido no bloco HTTP Request: %s", name)
	}
	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("valor inválido para o header %s", name)
	}
	if hasCURLHeader(config.HeaderFields, name) {
		return fmt.Errorf("header duplicado: %s", name)
	}
	config.HeaderFields = append(config.HeaderFields, CURLImportField{Key: name, Value: value})
	return nil
}

func hasCURLHeader(fields []CURLImportField, name string) bool {
	for _, field := range fields {
		if strings.EqualFold(field.Key, name) {
			return true
		}
	}
	return false
}

func curlHeaderValue(fields []CURLImportField, name string) string {
	for _, field := range fields {
		if strings.EqualFold(field.Key, name) {
			return field.Value
		}
	}
	return ""
}

func curlDataBody(parts []curlDataPart) string {
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		value := part.value
		if part.urlEncode {
			if separator := strings.IndexByte(value, '='); separator >= 0 {
				if separator == 0 {
					value = url.QueryEscape(value[1:])
				} else {
					value = value[:separator+1] + url.QueryEscape(value[separator+1:])
				}
			} else {
				value = url.QueryEscape(value)
			}
		}
		values = append(values, value)
	}
	return strings.Join(values, "&")
}

func appendCURLQuery(requestURL, query string) string {
	fragment := ""
	if index := strings.IndexByte(requestURL, '#'); index >= 0 {
		fragment = requestURL[index:]
		requestURL = requestURL[:index]
	}
	separator := "?"
	if strings.Contains(requestURL, "?") {
		separator = "&"
	}
	return requestURL + separator + query + fragment
}

func isJSONMediaType(contentType string) bool {
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	return mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
}

func splitLongOption(token string) (string, string, bool) {
	if strings.HasPrefix(token, "--") {
		if separator := strings.IndexByte(token, '='); separator > 2 {
			return token[:separator], token[separator+1:], true
		}
		return token, "", false
	}
	for _, option := range []string{"-X", "-H", "-d", "-u", "-A", "-e", "-b", "-m", "-F"} {
		if strings.HasPrefix(token, option) && len(token) > len(option) {
			return option, token[len(option):], true
		}
	}
	if strings.HasPrefix(token, "-") {
		return token, "", false
	}
	return "", "", false
}

func isCURLCommand(token string) bool {
	normalized := strings.ToLower(strings.TrimSpace(token))
	return normalized == "curl" || normalized == "curl.exe" || strings.HasSuffix(normalized, "/curl") || strings.HasSuffix(normalized, "/curl.exe")
}

func tokenizeCURL(command string) ([]string, error) {
	if strings.Contains(command, "^\"") || strings.Contains(command, "^\n") || strings.Contains(command, "^\r\n") {
		return tokenizeWindowsCURL(command)
	}
	return tokenizeShellCURL(command)
}

func tokenizeShellCURL(command string) ([]string, error) {
	var tokens []string
	var current strings.Builder
	var quote byte
	var tokenStarted, ansiQuote bool
	powerShellQuotes := strings.Contains(command, "''") || strings.Contains(command, "`")
	flush := func() {
		if tokenStarted {
			tokens = append(tokens, current.String())
			current.Reset()
			tokenStarted = false
		}
	}

	for index := 0; index < len(command); index++ {
		character := command[index]
		if quote != 0 {
			if quote == '\'' && powerShellQuotes && character == '\'' && index+1 < len(command) && command[index+1] == '\'' {
				current.WriteByte('\'')
				index++
				continue
			}
			if character == quote {
				quote = 0
				ansiQuote = false
				continue
			}
			if character == '\\' && quote == '"' && index+1 < len(command) {
				next := command[index+1]
				if next == '\n' {
					index++
					continue
				}
				if next == '\r' && index+2 < len(command) && command[index+2] == '\n' {
					index += 2
					continue
				}
				if strings.ContainsRune("$`\"\\", rune(next)) {
					index++
					current.WriteByte(next)
					continue
				}
			}
			if character == '\\' && ansiQuote && index+1 < len(command) {
				index++
				switch command[index] {
				case 'n':
					current.WriteByte('\n')
				case 'r':
					current.WriteByte('\r')
				case 't':
					current.WriteByte('\t')
				case 'a':
					current.WriteByte('\a')
				case 'b':
					current.WriteByte('\b')
				case 'f':
					current.WriteByte('\f')
				case 'v':
					current.WriteByte('\v')
				case 'x':
					value, consumed, err := parseCURLHexEscape(command[index+1:], 2)
					if err != nil {
						return nil, err
					}
					current.WriteByte(byte(value))
					index += consumed
				case 'u', 'U':
					digits := 4
					if command[index] == 'U' {
						digits = 8
					}
					value, consumed, err := parseCURLHexEscape(command[index+1:], digits)
					if err != nil || consumed != digits {
						return nil, fmt.Errorf("escape Unicode inválido no comando cURL")
					}
					current.WriteRune(rune(value))
					index += consumed
				default:
					if command[index] >= '0' && command[index] <= '7' {
						value, consumed := parseCURLOctalEscape(command[index:])
						current.WriteByte(byte(value))
						index += consumed - 1
					} else {
						current.WriteByte(command[index])
					}
				}
				continue
			}
			if character == '`' && quote == '"' && index+1 < len(command) {
				index++
				current.WriteByte(command[index])
				continue
			}
			current.WriteByte(character)
			continue
		}

		if unicode.IsSpace(rune(character)) {
			flush()
			continue
		}
		if character == '$' && index+1 < len(command) && command[index+1] == '\'' {
			quote = '\''
			ansiQuote = true
			tokenStarted = true
			index++
			continue
		}
		if character == '\'' || character == '"' {
			quote = character
			tokenStarted = true
			continue
		}
		if character == '\\' && index+1 < len(command) {
			index++
			if command[index] == '\n' {
				continue
			}
			if command[index] == '\r' && index+1 < len(command) && command[index+1] == '\n' {
				index++
				continue
			}
			current.WriteByte(command[index])
			tokenStarted = true
			continue
		}
		if character == '`' && index+1 < len(command) {
			index++
			if command[index] == '\r' && index+1 < len(command) && command[index+1] == '\n' {
				index++
				continue
			}
			if command[index] == '\n' {
				continue
			}
			current.WriteByte(command[index])
			tokenStarted = true
			continue
		}
		current.WriteByte(character)
		tokenStarted = true
	}
	if quote != 0 {
		return nil, fmt.Errorf("comando cURL possui aspas não fechadas")
	}
	flush()
	return tokens, nil
}

func parseCURLHexEscape(value string, maxDigits int) (uint64, int, error) {
	digits := 0
	for digits < len(value) && digits < maxDigits && isHexDigit(value[digits]) {
		digits++
	}
	if digits == 0 {
		return 0, 0, fmt.Errorf("escape hexadecimal inválido no comando cURL")
	}
	parsed, err := strconv.ParseUint(value[:digits], 16, 32)
	return parsed, digits, err
}

func parseCURLOctalEscape(value string) (uint64, int) {
	digits := 0
	for digits < len(value) && digits < 3 && value[digits] >= '0' && value[digits] <= '7' {
		digits++
	}
	parsed, _ := strconv.ParseUint(value[:digits], 8, 8)
	return parsed, digits
}

func isHexDigit(value byte) bool {
	return value >= '0' && value <= '9' || value >= 'a' && value <= 'f' || value >= 'A' && value <= 'F'
}

func tokenizeWindowsCURL(command string) ([]string, error) {
	var normalized strings.Builder
	for index := 0; index < len(command); index++ {
		if command[index] != '^' {
			normalized.WriteByte(command[index])
			continue
		}
		if index+1 >= len(command) {
			return nil, fmt.Errorf("comando cURL possui escape ^ incompleto")
		}
		index++
		if command[index] == '\r' && index+1 < len(command) && command[index+1] == '\n' {
			index++
			continue
		}
		if command[index] == '\n' {
			continue
		}
		normalized.WriteByte(command[index])
	}

	input := normalized.String()
	var tokens []string
	for index := 0; index < len(input); {
		for index < len(input) && unicode.IsSpace(rune(input[index])) {
			index++
		}
		if index >= len(input) {
			break
		}
		var token strings.Builder
		inQuotes := false
		tokenStarted := false
		for index < len(input) {
			if !inQuotes && unicode.IsSpace(rune(input[index])) {
				break
			}
			tokenStarted = true
			backslashes := 0
			for index < len(input) && input[index] == '\\' {
				backslashes++
				index++
			}
			if index < len(input) && input[index] == '"' {
				token.WriteString(strings.Repeat("\\", backslashes/2))
				if backslashes%2 == 1 {
					token.WriteByte('"')
				} else {
					inQuotes = !inQuotes
				}
				index++
				continue
			}
			token.WriteString(strings.Repeat("\\", backslashes))
			if index < len(input) && (!unicode.IsSpace(rune(input[index])) || inQuotes) {
				token.WriteByte(input[index])
				index++
			}
		}
		if inQuotes {
			return nil, fmt.Errorf("comando cURL possui aspas não fechadas")
		}
		if tokenStarted {
			tokens = append(tokens, token.String())
		}
	}
	return tokens, nil
}
