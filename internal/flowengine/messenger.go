package flowengine

import (
	"encoding/json"
	"fmt"
	"sophus/internal/repo"
	"sophus/pkg/http/requests"
	"strings"
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
	FooterText  string        `json:"footerText,omitempty"`
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
	status, _, err := msg.Send(m.Connection.ConnectionKey.String())
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("send text failed with status %d", status)
	}
	return nil
}

func (m *Messenger) SendMedia(conversation repo.Conversation, mediaType, mediaURL, caption string) error {
	contact, err := repo.GetContactById(conversation.Contact.Id)
	if err != nil {
		return err
	}
	payload := map[string]interface{}{
		"number":    contact.Number,
		"mediatype": mediaType,
		"media":     mediaURL,
	}
	if caption != "" {
		payload["caption"] = caption
	}
	if mediaType == "document" {
		parts := strings.Split(mediaURL, "/")
		payload["fileName"] = parts[len(parts)-1]
	}
	r := requests.Request{
		URL:     repo.ApiBaseURL + "/send/media",
		Method:  "POST",
		Payload: payload,
		Headers: map[string]string{
			"Content-Type": "application/json",
			"apikey":       m.Connection.ConnectionKey.String(),
		},
		Response: requests.Response{},
	}
	err = r.Do()
	if err != nil {
		return err
	}
	if r.StatusCode != 200 {
		return fmt.Errorf("send media failed with status %d: %s", r.StatusCode, string(r.Body))
	}
	return nil
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

	r := requests.Request{
		URL:     repo.ApiBaseURL + "/send/list",
		Method:  "POST",
		Payload: payload,
		Headers: map[string]string{
			"Content-Type": "application/json",
			"apikey":       m.Connection.ConnectionKey.String(),
		},
		Response: requests.Response{},
	}
	if err := r.Do(); err != nil {
		return err
	}
	if r.StatusCode != 200 {
		return fmt.Errorf("send menu failed with status %d: %s", r.StatusCode, string(r.Body))
	}
	return nil
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
