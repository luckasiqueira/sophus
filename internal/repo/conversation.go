package repo

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

const (
	ConversationStatusOpen    = "open"
	ConversationStatusRunning = "running"
	ConversationStatusPending = "pending"
	ConversationStatusClosed  = "closed"
)

type Conversation struct {
	Id                   int
	Status               string
	Contact              Contact
	ConnectionID         int
	AgentID              *int
	DepartmentID         *int
	AgentName            string
	DepartmentName       string
	LastContactMessage   string
	LastContactMediaType string
	LastContactMessageAt time.Time
	URL                  uuid.UUID
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

func GetConversationByMessage(message string) (Conversation, error) {
	var conversation Conversation
	stmt, err := db.Prepare(`SELECT c.id, c.status, c."contactId", c."connectionId", c."agentId", c."departmentId", c.url, c."createdAt", c."updatedAt"
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
		&conversation.DepartmentID,
		&conversation.URL,
		&conversation.CreatedAt,
		&conversation.UpdatedAt,
	)
	return conversation, err
}

func GetOpenConversationByContact(connectionID int, number, jid string) (Conversation, error) {
	var conversation Conversation
	stmt, err := db.Prepare(`SELECT c.id, c.status, c."contactId", c."connectionId", c."agentId", c."departmentId", c.url, c."createdAt", c."updatedAt"
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
		&conversation.DepartmentID,
		&conversation.URL,
		&conversation.CreatedAt,
		&conversation.UpdatedAt,
	)
	return conversation, err
}

func GetConversationByURL(url uuid.UUID) (Conversation, error) {
	var conversation Conversation
	stmt, err := db.Prepare(`SELECT id, status, "contactId", "connectionId", "agentId", "departmentId", url, "createdAt", "updatedAt" FROM conversations WHERE url = $1`)
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
		&conversation.DepartmentID,
		&conversation.URL,
		&conversation.CreatedAt,
		&conversation.UpdatedAt,
	)
	return conversation, err
}

func GetConversationsByAgent(agent Agent, tab string) ([]Conversation, error) {
	conversations := []Conversation{}
	statusFilter := conversationListFilter(tab)
	stmt, err := db.Prepare(`SELECT
		cv.id, cv.status, cv."contactId", cv."connectionId", cv."agentId", cv."departmentId", cv.url, cv."createdAt", cv."updatedAt",
		ct.id, ct.name, ct.number, ct."connectionId", ct.jid, ct.lid, ct."isGroup", ct."isBlocked",
		COALESCE(owner.name, ''), COALESCE(d.name, ''),
		COALESCE(last_contact.text, ''), COALESCE(last_contact."mediaType", ''),
		COALESCE(last_contact."createdAt", cv."updatedAt")
		FROM conversations cv
		INNER JOIN connections co ON co.id = cv."connectionId" AND co."companyId" = $1
		INNER JOIN contacts ct ON ct.id = cv."contactId"
		LEFT JOIN agents owner ON owner.id = cv."agentId"
		LEFT JOIN departments d ON d.id = cv."departmentId"
		LEFT JOIN LATERAL (
			SELECT m.text, m."mediaType", m."createdAt"
			FROM messages m
			WHERE m."conversationId" = cv.id AND m."isFromMe" = false AND m."isDeleted" = false
			ORDER BY m."createdAt" DESC, m.id DESC
			LIMIT 1
		) last_contact ON true
		WHERE (` + statusFilter + `)
		AND ($2 OR cv."agentId" = $3
			OR cv."departmentId" IN (SELECT ad."departmentId" FROM agent_departments ad WHERE ad."agentId" = $3)
			OR (cv."agentId" IS NULL AND cv."departmentId" IS NULL AND cv.status <> 'closed'))
		ORDER BY COALESCE(last_contact."createdAt", cv."updatedAt") DESC`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	rows, err := stmt.Query(agent.CompanyId, agent.IsAdmin(), agent.Id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var conversation Conversation
		if err := rows.Scan(&conversation.Id,
			&conversation.Status,
			&conversation.Contact.Id,
			&conversation.ConnectionID,
			&conversation.AgentID,
			&conversation.DepartmentID,
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
			&conversation.AgentName,
			&conversation.DepartmentName,
			&conversation.LastContactMessage,
			&conversation.LastContactMediaType,
			&conversation.LastContactMessageAt,
		); err != nil {
			return nil, err
		}
		conversations = append(conversations, conversation)
	}
	return conversations, rows.Err()
}

func conversationListFilter(tab string) string {
	switch tab {
	case "pending":
		return `(cv."agentId" IS NULL OR cv.status = 'running') AND cv.status <> 'closed'`
	case "closed":
		return `cv.status = 'closed'`
	default:
		return `cv."agentId" = $3 AND cv.status NOT IN ('closed', 'pending', 'running')`
	}
}

func GetVisibleConversationByURL(url uuid.UUID, agent Agent) (Conversation, error) {
	var conversation Conversation
	err := db.QueryRow(`SELECT cv.id, cv.status, cv."contactId", cv."connectionId", cv."agentId", cv."departmentId", cv.url, cv."createdAt", cv."updatedAt",
		ct.id, ct.name, ct.number, ct."connectionId", ct.jid, ct.lid, ct."isGroup", ct."isBlocked",
		COALESCE(owner.name, ''), COALESCE(d.name, '')
		FROM conversations cv
		INNER JOIN connections co ON co.id = cv."connectionId" AND co."companyId" = $2
		INNER JOIN contacts ct ON ct.id = cv."contactId"
		LEFT JOIN agents owner ON owner.id = cv."agentId"
		LEFT JOIN departments d ON d.id = cv."departmentId"
		WHERE cv.url = $1 AND ($3 OR cv."agentId" = $4
			OR cv."departmentId" IN (SELECT ad."departmentId" FROM agent_departments ad WHERE ad."agentId" = $4)
			OR (cv."agentId" IS NULL AND cv."departmentId" IS NULL AND cv.status <> 'closed'))`,
		url, agent.CompanyId, agent.IsAdmin(), agent.Id).Scan(
		&conversation.Id, &conversation.Status, &conversation.Contact.Id, &conversation.ConnectionID,
		&conversation.AgentID, &conversation.DepartmentID, &conversation.URL, &conversation.CreatedAt, &conversation.UpdatedAt,
		&conversation.Contact.Id, &conversation.Contact.Name, &conversation.Contact.Number, &conversation.Contact.ConnectionId,
		&conversation.Contact.JID, &conversation.Contact.LID, &conversation.Contact.IsGroup, &conversation.Contact.IsBlocked,
		&conversation.AgentName, &conversation.DepartmentName,
	)
	return conversation, err
}

func CreateConversation(conversation Conversation) (int, error) {
	stmt, err := db.Prepare(`INSERT INTO public.conversations (id, status, "contactId", "connectionId", "agentId", "departmentId", url, "createdAt", "updatedAt")
VALUES (DEFAULT, $1, $2, $3, $4, $5, $6, $7, $8) RETURNING id;`)
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
		conversation.DepartmentID,
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
		if err := ReopenConversation(conversationId); err != nil {
			return 0, err
		}
	}

	return conversationId, nil
}

func ReopenConversation(id int) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`UPDATE flow_executions
		SET status = 'failed',
			"errorMessage" = 'execução cancelada porque a conversa foi fechada',
			"completedAt" = now(),
			"updatedAt" = now()
		WHERE "conversationId" = $1 AND status IN ('running', 'waiting')`, id)
	if err != nil {
		return err
	}
	result, err := tx.Exec(`UPDATE conversations SET status = $1, "updatedAt" = now()
		WHERE id = $2 AND status IN ($3, 'resolved')`, ConversationStatusOpen, id, ConversationStatusClosed)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

func CloseConversationByURL(url uuid.UUID, companyID, agentID int, isAdmin bool) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var conversationID int
	err = tx.QueryRow(`SELECT cv.id
		FROM conversations cv
		INNER JOIN connections c ON c.id = cv."connectionId"
		WHERE cv.url = $1 AND c."companyId" = $2
		AND (cv."agentId" = $3 OR $4)
		FOR UPDATE`, url, companyID, agentID, isAdmin).Scan(&conversationID)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`UPDATE flow_executions
		SET status = 'failed',
			"errorMessage" = 'execução cancelada porque a conversa foi fechada',
			"completedAt" = now(),
			"updatedAt" = now()
		WHERE "conversationId" = $1 AND status IN ('running', 'waiting')`, conversationID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE conversations
		SET status = $1, "updatedAt" = now()
		WHERE id = $2`, ConversationStatusClosed, conversationID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func AcceptConversation(url uuid.UUID, agent Agent) (bool, error) {
	tx, err := db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var conversationID int
	err = tx.QueryRow(`SELECT cv.id
		FROM conversations cv
		INNER JOIN connections co ON co.id = cv."connectionId" AND co."companyId" = $2
		WHERE cv.url = $1 AND (cv."agentId" IS NULL OR cv.status = 'running') AND cv.status <> 'closed'
		AND ($3 OR cv."agentId" = $4
			OR cv."departmentId" IN (SELECT ad."departmentId" FROM agent_departments ad WHERE ad."agentId" = $4)
			OR (cv."agentId" IS NULL AND cv."departmentId" IS NULL))
		FOR UPDATE`, url, agent.CompanyId, agent.IsAdmin(), agent.Id).Scan(&conversationID)
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(`UPDATE flow_executions SET status = 'failed',
		"errorMessage" = 'execução cancelada porque a conversa foi aceita por um agente',
		"completedAt" = now(), "updatedAt" = now()
		WHERE "conversationId" = $1 AND status IN ('running', 'waiting')`, conversationID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`UPDATE conversations SET "agentId" = $1, status = $2, "updatedAt" = now() WHERE id = $3`,
		agent.Id, ConversationStatusOpen, conversationID); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func IgnoreConversation(url uuid.UUID, agent Agent) (bool, error) {
	tx, err := db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var conversationID int
	err = tx.QueryRow(`SELECT cv.id
		FROM conversations cv
		INNER JOIN connections co ON co.id = cv."connectionId" AND co."companyId" = $2
		WHERE cv.url = $1 AND (cv."agentId" IS NULL OR cv.status = 'running') AND cv.status <> 'closed'
		AND ($3 OR cv."agentId" = $4
			OR cv."departmentId" IN (SELECT ad."departmentId" FROM agent_departments ad WHERE ad."agentId" = $4)
			OR (cv."agentId" IS NULL AND cv."departmentId" IS NULL))
		FOR UPDATE`, url, agent.CompanyId, agent.IsAdmin(), agent.Id).Scan(&conversationID)
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(`UPDATE flow_executions SET status = 'failed',
		"errorMessage" = 'execução cancelada porque a conversa foi ignorada',
		"completedAt" = now(), "updatedAt" = now()
		WHERE "conversationId" = $1 AND status IN ('running', 'waiting')`, conversationID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`UPDATE conversations SET status = $1, "updatedAt" = now() WHERE id = $2`, ConversationStatusClosed, conversationID); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
