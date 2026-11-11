package waha

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

// groupInfo returns whatsmeow's GroupInfo JSON (the same object Evolution GO
// attaches as "groupData"), cached briefly per session and group.
func (b *Bridge) groupInfo(ctx context.Context, session, chatID string) (json.RawMessage, bool) {
	key := session + "|" + chatID
	if cached, ok := b.groups.Load(key); ok {
		entry := cached.(cachedGroup)
		if b.now().Before(entry.expires) {
			return entry.data, true
		}
	}
	response, err := b.Client.Do(ctx, http.MethodGet, "/api/"+url.PathEscape(session)+"/groups/"+url.PathEscape(chatID), nil, nil, "")
	if err != nil || !response.OK() || !json.Valid(response.Body) || len(response.Body) < 2 {
		return nil, false
	}
	data := json.RawMessage(response.Body)
	b.groups.Store(key, cachedGroup{data: data, expires: b.now().Add(groupCacheTTL)})
	return data, true
}

// POST /group/info {GroupJID} -> {data: GroupInfo}
func (b *Bridge) groupInfoEndpoint(ctx context.Context, session string, payload []byte) result {
	var request struct {
		GroupJID string `json:"GroupJID"`
	}
	if err := decode(payload, &request); err != nil {
		return failure(http.StatusBadRequest, err.Error())
	}
	chatID := ChatID(request.GroupJID)
	if !isGroupChat(chatID) {
		return failure(http.StatusBadRequest, "invalid group JID")
	}
	response, err := b.Client.Do(ctx, http.MethodGet, "/api/"+url.PathEscape(session)+"/groups/"+url.PathEscape(chatID), nil, nil, "")
	if err != nil || !response.OK() {
		return fromWAHA(response, err)
	}
	return success(map[string]any{"data": json.RawMessage(response.Body), "message": "success"})
}

type wahaContact struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	PushName string `json:"pushname"`
}

// GET /user/contacts -> {data:[{Jid, Found, FirstName, FullName, PushName}]}
func (b *Bridge) contacts(ctx context.Context, session string) result {
	response, err := b.Client.Do(ctx, http.MethodGet, "/api/contacts/all", url.Values{"session": {session}}, nil, "")
	if err != nil || !response.OK() {
		return fromWAHA(response, err)
	}
	var contacts []wahaContact
	if err := json.Unmarshal(response.Body, &contacts); err != nil {
		return failure(http.StatusBadGateway, "invalid WAHA contacts response")
	}
	data := make([]map[string]any, 0, len(contacts))
	for _, contact := range contacts {
		if contact.ID == "" || isGroupChat(contact.ID) {
			continue
		}
		name := strings.TrimSpace(contact.Name)
		push := strings.TrimSpace(contact.PushName)
		// GOWS may echo the push name as "name" for numbers that are not in
		// the address book; DoveChat only imports address-book names.
		if name == push {
			name = ""
		}
		data = append(data, map[string]any{
			"Jid":          whatsmeowJID(contact.ID),
			"Found":        true,
			"FirstName":    "",
			"FullName":     name,
			"PushName":     push,
			"BusinessName": "",
		})
	}
	return success(map[string]any{"data": data, "message": "success"})
}

// POST /user/check {Number} -> {data:{users:[{Query, IsInWhatsapp, JID, LID, Name}]}}
func (b *Bridge) checkUser(ctx context.Context, session string, payload []byte) result {
	var request struct {
		Number string `json:"Number"`
	}
	if err := decode(payload, &request); err != nil {
		return failure(http.StatusBadRequest, err.Error())
	}
	query := strings.TrimSpace(request.Number)
	phone := digitsOnly(strings.SplitN(query, "@", 2)[0])
	if phone == "" {
		return failure(http.StatusBadRequest, "invalid number")
	}
	response, err := b.Client.Do(ctx, http.MethodGet, "/api/contacts/check-exists", url.Values{"phone": {phone}, "session": {session}}, nil, "")
	if err != nil || !response.OK() {
		return fromWAHA(response, err)
	}
	var exists struct {
		NumberExists bool   `json:"numberExists"`
		ChatID       string `json:"chatId"`
		PN           string `json:"pn"`
	}
	if err := json.Unmarshal(response.Body, &exists); err != nil {
		return failure(http.StatusBadGateway, "invalid WAHA number check response")
	}
	user := map[string]any{"Query": query, "IsInWhatsapp": exists.NumberExists}
	if exists.NumberExists && exists.ChatID != "" {
		jid, lid := whatsmeowJID(exists.ChatID), ""
		if strings.HasSuffix(exists.ChatID, "@lid") {
			lid = exists.ChatID
			jid = ""
			if exists.PN != "" {
				jid = whatsmeowJID(exists.PN)
			}
		}
		user["JID"], user["RemoteJID"], user["LID"] = jid, jid, lid
		if contact, ok := b.contact(ctx, session, exists.ChatID); ok {
			name := strings.TrimSpace(contact.Name)
			if name == "" {
				name = strings.TrimSpace(contact.PushName)
			}
			user["Name"] = name
		}
	}
	return success(map[string]any{"data": map[string]any{"users": []map[string]any{user}}, "message": "success"})
}

func (b *Bridge) contact(ctx context.Context, session, chatID string) (wahaContact, bool) {
	response, err := b.Client.Do(ctx, http.MethodGet, "/api/contacts", url.Values{"contactId": {chatID}, "session": {session}}, nil, "")
	if err != nil || !response.OK() {
		return wahaContact{}, false
	}
	var contact wahaContact
	return contact, json.Unmarshal(response.Body, &contact) == nil
}

// POST /user/avatar {number, preview} -> {data:{url}}. A contact without a
// photo answers like Evolution GO (500 "no profile picture found"), which
// DoveChat classifies as "no avatar" instead of a temporary failure.
func (b *Bridge) avatar(ctx context.Context, session string, payload []byte) result {
	var request struct {
		Number string `json:"number"`
	}
	if err := decode(payload, &request); err != nil {
		return failure(http.StatusBadRequest, err.Error())
	}
	contactID := ChatID(request.Number)
	if contactID == "" || contactID == "@c.us" {
		return failure(http.StatusBadRequest, "invalid phone number")
	}
	response, err := b.Client.Do(ctx, http.MethodGet, "/api/contacts/profile-picture",
		url.Values{"contactId": {contactID}, "session": {session}, "refresh": {"true"}}, nil, "")
	if err != nil {
		return result{err: err}
	}
	if !response.OK() {
		message := errorText(response.Body)
		if strings.Contains(strings.ToLower(message), "does not have a profile picture") {
			message = "no profile picture found"
		}
		return failure(http.StatusInternalServerError, message)
	}
	var picture struct {
		URL string `json:"profilePictureURL"`
	}
	if err := json.Unmarshal(response.Body, &picture); err != nil {
		return failure(http.StatusBadGateway, "invalid WAHA profile picture response")
	}
	if strings.TrimSpace(picture.URL) == "" {
		return failure(http.StatusInternalServerError, "no profile picture found")
	}
	return success(map[string]any{"data": map[string]string{"url": picture.URL}, "message": "success"})
}
