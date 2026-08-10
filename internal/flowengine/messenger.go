package flowengine

import (
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"path"
	"sophus/internal/repo"
	"sophus/pkg/http/requests"
	"strings"
	"time"
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
		MessageBaseEVO: repo.MessageBaseEVO{Number: contact.Number},
		Text:           message,
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
	return m.sendMedia(conversation, mediaType, mediaURL, caption)
}

func (m *Messenger) SendAudio(conversation repo.Conversation, mediaURL string) error {
	return m.sendMedia(conversation, "audio", mediaURL, "")
}

func (m *Messenger) sendMedia(conversation repo.Conversation, mediaType, mediaURL, caption string) error {
	contact, err := repo.GetContactById(conversation.Contact.Id)
	if err != nil {
		return err
	}
	payload := buildMediaPayload(contact.Number, mediaType, mediaURL, caption)
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
		return fmt.Errorf("send media failed with status %d: %s", r.StatusCode, string(r.Body))
	}
	text := caption
	if strings.TrimSpace(text) == "" {
		text = fmt.Sprintf("%s enviado pelo fluxo", mediaType)
	}
	return repo.SaveFlowMessage(conversation.Id, evolutionMessageID(r.Body), text)
}

func buildMediaPayload(number, mediaType, mediaURL, caption string) map[string]interface{} {
	payload := map[string]interface{}{
		"number": number,
		"type":   mediaType,
		"url":    mediaURL,
	}
	if caption != "" {
		payload["caption"] = caption
	}
	if mediaType == "document" {
		parsed, err := url.Parse(mediaURL)
		if err == nil {
			payload["filename"] = path.Base(parsed.Path)
		}
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
	url := ReplaceVariables(stringVal(data, "url"), ctx)
	if strings.TrimSpace(url) == "" {
		return map[string]interface{}{
			"error":   true,
			"message": "URL é obrigatória para HTTP Request",
		}
	}
	headers := map[string]string{}
	if rawHeaders, ok := data["headers"].(map[string]interface{}); ok {
		for k, v := range rawHeaders {
			headers[k] = ReplaceVariables(fmt.Sprint(v), ctx)
		}
	}
	body := ReplaceVariables(stringVal(data, "body"), ctx)
	timeout := intVal(data, "timeout")
	if timeout <= 0 {
		timeout = 10000
	}

	var payload interface{}
	if body != "" && (method == "POST" || method == "PUT" || method == "PATCH") {
		var parsed interface{}
		if json.Unmarshal([]byte(body), &parsed) == nil {
			payload = parsed
		} else {
			payload = body
		}
	}

	r := requests.Request{
		URL:      url,
		Method:   method,
		Payload:  payload,
		Headers:  headers,
		Response: requests.Response{},
	}
	if r.Headers == nil {
		r.Headers = map[string]string{}
	}
	if _, ok := r.Headers["Content-Type"]; !ok && payload != nil {
		r.Headers["Content-Type"] = "application/json"
	}

	err := r.Do()
	if err != nil {
		return map[string]interface{}{
			"error":   true,
			"message": err.Error(),
			"status":  r.StatusCode,
		}
	}
	var responseData interface{}
	_ = json.Unmarshal(r.Body, &responseData)
	return map[string]interface{}{
		"status":     r.StatusCode,
		"statusText": httpStatusText(r.StatusCode),
		"data":       responseData,
	}
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
