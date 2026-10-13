package repo

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sophus/pkg/http/requests"
	"sophus/utils/env"
)

var ErrMediaSourceUnavailable = errors.New("o WhatsApp não disponibiliza mais os dados deste arquivo; solicite o reenvio da mídia")

func IsInternalWhatsAppEvent(msg EventMessageEVO) bool {
	message, viewOnce := messageObject(msg)
	if viewOnce {
		return false
	}
	for _, key := range []string{"protocolMessage", "senderKeyDistributionMessage", "secretEncryptedMessage"} {
		if _, ok := rawJSONObject(message, key); ok {
			return true
		}
	}
	return false
}

// Encryption metadata stays server-side and never enters the public MessageData
// JSON or Redis transcript DTO. Attachment bytes are not retained here.
func webhookMediaSource(msg EventMessageEVO) json.RawMessage {
	message, viewOnce := messageObject(msg)
	if viewOnce || msg.Data.IsViewOnce {
		return json.RawMessage(`{}`)
	}
	for _, key := range []string{"imageMessage", "videoMessage", "audioMessage", "documentMessage", "stickerMessage"} {
		if media, ok := rawJSONObject(message, key); ok {
			delete(media, "base64")
			delete(media, "jpegThumbnail")
			delete(media, "JPEGThumbnail")
			encoded, err := json.Marshal(map[string]any{key: media})
			if err == nil {
				return encoded
			}
		}
	}
	return json.RawMessage(`{}`)
}

func deviceMessageDestination(msg EventMessageEVO) string {
	if meta := msg.Data.Info.DeviceSentMeta; meta != nil && strings.TrimSpace(meta.DestinationJID) != "" {
		return strings.TrimSpace(meta.DestinationJID)
	}
	root := jsonObject(msg.FullJSON)
	data, _ := rawJSONObject(root, "data")
	message, _ := rawJSONObject(data, "message")
	for depth := 0; depth < 5; depth++ {
		if device, ok := rawJSONObject(message, "deviceSentMessage"); ok {
			return firstMessageText(device, "destinationJID", "destinationJid")
		}
		changed := false
		for _, key := range []string{"ephemeralMessage", "documentWithCaptionMessage"} {
			if wrapper, ok := rawJSONObject(message, key); ok {
				if nested, exists := rawJSONObject(wrapper, "message"); exists {
					message = nested
					changed = true
					break
				}
			}
		}
		if !changed {
			break
		}
	}
	return ""
}

// Whatsmeow includes DeviceSentMeta for messages sent by the phone or another
// linked device. Its destination survives provider PN/LID rewrites of Info.Chat.
func NormalizeWebhookMessage(msg *EventMessageEVO, connection ConnectionEVO) {
	NormalizeMessageDirection(msg, connection)
	message, viewOnce := messageObject(*msg)
	if viewOnce || msg.Data.IsViewOnce {
		return
	}
	if _, exists := rawJSONObject(message, "messageContextInfo"); !exists {
		data, _ := rawJSONObject(jsonObject(msg.FullJSON), "data")
		outer, _ := rawJSONObject(data, "message")
		if context, ok := rawJSONObject(outer, "messageContextInfo"); ok && message != nil {
			message["messageContextInfo"], _ = json.Marshal(context)
		}
	}
	content := msg.Data.Message.Base64
	if content == "" {
		data, _ := rawJSONObject(jsonObject(msg.FullJSON), "data")
		outer, _ := rawJSONObject(data, "message")
		content = firstMessageText(outer, "base64")
		if content == "" {
			content = rawJSONString(data, "base64")
		}
		if content == "" {
			content = rawJSONString(message, "base64")
		}
	}
	for key, raw := range message {
		if strings.EqualFold(key, "base64") {
			delete(message, key)
			continue
		}
		if strings.HasSuffix(strings.ToLower(key), "message") {
			media := jsonObject(raw)
			changed := false
			for field := range media {
				if strings.EqualFold(field, "base64") {
					if content == "" {
						content = rawJSONString(media, field)
					}
					delete(media, field)
					changed = true
				}
			}
			if changed {
				message[key], _ = json.Marshal(media)
			}
		}
	}
	if len(message) > 0 {
		if encoded, err := json.Marshal(message); err == nil {
			_ = json.Unmarshal(encoded, &msg.Data.Message)
		}
		// Type/text/quote parsing never needs the attachment bytes. Keeping a
		// small normalized projection avoids copying large base64 strings on
		// every subsequent metadata parse; the original queue item is untouched.
		msg.FullJSON, _ = json.Marshal(map[string]any{"data": map[string]any{"message": message}})
	}
	msg.Data.Message.Base64 = content
}

// Queue retries can complete a metadata-only message without duplicating it.
func CompleteWebhookMedia(msg EventMessageEVO, connection ConnectionEVO, stored MessageData) (bool, error) {
	if stored.IsDeleted || stored.MediaType == "view_once" || stored.MediaPath != "" || msg.Data.Message.Base64 == "" || CheckMessageType(msg) == "view_once" {
		return false, nil
	}
	path, err := saveMessageMedia(msg, connection.CompanyID, CheckMessageType(msg))
	if err != nil {
		return false, err
	}
	keep := false
	defer func() {
		if !keep {
			file := filepath.Join(env.Backend["MEDIA_DIRECTORY"], fmt.Sprint(connection.CompanyID), path)
			if info, err := os.Stat(file); err == nil {
				if os.Remove(file) == nil {
					_ = DecrementCompanyStorage(connection.CompanyID, info.Size())
				}
			}
		}
	}()
	display, err := json.Marshal(decodeInboundDisplay(msg).Display)
	if err != nil {
		return false, err
	}
	result, err := db.Exec(`UPDATE messages m SET "mediaPath"=$1,"mediaType"=$5,"displayData"=$6::jsonb,
		"isFromMe"=m."isFromMe" OR $7,"isRead"=m."isRead" OR $7,
		text=CASE WHEN btrim(COALESCE(m.text,''))='' THEN $8 ELSE m.text END
		FROM conversations cv JOIN connections co ON co.id=cv."connectionId"
		WHERE m.id=$2 AND m."conversationId"=cv.id AND co.id=$3 AND co."companyId"=$4 AND COALESCE(m."mediaPath",'')='' AND m."isDeleted"=false AND m."mediaType"<>'view_once'`, path, stored.Id, connection.Id, connection.CompanyID, CheckMessageType(msg), string(display), msg.Data.Info.IsFromMe, CheckMessageText(msg))
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	keep = count == 1
	if keep {
		scheduleConversationHistoryUpdate(stored.ConversationId, stored.MessageId, connection.CompanyID)
	}
	return keep, nil
}

func MarkWebhookMediaStorageLimit(message MessageData, connection ConnectionEVO) error {
	_, err := db.Exec(`UPDATE messages m SET "displayData"=m."displayData"||'{"mediaUnavailableReason":"storage_quota_exceeded"}'::jsonb
		FROM conversations cv JOIN connections co ON co.id=cv."connectionId"
		WHERE m.id=$1 AND m."conversationId"=cv.id AND co.id=$2 AND co."companyId"=$3 AND COALESCE(m."mediaPath",'')=''`, message.Id, connection.Id, connection.CompanyID)
	if err == nil {
		scheduleConversationHistoryUpdate(message.ConversationId, message.MessageId, connection.CompanyID)
	}
	return err
}

func DownloadWebhookMedia(msg *EventMessageEVO, connection ConnectionEVO) error {
	if msg.Data.Message.Base64 != "" || msg.MediaPath != "" {
		return nil
	}
	kind := CheckMessageType(*msg)
	var key string
	switch kind {
	case "image":
		key = "imageMessage"
	case "video":
		key = "videoMessage"
	case "audio":
		key = "audioMessage"
	case "document":
		key = "documentMessage"
	case "sticker":
		key = "stickerMessage"
	default:
		return nil
	}
	message, viewOnce := messageObject(*msg)
	if viewOnce || msg.Data.IsViewOnce {
		return nil
	}
	media, ok := rawJSONObject(message, key)
	if !ok {
		return ErrMediaSourceUnavailable
	}
	// Metadata-only media must be decrypted by the connection's WhatsApp
	// session. Never fetch the encrypted URL directly or substitute a thumbnail.
	if firstMessageText(media, "url", "directPath") == "" {
		return ErrMediaSourceUnavailable
	}
	request := requests.Request{URL: ApiBaseURL + "/message/downloadmedia", Method: "POST", Payload: map[string]any{"message": map[string]any{key: media}}, Headers: map[string]string{"Content-Type": "application/json", "apikey": connection.EvolutionAPIKey()}, Timeout: 45 * time.Second, MaxResponseBytes: 72 << 20}
	if err := request.Do(); err != nil {
		return fmt.Errorf("download WhatsApp %s: %w", kind, err)
	}
	var response struct {
		Data struct {
			Base64 string `json:"base64"`
		} `json:"data"`
	}
	if err := json.Unmarshal(request.Response.Body, &response); err != nil {
		return fmt.Errorf("invalid WhatsApp media response: %w", err)
	}
	if strings.TrimSpace(response.Data.Base64) == "" {
		return fmt.Errorf("WhatsApp media response has no file content")
	}
	msg.Data.Message.Base64 = response.Data.Base64
	return nil
}
