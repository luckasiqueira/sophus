package waha

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"sophus/internal/repo"
)

// ErrWebhookUnauthorized means the request is not a WAHA webhook signed for
// this connection's session.
var ErrWebhookUnauthorized = errors.New("WAHA webhook signature or session mismatch")

const groupLookupTimeout = 5 * time.Second

// TranslateWebhook verifies a WAHA webhook and converts it into the Evolution
// GO event Sophus' webhook handler already processes. ignored=true means the
// event has no Evolution GO equivalent (or Evolution GO would drop it).
func TranslateWebhook(connection repo.ConnectionEVO, headers http.Header, body []byte) ([]byte, bool, error) {
	return bridge().TranslateWebhook(connection, headers, body)
}

func (b *Bridge) TranslateWebhook(connection repo.ConnectionEVO, headers http.Header, body []byte) ([]byte, bool, error) {
	if !validSignature(connection.EvolutionAPIKey(), headers, body) {
		return nil, false, ErrWebhookUnauthorized
	}
	var envelope struct {
		Event   string          `json:"event"`
		Session string          `json:"session"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, false, err
	}
	session := SessionName(connection.InstanceID)
	if envelope.Session != session {
		return nil, false, ErrWebhookUnauthorized
	}
	switch envelope.Event {
	case "session.status":
		var status struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(envelope.Payload, &status) != nil || status.Status != statusWorking {
			return nil, true, nil
		}
		return evolutionEvent(connection, "Connected", json.RawMessage("{}"), nil)
	case "engine.event":
		var engine struct {
			Event string          `json:"event"`
			Data  json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(envelope.Payload, &engine); err != nil {
			return nil, false, err
		}
		return b.translateEngineEvent(connection, session, engine.Event, engine.Data)
	}
	return nil, true, nil
}

func validSignature(key string, headers http.Header, body []byte) bool {
	if key == "" {
		return false
	}
	if algorithm := headers.Get("X-Webhook-Hmac-Algorithm"); algorithm != "" && !strings.EqualFold(algorithm, "sha512") {
		return false
	}
	received, err := hex.DecodeString(strings.TrimSpace(headers.Get("X-Webhook-Hmac")))
	if err != nil || len(received) != sha512.Size {
		return false
	}
	signer := hmac.New(sha512.New, []byte(key))
	signer.Write(body)
	return hmac.Equal(signer.Sum(nil), received)
}

func evolutionEvent(connection repo.ConnectionEVO, event string, data any, extra map[string]any) ([]byte, bool, error) {
	payload := map[string]any{
		"event":         event,
		"data":          data,
		"instanceId":    connection.InstanceID.String(),
		"instanceName":  connection.Name,
		"instanceToken": connection.ConnectionKey.String(),
	}
	for key, value := range extra {
		payload[key] = value
	}
	encoded, err := json.Marshal(payload)
	return encoded, false, err
}

func (b *Bridge) translateEngineEvent(connection repo.ConnectionEVO, session, event string, data json.RawMessage) ([]byte, bool, error) {
	switch event {
	case "events.Message":
		return b.translateMessage(connection, session, data)
	case "events.Receipt":
		return translateReceipt(connection, data)
	case "events.Picture":
		return evolutionEvent(connection, "Picture", data, nil)
	case "gows.ConnectedEventData", "events.Connected":
		return evolutionEvent(connection, "Connected", json.RawMessage("{}"), nil)
	case "events.Disconnected":
		return evolutionEvent(connection, "Disconnected", data, nil)
	case "events.LoggedOut":
		return evolutionEvent(connection, "LoggedOut", data, nil)
	case "events.TemporaryBan":
		return evolutionEvent(connection, "TemporaryBan", data, nil)
	case "events.ConnectFailure":
		return evolutionEvent(connection, "ConnectFailure", data, nil)
	}
	return nil, true, nil
}

func decodeObject(raw []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

// translateMessage applies what Evolution GO does to whatsmeow's
// events.Message before dispatching it (see its myEventHandler).
func (b *Bridge) translateMessage(connection repo.ConnectionEVO, session string, raw json.RawMessage) ([]byte, bool, error) {
	data, err := decodeObject(raw)
	if err != nil {
		return nil, false, err
	}
	message, _ := data["Message"].(map[string]any)
	if message == nil || ignoredMessage(message) {
		return nil, true, nil
	}
	info, _ := data["Info"].(map[string]any)
	if info == nil {
		return nil, true, nil
	}
	normalizeSender(info)
	delete(data, "RawMessage")
	for _, key := range []string{"imageMessage", "videoMessage", "documentMessage"} {
		if media, ok := message[key].(map[string]any); ok {
			delete(media, "JPEGThumbnail")
		}
	}
	addQuoted(data, message)
	if chat, _ := info["Chat"].(string); isGroupChat(chat) {
		ctx, cancel := context.WithTimeout(context.Background(), groupLookupTimeout)
		group, ok := b.groupInfo(ctx, session, chat)
		cancel()
		if ok {
			data["groupData"] = group
		}
	}
	return evolutionEvent(connection, "Message", data, nil)
}

// ignoredMessage mirrors Evolution GO's GetMessageType "ignore" and
// "unknown_protocol_*" results, which are never dispatched.
func ignoredMessage(message map[string]any) bool {
	if _, ok := message["encEventResponseMessage"]; ok {
		return true
	}
	for _, key := range []string{"conversation", "extendedTextMessage"} {
		if _, ok := message[key]; ok {
			return false
		}
	}
	protocol, ok := message["protocolMessage"].(map[string]any)
	if !ok {
		return false
	}
	switch protocolType(protocol) {
	case "REVOKE":
		_, hasKey := protocol["key"].(map[string]any)
		return !hasKey
	case "MESSAGE_EDIT", "EPHEMERAL_SETTING":
		return false
	default:
		return true
	}
}

var protocolTypes = map[string]string{"0": "REVOKE", "3": "EPHEMERAL_SETTING", "14": "MESSAGE_EDIT"}

func protocolType(protocol map[string]any) string {
	value, ok := protocol["type"]
	if !ok || value == nil {
		return "REVOKE"
	}
	text := strings.ToUpper(strings.TrimSpace(jsonText(value)))
	if name, ok := protocolTypes[text]; ok {
		return name
	}
	return text
}

func jsonText(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	default:
		encoded, _ := json.Marshal(typed)
		return string(encoded)
	}
}

// normalizeSender replicates Evolution GO's LID/JID swap and device cleanup.
func normalizeSender(info map[string]any) {
	sender, _ := info["Sender"].(string)
	senderAlt, _ := info["SenderAlt"].(string)
	chat, _ := info["Chat"].(string)
	if strings.Contains(sender, "@lid") && strings.Contains(senderAlt, "@s.whatsapp.net") {
		info["Sender"] = cleanDevice(senderAlt)
		if strings.Contains(chat, "@lid") {
			info["Chat"] = cleanDevice(senderAlt)
		}
		info["SenderAlt"] = cleanDevice(sender)
		return
	}
	if sender != "" {
		info["Sender"] = cleanDevice(sender)
	}
	if senderAlt != "" {
		info["SenderAlt"] = cleanDevice(senderAlt)
	}
}

func addQuoted(data, message map[string]any) {
	for _, key := range []string{"extendedTextMessage", "imageMessage", "audioMessage", "documentMessage", "videoMessage"} {
		content, ok := message[key].(map[string]any)
		if !ok {
			continue
		}
		contextInfo, _ := content["contextInfo"].(map[string]any)
		stanza := field(contextInfo, "stanzaID")
		quoted, hasQuoted := contextInfo["quotedMessage"]
		if stanza != "" && hasQuoted && quoted != nil {
			data["quoted"] = map[string]any{"stanzaID": stanza, "quotedMessage": quoted}
			data["isQuoted"] = true
		}
		return
	}
}

// translateReceipt adds Evolution GO's "state" for read/delivered receipts.
func translateReceipt(connection repo.ConnectionEVO, raw json.RawMessage) ([]byte, bool, error) {
	data, err := decodeObject(raw)
	if err != nil {
		return nil, false, err
	}
	ids, _ := data["MessageIDs"].([]any)
	if len(ids) == 0 {
		return nil, true, nil
	}
	receiptType, _ := data["Type"].(string)
	var extra map[string]any
	switch receiptType {
	case "read":
		extra = map[string]any{"state": "Read"}
	case "read-self":
		extra = map[string]any{"state": "ReadSelf"}
	case "":
		extra = map[string]any{"state": "Delivered"}
	}
	return evolutionEvent(connection, "Receipt", data, extra)
}
