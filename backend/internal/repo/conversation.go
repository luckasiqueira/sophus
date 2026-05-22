package repo

import (
	"strings"
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

func setConversation(connectionId int, contact Contact) (int, error) {
	conversationId, err := checkExistentConversation(connectionId, contact)
	if err != nil {
		return 0, err
	}
	return conversationId, nil
}

func checkExistentConversation(connectionId int, contact Contact) (int, error) {
	stmt, err := db.Prepare(`SELECT "id" FROM "contacts" WHERE "number" = $1 OR "lid" = $2 OR "jid" = $3`)
	if err != nil {
		return 0, err
	}
	var contactId int
	err = stmt.QueryRow(contact.Number, contact.LID, contact.JID).Scan(&contactId)
	if err != nil {
		if !strings.Contains(err.Error(), "no rows in result set") {
			return 0, err
		}
		contactId, err = CreateContact(contact)
		if err != nil {
			return 0, err
		}
	}
	defer stmt.Close()
	stmtt, err := db.Prepare(`SELECT "id" FROM "conversations" WHERE "contactId" = $1 AND "status" = 'open'`)
	if err != nil {
		return 0, err
	}
	var conversationId int
	err = stmtt.QueryRow(contactId).Scan(&conversationId)
	if err != nil {
		if !strings.Contains(err.Error(), "no rows in result set") {
			return 0, err
		}
		conversation := Conversation{
			Status:       "open",
			ContactID:    contactId,
			ConnectionID: connectionId,
			URL:          uuid.New(),
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		}
		conversationId, err = CreateConversation(conversation)
		if err != nil {
			return 0, err
		}
	}

	return conversationId, nil
}
