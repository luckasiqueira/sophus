package repo

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

const (
	ConversationStatusOpen    = "open"
	ConversationStatusRunning = "running"
	ConversationStatusClosed  = "closed"
)

type Conversation struct {
	Id           int
	Status       string
	Contact      Contact // possible needed to use contact as struct
	ConnectionID int
	AgentID      int
	// Department // department as struct
	URL       uuid.UUID
	CreatedAt time.Time
	UpdatedAt time.Time
}

func GetConversationByMessage(message string) (Conversation, error) {
	var conversation Conversation
	stmt, err := db.Prepare(`SELECT c.*
		FROM messages m
		INNER JOIN conversations c
			ON c.id = m."conversationId"
		WHERE m."messageId" = $1`)
	if err != nil {
		return conversation, err
	}
	defer stmt.Close()
	err = stmt.QueryRow(message).Scan(
		&conversation.Id,
		&conversation.Status,
		&conversation.Contact.Id,
		&conversation.ConnectionID,
		&conversation.AgentID,
		&conversation.URL,
		&conversation.CreatedAt,
		&conversation.UpdatedAt,
	)
	return conversation, err
}

func GetOpenConversationByContact(connectionID int, number, jid string) (Conversation, error) {
	var conversation Conversation
	stmt, err := db.Prepare(`SELECT c.id, c.status, c."contactId", c."connectionId", c."agentId", c.url, c."createdAt", c."updatedAt"
		FROM conversations c
		INNER JOIN contacts ct ON ct.id = c."contactId"
		WHERE c."connectionId" = $1
		AND (ct.number = $2 OR ct.jid = $3 OR ct.lid = $3)
		ORDER BY c.id DESC LIMIT 1`)
	if err != nil {
		return conversation, err
	}
	defer stmt.Close()
	err = stmt.QueryRow(connectionID, number, jid).Scan(
		&conversation.Id,
		&conversation.Status,
		&conversation.Contact.Id,
		&conversation.ConnectionID,
		&conversation.AgentID,
		&conversation.URL,
		&conversation.CreatedAt,
		&conversation.UpdatedAt,
	)
	return conversation, err
}

func GetConversationByURL(url uuid.UUID) (Conversation, error) {
	var conversation Conversation
	stmt, err := db.Prepare(`SELECT * FROM conversations WHERE url = $1`)
	if err != nil {
		return conversation, err
	}
	defer stmt.Close()
	err = stmt.QueryRow(url).Scan(
		&conversation.Id,
		&conversation.Status,
		&conversation.Contact.Id,
		&conversation.ConnectionID,
		&conversation.AgentID,
		&conversation.URL,
		&conversation.CreatedAt,
		&conversation.UpdatedAt,
	)
	return conversation, nil
}

func GetConversationsByAgent(agent Agent) ([]Conversation, error) {
	conversations := []Conversation{}
	stmt, err := db.Prepare(`SELECT 
    conversations.*,
    contacts.*
FROM conversations
INNER JOIN contacts 
    ON contacts.id = conversations."contactId"
WHERE conversations."agentId" = $1;
`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	rows, err := stmt.Query(agent.Id)
	for rows.Next() {
		var conversation Conversation
		rows.Scan(&conversation.Id,
			&conversation.Status,
			&conversation.Contact.Id,
			&conversation.ConnectionID,
			&conversation.AgentID,
			&conversation.URL,
			&conversation.CreatedAt,
			&conversation.UpdatedAt,
			&conversation.Contact.Id,
			&conversation.Contact.Name,
			&conversation.Contact.Number,
			&conversation.Contact.ConnectionId,
			&conversation.Contact.JID,
			&conversation.Contact.LID,
			&conversation.Contact.IsGroup,
			&conversation.Contact.IsBlocked,
		)
		conversations = append(conversations, conversation)
	}
	return conversations, nil
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
		conversation.Contact.Id,
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

func setConversation(connectionId int, contact Contact, reopenClosed bool) (int, error) {
	conversationId, err := checkExistentConversation(connectionId, contact, reopenClosed)
	if err != nil {
		return 0, err
	}
	return conversationId, nil
}

func checkExistentConversation(connectionId int, contact Contact, reopenClosed bool) (int, error) {
	stmt, err := db.Prepare(`SELECT id FROM contacts
		WHERE "connectionId" = $1::text AND (number = $2
			OR ($3 <> '' AND lid = $3)
			OR ($4 <> '' AND jid = $4))
		ORDER BY id DESC LIMIT 1`)
	if err != nil {
		return 0, err
	}
	var contactId int
	err = stmt.QueryRow(connectionId, contact.Number, contact.LID, contact.JID).Scan(&contactId)
	if err != nil {
		if err != sql.ErrNoRows {
			return 0, err
		}
		contactId, err = CreateContact(contact)
		if err != nil {
			return 0, err
		}
	}
	defer stmt.Close()
	stmtt, err := db.Prepare(`SELECT id, status FROM conversations
		WHERE "contactId" = $1 AND "connectionId" = $2
		ORDER BY id DESC LIMIT 1`)
	if err != nil {
		return 0, err
	}
	var conversationId int
	var status string
	err = stmtt.QueryRow(contactId, connectionId).Scan(&conversationId, &status)
	if err != nil {
		if err != sql.ErrNoRows {
			return 0, err
		}
		conversation := Conversation{
			Status: ConversationStatusOpen,
			Contact: Contact{
				Id: contactId,
			},
			ConnectionID: connectionId,
			URL:          uuid.New(),
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		}
		conversationId, err = CreateConversation(conversation)
		if err != nil {
			return 0, err
		}
	} else if reopenClosed && (status == ConversationStatusClosed || status == "resolved") {
		if err := UpdateConversationStatus(conversationId, ConversationStatusOpen); err != nil {
			return 0, err
		}
	}

	return conversationId, nil
}

func ReopenConversation(id int) error {
	_, err := db.Exec(`UPDATE conversations SET status = $1, "updatedAt" = now()
		WHERE id = $2 AND status IN ($3, 'resolved')`, ConversationStatusOpen, id, ConversationStatusClosed)
	return err
}
