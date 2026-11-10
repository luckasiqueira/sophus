package waha

import (
	"context"
	"encoding/json"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// sendResponse returns the Evolution GO send response Sophus parses:
// data.Info.ID / data.key.id plus the sent protobuf message.
func (b *Bridge) sendResponse(chatID string, response apiResponse) result {
	var sent struct {
		ID   string          `json:"id"`
		Data json.RawMessage `json:"_data"`
	}
	if err := json.Unmarshal(response.Body, &sent); err != nil || sent.ID == "" {
		return failure(http.StatusBadGateway, "WAHA send response has no message ID")
	}
	messageID := rawMessageID(sent.ID)
	jid := whatsmeowJID(chatID)
	message := sent.Data
	if len(message) == 0 || string(message) == "null" {
		message = json.RawMessage("{}")
	}
	return success(map[string]any{
		"message": "success",
		"data": map[string]any{
			"Info": map[string]any{
				"ID":        messageID,
				"Chat":      jid,
				"IsFromMe":  true,
				"IsGroup":   isGroupChat(chatID),
				"Timestamp": b.now().Format(time.RFC3339Nano),
			},
			"Message": message,
			"key":     map[string]any{"id": messageID, "remoteJid": jid, "fromMe": true},
		},
	})
}

func (b *Bridge) post(ctx context.Context, endpoint string, body any) (apiResponse, error) {
	return b.Client.Do(ctx, http.MethodPost, endpoint, nil, body, "")
}

// mentions converts Evolution's mentionedJid/mentionAll into WAHA mentions.
func (b *Bridge) mentions(ctx context.Context, session, chatID string, jids []string, all bool) []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(value string) {
		if id := ChatID(value); id != "" && id != "@c.us" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, jid := range jids {
		add(jid)
	}
	if all && isGroupChat(chatID) {
		if group, ok := b.groupInfo(ctx, session, chatID); ok {
			var info struct {
				Participants []struct {
					JID         string `json:"JID"`
					PhoneNumber string `json:"PhoneNumber"`
				} `json:"Participants"`
			}
			if json.Unmarshal(group, &info) == nil {
				for _, participant := range info.Participants {
					if participant.PhoneNumber != "" {
						add(participant.PhoneNumber)
					} else {
						add(participant.JID)
					}
				}
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// POST /send/text (repo.TextMessageEVO)
func (b *Bridge) sendText(ctx context.Context, session string, payload []byte) result {
	var request struct {
		Delay        int      `json:"delay"`
		MentionAll   bool     `json:"mentionAll"`
		MentionedJid []string `json:"mentionedJid"`
		Number       string   `json:"number"`
		Text         string   `json:"text"`
		Quoted       struct {
			MessageID string `json:"messageId"`
		} `json:"quoted"`
	}
	if err := decode(payload, &request); err != nil {
		return failure(http.StatusBadRequest, err.Error())
	}
	chatID := ChatID(request.Number)
	if chatID == "" || chatID == "@c.us" || strings.TrimSpace(request.Text) == "" {
		return failure(http.StatusBadRequest, "number and text are required")
	}
	b.typing(ctx, session, chatID, request.Delay)
	body := map[string]any{"session": session, "chatId": chatID, "text": request.Text, "linkPreview": false}
	if quoted := strings.TrimSpace(request.Quoted.MessageID); quoted != "" {
		body["reply_to"] = quoted
	}
	if mentions := b.mentions(ctx, session, chatID, request.MentionedJid, request.MentionAll); mentions != nil {
		body["mentions"] = mentions
	}
	response, err := b.post(ctx, "/api/sendText", body)
	if err != nil || !response.OK() {
		return fromWAHA(response, err)
	}
	return b.sendResponse(chatID, response)
}

var defaultMimeTypes = map[string]string{
	"image":    "image/jpeg",
	"video":    "video/mp4",
	"audio":    "audio/ogg; codecs=opus",
	"document": "application/octet-stream",
}

func mediaMimeType(kind, mediaURL, fileName string) string {
	for _, name := range []string{fileName, mediaURL} {
		if name == "" {
			continue
		}
		if parsed, err := url.Parse(name); err == nil && parsed.Path != "" {
			name = parsed.Path
		}
		if found := mime.TypeByExtension(strings.ToLower(path.Ext(name))); found != "" {
			return found
		}
	}
	return defaultMimeTypes[kind]
}

// POST /send/media {number, type, url, caption, filename, delay}
func (b *Bridge) sendMedia(ctx context.Context, session string, payload []byte) result {
	var request struct {
		Number   string `json:"number"`
		Type     string `json:"type"`
		URL      string `json:"url"`
		Caption  string `json:"caption"`
		Filename string `json:"filename"`
		Delay    int    `json:"delay"`
	}
	if err := decode(payload, &request); err != nil {
		return failure(http.StatusBadRequest, err.Error())
	}
	chatID := ChatID(request.Number)
	if chatID == "" || chatID == "@c.us" || strings.TrimSpace(request.URL) == "" {
		return failure(http.StatusBadRequest, "number and url are required")
	}
	kind := strings.ToLower(strings.TrimSpace(request.Type))
	file := map[string]any{"url": request.URL, "mimetype": mediaMimeType(kind, request.URL, request.Filename)}
	if request.Filename != "" {
		file["filename"] = request.Filename
	}
	body := map[string]any{"session": session, "chatId": chatID, "file": file}
	var endpoint string
	switch kind {
	case "image":
		endpoint = "/api/sendImage"
		body["caption"] = request.Caption
	case "video":
		endpoint = "/api/sendVideo"
		body["caption"] = request.Caption
		body["convert"] = true
	case "audio", "ptt", "voice":
		// Evolution GO delivers audio as a voice note.
		endpoint = "/api/sendVoice"
		body["convert"] = true
	case "document":
		endpoint = "/api/sendFile"
		body["caption"] = request.Caption
	default:
		return failure(http.StatusBadRequest, "unsupported media type: "+request.Type)
	}
	b.typing(ctx, session, chatID, request.Delay)
	response, err := b.post(ctx, endpoint, body)
	if err != nil || !response.OK() {
		return fromWAHA(response, err)
	}
	return b.sendResponse(chatID, response)
}

// POST /send/button. GOWS has no reply buttons, so they are sent as a single
// section list: the selected row ID equals the button ID, which Sophus' flow
// engine already reads from listResponseMessage.
func (b *Bridge) sendButtons(ctx context.Context, session string, payload []byte) result {
	var request struct {
		Number      string `json:"number"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Footer      string `json:"footer"`
		Delay       int    `json:"delay"`
		Buttons     []struct {
			DisplayText string `json:"displayText"`
			ID          string `json:"id"`
		} `json:"buttons"`
	}
	if err := decode(payload, &request); err != nil {
		return failure(http.StatusBadRequest, err.Error())
	}
	chatID := ChatID(request.Number)
	if chatID == "" || chatID == "@c.us" || len(request.Buttons) == 0 {
		return failure(http.StatusBadRequest, "number and buttons are required")
	}
	rows := make([]map[string]string, 0, len(request.Buttons))
	for _, button := range request.Buttons {
		rows = append(rows, map[string]string{"title": button.DisplayText, "rowId": button.ID})
	}
	title := strings.TrimSpace(request.Title)
	if title == "" {
		title = "Opções"
	}
	b.typing(ctx, session, chatID, request.Delay)
	response, err := b.post(ctx, "/api/sendList", map[string]any{
		"session": session,
		"chatId":  chatID,
		"message": map[string]any{
			"title":       title,
			"description": request.Description,
			"footer":      request.Footer,
			"button":      "Ver opções",
			"sections":    []map[string]any{{"title": title, "rows": rows}},
		},
	})
	if err != nil || !response.OK() {
		return fromWAHA(response, err)
	}
	return b.sendResponse(chatID, response)
}

// POST /message/react {number, reaction, id, fromMe, participant}
func (b *Bridge) react(ctx context.Context, session string, payload []byte) result {
	var request struct {
		Number      string `json:"number"`
		Reaction    string `json:"reaction"`
		ID          string `json:"id"`
		FromMe      bool   `json:"fromMe"`
		Participant string `json:"participant"`
	}
	if err := decode(payload, &request); err != nil {
		return failure(http.StatusBadRequest, err.Error())
	}
	if ChatID(request.Number) == "" || strings.TrimSpace(request.ID) == "" {
		return failure(http.StatusBadRequest, "number and id are required")
	}
	participant := ""
	if !request.FromMe {
		participant = request.Participant
	}
	response, err := b.Client.Do(ctx, http.MethodPut, "/api/reaction", nil, map[string]any{
		"session":   session,
		"messageId": serializedMessageID(request.FromMe, request.Number, request.ID, participant),
		"reaction":  request.Reaction,
	}, "")
	if err != nil || !response.OK() {
		return fromWAHA(response, err)
	}
	return success(map[string]any{"message": "success", "data": map[string]string{"id": request.ID}})
}

// POST /message/edit {chat, messageId, message}
func (b *Bridge) edit(ctx context.Context, session string, payload []byte) result {
	var request struct {
		Chat      string `json:"chat"`
		MessageID string `json:"messageId"`
		Message   string `json:"message"`
	}
	if err := decode(payload, &request); err != nil {
		return failure(http.StatusBadRequest, err.Error())
	}
	chatID := ChatID(request.Chat)
	if chatID == "" || strings.TrimSpace(request.MessageID) == "" || request.Message == "" {
		return failure(http.StatusBadRequest, "chat, messageId and message are required")
	}
	endpoint := "/api/" + url.PathEscape(session) + "/chats/" + url.PathEscape(chatID) +
		"/messages/" + url.PathEscape(serializedMessageID(true, chatID, request.MessageID, ""))
	response, err := b.Client.Do(ctx, http.MethodPut, endpoint, nil, map[string]any{
		"text": request.Message, "linkPreview": false,
	}, "")
	if err != nil || !response.OK() {
		return fromWAHA(response, err)
	}
	return success(map[string]any{"message": "success", "data": map[string]string{"messageId": request.MessageID}})
}

// POST /message/markread {number, id: []}
func (b *Bridge) markRead(ctx context.Context, session string, payload []byte) result {
	var request struct {
		Number string   `json:"number"`
		IDs    []string `json:"id"`
	}
	if err := decode(payload, &request); err != nil {
		return failure(http.StatusBadRequest, err.Error())
	}
	chatID := ChatID(request.Number)
	if chatID == "" || len(request.IDs) == 0 {
		return failure(http.StatusBadRequest, "number and id are required")
	}
	ids := make([]string, 0, len(request.IDs))
	for _, id := range request.IDs {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, serializedMessageID(false, chatID, id, ""))
		}
	}
	response, err := b.post(ctx, "/api/sendSeen", map[string]any{"session": session, "chatId": chatID, "messageIds": ids})
	if err != nil || !response.OK() {
		return fromWAHA(response, err)
	}
	return success(map[string]any{"message": "success"})
}
