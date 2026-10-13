package repo

import (
	"encoding/json"
	"time"

	"sophus/pkg/http/requests"
)

func GetMessageMediaRecovery(anchorID, companyID, id int) (MessageData, EventMessageEVO, error) {
	var stored MessageData
	var source json.RawMessage
	err := db.QueryRow(contactHistoryScopeSQL+`SELECT m.id,m."messageId",m."conversationId",m."mediaType",COALESCE(m."mediaPath",''),m."isFromMe",m."isDeleted",m."mediaSource"
		FROM messages m JOIN history_conversations cv ON cv.id=m."conversationId" WHERE m.id=$3 AND m."mediaType"<>'system'`, anchorID, companyID, id).Scan(&stored.Id, &stored.MessageId, &stored.ConversationId, &stored.MediaType, &stored.MediaPath, &stored.IsFromMe, &stored.IsDeleted, &source)
	message := EventMessageEVO{}
	message.Data.Info.ID = stored.MessageId
	message.Data.Info.IsFromMe = stored.IsFromMe
	message.FullJSON, _ = json.Marshal(map[string]any{"data": map[string]any{"message": source}})
	return stored, message, err
}

// Legacy messages did not retain encrypted media metadata. Some providers still
// have the original message; a missing result cannot be reconstructed locally.
func LoadLegacyMediaSource(message *EventMessageEVO, connection ConnectionEVO) error {
	if len(webhookMediaSource(*message)) > 2 {
		return nil
	}
	request := requests.Request{URL: ApiBaseURL + "/message/status", Method: "POST", Payload: map[string]string{"id": message.Data.Info.ID}, Headers: map[string]string{"Content-Type": "application/json", "apikey": connection.EvolutionAPIKey()}, Timeout: 10 * time.Second, MaxResponseBytes: 72 << 20}
	if err := request.Do(); err != nil {
		return ErrMediaSourceUnavailable
	}
	data, _ := rawJSONObject(jsonObject(request.Response.Body), "data")
	result, ok := rawJSONObject(data, "result")
	if !ok {
		return ErrMediaSourceUnavailable
	}
	candidates := []map[string]json.RawMessage{result}
	for _, key := range []string{"message", "rawMessage", "messageData", "rawData"} {
		if nested, ok := rawJSONObject(result, key); ok {
			candidates = append(candidates, nested)
		} else if text := rawJSONString(result, key); text != "" {
			candidates = append(candidates, jsonObject(json.RawMessage(text)))
		}
	}
	for _, candidate := range candidates {
		copy := *message
		copy.FullJSON, _ = json.Marshal(map[string]any{"data": map[string]any{"message": candidate}})
		if len(webhookMediaSource(copy)) > 2 {
			*message = copy
			return nil
		}
		if _, ok := rawJSONObject(candidate, "data"); ok {
			encoded, _ := json.Marshal(candidate)
			copy.FullJSON = encoded
			if len(webhookMediaSource(copy)) > 2 {
				*message = copy
				return nil
			}
		}
	}
	return ErrMediaSourceUnavailable
}
