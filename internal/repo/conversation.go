package repo

import (
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrInvalidConversationTransferTarget = errors.New("invalid conversation transfer target")
var ErrPhoneConversationCreationDisabled = errors.New("conversation creation from phone is disabled")

const (
	ConversationStatusOpen    = "open"
	ConversationStatusRunning = "running"
	ConversationStatusPending = "pending"
	ConversationStatusClosed  = "closed"
)

type Conversation struct {
	Id                   int
	Protocol             int64 `json:"protocol"`
	Status               string
	Contact              Contact
	ConnectionID         int
	AgentID              *int
	DepartmentID         *int
	AgentName            string
	DepartmentName       string
	DepartmentColor      string
	LastContactMessage   string
	LastContactMediaType string
	LastContactMessageAt time.Time
	LastMessageFromMe    bool
	UnreadCount          int
	URL                  uuid.UUID
	CreatedAt            time.Time
	UpdatedAt            time.Time
	Timezone             string
	CanEditMessages      bool `json:"-"`
}

type ConversationScopeCounts struct {
	Individual    int
	Group         int
	ActiveUnread  int
	PendingUnread int
}

func GetConversationByMessage(message string, companyID int, connectionIDs ...int) (Conversation, error) {
	var conversation Conversation
	var connectionID any
	if len(connectionIDs) > 0 {
		connectionID = connectionIDs[0]
	}
	stmt, err := db.Prepare(`SELECT c.id, c.status, c."contactId", c."connectionId", c."agentId", c."departmentId", c.url, c."createdAt", c."updatedAt", c.protocol
		FROM messages m
		INNER JOIN conversations c
			ON c.id = m."conversationId"
		INNER JOIN connections connection ON connection.id = c."connectionId"
		WHERE m."messageId" = $1 AND connection."companyId" = $2 AND ($3::integer IS NULL OR connection.id=$3)
		ORDER BY m.id DESC LIMIT 1`)
	if err != nil {
		return conversation, err
	}
	defer stmt.Close()
	err = stmt.QueryRow(message, companyID, connectionID).Scan(
		&conversation.Id,
		&conversation.Status,
		&conversation.Contact.Id,
		&conversation.ConnectionID,
		&conversation.AgentID,
		&conversation.DepartmentID,
		&conversation.URL,
		&conversation.CreatedAt,
		&conversation.UpdatedAt,
		&conversation.Protocol,
	)
	return conversation, err
}

func GetOpenConversationByContact(connectionID int, identifiers ...string) (Conversation, error) {
	var conversation Conversation
	number, jid, lid := canonicalWhatsAppIdentity(identifiers...)
	if number == "" && jid == "" && lid == "" {
		return conversation, sql.ErrNoRows
	}
	stmt, err := db.Prepare(`SELECT c.id, c.status, c."contactId", c."connectionId", c."agentId", c."departmentId", c.url, c."createdAt", c."updatedAt", c.protocol
		FROM conversations c
		INNER JOIN contacts ct ON ct.id = c."contactId"
		WHERE c."connectionId" = $1
		AND COALESCE(ct."isGroup", false) = false
		AND (($2 <> '' AND ct.number = $2) OR ($3 <> '' AND ct.jid = $3) OR ($4 <> '' AND ct.lid = $4))
		ORDER BY c.id DESC LIMIT 1`)
	if err != nil {
		return conversation, err
	}
	defer stmt.Close()
	err = stmt.QueryRow(connectionID, number, jid, lid).Scan(
		&conversation.Id,
		&conversation.Status,
		&conversation.Contact.Id,
		&conversation.ConnectionID,
		&conversation.AgentID,
		&conversation.DepartmentID,
		&conversation.URL,
		&conversation.CreatedAt,
		&conversation.UpdatedAt,
		&conversation.Protocol,
	)
	return conversation, err
}

func GetConversationByURL(url uuid.UUID) (Conversation, error) {
	var conversation Conversation
	stmt, err := db.Prepare(`SELECT id, status, "contactId", "connectionId", "agentId", "departmentId", url, "createdAt", "updatedAt", protocol FROM conversations WHERE url = $1`)
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
		&conversation.Protocol,
	)
	return conversation, err
}

func conversationListQuery(tab string) string {
	statusFilter := conversationListFilter(tab)
	return `SELECT
		cv.id, cv.status, cv."contactId", COALESCE(cv."connectionId", 0), cv."agentId", cv."departmentId", cv.url, cv."createdAt", cv."updatedAt",
		ct.id, ct.name, ct.number, ct.email, ct."companyId", COALESCE(ct.jid, ''), COALESCE(ct.lid, ''), COALESCE(ct."isGroup", false), COALESCE(ct."isBlocked", false),
		COALESCE(owner.name, ''), COALESCE(d.name, ''), COALESCE(d.color, '#E7E5E4'),
		COALESCE(last_contact.text, ''), COALESCE(last_contact."mediaType", ''),
		COALESCE(last_contact."createdAt", cv."updatedAt"), COALESCE(unread.count, 0),COALESCE(last_contact."isFromMe",false), cv.protocol
		FROM conversations cv
		INNER JOIN contacts ct ON ct.id = cv."contactId" AND ct."companyId" = $1
		LEFT JOIN agents owner ON owner.id = cv."agentId"
		LEFT JOIN departments d ON d.id = cv."departmentId"
		LEFT JOIN LATERAL (
			SELECT m.text, m."mediaType", m."createdAt",m."isFromMe"
			FROM messages m
			WHERE m."conversationId" = cv.id AND m."isDeleted" = false
			ORDER BY m."createdAt" DESC, m.id DESC
			LIMIT 1
		) last_contact ON true
		LEFT JOIN LATERAL (
			SELECT COUNT(*)::int AS count FROM messages unread_message
			WHERE unread_message."conversationId" = cv.id
			AND unread_message."isFromMe" = false AND unread_message."isRead" = false AND unread_message."isDeleted" = false
		) unread ON true
		WHERE cv."companyId" = $1 AND (` + statusFilter + `)
		AND COALESCE(ct."isGroup", false) = $4
		AND ($2 OR cv."agentId" = $3
			OR cv."departmentId" IN (SELECT ad."departmentId" FROM agent_departments ad WHERE ad."agentId" = $3)
			OR (cv."agentId" IS NULL AND cv."departmentId" IS NULL AND cv.status <> 'closed'))
		ORDER BY COALESCE(last_contact."createdAt", cv."updatedAt") DESC`
}

func GetConversationsByAgent(agent Agent, tab string, groups bool) ([]Conversation, error) {
	conversations := []Conversation{}
	rows, err := db.Query(conversationListQuery(tab), agent.CompanyId, agent.IsAdmin(), agent.Id, groups)
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
			&conversation.Contact.Email,
			&conversation.Contact.CompanyId,
			&conversation.Contact.JID,
			&conversation.Contact.LID,
			&conversation.Contact.IsGroup,
			&conversation.Contact.IsBlocked,
			&conversation.AgentName,
			&conversation.DepartmentName,
			&conversation.DepartmentColor,
			&conversation.LastContactMessage,
			&conversation.LastContactMediaType,
			&conversation.LastContactMessageAt,
			&conversation.UnreadCount,
			&conversation.LastMessageFromMe,
			&conversation.Protocol,
		); err != nil {
			return nil, err
		}
		conversations = append(conversations, conversation)
	}
	return conversations, rows.Err()
}

func GetConversationScopeCounts(agent Agent, groups bool) (ConversationScopeCounts, error) {
	var counts ConversationScopeCounts
	err := db.QueryRow(`SELECT
		COUNT(*) FILTER (WHERE COALESCE(ct."isGroup", false) = false
			AND ((cv."agentId" = $3 AND cv.status NOT IN ('closed', 'pending', 'running'))
				OR ((cv."agentId" IS NULL OR cv.status = 'running') AND cv.status <> 'closed'))
			AND EXISTS (SELECT 1 FROM messages unread WHERE unread."conversationId" = cv.id
				AND unread."isFromMe" = false AND unread."isRead" = false AND unread."isDeleted" = false)),
		COUNT(*) FILTER (WHERE COALESCE(ct."isGroup", false) = true
			AND ((cv."agentId" = $3 AND cv.status NOT IN ('closed', 'pending', 'running'))
				OR ((cv."agentId" IS NULL OR cv.status = 'running') AND cv.status <> 'closed'))
			AND EXISTS (SELECT 1 FROM messages unread WHERE unread."conversationId" = cv.id
				AND unread."isFromMe" = false AND unread."isRead" = false AND unread."isDeleted" = false)),
		COUNT(*) FILTER (WHERE cv."agentId" = $3 AND cv.status NOT IN ('closed', 'pending', 'running')
			AND COALESCE(ct."isGroup", false) = $4 AND EXISTS (
				SELECT 1 FROM messages unread WHERE unread."conversationId" = cv.id
				AND unread."isFromMe" = false AND unread."isRead" = false AND unread."isDeleted" = false)),
		COUNT(*) FILTER (WHERE (cv."agentId" IS NULL OR cv.status = 'running') AND cv.status <> 'closed'
			AND COALESCE(ct."isGroup", false) = $4 AND EXISTS (
				SELECT 1 FROM messages unread WHERE unread."conversationId" = cv.id
				AND unread."isFromMe" = false AND unread."isRead" = false AND unread."isDeleted" = false))
		FROM conversations cv
		INNER JOIN contacts ct ON ct.id = cv."contactId" AND ct."companyId" = $1
		WHERE cv."companyId" = $1 AND ($2 OR cv."agentId" = $3
			OR cv."departmentId" IN (SELECT ad."departmentId" FROM agent_departments ad WHERE ad."agentId" = $3)
			OR (cv."agentId" IS NULL AND cv."departmentId" IS NULL AND cv.status <> 'closed'))`,
		agent.CompanyId, agent.IsAdmin(), agent.Id, groups).Scan(&counts.Individual, &counts.Group, &counts.ActiveUnread, &counts.PendingUnread)
	return counts, err
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

func visibleConversationQuery(selector string) string {
	return `SELECT cv.id, cv.status, cv."contactId", COALESCE(cv."connectionId", 0), cv."agentId", cv."departmentId", cv.url, cv."createdAt", cv."updatedAt",
		ct.id, ct.name, ct.number, ct.email, ct."companyId", COALESCE(ct.jid, ''), COALESCE(ct.lid, ''), COALESCE(ct."isGroup", false), COALESCE(ct."isBlocked", false),
		COALESCE(owner.name, ''), COALESCE(d.name, ''), COALESCE(d.color, '#E7E5E4'), cv.protocol
		FROM conversations cv
		INNER JOIN contacts ct ON ct.id = cv."contactId" AND ct."companyId" = $2
		LEFT JOIN agents owner ON owner.id = cv."agentId"
		LEFT JOIN departments d ON d.id = cv."departmentId"
		WHERE cv."companyId" = $2 AND (` + selector + `) AND ($3 OR cv."agentId" = $4
			OR cv."departmentId" IN (SELECT ad."departmentId" FROM agent_departments ad WHERE ad."agentId" = $4)
			OR (cv."agentId" IS NULL AND cv."departmentId" IS NULL AND cv.status <> 'closed'))`
}

func GetVisibleConversationByURL(url uuid.UUID, agent Agent) (Conversation, error) {
	return getVisibleConversation(visibleConversationQuery("cv.url = $1"), url, agent)
}

func GetVisibleConversationByProtocol(protocol int64, agent Agent) (Conversation, error) {
	if protocol <= 0 {
		return Conversation{}, sql.ErrNoRows
	}
	return getVisibleConversation(visibleConversationQuery(`cv.protocol = $1 AND cv."companyId" = $2`), protocol, agent)
}

func getVisibleConversation(query string, value any, agent Agent) (Conversation, error) {
	var conversation Conversation
	err := db.QueryRow(query, value, agent.CompanyId, agent.IsAdmin(), agent.Id).Scan(
		&conversation.Id, &conversation.Status, &conversation.Contact.Id, &conversation.ConnectionID,
		&conversation.AgentID, &conversation.DepartmentID, &conversation.URL, &conversation.CreatedAt, &conversation.UpdatedAt,
		&conversation.Contact.Id, &conversation.Contact.Name, &conversation.Contact.Number, &conversation.Contact.Email, &conversation.Contact.CompanyId,
		&conversation.Contact.JID, &conversation.Contact.LID, &conversation.Contact.IsGroup, &conversation.Contact.IsBlocked,
		&conversation.AgentName, &conversation.DepartmentName, &conversation.DepartmentColor, &conversation.Protocol,
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

func setConversation(connectionId int, contact Contact, reopenClosed bool, preventCreation ...bool) (int, error) {
	conversationId, err := checkExistentConversation(connectionId, contact, reopenClosed, preventCreation...)
	if err != nil {
		return 0, err
	}
	return conversationId, nil
}

func checkExistentConversation(connectionId int, contact Contact, reopenClosed bool, preventCreation ...bool) (int, error) {
	prevent := len(preventCreation) > 0 && preventCreation[0]
	var contactId int
	var err error
	if prevent {
		err = db.QueryRow(`SELECT id FROM contacts WHERE "companyId"=$1 AND COALESCE("isGroup",false)=$2
			AND (($3<>'' AND number=$3) OR ($4<>'' AND jid=$4) OR ($5<>'' AND lid=$5)) ORDER BY CASE WHEN $3<>'' AND number=$3 THEN 0 ELSE 1 END,id LIMIT 1`, contact.CompanyId, contact.IsGroup, contact.Number, contact.JID, contact.LID).Scan(&contactId)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrPhoneConversationCreationDisabled
		}
	} else {
		contactId, err = FindOrCreateContact(contact)
	}
	if err != nil {
		return 0, err
	}
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock($1, $2)`, contactId, connectionId); err != nil {
		return 0, err
	}
	var conversationId int
	var status string
	created := false
	err = tx.QueryRow(`SELECT id, status FROM conversations
		WHERE "contactId" = $1 AND "connectionId" = $2
		ORDER BY id DESC LIMIT 1 FOR UPDATE`, contactId, connectionId).Scan(&conversationId, &status)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if errors.Is(err, sql.ErrNoRows) || (reopenClosed && (status == ConversationStatusClosed || status == "resolved")) {
		if prevent {
			return 0, ErrPhoneConversationCreationDisabled
		}
		now := time.Now()
		err = tx.QueryRow(`INSERT INTO conversations
			(status, "contactId", "connectionId", "agentId", "departmentId", url, "createdAt", "updatedAt")
			VALUES ($1, $2, $3, NULL, NULL, $4, $5, $5) RETURNING id`,
			ConversationStatusOpen, contactId, connectionId, uuid.New(), now).Scan(&conversationId)
		if err != nil {
			return 0, err
		}
		created = true
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	if created {
		if err := RequestContactAvatarRefresh(contactId); err != nil {
			return 0, err
		}
	}

	return conversationId, nil
}

func StartFollowingConversation(id int) (Conversation, error) {
	previous, err := GetConversationById(id)
	if err != nil {
		return Conversation{}, err
	}
	if previous.ConnectionID <= 0 {
		return Conversation{}, sql.ErrNoRows
	}
	tx, err := db.Begin()
	if err != nil {
		return Conversation{}, err
	}
	defer tx.Rollback()

	if _, err = tx.Exec(`SELECT pg_advisory_xact_lock($1,$2)`, previous.Contact.Id, previous.ConnectionID); err != nil {
		return Conversation{}, err
	}
	var latestID int
	var status string
	if err = tx.QueryRow(`SELECT id,status FROM conversations WHERE "contactId"=$1 AND "connectionId"=$2 ORDER BY id DESC LIMIT 1 FOR UPDATE`, previous.Contact.Id, previous.ConnectionID).Scan(&latestID, &status); err != nil {
		return Conversation{}, err
	}
	if status == ConversationStatusClosed || status == "resolved" {
		if err = tx.QueryRow(`INSERT INTO conversations (status,"contactId","connectionId","agentId","departmentId",url,"createdAt","updatedAt") VALUES('open',$1,$2,NULL,NULL,$3,now(),now()) RETURNING id`, previous.Contact.Id, previous.ConnectionID, uuid.New()).Scan(&latestID); err != nil {
			return Conversation{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return Conversation{}, err
	}
	return GetConversationById(latestID)
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
	if _, err = tx.Exec(`UPDATE ai_agent_sessions SET status = 'ended', "endedAt" = now(), "updatedAt" = now()
		WHERE "conversationId" = $1 AND status = 'active'`, conversationID); err != nil {
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

func TransferConversationByURL(url uuid.UUID, companyID, actorID int, isAdmin bool, targetAgentID, targetDepartmentID *int) error {
	if targetDepartmentID == nil || *targetDepartmentID <= 0 || (targetAgentID != nil && *targetAgentID <= 0) {
		return ErrInvalidConversationTransferTarget
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var conversationID int
	err = tx.QueryRow(`SELECT cv.id FROM conversations cv
		INNER JOIN connections co ON co.id = cv."connectionId" AND co."companyId" = $2
		WHERE cv.url = $1 AND cv.status <> 'closed' AND (cv."agentId" = $3 OR $4)
		FOR UPDATE`, url, companyID, actorID, isAdmin).Scan(&conversationID)
	if err != nil {
		return err
	}

	var valid bool
	if targetAgentID == nil {
		err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM departments WHERE id = $1 AND "companyId" = $2 AND "isActive" = true)`, *targetDepartmentID, companyID).Scan(&valid)
	} else {
		err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM agents a
			INNER JOIN agent_departments ad ON ad."agentId" = a.id AND ad."departmentId" = $2
			INNER JOIN departments d ON d.id = ad."departmentId" AND d."companyId" = $3 AND d."isActive" = true
			WHERE a.id = $1 AND a."companyId" = $3 AND a."isActive" = true)`, *targetAgentID, *targetDepartmentID, companyID).Scan(&valid)
	}
	if err != nil {
		return err
	}
	if !valid {
		return ErrInvalidConversationTransferTarget
	}

	if _, err = tx.Exec(`UPDATE flow_executions SET status = 'failed',
		"errorMessage" = 'execução cancelada porque a conversa foi transferida',
		"completedAt" = now(), "updatedAt" = now()
		WHERE "conversationId" = $1 AND status IN ('running', 'waiting')`, conversationID); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE ai_agent_sessions SET status = 'ended', "endedAt" = now(), "updatedAt" = now()
		WHERE "conversationId" = $1 AND status = 'active'`, conversationID); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE conversations SET "agentId" = $1, "departmentId" = $2,
		status = $3, "updatedAt" = now() WHERE id = $4`, targetAgentID, *targetDepartmentID, ConversationStatusOpen, conversationID); err != nil {
		return err
	}
	return tx.Commit()
}

func AutoAssignConversation(conversationID, companyID int, strategy string) error {
	orderBy := `count(workload.id), agent.id`
	if strategy == "random" {
		orderBy = `random()`
	}
	query := `WITH selected AS (
		SELECT agent.id FROM agents agent
		LEFT JOIN conversations workload ON workload."agentId" = agent.id AND workload.status = 'open'
		WHERE agent."companyId" = $1 AND agent."isActive" = true
		GROUP BY agent.id ORDER BY ` + orderBy + ` LIMIT 1
	) UPDATE conversations conversation SET "agentId" = selected.id, status = 'open', "updatedAt" = now()
	FROM selected, connections connection
	WHERE conversation.id = $2 AND conversation."agentId" IS NULL
		AND connection.id = conversation."connectionId" AND connection."companyId" = $1`
	_, err := db.Exec(query, companyID, conversationID)
	return err
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
	if _, err := tx.Exec(`UPDATE ai_agent_sessions SET status = 'ended', "endedAt" = now(), "updatedAt" = now()
		WHERE "conversationId" = $1 AND status = 'active'`, conversationID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`UPDATE conversations SET "agentId" = $1, status = $2, "updatedAt" = now() WHERE id = $3`,
		agent.Id, ConversationStatusOpen, conversationID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`UPDATE contacts SET "avatarRefreshRequestedAt" = now()
		WHERE id = (SELECT "contactId" FROM conversations WHERE id = $1)`, conversationID); err != nil {
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
	if _, err := tx.Exec(`UPDATE ai_agent_sessions SET status = 'ended', "endedAt" = now(), "updatedAt" = now()
		WHERE "conversationId" = $1 AND status = 'active'`, conversationID); err != nil {
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
