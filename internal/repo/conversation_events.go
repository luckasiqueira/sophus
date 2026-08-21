package repo

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type ConversationEvent struct {
	ID             int64           `json:"id"`
	CompanyID      int             `json:"companyId"`
	ConversationID int             `json:"conversationId"`
	ExecutionID    *int            `json:"executionId,omitempty"`
	NodeID         *string         `json:"nodeId,omitempty"`
	EventType      string          `json:"eventType"`
	Content        string          `json:"content"`
	Metadata       json.RawMessage `json:"metadata"`
	CreatedAt      time.Time       `json:"createdAt"`
}

func AddFlowConversationEvent(conversationID, executionID, companyID int, nodeID, eventType, content string) error {
	var id int64
	err := db.QueryRow(`INSERT INTO conversation_events
		("companyId", "conversationId", "executionId", "nodeId", "eventType", content)
		SELECT $3, conversation.id, execution.id, $4, $5, $6
		FROM conversations conversation
		JOIN connections connection ON connection.id = conversation."connectionId" AND connection."companyId" = $3
		JOIN flow_executions execution ON execution.id = $2 AND execution."conversationId" = conversation.id
			AND execution."companyId" = $3 AND execution.status IN ('running', 'waiting')
		WHERE conversation.id = $1
		ON CONFLICT ("executionId", "nodeId", "eventType")
			WHERE "executionId" IS NOT NULL AND "nodeId" IS NOT NULL DO NOTHING
		RETURNING id`, conversationID, executionID, companyID, nodeID, eventType, content).Scan(&id)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var exists bool
	if checkErr := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM conversation_events
		WHERE "companyId" = $1 AND "conversationId" = $2 AND "executionId" = $3
		AND "nodeId" = $4 AND "eventType" = $5)`, companyID, conversationID, executionID, nodeID, eventType).Scan(&exists); checkErr != nil {
		return checkErr
	}
	if exists {
		return nil
	}
	return sql.ErrNoRows
}

func GetConversationEvents(conversationID, companyID int) ([]ConversationEvent, error) {
	rows, err := db.Query(`SELECT event.id, event."companyId", event."conversationId", event."executionId",
		event."nodeId", event."eventType", event.content, event.metadata, event."createdAt"
		FROM conversation_events event
		JOIN conversations conversation ON conversation.id = event."conversationId"
		JOIN connections connection ON connection.id = conversation."connectionId"
		WHERE event."conversationId" = $1 AND event."companyId" = $2 AND connection."companyId" = $2
		ORDER BY event."createdAt" DESC, event.id DESC LIMIT 100`, conversationID, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []ConversationEvent{}
	for rows.Next() {
		var event ConversationEvent
		if err := rows.Scan(&event.ID, &event.CompanyID, &event.ConversationID, &event.ExecutionID,
			&event.NodeID, &event.EventType, &event.Content, &event.Metadata, &event.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}
