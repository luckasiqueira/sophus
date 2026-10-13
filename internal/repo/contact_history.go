package repo

import (
	"database/sql"
	"time"
)

// Access to the anchor is checked by the controller. Only the anchor and closed
// appointments for its contact on the same live connection enter this scope.
const contactHistoryScopeSQL = `WITH history_conversations AS (
	SELECT cv.* FROM conversations anchor
	JOIN contacts contact ON contact.id = anchor."contactId" AND contact."companyId" = $2
	JOIN connections connection ON connection.id = anchor."connectionId" AND connection."companyId" = $2
	JOIN conversations cv ON cv."contactId" = anchor."contactId" AND cv."connectionId" = anchor."connectionId"
	WHERE anchor.id = $1 AND anchor."companyId" = $2 AND cv."companyId" = $2
	AND (cv.id = anchor.id OR cv.status IN ('closed','resolved'))
) `

const contactHistoryPageIDsSQL = contactHistoryScopeSQL + `, page_ids AS (
	SELECT m.id FROM messages m JOIN history_conversations cv ON cv.id = m."conversationId"
	WHERE m."mediaType"<>'system' AND ($3::integer = 0 OR (m."createdAt", m.id) < (
		SELECT cursor."createdAt", cursor.id FROM messages cursor JOIN history_conversations allowed ON allowed.id = cursor."conversationId" WHERE cursor.id = $3))
	ORDER BY m."createdAt" DESC, m.id DESC LIMIT $4
) `

const contactHistoryConversationsSQL = contactHistoryScopeSQL + `SELECT cv.id, cv.url, cv.status, cv."createdAt", cv."updatedAt",
	COALESCE(agent.name, 'Sem operador'), COALESCE(department.name, 'Sem departamento'), cv.protocol
	FROM history_conversations cv
	LEFT JOIN agents agent ON agent.id = cv."agentId" AND agent."companyId" = $2
	LEFT JOIN departments department ON department.id = cv."departmentId" AND department."companyId" = $2
	ORDER BY cv."createdAt", cv.id`

const contactHistoryEventsSQL = contactHistoryScopeSQL + `SELECT event.id, event."companyId",event."conversationId",event."executionId",event."nodeId",event."eventType",event.content,event.metadata,event."createdAt"
	FROM conversation_events event JOIN history_conversations cv ON cv.id=event."conversationId"
	WHERE event."companyId"=$2 AND event."eventType"='note'
	AND ($3::timestamptz IS NULL OR event."createdAt">=$3)
	AND ($4::timestamptz IS NULL OR event."createdAt"<$4)
	ORDER BY event."createdAt",event.id`

func GetContactHistoryConversations(conversationID, companyID int) ([]Conversation, error) {
	rows, err := db.Query(contactHistoryConversationsSQL, conversationID, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	conversations := []Conversation{}
	for rows.Next() {
		var conversation Conversation
		if err := rows.Scan(&conversation.Id, &conversation.URL, &conversation.Status, &conversation.CreatedAt, &conversation.UpdatedAt, &conversation.AgentName, &conversation.DepartmentName, &conversation.Protocol); err != nil {
			return nil, err
		}
		conversations = append(conversations, conversation)
	}
	return conversations, rows.Err()
}

func GetContactHistoryPage(conversationID, companyID, beforeID int) (ConversationMessagePage, error) {
	messages, err := getSelectedConversationMessages(conversationID, companyID, beforeID, 51, contactHistoryPageIDsSQL)
	if err != nil {
		return ConversationMessagePage{}, err
	}
	page := ConversationMessagePage{HistoryScope: "contact", Messages: messages, HasOlder: len(messages) > 50}
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
	if beforeID > 0 {
		var upper time.Time
		if err := db.QueryRow(contactHistoryScopeSQL+`SELECT m."createdAt" FROM messages m JOIN history_conversations cv ON cv.id=m."conversationId" WHERE m.id=$3`, conversationID, companyID, beforeID).Scan(&upper); err != nil {
			return page, err
		}
		page.WindowBefore = &upper
	}
	page.Conversations, err = GetContactHistoryConversations(conversationID, companyID)
	return page, err
}

func GetContactHistoryEvents(conversationID, companyID int, page ConversationMessagePage) ([]ConversationEvent, error) {
	// Use the same authorized scope as messages. Half-open windows avoid notes
	// appearing twice at a pagination boundary.
	rows, err := db.Query(contactHistoryEventsSQL, conversationID, companyID, page.WindowAfter, page.WindowBefore)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []ConversationEvent{}
	for rows.Next() {
		var event ConversationEvent
		if err := rows.Scan(&event.ID, &event.CompanyID, &event.ConversationID, &event.ExecutionID, &event.NodeID, &event.EventType, &event.Content, &event.Metadata, &event.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func contactHistoryMessages(conversationID, companyID int) ([]MessageData, []Conversation, error) {
	conversations, err := GetContactHistoryConversations(conversationID, companyID)
	if err != nil {
		return nil, nil, err
	}
	if len(conversations) == 0 {
		return nil, nil, sql.ErrNoRows
	}
	messages := []MessageData{}
	for _, conversation := range conversations {
		snapshot, err := cachedConversationHistory(companyID, conversation.Id)
		if err != nil {
			return nil, nil, err
		}
		messages = append(messages, snapshot.Messages...)
	}
	// Refresh recent messages in the active appointment before the asynchronous
	// snapshot update, matching the existing single-conversation search behavior.
	recent, err := GetConversationMessagePage(conversationID, companyID, 0)
	if err != nil {
		return nil, nil, err
	}
	return mergeHistoryMessages(messages, recent.Messages), conversations, nil
}

func SearchContactHistoryMessages(conversationID, companyID int, query string, offset int) (ConversationSearchResults, error) {
	messages, _, err := contactHistoryMessages(conversationID, companyID)
	if err != nil {
		return ConversationSearchResults{}, err
	}
	return searchHistoryMessages(messages, query, offset), nil
}

func GetContactHistoryContext(conversationID, companyID, messageID int) ([]MessageData, []Conversation, error) {
	messages, conversations, err := contactHistoryMessages(conversationID, companyID)
	if err != nil {
		return nil, nil, err
	}
	context, err := messageContext(messages, messageID)
	return context, conversations, err
}
