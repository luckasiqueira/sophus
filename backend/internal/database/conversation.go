package database

import (
	"time"

	"github.com/google/uuid"
)

type Conversation struct {
	ID           int
	Status       string
	ContactID    int
	ConnectionID int
	AgentID      int
	URL          uuid.UUID
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func CreateConversation(conversation Conversation) (int, error) {
	stmt, err := db.Prepare(`INSERT INTO public.conversations (id, status, "contactId", "connectionId", "agentId", url, "createdAt", "updatedAt")
VALUES (DEFAULT, $1, $2, $3, $4, $5, $6, $7) RETURNING id;`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	var conversationId int
	err = stmt.QueryRow(
		conversation.Status,
		conversation.ContactID,
		conversation.ConnectionID,
		conversation.AgentID,
		conversation.URL,
		conversation.CreatedAt,
		conversation.UpdatedAt,
	).Scan(&conversationId)
	if err != nil {
		return 0, err
	}
	return conversationId, nil
}
