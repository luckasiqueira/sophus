package repo

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sophus/pkg/http/requests"
	"sophus/utils"
	"sophus/utils/env"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

func SaveEvoMessage(msg Message, connection ConnectionEVO) error {
	return msg.Save(connection)
}

func SaveFlowMessage(conversationID int, messageID, text string, companyIDs ...int) error {
	if messageID == "" {
		messageID = "flow-" + uuid.NewString()
	}
	now := time.Now()
	_, err := db.Exec(`INSERT INTO public.messages
		("messageId", text, "conversationId", "quotedId", "mediaType", "mediaPath",
		 "createdAt", "updatedAt", "isFromMe", "isGroup", "isRead", "isDeleted")
		VALUES ($1, $2, $3, '', 'flow', '', $4, $4, true, false, true, false)`,
		messageID, text, conversationID, now)
	if err == nil {
		scheduleConversationHistoryUpdate(conversationID, messageID, companyIDs...)
		if len(companyIDs) > 0 {
			markOutgoingConversationRead(conversationID, companyIDs[0])
		}
	}
	return err
}

func ConversationMessageCount(conversationID int) (int, error) {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM messages WHERE "conversationId" = $1`, conversationID).Scan(&count)
	return count, err
}

func MessageExists(messageID string, companyID int, connectionIDs ...int) (bool, error) {
	if messageID == "" {
		return false, nil
	}
	var exists bool
	var connectionID any
	if len(connectionIDs) > 0 {
		connectionID = connectionIDs[0]
	}
	err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM messages message
		JOIN conversations conversation ON conversation.id = message."conversationId"
		JOIN connections connection ON connection.id = conversation."connectionId"
		WHERE message."messageId" = $1 AND connection."companyId" = $2 AND ($3::integer IS NULL OR connection.id=$3))`, messageID, companyID, connectionID).Scan(&exists)
	return exists, err
}

func SaveOutgoingMediaMessage(conversationID int, messageID, text, mediaType, mediaScope, mediaPath string, companyIDs ...int) (string, error) {
	if messageID == "" {
		messageID = "media-" + uuid.NewString()
	}
	now := time.Now()
	_, err := db.Exec(`INSERT INTO public.messages
		("messageId", text, "conversationId", "quotedId", "mediaType", "mediaPath", "mediaScope",
		 "createdAt", "updatedAt", "isFromMe", "isGroup", "isRead", "isDeleted")
		SELECT $1, $2, conversation.id, '', $4, $5, $6, $7, $7, true,
			COALESCE(contact."isGroup", false), true, false
		FROM conversations conversation
		JOIN contacts contact ON contact.id = conversation."contactId"
		WHERE conversation.id = $3`,
		messageID, text, conversationID, mediaType, mediaPath, mediaScope, now)
	if err == nil {
		scheduleConversationHistoryUpdate(conversationID, messageID, companyIDs...)
		if len(companyIDs) > 0 {
			markOutgoingConversationRead(conversationID, companyIDs[0])
		}
	}
	return messageID, err
}

func (msg TextMessageEVO) Save(connection ConnectionEVO) error {
	m := EventMessageEVO{}
	if err := json.Unmarshal(msg.JSON, &m); err != nil {
		return err
	}
	m.Data.Info.IsFromMe = true
	m.ReopenClosedConversation = true
	m.Data.Message.TXT.ContextInfo.QuotedMessageID = msg.MessageID
	if m.Data.Info.IsGroup || strings.HasSuffix(strings.ToLower(msg.Number), "@g.us") {
		if m.Data.Info.Chat == "" {
			m.Data.Info.Chat = msg.Number
		}
		m.Data.Info.IsGroup = true
		m.FullJSON = msg.JSON
		return m.Save(connection)
	}
	number, jid, _ := canonicalWhatsAppIdentity(msg.Number)
	if number == "" {
		return errors.New("número de destino inválido")
	}
	contact := Contact{
		Name:      "Contato salvo automaticamente",
		Number:    number,
		CompanyId: connection.CompanyID,
		JID:       jid,
	}
	if name, lookupErr := GetWhatsAppContactName(number, connection.EvolutionAPIKey()); lookupErr == nil && name != "" {
		contact.Name = name
	}
	return saveMessageEvo(m, msg.JSON, contact, connection)
}

// SaveChatTextMessage reuses the contact and conversation authorized before
// sending. It never performs a WhatsApp contact lookup on the response path.
func SaveChatTextMessage(msg TextMessageEVO, connection ConnectionEVO, conversation Conversation) (string, error) {
	if conversation.Id <= 0 || conversation.ConnectionID != connection.Id || conversation.Contact.CompanyId != connection.CompanyID || conversation.Contact.Id <= 0 {
		return "", errors.New("invalid outgoing conversation context")
	}
	var event EventMessageEVO
	if err := json.Unmarshal(msg.JSON, &event); err != nil {
		return "", err
	}
	event.Data.Info.IsFromMe = true
	NormalizeMessageDirection(&event, connection)
	if event.Data.Info.ID == "" {
		return "", errors.New("send response has no message ID")
	}
	event.Data.Info.IsGroup = conversation.Contact.IsGroup
	if event.Data.Info.Timestamp == "" {
		event.Data.Info.Timestamp = time.Now().Format(time.RFC3339Nano)
	}
	event.Data.Message.TXT.Text = msg.Text
	event.Data.Message.TXT.ContextInfo.QuotedMessageID = msg.MessageID
	event.ReopenClosedConversation = true
	if err := saveMessageEvo(event, msg.JSON, conversation.Contact, connection, conversation); err != nil {
		return "", err
	}
	ScheduleContactNameRefresh(conversation.Contact, connection)
	return event.Data.Info.ID, nil
}

func (msg EventMessageEVO) Save(connection ConnectionEVO) error {
	NormalizeWebhookMessage(&msg, connection)
	var contact Contact
	var err error
	if msg.Data.Info.IsGroup {
		contact, err = groupContactFromWebhook(msg, connection.CompanyID)
		if err != nil {
			return err
		}
	} else {
		identifiers := []string{msg.Data.Info.Sender, msg.Data.Info.SenderAlt}
		name := strings.TrimSpace(msg.Data.Info.PushName)
		if msg.Data.Info.IsFromMe {
			identifiers = []string{deviceMessageDestination(msg), msg.Data.Info.RecipientAlt, msg.Data.Info.Chat}
			name = "Contato salvo automaticamente"
		}
		number, jid, lid := canonicalWhatsAppIdentity(identifiers...)
		if number == "" && lid == "" {
			return fmt.Errorf("%w de contato canônica", ErrMessageWithoutIdentity)
		}
		if name == "" {
			name, _ = GetWhatsAppContactName(number, connection.EvolutionAPIKey())
		}
		if name == "" {
			name = "Contato salvo automaticamente"
		}
		contact = Contact{
			Name:      name,
			Number:    number,
			JID:       jid,
			LID:       lid,
			CompanyId: connection.CompanyID,
			IsGroup:   msg.Data.Info.IsGroup,
		}
	}
	return saveMessageEvo(msg, msg.FullJSON, contact, connection)
}

func NormalizeMessageDirection(msg *EventMessageEVO, connection ConnectionEVO) {
	msg.Data.Info.IsFromMe = messageIsFromConnection(*msg, connection)
	if msg.Data.Info.ID == "" {
		msg.Data.Info.ID = msg.Data.Key.ID
		if msg.Data.Info.ID == "" {
			msg.Data.Info.ID = msg.Data.Message.Key.ID
		}
	}
	if msg.Data.Info.IsFromMe {
		if destination := deviceMessageDestination(*msg); destination != "" {
			msg.Data.Info.Chat = destination
		}
	}
	if msg.Data.Info.Chat == "" {
		msg.Data.Info.Chat = msg.Data.Key.RemoteJID
		if msg.Data.Info.Chat == "" {
			msg.Data.Info.Chat = msg.Data.Message.Key.RemoteJID
		}
	}
	if strings.HasSuffix(strings.ToLower(msg.Data.Info.Chat), "@g.us") {
		msg.Data.Info.IsGroup = true
	}
}

func messageIsFromConnection(msg EventMessageEVO, connection ConnectionEVO) bool {
	if msg.Data.Info.IsFromMe || msg.Data.Key.FromMe || msg.Data.Message.Key.FromMe || deviceMessageDestination(msg) != "" {
		return true
	}
	senderNumber, _, _ := canonicalWhatsAppIdentity(msg.Data.Info.Sender, msg.Data.Info.SenderAlt)
	connectionNumber, _, _ := canonicalWhatsAppIdentity(connection.Number)
	return senderNumber != "" && senderNumber == connectionNumber
}

func canonicalWhatsAppIdentity(values ...string) (number, jid, lid string) {
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		parts := strings.SplitN(value, "@", 2)
		local := strings.SplitN(parts[0], ":", 2)[0]
		if len(parts) == 1 {
			local = phoneDigits(local)
		}
		if !decimalDigits(local) {
			continue
		}
		if len(parts) == 1 {
			if number == "" {
				number, jid = local, local+"@s.whatsapp.net"
			}
			continue
		}
		switch parts[1] {
		case "s.whatsapp.net":
			if number == "" {
				number, jid = local, local+"@s.whatsapp.net"
			}
		case "lid":
			if lid == "" {
				lid = local + "@lid"
			}
		}
	}
	return number, jid, lid
}

func WhatsAppParticipant(values ...string) string {
	_, jid, lid := canonicalWhatsAppIdentity(values...)
	if jid != "" {
		return jid
	}
	return lid
}

func phoneDigits(value string) string {
	var digits strings.Builder
	for _, character := range value {
		if character >= '0' && character <= '9' {
			digits.WriteRune(character)
		}
	}
	return digits.String()
}

func decimalDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func (msg TextMessageEVO) Send(connectionKey string) (int, []byte, error) {
	if msg.Participant == "" {
		msg.Participant = msg.MessageParticipant
	}
	if msg.MessageParticipant == "" {
		msg.MessageParticipant = msg.Participant
	}
	r := requests.Request{
		URL:     ApiBaseURL + `/send/text`,
		Method:  "POST",
		Payload: msg,
		Headers: map[string]string{
			"Content-Type": "application/json",
			"apikey":       connectionKey,
		},
		Response: requests.Response{},
	}
	err := r.Do()
	return r.StatusCode, r.Response.Body, err
}

func (msg ReactionMessageEVO) Send(connectionKey string) (int, []byte, error) {
	r := requests.Request{
		URL:     ApiBaseURL + `/message/react`,
		Method:  "POST",
		Payload: msg,
		Headers: map[string]string{
			"Content-Type": "application/json",
			"apikey":       connectionKey,
		},
		Response: requests.Response{},
	}
	err := r.Do()
	return r.StatusCode, r.Response.Body, err
}

func saveMessageEvo(msg EventMessageEVO, fullJson []byte, contact Contact, connection ConnectionEVO, loadedConversations ...Conversation) error {
	if len(msg.FullJSON) == 0 {
		msg.FullJSON = fullJson
	}
	if msg.Data.Info.ID != "" {
		var exists bool
		if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM messages m
			JOIN conversations cv ON cv.id = m."conversationId"
			JOIN connections co ON co.id = cv."connectionId"
			WHERE m."messageId" = $1 AND co."companyId" = $2 AND co.id=$3)`, msg.Data.Info.ID, connection.CompanyID, connection.Id).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return nil
		}
	}
	var conversationId int
	var err error
	if len(loadedConversations) > 0 && loadedConversations[0].Status != ConversationStatusClosed && loadedConversations[0].Status != "resolved" {
		conversationId = loadedConversations[0].Id
	} else {
		conversationId, err = setConversation(connection.Id, contact, !msg.Data.Info.IsFromMe || msg.ReopenClosedConversation, msg.PreventConversationCreation)
		if err != nil {
			return err
		}
	}
	query := `INSERT INTO public.messages (id, "messageId", text, "conversationId", "quotedId", "mediaType", "mediaPath", "createdAt", "updatedAt",
	 "isFromMe", "isGroup", "isRead", "isDeleted", sender, "senderName", "displayData", "mediaSource", "whatsappReadSynced", "editSource") VALUES (DEFAULT, $1::text, $2, $3, $4, $5, $6, $7, $8, $9, $10,
	 $11 OR EXISTS(SELECT 1 FROM message_read_receipts WHERE "connectionId"=$17::integer AND "messageId"=$1::text), $12, $13, $14, $15::jsonb, $16::jsonb,
	 EXISTS(SELECT 1 FROM message_read_receipts WHERE "connectionId"=$17::integer AND "messageId"=$1::text), $18::jsonb);`
	display := decodeInboundDisplay(msg)
	displayJSON, err := json.Marshal(display.Display)
	if err != nil {
		return err
	}
	msgType := CheckMessageType(msg)
	if msg.Data.IsViewOnce {
		msgType = "view_once"
	}
	text := CheckMessageText(msg)
	if msg.Data.Message.Base64 != "" && msgType != "view_once" {
		msg.MediaPath, err = saveMessageMedia(msg, connection.CompanyID, msgType)
		if err != nil {
			return err
		}
	}
	err = insert(query,
		msg.Data.Info.ID,
		text,
		conversationId,
		QuotedMessageID(msg),
		msgType,
		msg.MediaPath,
		msg.Data.Info.Timestamp,
		msg.Data.Info.Timestamp,
		msg.Data.Info.IsFromMe,
		msg.Data.Info.IsGroup,
		msg.Data.Info.IsFromMe,
		false,
		msg.Data.Info.Sender,
		strings.TrimSpace(msg.Data.Info.PushName), string(displayJSON), string(webhookMediaSource(msg)), connection.Id, string(messageEditSource(msg, connection)))
	if err == nil {
		scheduleConversationHistoryUpdate(conversationId, msg.Data.Info.ID, connection.CompanyID)
		if msg.Data.Info.IsFromMe {
			markOutgoingConversationRead(conversationId, connection.CompanyID)
		}
	}
	return err
}

func MarkConversationRead(conversationID, companyID int) (int64, error) {
	result, err := db.Exec(`UPDATE messages message SET "isRead" = true
		FROM conversations conversation
		JOIN connections connection ON connection.id = conversation."connectionId"
		WHERE message."conversationId" = conversation.id
		AND conversation.id = $1 AND connection."companyId" = $2
		AND message."isFromMe" = false AND message."isRead" = false AND message."isDeleted" = false`,
		conversationID, companyID)
	if err != nil {
		return 0, err
	}
	marked, err := result.RowsAffected()
	if err == nil {
		ScheduleWhatsAppReadSync(conversationID, companyID)
	}
	return marked, err
}

func QuotedMessageID(msg EventMessageEVO) string {
	for _, messageID := range []string{
		msg.Data.Message.TXT.ContextInfo.QuotedMessageID,
		msg.Data.Message.IMG.ContextInfo.QuotedMessageID,
		msg.Data.Message.VID.ContextInfo.QuotedMessageID,
		msg.Data.Message.AUD.ContextInfo.QuotedMessageID,
		msg.Data.Message.DOC.ContextInfo.QuotedMessageID,
		msg.Data.Message.STK.ContextInfo.QuotedMessageID,
		decodeInboundDisplay(msg).QuoteID,
		receivedDisplayQuoteID(msg),
	} {
		if messageID != "" {
			return messageID
		}
	}
	return ""
}

func CheckMessageText(msg EventMessageEVO) string {
	text := msg.Data.Message.Text
	if msg.Data.Message.TXT.Text != "" {
		text = msg.Data.Message.TXT.Text // text = m.Body.Data.Message.ExtendedTextMessage.ContextInfo.QuotedMessage.Text
	}
	if msg.Data.Message.Conversation != "" {
		text = msg.Data.Message.Conversation
	}
	if msg.Data.Message.IMG.Caption != "" {
		text = msg.Data.Message.IMG.Caption
	}
	if msg.Data.Message.VID.Caption != "" {
		text = msg.Data.Message.VID.Caption
	}
	if msg.Data.Message.DOC.Caption != "" {
		text = msg.Data.Message.DOC.Caption
	}
	if strings.TrimSpace(text) == "" {
		text, _ = rawFlowMessageData(msg.FullJSON)
	}
	if strings.TrimSpace(text) == "" {
		text = decodeInboundDisplay(msg).summary()
	}
	return text
}

func CheckMessageType(msg EventMessageEVO) string {
	if kind := decodeInboundDisplay(msg).Kind; kind != "" {
		return kind
	}
	if msg.Data.Message.IMG.MimeType != "" {
		return "image"
	}
	if msg.Data.Message.VID.MimeType != "" {
		return "video"
	}
	if msg.Data.Message.AUD.MimeType != "" {
		return "audio"
	}
	return "text"
}

func CheckFlowMessageType(msg EventMessageEVO) string {
	mediaType := strings.ToLower(strings.TrimSpace(msg.Data.Info.Mediatype))
	for _, kind := range []string{"image", "video", "audio", "document"} {
		if strings.Contains(mediaType, kind) {
			return kind
		}
	}
	if messageType := CheckMessageType(msg); messageType != "text" {
		return messageType
	}
	_, messageType := rawFlowMessageData(msg.FullJSON)
	if messageType != "" {
		return messageType
	}
	return "text"
}

type FlowInboundMessage struct {
	Text string
	Type string
}

func NormalizeFlowInboundMessage(msg EventMessageEVO) FlowInboundMessage {
	return FlowInboundMessage{
		Text: CheckMessageText(msg),
		Type: CheckFlowMessageType(msg),
	}
}

func rawFlowMessageData(payload []byte) (string, string) {
	if len(payload) == 0 {
		return "", ""
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(payload, &root) != nil {
		return "", ""
	}
	data, ok := rawJSONObject(root, "data")
	if !ok {
		return "", ""
	}
	message, ok := rawJSONObject(data, "message")
	if !ok {
		return "", ""
	}
	for _, key := range []string{"text", "conversation"} {
		if value := rawJSONString(message, key); value != "" {
			return value, "text"
		}
	}
	for _, item := range []struct {
		key  string
		kind string
	}{
		{"extendedTextMessage", "text"},
		{"imageMessage", "image"},
		{"videoMessage", "video"},
		{"audioMessage", "audio"},
		{"documentMessage", "document"},
	} {
		if nested, exists := rawJSONObject(message, item.key); exists {
			for _, textKey := range []string{"text", "caption"} {
				if value := rawJSONString(nested, textKey); value != "" {
					return value, item.kind
				}
			}
			return "", item.kind
		}
	}
	for _, key := range []string{"buttonsResponseMessage", "listResponseMessage", "templateButtonReplyMessage", "interactiveResponseMessage"} {
		if nested, exists := rawJSONObject(message, key); exists {
			if value := rawInteractiveResponse(nested); value != "" {
				return value, "interactive"
			}
			return "", "interactive"
		}
	}
	return "", ""
}

func rawInteractiveResponse(value map[string]json.RawMessage) string {
	for _, key := range []string{"selectedButtonId", "selectedRowId", "selectedId", "id"} {
		if text := rawJSONString(value, key); text != "" {
			return text
		}
	}
	for _, key := range []string{"singleSelectReply", "buttonReply", "nativeFlowResponseMessage"} {
		if nested, exists := rawJSONObject(value, key); exists {
			if text := rawInteractiveResponse(nested); text != "" {
				return text
			}
		}
	}
	return ""
}

func rawJSONObject(values map[string]json.RawMessage, key string) (map[string]json.RawMessage, bool) {
	for actual, raw := range values {
		if !strings.EqualFold(actual, key) {
			continue
		}
		var object map[string]json.RawMessage
		if json.Unmarshal(raw, &object) == nil && object != nil {
			return object, true
		}
	}
	return nil, false
}

func rawJSONString(values map[string]json.RawMessage, key string) string {
	for actual, raw := range values {
		if !strings.EqualFold(actual, key) {
			continue
		}
		var value string
		if json.Unmarshal(raw, &value) == nil {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func saveMessageMedia(msg EventMessageEVO, companyId int, messageType string) (string, error) {
	var mimeType string
	switch messageType {
	case "image":
		mimeType = msg.Data.Message.IMG.MimeType
	case "video":
		mimeType = msg.Data.Message.VID.MimeType
	case "audio":
		mimeType = msg.Data.Message.AUD.MimeType
	case "document":
		mimeType = msg.Data.Message.DOC.MimeType
	case "sticker":
		mimeType = msg.Data.Message.STK.MimeType
	default:
		mimeType = "application/octet-stream"
	}
	if mimeType == "" {
		mimeType = decodeInboundDisplay(msg).Display.MimeType
	}
	if mimeType == "" && strings.HasPrefix(msg.Data.Message.Base64, "data:") {
		header, _, _ := strings.Cut(msg.Data.Message.Base64, ",")
		mimeType = strings.SplitN(strings.TrimPrefix(header, "data:"), ";", 2)[0]
	}
	if mimeType == "" && messageType == "sticker" {
		mimeType = "image/webp"
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	mimeType = strings.SplitN(mimeType, ";", 2)[0]
	format := receivedMediaExtension(mimeType, decodeInboundDisplay(msg).Display.FileName)
	if strings.HasPrefix(msg.Data.Message.Base64, "data:") {
		if _, body, ok := strings.Cut(msg.Data.Message.Base64, ","); ok {
			msg.Data.Message.Base64 = body
		}
	}
	reserved := int64(base64.StdEncoding.DecodedLen(len(msg.Data.Message.Base64)))
	if reserved <= 0 {
		return "", errors.New("mídia vazia")
	}
	if err := ReserveCompanyStorage(companyId, reserved); err != nil {
		return "", err
	}
	completed := false
	defer func() {
		if !completed {
			_ = ReleaseCompanyStorageReservation(companyId, reserved)
		}
	}()
	path := fmt.Sprintf("%s/%d/", env.Backend["MEDIA_DIRECTORY"], companyId)
	fileName := fmt.Sprintf("%s.%s", uuid.NewString(), format)
	if err := utils.MediaDecoder(msg.Data.Message.Base64, path, fileName); err != nil {
		return "", err
	}
	filePath := filepath.Join(path, fileName)
	info, err := os.Stat(filePath)
	if err != nil {
		return "", err
	}
	if err := CompleteCompanyStorageReservation(companyId, reserved, info.Size()); err != nil {
		_ = os.Remove(filePath)
		return "", err
	}
	completed = true
	return fileName, nil
}

func GetMessagesByConversation(conversationID, companyID int) ([]MessageData, error) {
	return getMessagesByConversation(conversationID, companyID, 0, 0)
}

type ConversationMessagePage struct {
	HistoryScope  string
	Conversations []Conversation
	Messages      []MessageData
	HasOlder      bool
	BeforeID      int
	WindowBefore  *time.Time
	WindowAfter   *time.Time
}

func GetConversationMessagePage(conversationID, companyID, beforeID int) (ConversationMessagePage, error) {
	if beforeID > 0 {
		return cachedConversationHistoryPage(conversationID, companyID, beforeID)
	}
	messages, err := getMessagesByConversation(conversationID, companyID, beforeID, 51)
	if err != nil {
		return ConversationMessagePage{}, err
	}
	page := ConversationMessagePage{Messages: messages, HasOlder: len(messages) > 50}
	if page.HasOlder {
		page.Messages = messages[1:]
	}
	if len(page.Messages) > 0 {
		page.BeforeID = page.Messages[0].Id
		if page.HasOlder {
			timestamp := page.Messages[0].CreatedAt
			page.WindowAfter = &timestamp
		}
	}
	return page, nil
}

const conversationMessagePageIDsSQL = `WITH page_ids AS (
	SELECT m.id FROM messages m WHERE m."conversationId" = $1
	AND m."mediaType"<>'system'
	AND ($3::integer = 0 OR (m."createdAt", m.id) < (
		SELECT anchor."createdAt", anchor.id FROM messages anchor WHERE anchor.id = $3 AND anchor."conversationId" = $1))
	ORDER BY m."createdAt" DESC, m.id DESC LIMIT $4
) `

func getMessagesByConversation(conversationID, companyID, beforeID, pageSize int) ([]MessageData, error) {
	return getSelectedConversationMessages(conversationID, companyID, beforeID, pageSize, "")
}

func getSelectedConversationMessages(conversationID, companyID, beforeID, pageSize int, selectionSQL string) ([]MessageData, error) {
	query := `SELECT
	 m.id,
    m."messageId",
    m.text,
	 m."conversationId",
    m."quotedId",
    m."mediaType",
	 m."createdAt",
    m."updatedAt",
    m."isFromMe",
    m."isGroup",
    CASE WHEN m."isFromMe" THEN COALESCE((SELECT receipt.status = 3 FROM message_receipts receipt WHERE receipt."connectionId" = c."connectionId" AND receipt."messageId" = m."messageId"), false) ELSE m."isRead" END,
    m."isDeleted",
	 COALESCE(m."mediaPath", ''),
	 COALESCE(m."mediaScope", 'messages'),
	 COALESCE(m.sender, ''),
	 COALESCE(NULLIF(sender_contact.name, ''), NULLIF(m."senderName", ''), 'Participante'),
	 COALESCE(sender_contact.number, CASE WHEN lower(m.sender) LIKE '%@s.whatsapp.net' THEN split_part(m.sender, '@', 1) ELSE '' END),
	 COALESCE(sender_contact.id, 0),
	 COALESCE(quoted.text, ''),
	 COALESCE(quoted."mediaType", ''),
		COALESCE(quoted."isFromMe", false),
		COALESCE(quoted."senderName", ''),
		COALESCE((SELECT receipt.status FROM message_receipts receipt WHERE receipt."connectionId" = c."connectionId" AND receipt."messageId" = m."messageId"), 1),
		m."displayData"
		FROM messages m
		INNER JOIN public.conversations c ON c.id = m."conversationId"
		INNER JOIN public.contacts conversation_contact ON conversation_contact.id = c."contactId"
		LEFT JOIN LATERAL (
			SELECT quote.text, quote."mediaType", quote."isFromMe",
				COALESCE(NULLIF(quote."senderName", ''), CASE WHEN quote."isFromMe" = false THEN conversation_contact.name ELSE '' END) AS "senderName"
			FROM messages quote
			WHERE quote."conversationId" = m."conversationId" AND quote."messageId" = m."quotedId"
			ORDER BY quote.id DESC LIMIT 1
		) quoted ON true
		LEFT JOIN LATERAL (
			SELECT participant.id, participant.name, participant.number
			FROM contacts participant
			WHERE participant."companyId" = conversation_contact."companyId"
			AND COALESCE(participant."isGroup", false) = false
			AND (participant.jid = m.sender OR participant.lid = m.sender
				OR (participant.number <> '' AND participant.number = split_part(m.sender, '@', 1)))
			ORDER BY CASE WHEN participant.jid = m.sender OR participant.lid = m.sender THEN 0 ELSE 1 END, participant.id
			LIMIT 1
		) sender_contact ON m."isGroup" = true AND m."isFromMe" = false
		WHERE c.id = $1 AND conversation_contact."companyId" = $2 AND m."mediaType"<>'system'
		ORDER BY m."createdAt" ASC, m.id ASC;`
	args := []any{conversationID, companyID}
	if selectionSQL != "" {
		query = strings.Replace(query, `WHERE c.id = $1 AND conversation_contact."companyId" = $2`, `WHERE conversation_contact."companyId" = $2`, 1)
		query = selectionSQL + strings.Replace(query, "FROM messages m", "FROM messages m JOIN page_ids page ON page.id = m.id", 1)
		args = append(args, beforeID, pageSize)
	} else if pageSize > 0 {
		query = conversationMessagePageIDsSQL + strings.Replace(query, "FROM messages m", "FROM messages m JOIN page_ids page ON page.id = m.id", 1)
		args = append(args, beforeID, pageSize)
	}
	rows, err := db.Query(query, args...)
	if err != nil {
		return []MessageData{}, err
	}
	defer rows.Close()
	messages := []MessageData{}
	for rows.Next() {
		msg := MessageData{}
		err = rows.Scan(
			&msg.Id,
			&msg.MessageId,
			&msg.Text,
			&msg.ConversationId,
			&msg.QuotedId,
			&msg.MediaType,
			&msg.CreatedAt,
			&msg.UpdatedAt,
			&msg.IsFromMe,
			&msg.IsGroup,
			&msg.IsRead,
			&msg.IsDeleted,
			&msg.MediaPath,
			&msg.MediaScope,
			&msg.Sender,
			&msg.SenderName,
			&msg.SenderNumber,
			&msg.SenderContactId,
			&msg.QuotedText,
			&msg.QuotedMediaType,
			&msg.QuotedFromMe,
			&msg.QuotedSenderName, &msg.DeliveryStatus, &msg.DisplayData)
		if err != nil {
			if !strings.Contains(err.Error(), "converting NULL to string is unsupported") {
				return []MessageData{}, err
			}
		}
		messages = append(messages, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	conversationIDs := []int{conversationID}
	if selectionSQL != "" {
		conversationIDs = nil
		seen := map[int]bool{}
		for _, message := range messages {
			if !seen[message.ConversationId] {
				seen[message.ConversationId] = true
				conversationIDs = append(conversationIDs, message.ConversationId)
			}
		}
	}
	if err := loadSelectedMessageReactions(messages, conversationIDs, companyID); err != nil {
		return nil, err
	}
	return messages, nil
}

func GetMessageForConversation(messageID string, conversationID, companyID int) (MessageData, error) {
	var msg MessageData
	err := db.QueryRow(`SELECT m.id, m."messageId", m.text, m."conversationId", m."quotedId", m."mediaType",
		m."createdAt", m."updatedAt", m."isFromMe", m."isGroup",
		CASE WHEN m."isFromMe" THEN COALESCE((SELECT receipt.status = 3 FROM message_receipts receipt WHERE receipt."connectionId" = c."connectionId" AND receipt."messageId" = m."messageId"), false) ELSE m."isRead" END, m."isDeleted",
		COALESCE(m."mediaPath", ''), COALESCE(m."mediaScope", 'messages'), COALESCE(m.sender, ''),
		COALESCE(NULLIF(sender_contact.name, ''), NULLIF(m."senderName", ''), 'Participante'),
		COALESCE(sender_contact.number, CASE WHEN lower(m.sender) LIKE '%@s.whatsapp.net' THEN split_part(m.sender, '@', 1) ELSE '' END),
		COALESCE(sender_contact.id, 0), COALESCE(quoted.text, ''),
		COALESCE(quoted."mediaType", ''), COALESCE(quoted."isFromMe", false), COALESCE(quoted."senderName", ''),
		COALESCE((SELECT receipt.status FROM message_receipts receipt WHERE receipt."connectionId" = c."connectionId" AND receipt."messageId" = m."messageId"), 1),m."displayData"
		FROM messages m
		JOIN conversations c ON c.id = m."conversationId"
		JOIN contacts conversation_contact ON conversation_contact.id = c."contactId"
		JOIN connections connection ON connection.id = c."connectionId" AND connection."companyId" = $3
		LEFT JOIN LATERAL (
			SELECT quote.text, quote."mediaType", quote."isFromMe",
				COALESCE(NULLIF(quote."senderName", ''), CASE WHEN quote."isFromMe" = false THEN conversation_contact.name ELSE '' END) AS "senderName" FROM messages quote
			WHERE quote."conversationId" = m."conversationId" AND quote."messageId" = m."quotedId"
			ORDER BY quote.id DESC LIMIT 1
		) quoted ON true
		LEFT JOIN LATERAL (
			SELECT participant.id, participant.name, participant.number
			FROM contacts participant
			WHERE participant."companyId" = connection."companyId"
			AND COALESCE(participant."isGroup", false) = false
			AND (participant.jid = m.sender OR participant.lid = m.sender
				OR (participant.number <> '' AND participant.number = split_part(m.sender, '@', 1)))
			ORDER BY CASE WHEN participant.jid = m.sender OR participant.lid = m.sender THEN 0 ELSE 1 END, participant.id
			LIMIT 1
		) sender_contact ON m."isGroup" = true AND m."isFromMe" = false
		WHERE m."messageId" = $1 AND m."conversationId" = $2
		ORDER BY m.id DESC LIMIT 1`, messageID, conversationID, companyID).Scan(
		&msg.Id, &msg.MessageId, &msg.Text, &msg.ConversationId, &msg.QuotedId, &msg.MediaType,
		&msg.CreatedAt, &msg.UpdatedAt, &msg.IsFromMe, &msg.IsGroup, &msg.IsRead, &msg.IsDeleted,
		&msg.MediaPath, &msg.MediaScope, &msg.Sender, &msg.SenderName, &msg.SenderNumber, &msg.SenderContactId,
		&msg.QuotedText, &msg.QuotedMediaType, &msg.QuotedFromMe, &msg.QuotedSenderName, &msg.DeliveryStatus, &msg.DisplayData)
	if err != nil {
		return msg, err
	}
	rows := []MessageData{msg}
	if err := loadMessageReactions(rows, conversationID, companyID); err != nil {
		return MessageData{}, err
	}
	return rows[0], nil
}

func loadMessageReactions(messages []MessageData, conversationID, companyID int) error {
	return loadSelectedMessageReactions(messages, []int{conversationID}, companyID)
}

func loadSelectedMessageReactions(messages []MessageData, conversationIDs []int, companyID int) error {
	if len(messages) == 0 {
		return nil
	}
	byID := make(map[int]*MessageData, len(messages))
	messageIDs := make([]int, 0, len(messages))
	for index := range messages {
		byID[messages[index].Id] = &messages[index]
		messageIDs = append(messageIDs, messages[index].Id)
	}
	rows, err := db.Query(`SELECT m.id, reaction.emoji, COUNT(*), BOOL_OR(reaction."isFromMe")
		FROM message_reactions reaction
		JOIN messages m ON m.id = reaction."messageId"
		JOIN conversations c ON c.id = m."conversationId"
		JOIN connections connection ON connection.id = c."connectionId"
		WHERE c.id = ANY($1) AND connection."companyId" = $2 AND m.id = ANY($3)
		GROUP BY m.id, reaction.emoji
		ORDER BY MAX(reaction."updatedAt"), reaction.emoji`, pq.Array(conversationIDs), companyID, pq.Array(messageIDs))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var messageID int
		var reaction MessageReaction
		if err := rows.Scan(&messageID, &reaction.Emoji, &reaction.Count, &reaction.Mine); err != nil {
			return err
		}
		if message := byID[messageID]; message != nil {
			message.Reactions = append(message.Reactions, reaction)
		}
	}
	return rows.Err()
}

func OwnMessageReaction(messageID string, conversationID, companyID int) (string, error) {
	var emoji string
	err := db.QueryRow(`SELECT reaction.emoji FROM message_reactions reaction
		JOIN messages m ON m.id = reaction."messageId"
		JOIN conversations c ON c.id = m."conversationId"
		JOIN connections connection ON connection.id = c."connectionId"
		WHERE m."messageId" = $1 AND c.id = $2 AND connection."companyId" = $3
		AND reaction."isFromMe" = true ORDER BY reaction.id DESC LIMIT 1`, messageID, conversationID, companyID).Scan(&emoji)
	return emoji, err
}

func ApplyMessageReaction(messageID string, conversationID, companyID int, sender, emoji, eventID string, fromMe bool) (MessageData, error) {
	message, err := GetMessageForConversation(messageID, conversationID, companyID)
	if err != nil {
		return MessageData{}, err
	}
	if fromMe {
		sender = "self"
	}
	sender = strings.TrimSpace(sender)
	if sender == "" {
		return MessageData{}, errors.New("reaction sender is required")
	}
	if emoji == "" {
		_, err = db.Exec(`DELETE FROM message_reactions WHERE "messageId" = $1 AND sender = $2`, message.Id, sender)
	} else {
		_, err = db.Exec(`INSERT INTO message_reactions ("messageId", sender, emoji, "isFromMe", "eventId", "updatedAt")
			VALUES ($1, $2, $3, $4, $5, now())
			ON CONFLICT ("messageId", sender) DO UPDATE SET emoji = EXCLUDED.emoji,
			"isFromMe" = EXCLUDED."isFromMe", "eventId" = EXCLUDED."eventId", "updatedAt" = now()`,
			message.Id, sender, emoji, fromMe, eventID)
	}
	if err != nil {
		return MessageData{}, err
	}
	scheduleConversationHistoryUpdate(conversationID, messageID, companyID)
	return GetMessageForConversation(messageID, conversationID, companyID)
}

func GetLatestIncomingMedia(conversationID, companyID int) (MessageData, error) {
	var msg MessageData
	err := db.QueryRow(`SELECT m."messageId", m.text, m."quotedId", m."mediaType", m."updatedAt",
		m."isFromMe", m."isGroup", m."isRead", m."isDeleted", m."mediaPath"
		FROM messages m
		JOIN conversations c ON c.id = m."conversationId"
		JOIN connections co ON co.id = c."connectionId"
		WHERE c.id = $1 AND co."companyId" = $2 AND m."isFromMe" = false
		AND m."mediaType" IN ('audio', 'image', 'video', 'document')
		ORDER BY m.id DESC LIMIT 1`, conversationID, companyID).Scan(
		&msg.MessageId, &msg.Text, &msg.QuotedId, &msg.MediaType, &msg.UpdatedAt,
		&msg.IsFromMe, &msg.IsGroup, &msg.IsRead, &msg.IsDeleted, &msg.MediaPath)
	return msg, err
}

func GetIncomingMediaByMessage(messageID string, companyID int) (MessageData, error) {
	var msg MessageData
	err := db.QueryRow(`SELECT m."messageId", m.text, m."quotedId", m."mediaType", m."updatedAt",
		m."isFromMe", m."isGroup", m."isRead", m."isDeleted", m."mediaPath"
		FROM messages m JOIN conversations c ON c.id = m."conversationId"
		JOIN connections co ON co.id = c."connectionId"
		WHERE m."messageId" = $1 AND co."companyId" = $2 AND m."isFromMe" = false
		AND m."mediaType" IN ('audio', 'image', 'video', 'document')
		ORDER BY m.id DESC LIMIT 1`, messageID, companyID).Scan(
		&msg.MessageId, &msg.Text, &msg.QuotedId, &msg.MediaType, &msg.UpdatedAt,
		&msg.IsFromMe, &msg.IsGroup, &msg.IsRead, &msg.IsDeleted, &msg.MediaPath)
	return msg, err
}

func GetMediaPathByMessage(message string, companyID int) (string, error) {
	mediaPath := ""
	stmt, err := db.Prepare(`SELECT message."mediaPath" FROM messages message
		JOIN conversations conversation ON conversation.id = message."conversationId"
		JOIN connections connection ON connection.id = conversation."connectionId"
		WHERE message."messageId" = $1 AND connection."companyId" = $2
		ORDER BY message.id DESC LIMIT 1`)
	if err != nil {
		return mediaPath, err
	}
	defer stmt.Close()
	return mediaPath, stmt.QueryRow(message, companyID).Scan(&mediaPath)
}

//func (msg EventMesageImageEVO) Save(connection ConnectionEVO) error {
//	contact, err := contactHelper(msg.Data.Info.BaseEventMSGInfoEVO.IsGroup, connection.Id, msg.Data.Info.BaseEventMSGInfoEVO, msg.InstanceToken)
//	fullJson, err := json.Marshal(msg.Data)
//	if err != nil {
//		return err
//	}
//	return saveMessageEvo(msg, fullJson, contact, connection.Id)
//}

//func contactHelper(isGroup bool, connectionId int, info BaseEventMSGInfoEVO, connectionKey uuid.UUID) (Contact, error) {
//	var contact Contact
//	var err error
//	if isGroup {
//		contact, err = getGroupInfo(info.Chat, connectionKey.String())
//		if err != nil {
//			return contact, err
//		}
//		contact.ConnectionId = connectionId
//	} else {
//		contact = Contact{
//			Name:         info.PushName,
//			Number:       strings.Split(info.Sender, "@")[0],
//			JID:          info.Sender,
//			LID:          info.SenderAlt,
//			ConnectionId: connectionId,
//			IsGroup:      isGroup,
//		}
//	}
//	return contact, nil
//}
