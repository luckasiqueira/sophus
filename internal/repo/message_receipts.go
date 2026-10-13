package repo

import (
	"encoding/json"
	"strings"

	"github.com/lib/pq"
)

// Receipt types read-self and sender only synchronize the local account;
// they are not evidence that the recipient read an outgoing message.
func ParseMessageReceipt(body []byte) ([]string, int, error) {
	var event struct {
		Data struct {
			MessageIDs []string `json:"messageIDs"`
			Type       string   `json:"type"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &event); err != nil {
		return nil, 0, err
	}
	status := 0
	switch strings.ToLower(strings.TrimSpace(event.Data.Type)) {
	case "", "delivery", "delivered":
		status = 2
	case "read", "played":
		status = 3
	}
	ids := []string{}
	seen := map[string]bool{}
	for _, id := range event.Data.MessageIDs {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	return ids, status, nil
}

type MessageReceiptUpdate struct {
	ConversationID  int
	MessageID       string
	ConversationURL string
	Status          int
	IsGroup         bool
}

func ApplyMessageReceipt(connection ConnectionEVO, ids []string, status int) ([]MessageReceiptUpdate, error) {
	if len(ids) == 0 || status < 1 || status > 3 {
		return nil, nil
	}
	// Keep receipts independently of messages: webhooks can arrive before the
	// send response is saved or before the incoming-message queue is consumed.
	_, err := db.Exec(`INSERT INTO message_receipts ("connectionId", "messageId", status)
 SELECT $1, unnest($2::text[]), $3
 ON CONFLICT ("connectionId", "messageId") DO UPDATE
 SET status = GREATEST(message_receipts.status, EXCLUDED.status)`, connection.Id, pq.Array(ids), status)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT m."messageId", c.id, c.url, r.status, m."isGroup"
 FROM message_receipts r
 JOIN conversations c ON c."connectionId" = r."connectionId"
 JOIN connections connection ON connection.id = c."connectionId"
 JOIN messages m ON m."conversationId" = c.id AND m."messageId" = r."messageId"
 WHERE r."connectionId" = $1 AND connection."companyId" = $2
 AND r."messageId" = ANY($3::text[]) AND m."isFromMe" = true`, connection.Id, connection.CompanyID, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	updates := []MessageReceiptUpdate{}
	for rows.Next() {
		var update MessageReceiptUpdate
		if err := rows.Scan(&update.MessageID, &update.ConversationID, &update.ConversationURL, &update.Status, &update.IsGroup); err != nil {
			return nil, err
		}
		updates = append(updates, update)
		scheduleConversationHistoryUpdate(update.ConversationID, update.MessageID, connection.CompanyID)
	}
	return updates, rows.Err()
}
