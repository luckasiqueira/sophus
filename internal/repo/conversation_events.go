package repo

import (
	"database/sql"
	"encoding/json"
	"errors"
	"sophus/internal/objectcache"
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
		invalidateConversationEventHistory(companyID, conversationID)
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
	return GetConversationEventsWindow(conversationID, companyID, nil, nil)
}

func GetConversationEventsWindow(conversationID, companyID int, after, before *time.Time) ([]ConversationEvent, error) {
	return getConversationEventsWindow(conversationID, companyID, after, before, 100)
}

func getConversationEventsWindow(conversationID, companyID int, after, before *time.Time, limit int) ([]ConversationEvent, error) {
	var limitArg any
	if limit > 0 {
		limitArg = limit
	}
	rows, err := db.Query(`SELECT event.id, event."companyId", event."conversationId", event."executionId",
		event."nodeId", event."eventType", event.content, event.metadata, event."createdAt"
		FROM conversation_events event
		JOIN conversations conversation ON conversation.id = event."conversationId"
		JOIN contacts contact ON contact.id = conversation."contactId"
		WHERE event."conversationId" = $1 AND event."companyId" = $2 AND contact."companyId" = $2
		AND ($3::timestamptz IS NULL OR event."createdAt" >= $3)
		AND ($4::timestamptz IS NULL OR event."createdAt" <= $4)
		ORDER BY event."createdAt" DESC, event.id DESC LIMIT $5`, conversationID, companyID, after, before, limitArg)
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

func AddAgentConversationNote(conversationURL string, agent Agent, content string) (ConversationEvent, error) {
	var event ConversationEvent
	metadata, err := json.Marshal(map[string]any{"authorId": agent.Id, "authorName": agent.Name})
	if err != nil {
		return event, err
	}
	err = db.QueryRow(`INSERT INTO conversation_events ("companyId", "conversationId", "eventType", content, metadata)
		SELECT $2, cv.id, 'note', $4, $5::jsonb FROM conversations cv
		JOIN contacts ct ON ct.id = cv."contactId" AND ct."companyId" = $2
		WHERE cv.url = $1::uuid AND ($3 OR cv."agentId" = $6
			OR cv."departmentId" IN (SELECT "departmentId" FROM agent_departments WHERE "agentId" = $6)
			OR (cv."agentId" IS NULL AND cv."departmentId" IS NULL AND cv.status <> 'closed'))
		RETURNING id, "companyId", "conversationId", "executionId", "nodeId", "eventType", content, metadata, "createdAt"`,
		conversationURL, agent.CompanyId, agent.IsAdmin(), content, string(metadata), agent.Id).Scan(
		&event.ID, &event.CompanyID, &event.ConversationID, &event.ExecutionID, &event.NodeID,
		&event.EventType, &event.Content, &event.Metadata, &event.CreatedAt)
	if err == nil {
		invalidateConversationEventHistory(agent.CompanyId, event.ConversationID)
	}
	return event, err
}

func conversationEventHistoryScope(companyID, conversationID int) string {
	return conversationHistoryScope(companyID, conversationID) + "-events"
}
func invalidateConversationEventHistory(companyID, conversationID int) {
	objectcache.Delete(conversationEventHistoryScope(companyID, conversationID), "events-v1")
}

func GetCachedConversationEventsWindow(conversationID, companyID int, after, before *time.Time) ([]ConversationEvent, error) {
	var events []ConversationEvent
	err := objectcache.Get(conversationEventHistoryScope(companyID, conversationID), "events-v1", conversationHistoryTTL, &events, func() error {
		var err error
		events, err = getConversationEventsWindow(conversationID, companyID, nil, nil, 0)
		return err
	})
	if err != nil {
		return nil, err
	}
	filtered := []ConversationEvent{}
	for _, event := range events {
		if after != nil && event.CreatedAt.Before(*after) {
			continue
		}
		if before != nil && event.CreatedAt.After(*before) {
			continue
		}
		filtered = append(filtered, event)
	}
	return filtered, nil
}
