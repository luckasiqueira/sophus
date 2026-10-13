package repo

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"sophus/pkg/http/requests"

	"golang.org/x/crypto/hkdf"
	"google.golang.org/protobuf/encoding/protowire"
)

var ErrMessageEditUnavailable = errors.New("dados de descriptografia da edição indisponíveis no provedor")

type webhookMessageEdit struct {
	TargetID  string
	Text      string
	HasText   bool
	Timestamp int64
	Encrypted map[string]json.RawMessage
}

// This source is stored only in PostgreSQL, never in transcript DTOs or SSE.
type messageEditSecret struct {
	Secret  []byte   `json:"messageSecret,omitempty"`
	Senders []string `json:"senders,omitempty"`
}

func messageEditSource(msg EventMessageEVO, connection ConnectionEVO) json.RawMessage {
	message, viewOnce := messageObject(msg)
	if viewOnce || msg.Data.IsViewOnce {
		return json.RawMessage(`{}`)
	}
	context, _ := rawJSONObject(message, "messageContextInfo")
	secret, _ := base64.StdEncoding.DecodeString(rawJSONString(context, "messageSecret"))
	if len(secret) != 32 {
		return json.RawMessage(`{}`)
	}
	senders := []string{msg.Data.Info.Sender, msg.Data.Info.SenderAlt}
	if msg.Data.Info.IsFromMe {
		senders = append(senders, WhatsAppParticipant(connection.Number))
	}
	raw, _ := json.Marshal(messageEditSecret{Secret: secret, Senders: senders})
	return raw
}

func editTimestamp(value string) int64 {
	if timestamp, err := strconv.ParseInt(value, 10, 64); err == nil {
		if timestamp < 1e12 {
			return timestamp * 1000
		}
		return timestamp
	}
	if timestamp, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return timestamp.UnixMilli()
	}
	return 0
}

func parseWebhookMessageEdit(msg EventMessageEVO) (webhookMessageEdit, bool) {
	message, viewOnce := messageObject(msg)
	if viewOnce || msg.Data.IsViewOnce {
		return webhookMessageEdit{}, false
	}
	edit := webhookMessageEdit{TargetID: msg.Data.Info.ID, Timestamp: editTimestamp(msg.Data.Info.Timestamp)}
	if encrypted, ok := rawJSONObject(message, "secretEncryptedMessage"); ok {
		kind := rawJSONString(encrypted, "secretEncType")
		if kind == "" {
			kind = string(encrypted["secretEncType"])
		}
		if kind != "MESSAGE_EDIT" && kind != "2" {
			return webhookMessageEdit{}, false
		}
		key, _ := rawJSONObject(encrypted, "targetMessageKey")
		edit.TargetID = rawJSONString(key, "id")
		edit.Encrypted = encrypted
		return edit, true
	}
	if protocol, ok := rawJSONObject(message, "protocolMessage"); ok {
		kind := rawJSONString(protocol, "type")
		if kind == "" {
			kind = string(protocol["type"])
		}
		if kind != "MESSAGE_EDIT" && kind != "14" {
			return webhookMessageEdit{}, false
		}
		key, _ := rawJSONObject(protocol, "key")
		edit.TargetID = rawJSONString(key, "id")
		message, _ = rawJSONObject(protocol, "editedMessage")
		var timestamp json.Number
		if json.Unmarshal(protocol["timestampMS"], &timestamp) == nil {
			if value, err := timestamp.Int64(); err == nil {
				edit.Timestamp = value
			}
		}
	} else if wrapper, ok := rawJSONObject(message, "editedMessage"); ok {
		message, _ = rawJSONObject(wrapper, "message")
	} else if !msg.Data.IsEdit && !msg.Data.Info.IsEdit {
		return webhookMessageEdit{}, false
	}
	edit.Text, edit.HasText = editedJSONText(message)
	return edit, true
}

func editedJSONText(message map[string]json.RawMessage) (string, bool) {
	for depth := 0; depth < 6; depth++ {
		for _, key := range []string{"conversation", "text"} {
			for name, raw := range message {
				if strings.EqualFold(name, key) {
					var text string
					if json.Unmarshal(raw, &text) == nil {
						return text, true
					}
				}
			}
		}
		for _, content := range []struct{ kind, field string }{{"extendedTextMessage", "text"}, {"imageMessage", "caption"}, {"videoMessage", "caption"}, {"documentMessage", "caption"}} {
			if nested, ok := rawJSONObject(message, content.kind); ok {
				// A missing media caption means the edit removed it.
				return rawJSONString(nested, content.field), true
			}
		}
		unwrapped := false
		for _, key := range []string{"editedMessage", "ephemeralMessage", "documentWithCaptionMessage", "deviceSentMessage"} {
			if wrapper, ok := rawJSONObject(message, key); ok {
				message, _ = rawJSONObject(wrapper, "message")
				unwrapped = true
				break
			}
		}
		if !unwrapped {
			return "", false
		}
	}
	return "", false
}

func nonDeviceJID(jid string) string {
	parts := strings.SplitN(strings.TrimSpace(jid), "@", 2)
	if len(parts) != 2 {
		return ""
	}
	return strings.SplitN(parts[0], ":", 2)[0] + "@" + parts[1]
}

func decryptMessageEdit(edit webhookMessageEdit, source messageEditSecret, senders []string) (string, bool) {
	iv, err := base64.StdEncoding.DecodeString(rawJSONString(edit.Encrypted, "encIV"))
	if err != nil || len(iv) != 12 || len(source.Secret) != 32 {
		return "", false
	}
	payload, err := base64.StdEncoding.DecodeString(rawJSONString(edit.Encrypted, "encPayload"))
	if err != nil || len(payload) > 1<<20 {
		return "", false
	}
	for _, original := range source.Senders {
		for _, sender := range senders {
			original, sender = nonDeviceJID(original), nonDeviceJID(sender)
			if original == "" || sender == "" {
				continue
			}
			key := make([]byte, 32)
			_, _ = io.ReadFull(hkdf.New(sha256.New, source.Secret, nil, []byte(edit.TargetID+original+sender+"Message Edit")), key)
			block, _ := aes.NewCipher(key)
			gcm, _ := cipher.NewGCM(block)
			plaintext, err := gcm.Open(nil, iv, payload, nil)
			if err == nil {
				return editedProtoText(plaintext, 0)
			}
		}
	}
	return "", false
}

// Decode only the text/caption fields of waE2E.Message. The authenticated
// ciphertext can also contain metadata which must never be rendered as text.
func protoBytesField(data []byte, field protowire.Number) ([]byte, bool) {
	for len(data) > 0 {
		number, kind, length := protowire.ConsumeTag(data)
		if length < 0 {
			return nil, false
		}
		data = data[length:]
		if kind == protowire.BytesType && number == field {
			value, length := protowire.ConsumeBytes(data)
			return value, length >= 0
		}
		length = protowire.ConsumeFieldValue(number, kind, data)
		if length < 0 {
			return nil, false
		}
		data = data[length:]
	}
	return nil, false
}

func editedProtoText(data []byte, depth int) (string, bool) {
	if depth > 6 {
		return "", false
	}
	if text, ok := protoBytesField(data, 1); ok && utf8.Valid(text) {
		return string(text), true
	}
	for _, fields := range [][2]protowire.Number{{6, 1}, {3, 3}, {9, 7}, {7, 20}} {
		if nested, ok := protoBytesField(data, fields[0]); ok {
			text, _ := protoBytesField(nested, fields[1])
			return string(text), utf8.Valid(text)
		}
	}
	if protocol, ok := protoBytesField(data, 12); ok {
		if edited, ok := protoBytesField(protocol, 14); ok {
			return editedProtoText(edited, depth+1)
		}
	}
	for _, field := range []protowire.Number{58, 40, 53, 31} {
		if wrapper, ok := protoBytesField(data, field); ok {
			if nested, ok := protoBytesField(wrapper, 1); ok {
				return editedProtoText(nested, depth+1)
			}
		}
	}
	return "", false
}

func loadLegacyEditSecret(id string, connection ConnectionEVO) (messageEditSecret, error) {
	request := requests.Request{URL: ApiBaseURL + "/message/status", Method: "POST", Payload: map[string]string{"id": id}, Headers: map[string]string{"Content-Type": "application/json", "apikey": connection.EvolutionAPIKey()}, Timeout: 10 * time.Second, MaxResponseBytes: 72 << 20}
	if err := request.Do(); err != nil {
		return messageEditSecret{}, ErrMessageEditUnavailable
	}
	data, _ := rawJSONObject(jsonObject(request.Response.Body), "data")
	result, _ := rawJSONObject(data, "result")
	candidates := []map[string]json.RawMessage{result}
	for _, key := range []string{"message", "rawMessage", "messageData", "rawData"} {
		if nested, ok := rawJSONObject(result, key); ok {
			candidates = append(candidates, nested)
		} else if text := rawJSONString(result, key); text != "" {
			candidates = append(candidates, jsonObject(json.RawMessage(text)))
		}
	}
	for _, candidate := range candidates {
		msg := EventMessageEVO{}
		if _, ok := rawJSONObject(candidate, "data"); ok {
			msg.FullJSON, _ = json.Marshal(candidate)
			_ = json.Unmarshal(msg.FullJSON, &msg)
		} else {
			msg.FullJSON, _ = json.Marshal(map[string]any{"data": map[string]any{"message": candidate}})
		}
		NormalizeWebhookMessage(&msg, connection)
		var source messageEditSecret
		_ = json.Unmarshal(messageEditSource(msg, connection), &source)
		if len(source.Secret) == 32 {
			return source, nil
		}
	}
	return messageEditSecret{}, ErrMessageEditUnavailable
}

// Edits update the original record, never create/reopen attendance, mark unread,
// send policy replies, or trigger automation. Connection/company scope is mandatory.
func ApplyWebhookMessageEdit(msg EventMessageEVO, connection ConnectionEVO) (string, bool, error) {
	edit, handled := parseWebhookMessageEdit(msg)
	if !handled || edit.TargetID == "" {
		return "", handled, nil
	}
	var id, conversationID int
	var raw []byte
	var fromMe bool
	err := db.QueryRow(`SELECT m.id,m."conversationId",m."editSource",m."isFromMe" FROM messages m
		JOIN conversations cv ON cv.id=m."conversationId" JOIN connections co ON co.id=cv."connectionId"
		WHERE m."messageId"=$1 AND co.id=$2 AND co."companyId"=$3 AND m."isDeleted"=false AND m."mediaType"<>'view_once'
		ORDER BY m.id DESC LIMIT 1`, edit.TargetID, connection.Id, connection.CompanyID).Scan(&id, &conversationID, &raw, &fromMe)
	if errors.Is(err, sql.ErrNoRows) || err == nil && fromMe != msg.Data.Info.IsFromMe {
		return "", true, nil
	}
	if err != nil {
		return "", true, err
	}
	if edit.Encrypted != nil {
		var source messageEditSecret
		_ = json.Unmarshal(raw, &source)
		if len(source.Secret) != 32 {
			legacy, err := loadLegacyEditSecret(edit.TargetID, connection)
			if err != nil {
				return "", true, err
			}
			source.Secret = legacy.Secret
			source.Senders = append(source.Senders, legacy.Senders...)
		}
		senders := []string{msg.Data.Info.Sender, msg.Data.Info.SenderAlt}
		if msg.Data.Info.IsFromMe {
			senders = append(senders, WhatsAppParticipant(connection.Number))
		}
		// An edit has the same author as its original message. Legacy provider
		// records may omit Info, while the authenticated edit still identifies it.
		source.Senders = append(source.Senders, senders...)
		edit.Text, edit.HasText = decryptMessageEdit(edit, source, senders)
		if !edit.HasText {
			return "", true, ErrMessageEditUnavailable
		}
		raw, _ = json.Marshal(source)
	}
	if !edit.HasText {
		return "", true, nil
	}
	if edit.Timestamp <= 0 {
		edit.Timestamp = time.Now().UnixMilli()
	}
	// Keep the original creation/display time, media, quote, direction and read
	// receipts. Equal second-resolution timestamps can represent different edits.
	result, err := db.Exec(`UPDATE messages SET text=$1,"editSource"=$4::jsonb,
		"displayData"="displayData"||jsonb_build_object('edited',true,'editedAt',$2::bigint,'body',$1::text)
		WHERE id=$3 AND (COALESCE(("displayData"->>'editedAt')::bigint,0)<$2
		OR (COALESCE(("displayData"->>'editedAt')::bigint,0)=$2 AND text IS DISTINCT FROM $1))`, edit.Text, edit.Timestamp, id, string(raw))
	if err != nil {
		return "", true, err
	}
	count, err := result.RowsAffected()
	if err == nil && count > 0 {
		// Quotes in other cached messages also depend on the changed text.
		invalidateConversationHistory(connection.CompanyID, conversationID)
		return edit.TargetID, true, nil
	}
	return "", true, err
}
