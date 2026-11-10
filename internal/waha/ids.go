package waha

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// SessionName is the WAHA session that backs one Sophus connection. It is
// derived from the Evolution instance ID, so it is stable across restarts and
// never contains secrets.
func SessionName(instanceID uuid.UUID) string {
	return "sophus_" + strings.ReplaceAll(instanceID.String(), "-", "")
}

func instanceIDFromSession(name string) (uuid.UUID, bool) {
	raw, ok := strings.CutPrefix(name, "sophus_")
	if !ok {
		return uuid.UUID{}, false
	}
	id, err := uuid.Parse(raw)
	return id, err == nil
}

// cleanDevice removes the ":device" part of a JID, like Evolution GO's
// cleanSenderID ("5511999:27@s.whatsapp.net" -> "5511999@s.whatsapp.net").
func cleanDevice(jid string) string {
	colon := strings.Index(jid, ":")
	at := strings.Index(jid, "@")
	if colon != -1 && at != -1 && colon < at {
		return jid[:colon] + jid[at:]
	}
	return jid
}

// ChatID converts what Sophus sends as "number" (digits, phone with symbols, a
// whatsmeow JID or a WAHA chat ID) into WAHA's chat ID format.
func ChatID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if user, server, ok := strings.Cut(value, "@"); ok {
		if device := strings.Index(user, ":"); device != -1 {
			user = user[:device]
		}
		switch strings.ToLower(server) {
		case "s.whatsapp.net", "c.us":
			return digitsOnly(user) + "@c.us"
		default:
			return user + "@" + strings.ToLower(server)
		}
	}
	return digitsOnly(value) + "@c.us"
}

// whatsmeowJID converts a WAHA chat ID into the whatsmeow JID format that
// Evolution GO payloads (and therefore Sophus) use.
func whatsmeowJID(chatID string) string {
	chatID = strings.TrimSpace(chatID)
	if user, ok := strings.CutSuffix(chatID, "@c.us"); ok {
		return user + "@s.whatsapp.net"
	}
	return chatID
}

func isGroupChat(chatID string) bool {
	return strings.HasSuffix(strings.ToLower(chatID), "@g.us")
}

func digitsOnly(value string) string {
	var out strings.Builder
	for _, character := range value {
		if character >= '0' && character <= '9' {
			out.WriteRune(character)
		}
	}
	return out.String()
}

// serializedMessageID builds WAHA's message key: {fromMe}_{chat}_{id}[_{participant}].
func serializedMessageID(fromMe bool, chatID, messageID, participant string) string {
	serialized := fmt.Sprintf("%t_%s_%s", fromMe, ChatID(chatID), messageID)
	if participant = strings.TrimSpace(participant); participant != "" && isGroupChat(chatID) {
		serialized += "_" + ChatID(participant)
	}
	return serialized
}

// rawMessageID extracts the WhatsApp message ID from a WAHA serialized key.
func rawMessageID(serialized string) string {
	parts := strings.Split(serialized, "_")
	if len(parts) >= 3 && (parts[0] == "true" || parts[0] == "false") {
		return parts[2]
	}
	return serialized
}
