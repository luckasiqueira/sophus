package repo

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

type MessageDisplayRow struct {
	ID          string `json:"id,omitempty"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
}
type MessageDisplaySection struct {
	Title string              `json:"title"`
	Rows  []MessageDisplayRow `json:"rows"`
}
type MessageDisplay struct {
	Edited                 bool                    `json:"edited,omitempty"`
	Title                  string                  `json:"title,omitempty"`
	Body                   string                  `json:"body,omitempty"`
	Footer                 string                  `json:"footer,omitempty"`
	ButtonText             string                  `json:"buttonText,omitempty"`
	Sections               []MessageDisplaySection `json:"sections,omitempty"`
	Buttons                []MessageDisplayRow     `json:"buttons,omitempty"`
	FileName               string                  `json:"fileName,omitempty"`
	MimeType               string                  `json:"mimeType,omitempty"`
	MediaUnavailableReason string                  `json:"mediaUnavailableReason,omitempty"`
	Animated               bool                    `json:"animated,omitempty"`
	Latitude               *float64                `json:"latitude,omitempty"`
	Longitude              *float64                `json:"longitude,omitempty"`
	Contacts               []MessageDisplayRow     `json:"contacts,omitempty"`
}

type inboundDisplay struct {
	Kind    string
	Display MessageDisplay
	QuoteID string
}

func messageObject(msg EventMessageEVO) (map[string]json.RawMessage, bool) {
	var root map[string]json.RawMessage
	if len(msg.FullJSON) == 0 || json.Unmarshal(msg.FullJSON, &root) != nil {
		return nil, false
	}
	data, ok := rawJSONObject(root, "data")
	if !ok {
		return nil, false
	}
	message, ok := rawJSONObject(data, "message")
	if !ok {
		return nil, false
	}
	for depth := 0; depth < 5; depth++ {
		for _, key := range []string{"viewOnceMessage", "viewOnceMessageV2", "viewOnceMessageV2Extension"} {
			if _, found := rawJSONObject(message, key); found {
				return nil, true
			}
		}
		unwrapped := false
		for _, key := range []string{"ephemeralMessage", "documentWithCaptionMessage", "deviceSentMessage", "associatedChildMessage"} {
			if wrapper, found := rawJSONObject(message, key); found {
				if nested, exists := rawJSONObject(wrapper, "message"); exists {
					message = nested
					unwrapped = true
					break
				}
			}
		}
		if !unwrapped {
			break
		}
	}
	return message, false
}

func jsonObject(raw json.RawMessage) map[string]json.RawMessage {
	var value map[string]json.RawMessage
	_ = json.Unmarshal(raw, &value)
	return value
}
func jsonRows(value map[string]json.RawMessage, key string) []json.RawMessage {
	for actual, raw := range value {
		if strings.EqualFold(actual, key) {
			var rows []json.RawMessage
			_ = json.Unmarshal(raw, &rows)
			return rows
		}
	}
	return nil
}
func firstMessageText(value map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		if text := rawJSONString(value, key); text != "" {
			return text
		}
	}
	return ""
}

func displayList(value map[string]json.RawMessage) MessageDisplay {
	display := MessageDisplay{Title: rawJSONString(value, "title"), Body: firstMessageText(value, "description", "text", "contentText"), Footer: firstMessageText(value, "footerText", "footer"), ButtonText: firstMessageText(value, "buttonText", "button")}
	for index, raw := range jsonRows(value, "sections") {
		if index >= 20 {
			break
		}
		section := jsonObject(raw)
		result := MessageDisplaySection{Title: rawJSONString(section, "title")}
		for rowIndex, rowRaw := range jsonRows(section, "rows") {
			if rowIndex >= 100 {
				break
			}
			row := jsonObject(rowRaw)
			result.Rows = append(result.Rows, MessageDisplayRow{ID: firstMessageText(row, "rowId", "id"), Title: rawJSONString(row, "title"), Description: rawJSONString(row, "description")})
		}
		display.Sections = append(display.Sections, result)
	}
	return display
}

func decodeInboundDisplay(msg EventMessageEVO) inboundDisplay {
	message, viewOnce := messageObject(msg)
	if viewOnce || msg.Data.IsViewOnce {
		return inboundDisplay{Kind: "view_once"}
	}
	if value, ok := rawJSONObject(message, "listMessage"); ok {
		return inboundDisplay{Kind: "list", Display: displayList(value)}
	}
	if value, ok := rawJSONObject(message, "buttonsMessage"); ok {
		display := MessageDisplay{Title: rawJSONString(value, "title"), Body: rawJSONString(value, "contentText"), Footer: rawJSONString(value, "footerText")}
		for index, raw := range jsonRows(value, "buttons") {
			if index >= 100 {
				break
			}
			button := jsonObject(raw)
			text, _ := rawJSONObject(button, "buttonText")
			display.Buttons = append(display.Buttons, MessageDisplayRow{ID: rawJSONString(button, "buttonId"), Title: rawJSONString(text, "displayText")})
		}
		return inboundDisplay{Kind: "buttons", Display: display}
	}
	if value, ok := rawJSONObject(message, "interactiveMessage"); ok {
		header, _ := rawJSONObject(value, "header")
		body, _ := rawJSONObject(value, "body")
		footer, _ := rawJSONObject(value, "footer")
		display := MessageDisplay{Title: rawJSONString(header, "title"), Body: rawJSONString(body, "text"), Footer: rawJSONString(footer, "text")}
		native, _ := rawJSONObject(value, "nativeFlowMessage")
		for index, raw := range jsonRows(native, "buttons") {
			if index >= 100 {
				break
			}
			button := jsonObject(raw)
			params := jsonObject(json.RawMessage(rawJSONString(button, "buttonParamsJson")))
			if rawJSONString(button, "name") == "single_select" {
				list := displayList(params)
				display.Sections = list.Sections
				display.ButtonText = list.Title
				if display.ButtonText == "" {
					display.ButtonText = list.ButtonText
				}
				return inboundDisplay{Kind: "list", Display: display}
			}
			display.Buttons = append(display.Buttons, MessageDisplayRow{ID: rawJSONString(params, "id"), Title: firstMessageText(params, "display_text", "title", "text")})
		}
		return inboundDisplay{Kind: "buttons", Display: display}
	}
	if value, ok := rawJSONObject(message, "templateMessage"); ok {
		for _, key := range []string{"hydratedTemplate", "hydratedFourRowTemplate"} {
			if nested, found := rawJSONObject(value, key); found {
				value = nested
				break
			}
		}
		display := MessageDisplay{Title: rawJSONString(value, "title"), Body: rawJSONString(value, "hydratedContentText"), Footer: rawJSONString(value, "hydratedFooterText")}
		for index, raw := range jsonRows(value, "hydratedButtons") {
			if index >= 100 {
				break
			}
			button := jsonObject(raw)
			for _, key := range []string{"quickReplyButton", "urlButton", "callButton"} {
				if nested, ok := rawJSONObject(button, key); ok {
					display.Buttons = append(display.Buttons, MessageDisplayRow{Title: rawJSONString(nested, "displayText"), ID: rawJSONString(nested, "id")})
				}
			}
		}
		return inboundDisplay{Kind: "buttons", Display: display}
	}
	for _, key := range []string{"pollCreationMessage", "pollCreationMessageV2", "pollCreationMessageV3"} {
		if value, ok := rawJSONObject(message, key); ok {
			display := MessageDisplay{Title: rawJSONString(value, "name")}
			for index, raw := range jsonRows(value, "options") {
				if index >= 100 {
					break
				}
				display.Buttons = append(display.Buttons, MessageDisplayRow{Title: rawJSONString(jsonObject(raw), "optionName")})
			}
			return inboundDisplay{Kind: "poll", Display: display}
		}
	}
	for _, key := range []string{"locationMessage", "liveLocationMessage"} {
		if value, ok := rawJSONObject(message, key); ok {
			display := MessageDisplay{Title: rawJSONString(value, "name"), Body: rawJSONString(value, "address")}
			_ = json.Unmarshal(value["degreesLatitude"], &display.Latitude)
			_ = json.Unmarshal(value["degreesLongitude"], &display.Longitude)
			return inboundDisplay{Kind: "location", Display: display}
		}
	}
	if value, ok := rawJSONObject(message, "contactMessage"); ok {
		return inboundDisplay{Kind: "contact", Display: MessageDisplay{Contacts: []MessageDisplayRow{displayContact(value)}}}
	}
	if value, ok := rawJSONObject(message, "contactsArrayMessage"); ok {
		display := MessageDisplay{}
		for index, raw := range jsonRows(value, "contacts") {
			if index >= 100 {
				break
			}
			display.Contacts = append(display.Contacts, displayContact(jsonObject(raw)))
		}
		return inboundDisplay{Kind: "contact", Display: display}
	}
	for _, key := range []string{"buttonsResponseMessage", "listResponseMessage", "templateButtonReplyMessage", "interactiveResponseMessage"} {
		if value, ok := rawJSONObject(message, key); ok {
			return inboundDisplay{Kind: "interactive", Display: MessageDisplay{Body: rawInteractiveResponse(value)}}
		}
	}
	for _, item := range []struct{ key, kind string }{{"stickerMessage", "sticker"}, {"documentMessage", "document"}, {"imageMessage", "image"}, {"videoMessage", "video"}, {"audioMessage", "audio"}} {
		if value, ok := rawJSONObject(message, item.key); ok {
			display := MessageDisplay{MimeType: rawJSONString(value, "mimetype"), FileName: firstMessageText(value, "fileName", "title"), Body: rawJSONString(value, "caption")}
			_ = json.Unmarshal(value["isAnimated"], &display.Animated)
			context, _ := rawJSONObject(value, "contextInfo")
			return inboundDisplay{Kind: item.kind, Display: display, QuoteID: rawJSONString(context, "stanzaID")}
		}
	}
	if msg.Data.Message.DOC.MimeType != "" || msg.Data.Message.DOC.URL != "" || msg.Data.Message.DOC.FileName != "" {
		return inboundDisplay{Kind: "document", Display: MessageDisplay{MimeType: msg.Data.Message.DOC.MimeType, FileName: msg.Data.Message.DOC.FileName, Body: msg.Data.Message.DOC.Caption}}
	}
	if msg.Data.Message.STK.MimeType != "" || msg.Data.Message.STK.URL != "" {
		return inboundDisplay{Kind: "sticker", Display: MessageDisplay{MimeType: msg.Data.Message.STK.MimeType, Animated: msg.Data.Message.STK.IsAnimated}}
	}
	if text := firstMessageText(message, "conversation", "text"); text != "" {
		return inboundDisplay{Kind: "text", Display: MessageDisplay{Body: text}}
	}
	if value, ok := rawJSONObject(message, "extendedTextMessage"); ok {
		return inboundDisplay{Kind: "text", Display: MessageDisplay{Body: rawJSONString(value, "text")}, QuoteID: rawDisplayQuoteID(value)}
	}
	for _, kind := range []string{"sticker", "document", "image", "video", "audio"} {
		if strings.EqualFold(msg.Data.Info.Mediatype, kind) || strings.EqualFold(msg.Data.Info.Mediatype, kind+"Message") {
			return inboundDisplay{Kind: kind}
		}
	}
	for key := range message {
		if key != "conversation" && key != "text" && key != "extendedTextMessage" && key != "base64" && strings.HasSuffix(key, "Message") {
			return inboundDisplay{Kind: "unknown", Display: MessageDisplay{Title: key}}
		}
	}
	return inboundDisplay{}
}

func rawDisplayQuoteID(value map[string]json.RawMessage) string {
	context, _ := rawJSONObject(value, "contextInfo")
	return firstMessageText(context, "stanzaID", "quotedMessageId")
}

func receivedDisplayQuoteID(msg EventMessageEVO) string {
	message, viewOnce := messageObject(msg)
	if viewOnce {
		return ""
	}
	for _, raw := range message {
		if id := rawDisplayQuoteID(jsonObject(raw)); id != "" {
			return id
		}
	}
	return ""
}

func displayContact(value map[string]json.RawMessage) MessageDisplayRow {
	contact := MessageDisplayRow{Title: rawJSONString(value, "displayName")}
	for _, line := range strings.Split(rawJSONString(value, "vcard"), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToUpper(line), "FN:") && contact.Title == "" {
			contact.Title = strings.TrimPrefix(line, "FN:")
		}
		if strings.HasPrefix(strings.ToUpper(line), "TEL") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				contact.Description = parts[1]
				break
			}
		}
	}
	return contact
}

func (display inboundDisplay) summary() string {
	parts := []string{}
	for _, text := range []string{display.Display.Title, display.Display.Body} {
		if text != "" {
			parts = append(parts, text)
		}
	}
	for _, section := range display.Display.Sections {
		if section.Title != "" {
			parts = append(parts, section.Title)
		}
		for _, row := range section.Rows {
			parts = append(parts, strings.TrimSpace(row.Title+" "+row.Description))
		}
	}
	for _, button := range display.Display.Buttons {
		if button.Title != "" {
			parts = append(parts, button.Title)
		}
	}
	for _, contact := range display.Display.Contacts {
		parts = append(parts, strings.TrimSpace(contact.Title+" "+contact.Description))
	}
	if display.Kind == "location" && display.Display.Latitude != nil && display.Display.Longitude != nil {
		parts = append(parts, fmt.Sprintf("Localização: %.6f, %.6f", *display.Display.Latitude, *display.Display.Longitude))
	}
	if display.Display.Footer != "" {
		parts = append(parts, display.Display.Footer)
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func receivedMediaExtension(mimeType, fileName string) string {
	known := map[string]string{"image/jpeg": "jpg", "image/png": "png", "image/gif": "gif", "image/webp": "webp", "video/mp4": "mp4", "video/webm": "webm", "video/quicktime": "mov", "audio/webm": "weba", "audio/ogg": "ogg", "audio/mpeg": "mp3", "audio/mp4": "m4a", "audio/wav": "wav", "application/pdf": "pdf", "text/plain": "txt", "text/csv": "csv", "application/zip": "zip"}
	if extension := known[strings.ToLower(strings.TrimSpace(mimeType))]; extension != "" {
		return extension
	}
	extension := strings.TrimPrefix(strings.ToLower(filepath.Ext(fileName)), ".")
	if len(extension) > 0 && len(extension) <= 10 {
		safe := true
		for _, r := range extension {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
				safe = false
			}
		}
		if safe {
			return extension
		}
	}
	return "bin"
}
