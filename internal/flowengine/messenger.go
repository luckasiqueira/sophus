package flowengine

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"path"
	"sophus/internal/repo"
	"sophus/pkg/http/requests"
	"strings"
	"time"
)

const (
	defaultHTTPRequestTimeout = 10 * time.Second
	maxHTTPRequestTimeout     = 60 * time.Second
	maxHTTPRequestBytes       = 1 << 20
	maxHTTPResponseBytes      = 2 << 20
	maxHTTPHeaders            = 50
	maxHTTPHeaderBytes        = 64 << 10
)

type Messenger struct {
	Connection repo.ConnectionEVO
}

type menuRow struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	RowID       string `json:"rowId"`
}

type menuSection struct {
	Title string    `json:"title"`
	Rows  []menuRow `json:"rows"`
}

type menuPayload struct {
	Number      string        `json:"number"`
	Title       string        `json:"title,omitempty"`
	Description string        `json:"description"`
	ButtonText  string        `json:"buttonText"`
	FooterText  string        `json:"footerText"`
	Sections    []menuSection `json:"sections"`
}

func NewMessenger(connection repo.ConnectionEVO) *Messenger {
	return &Messenger{Connection: connection}
}

func (m *Messenger) SendText(conversation repo.Conversation, message string) error {
	contact, err := repo.GetContactById(conversation.Contact.Id)
	if err != nil {
		return err
	}
	msg := repo.TextMessageEVO{
		MessageBaseEVO: repo.MessageBaseEVO{
			Number: contact.Number,
			Delay:  repo.TypingDelayMilliseconds(message),
		},
		Text: message,
	}
	status, responseBody, err := msg.Send(m.Connection.EvolutionAPIKey())
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("send text failed with status %d", status)
	}
	return repo.SaveFlowMessage(conversation.Id, evolutionMessageID(responseBody), message)
}

func (m *Messenger) SendMedia(conversation repo.Conversation, mediaType, mediaURL, caption string) error {
	return m.sendMedia(conversation, mediaType, mediaURL, caption, "")
}

func (m *Messenger) SendAudio(conversation repo.Conversation, mediaURL string) error {
	return m.sendMedia(conversation, "audio", mediaURL, "", "")
}

func (m *Messenger) SendDocument(conversation repo.Conversation, mediaURL, caption, fileName string) error {
	return m.sendMedia(conversation, "document", mediaURL, caption, fileName)
}

func (m *Messenger) sendMedia(conversation repo.Conversation, mediaType, mediaURL, caption, fileName string) error {
	contact, err := repo.GetContactById(conversation.Contact.Id)
	if err != nil {
		return err
	}
	payload := buildMediaPayload(contact.Number, mediaType, mediaURL, caption, fileName)
	r := requests.Request{
		URL:     repo.ApiBaseURL + "/send/media",
		Method:  "POST",
		Payload: payload,
		Timeout: 2 * time.Minute,
		Headers: map[string]string{
			"Content-Type": "application/json",
			"apikey":       m.Connection.EvolutionAPIKey(),
		},
		Response: requests.Response{},
	}
	err = r.Do()
	if err != nil {
		return err
	}
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		return fmt.Errorf("send media failed with status %d: %s", r.StatusCode, string(r.Response.Body))
	}
	text := caption
	if strings.TrimSpace(text) == "" {
		text = fmt.Sprintf("%s enviado pelo fluxo", mediaType)
	}
	return repo.SaveFlowMessage(conversation.Id, evolutionMessageID(r.Response.Body), text)
}

func buildMediaPayload(number, mediaType, mediaURL, caption, fileName string) map[string]interface{} {
	payload := map[string]interface{}{
		"number": number,
		"type":   mediaType,
		"url":    mediaURL,
		"delay":  repo.TypingDelayMilliseconds(caption),
	}
	if caption != "" {
		payload["caption"] = caption
	}
	if mediaType == "document" {
		if strings.TrimSpace(fileName) == "" {
			parsed, err := url.Parse(mediaURL)
			if err == nil {
				fileName = path.Base(parsed.Path)
			}
		}
		payload["filename"] = path.Base(fileName)
	}
	return payload
}

func evolutionMessageID(body []byte) string {
	var response struct {
		Data struct {
			Info struct {
				ID string `json:"ID"`
			} `json:"Info"`
			Key struct {
				ID string `json:"id"`
			} `json:"key"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &response) != nil {
		return ""
	}
	if response.Data.Info.ID != "" {
		return response.Data.Info.ID
	}
	return response.Data.Key.ID
}

func (m *Messenger) SendMenu(conversation repo.Conversation, data map[string]interface{}, ctx ExecutionContext) error {
	contact, err := repo.GetContactById(conversation.Contact.Id)
	if err != nil {
		return err
	}

	rawSections, err := json.Marshal(data["sections"])
	if err != nil {
		return fmt.Errorf("invalid menu sections: %w", err)
	}
	var sections []menuSection
	if err := json.Unmarshal(rawSections, &sections); err != nil {
		return fmt.Errorf("invalid menu sections: %w", err)
	}

	payload := menuPayload{
		Number:      contact.Number,
		Title:       ReplaceVariables(stringVal(data, "title"), ctx),
		Description: ReplaceVariables(stringVal(data, "description"), ctx),
		ButtonText:  ReplaceVariables(stringVal(data, "buttonText"), ctx),
		FooterText:  ReplaceVariables(stringVal(data, "footerText"), ctx),
		Sections:    sections,
	}
	if strings.TrimSpace(payload.Description) == "" {
		return fmt.Errorf("menu description is required")
	}
	if strings.TrimSpace(payload.Title) == "" {
		payload.Title = "Menu"
	}
	if strings.TrimSpace(payload.FooterText) == "" {
		payload.FooterText = "Sophus"
	}
	if strings.TrimSpace(payload.ButtonText) == "" {
		payload.ButtonText = "Ver Menu"
	}
	if len(payload.Sections) == 0 {
		return fmt.Errorf("menu requires at least one section")
	}
	rowIDs := make(map[string]struct{})
	for sectionIndex := range payload.Sections {
		section := &payload.Sections[sectionIndex]
		section.Title = ReplaceVariables(section.Title, ctx)
		if strings.TrimSpace(section.Title) == "" || len(section.Rows) == 0 {
			return fmt.Errorf("menu section %d requires a title and at least one option", sectionIndex+1)
		}
		for rowIndex := range section.Rows {
			row := &section.Rows[rowIndex]
			row.Title = ReplaceVariables(row.Title, ctx)
			row.Description = ReplaceVariables(row.Description, ctx)
			row.RowID = ReplaceVariables(row.RowID, ctx)
			if strings.TrimSpace(row.Title) == "" || strings.TrimSpace(row.RowID) == "" {
				return fmt.Errorf("menu option %d in section %d requires a title and ID", rowIndex+1, sectionIndex+1)
			}
			if _, exists := rowIDs[row.RowID]; exists {
				return fmt.Errorf("menu option ID %q is duplicated", row.RowID)
			}
			rowIDs[row.RowID] = struct{}{}
		}
	}

	startedAt := time.Now()
	log.Printf("sending flow text menu: conversation=%d connection=%d sections=%d", conversation.Id, m.Connection.Id, len(payload.Sections))
	if err := m.SendText(conversation, formatMenuAsText(payload)); err != nil {
		log.Printf("failed to send flow text menu: conversation=%d connection=%d duration=%s error=%v", conversation.Id, m.Connection.Id, time.Since(startedAt).Round(time.Millisecond), err)
		return err
	}
	log.Printf("flow text menu sent: conversation=%d connection=%d duration=%s", conversation.Id, m.Connection.Id, time.Since(startedAt).Round(time.Millisecond))
	return nil
}

func formatMenuAsText(payload menuPayload) string {
	var text strings.Builder
	if payload.Title != "" {
		fmt.Fprintf(&text, "*%s*\n", payload.Title)
	}
	if payload.Description != "" {
		fmt.Fprintf(&text, "%s\n", payload.Description)
	}
	option := 1
	for _, section := range payload.Sections {
		if section.Title != "" {
			fmt.Fprintf(&text, "\n*%s*\n", section.Title)
		}
		for _, row := range section.Rows {
			fmt.Fprintf(&text, "%d. %s\n", option, row.Title)
			if row.Description != "" {
				fmt.Fprintf(&text, "   %s\n", row.Description)
			}
			option++
		}
	}
	text.WriteString("\nResponda com o número da opção.")
	if payload.FooterText != "" {
		fmt.Fprintf(&text, "\n_%s_", payload.FooterText)
	}
	return text.String()
}

func (m *Messenger) ExecuteHTTPRequest(data map[string]interface{}, ctx ExecutionContext) map[string]interface{} {
	method := strings.ToUpper(stringVal(data, "method"))
	if method == "" {
		method = "POST"
	}
	if !allowedHTTPMethod(method) {
		return httpRequestError("método HTTP não permitido")
	}
	url := ReplaceVariables(stringVal(data, "url"), ctx)
	if strings.TrimSpace(url) == "" {
		return httpRequestError("URL é obrigatória para HTTP Request")
	}
	if len(url) > 4096 {
		return httpRequestError("URL excede o limite de 4096 caracteres")
	}
	headers, err := httpRequestHeaders(data, ctx)
	if err != nil {
		return httpRequestError(err.Error())
	}
	timeout := defaultHTTPRequestTimeout
	if configuredTimeout := intVal(data, "timeout"); configuredTimeout != 0 {
		if configuredTimeout < 100 || configuredTimeout > int(maxHTTPRequestTimeout/time.Millisecond) {
			return httpRequestError("timeout deve estar entre 100 e 60000 ms")
		}
		timeout = time.Duration(configuredTimeout) * time.Millisecond
	}

	payload, rawBody, contentType, payloadErr := httpRequestPayload(data, ctx, method)
	if payloadErr != nil {
		return httpRequestError(payloadErr.Error())
	}

	r := requests.Request{
		URL:              url,
		Method:           method,
		Payload:          payload,
		RequestBody:      rawBody,
		Headers:          headers,
		Timeout:          timeout,
		PublicOnly:       true,
		MaxRequestBytes:  maxHTTPRequestBytes,
		MaxResponseBytes: maxHTTPResponseBytes,
		Response:         requests.Response{},
	}
	if _, ok := r.Headers[http.CanonicalHeaderKey("Content-Type")]; !ok && contentType != "" {
		r.Headers["Content-Type"] = contentType
	}

	err = r.Do()
	if err != nil {
		return map[string]interface{}{
			"error":   true,
			"message": err.Error(),
			"status":  r.StatusCode,
		}
	}
	var responseData interface{}
	if len(r.Response.Body) > 0 {
		if err := json.Unmarshal(r.Response.Body, &responseData); err != nil {
			responseData = string(r.Response.Body)
		}
	}
	return map[string]interface{}{
		"status":     r.StatusCode,
		"statusText": httpStatusText(r.StatusCode),
		"data":       responseData,
	}
}

func httpRequestError(message string) map[string]interface{} {
	return map[string]interface{}{"error": true, "message": message}
}

func allowedHTTPMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func httpRequestHeaders(data map[string]interface{}, ctx ExecutionContext) (map[string]string, error) {
	mode := strings.TrimSpace(stringVal(data, "headerMode"))
	var fields []httpKeyValue
	if mode == "" {
		if legacy, ok := data["headers"].(map[string]interface{}); ok {
			fields = make([]httpKeyValue, 0, len(legacy))
			for key, value := range legacy {
				fields = append(fields, httpKeyValue{Key: key, Value: ReplaceVariables(fmt.Sprint(value), ctx)})
			}
		} else {
			mode = "fields"
		}
	}

	switch mode {
	case "none":
		return map[string]string{}, nil
	case "fields":
		fields = keyValueFields(data["headerFields"], ctx)
	case "rawJSON":
		raw := strings.TrimSpace(stringVal(data, "headersRaw"))
		if raw == "" {
			return map[string]string{}, nil
		}
		var values map[string]interface{}
		if err := json.Unmarshal([]byte(raw), &values); err != nil {
			return nil, fmt.Errorf("JSON de headers inválido: %w", err)
		}
		fields = make([]httpKeyValue, 0, len(values))
		for key, value := range values {
			text, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("o header %s deve possuir um valor de texto", key)
			}
			fields = append(fields, httpKeyValue{Key: key, Value: ReplaceVariables(text, ctx)})
		}
	case "":
		// Legacy map was converted above.
	default:
		return nil, fmt.Errorf("modo de headers HTTP inválido: %s", mode)
	}

	if len(fields) > maxHTTPHeaders {
		return nil, fmt.Errorf("o limite é de %d headers", maxHTTPHeaders)
	}
	headers := make(map[string]string, len(fields))
	totalBytes := 0
	for _, field := range fields {
		if strings.TrimSpace(field.Key) == "" {
			continue
		}
		name := http.CanonicalHeaderKey(strings.TrimSpace(field.Key))
		if !validHTTPHeaderName(name) {
			return nil, fmt.Errorf("nome de header inválido: %s", field.Key)
		}
		if blockedHTTPHeader(name) {
			return nil, fmt.Errorf("header não permitido: %s", name)
		}
		if len(field.Value) > 8192 || strings.ContainsAny(field.Value, "\r\n") {
			return nil, fmt.Errorf("valor inválido para o header %s", name)
		}
		if _, exists := headers[name]; exists {
			return nil, fmt.Errorf("header duplicado: %s", name)
		}
		totalBytes += len(name) + len(field.Value)
		if totalBytes > maxHTTPHeaderBytes {
			return nil, fmt.Errorf("headers excedem o limite de %d bytes", maxHTTPHeaderBytes)
		}
		headers[name] = field.Value
	}
	return headers, nil
}

func validHTTPHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for index := 0; index < len(name); index++ {
		character := name[index]
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(character)) {
			continue
		}
		return false
	}
	return true
}

func blockedHTTPHeader(name string) bool {
	lower := strings.ToLower(name)
	switch lower {
	case "host", "connection", "content-length", "keep-alive", "proxy-authenticate", "proxy-authorization",
		"proxy-connection", "te", "trailer", "transfer-encoding", "upgrade", "via", "forwarded",
		"x-real-ip", "x-original-url", "x-rewrite-url", "x-http-method-override", "cf-connecting-ip", "true-client-ip":
		return true
	}
	return strings.HasPrefix(lower, "x-forwarded-")
}

func httpRequestPayload(data map[string]interface{}, ctx ExecutionContext, method string) (interface{}, []byte, string, error) {
	if method == "GET" || method == "HEAD" {
		return nil, nil, "", nil
	}
	mode := strings.TrimSpace(stringVal(data, "bodyMode"))
	if mode == "" {
		mode = "rawJSON"
	}
	switch mode {
	case "none":
		return nil, nil, "", nil
	case "json":
		return keyValueJSON(data["bodyFields"], ctx), nil, "application/json", nil
	case "form":
		values := url.Values{}
		for _, field := range keyValueFields(data["bodyFields"], ctx) {
			if field.Key != "" {
				values.Add(field.Key, field.Value)
			}
		}
		return nil, []byte(values.Encode()), "application/x-www-form-urlencoded", nil
	case "rawJSON":
		body := strings.TrimSpace(ReplaceVariables(stringVal(data, "body"), ctx))
		if body == "" {
			return nil, nil, "", nil
		}
		var parsed interface{}
		if err := json.Unmarshal([]byte(body), &parsed); err != nil {
			return nil, nil, "", fmt.Errorf("corpo JSON inválido: %w", err)
		}
		return parsed, nil, "application/json", nil
	default:
		return nil, nil, "", fmt.Errorf("modo de corpo HTTP inválido: %s", mode)
	}
}

type httpKeyValue struct {
	Key   string
	Value string
}

func keyValueFields(raw interface{}, ctx ExecutionContext) []httpKeyValue {
	items, ok := raw.([]interface{})
	if !ok {
		if typed, typedOK := raw.([]map[string]interface{}); typedOK {
			items = make([]interface{}, len(typed))
			for index := range typed {
				items[index] = typed[index]
			}
		}
	}
	fields := make([]httpKeyValue, 0, len(items))
	for _, item := range items {
		field, _ := item.(map[string]interface{})
		key := strings.TrimSpace(ReplaceVariables(stringVal(field, "key"), ctx))
		value := ReplaceVariables(stringVal(field, "value"), ctx)
		fields = append(fields, httpKeyValue{Key: key, Value: value})
	}
	return fields
}

func keyValueJSON(raw interface{}, ctx ExecutionContext) map[string]interface{} {
	result := map[string]interface{}{}
	for _, field := range keyValueFields(raw, ctx) {
		if field.Key == "" {
			continue
		}
		var typed interface{}
		if json.Unmarshal([]byte(field.Value), &typed) == nil {
			result[field.Key] = typed
		} else {
			result[field.Key] = field.Value
		}
	}
	return result
}

func httpStatusText(code int) string {
	if code >= 200 && code < 300 {
		return "OK"
	}
	if code >= 400 {
		return "Error"
	}
	return ""
}
