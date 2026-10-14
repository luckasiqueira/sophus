package repo

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

var ErrConversationUnavailable = errors.New("conversation is assigned to another agent")

type ContactSearchResult struct {
	Id                    int        `json:"id"`
	Name                  string     `json:"name"`
	Number                string     `json:"number"`
	Email                 string     `json:"email"`
	Company               string     `json:"company"`
	ConnectionId          int        `json:"connectionId"`
	ConnectionName        string     `json:"connectionName"`
	ConnectionStatus      string     `json:"connectionStatus"`
	AvatarURL             string     `json:"avatarUrl"`
	Tags                  []string   `json:"tags"`
	TagIds                []int64    `json:"tagIds"`
	TagColors             []string   `json:"tagColors"`
	LatestConversationURL *uuid.UUID `json:"latestConversationURL"`
	LatestStatus          string     `json:"latestStatus"`
	IsGroup               bool       `json:"isGroup"`
}

type ContactSearchPage struct {
	Data     []ContactSearchResult `json:"data"`
	Page     int                   `json:"page"`
	PageSize int                   `json:"pageSize"`
	Total    int                   `json:"total"`
}

type ConversationStartResult struct {
	URL      uuid.UUID `json:"url"`
	Created  bool      `json:"created"`
	Reopened bool      `json:"reopened"`
	Reused   bool      `json:"reused"`
}

type ConnectionOption struct {
	Id     int    `json:"id"`
	Name   string `json:"name"`
	Number string `json:"number"`
	Status string `json:"status"`
}

func ListCompanyConnectionOptions(companyID int, connectedOnly bool) ([]ConnectionOption, error) {
	rows, err := db.Query(`SELECT id, name, number, status FROM connections
		WHERE "companyId" = $1 AND (NOT $2 OR status = 'connected') ORDER BY name, id`, companyID, connectedOnly)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	options := []ConnectionOption{}
	for rows.Next() {
		var option ConnectionOption
		if err := rows.Scan(&option.Id, &option.Name, &option.Number, &option.Status); err != nil {
			return nil, err
		}
		options = append(options, option)
	}
	return options, rows.Err()
}

func SearchCompanyContacts(companyID int, connectionID *int, tagID *int64, query string, page, pageSize int) (ContactSearchPage, error) {
	query = strings.TrimSpace(query)
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 50
	}
	var selectedConnection any
	if connectionID != nil {
		selectedConnection = *connectionID
	}
	var selectedTag any
	if tagID != nil {
		selectedTag = *tagID
	}
	result := ContactSearchPage{Data: []ContactSearchResult{}, Page: page, PageSize: pageSize}
	rows, err := db.Query(`SELECT ct.id, ct.name, ct.number, ct.email, ct.company, COALESCE(latest."connectionId", 0), COALESCE(latest.connection_name, ''), COALESCE(latest.connection_status, ''), COALESCE(ct."avatarUrl", ''),
		latest.url, COALESCE(latest.status, ''), COALESCE(contact_tags.tags, '{}'), COALESCE(contact_tags.tag_ids, '{}'), COALESCE(contact_tags.tag_colors, '{}')
		, COALESCE(ct."isGroup", false), COUNT(*) OVER()
		FROM contacts ct
		LEFT JOIN LATERAL (
			SELECT cv.url, cv.status, cv."connectionId", co.name AS connection_name, co.status AS connection_status
			FROM conversations cv JOIN connections co ON co.id = cv."connectionId" AND co."companyId" = $1
			WHERE cv."contactId" = ct.id AND ($2::integer IS NULL OR cv."connectionId" = $2) ORDER BY cv.id DESC LIMIT 1
		) latest ON true
		LEFT JOIN LATERAL (
			SELECT array_agg(tag.name ORDER BY lower(tag.name)) AS tags, array_agg(tag.id ORDER BY lower(tag.name)) AS tag_ids,
				array_agg(tag.color ORDER BY lower(tag.name)) AS tag_colors
			FROM contact_tag_assignments assignment JOIN company_tags tag ON tag.id = assignment."tagId"
			WHERE assignment."contactId" = ct.id AND tag."companyId" = ct."companyId"
		) contact_tags ON true
		WHERE ct."companyId" = $1 AND COALESCE(ct."isBlocked", false) = false
			AND ($3 = '' OR ct.name ILIKE '%' || $3 || '%' OR ct.email ILIKE '%' || $3 || '%' OR ct.company ILIKE '%' || $3 || '%'
				OR ct.number ILIKE '%' || $3 || '%'
				OR (regexp_replace($3, '[^0-9]', '', 'g') <> '' AND regexp_replace(ct.number, '[^0-9]', '', 'g') LIKE '%' || regexp_replace($3, '[^0-9]', '', 'g') || '%')
				OR EXISTS (SELECT 1 FROM contact_tag_assignments assignment JOIN company_tags tag ON tag.id = assignment."tagId"
					WHERE assignment."contactId" = ct.id AND tag."companyId" = ct."companyId" AND tag.name ILIKE '%' || $3 || '%'))
			AND ($6::bigint IS NULL OR EXISTS (SELECT 1 FROM contact_tag_assignments assignment JOIN company_tags tag ON tag.id = assignment."tagId"
				WHERE assignment."contactId" = ct.id AND tag.id = $6 AND tag."companyId" = ct."companyId"))
		ORDER BY COALESCE(ct."isGroup", false), lower(ct.name), ct.id LIMIT $4 OFFSET $5`, companyID, selectedConnection, query, pageSize, (page-1)*pageSize, selectedTag)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var contact ContactSearchResult
		var conversationURL uuid.NullUUID
		var tags pq.StringArray
		var tagIDs pq.Int64Array
		var tagColors pq.StringArray
		var total int
		if err := rows.Scan(&contact.Id, &contact.Name, &contact.Number, &contact.Email, &contact.Company, &contact.ConnectionId,
			&contact.ConnectionName, &contact.ConnectionStatus, &contact.AvatarURL, &conversationURL, &contact.LatestStatus, &tags, &tagIDs, &tagColors, &contact.IsGroup, &total); err != nil {
			return result, err
		}
		contact.Tags = []string(tags)
		contact.TagIds = []int64(tagIDs)
		contact.TagColors = []string(tagColors)
		if conversationURL.Valid {
			value := conversationURL.UUID
			contact.LatestConversationURL = &value
		}
		result.Data = append(result.Data, contact)
		result.Total = total
	}
	return result, rows.Err()
}

type ContactUpdateInput struct {
	Name    string
	Number  string
	Email   string
	Company string
}

// Cast the shared phone parameter explicitly: assignment to varchar and text
// concatenation otherwise infer conflicting types in PostgreSQL (42P08).
const updateCompanyContactSQL = `UPDATE contacts ct SET name = $1, number = $2::text,
		jid = CASE WHEN COALESCE(ct.jid, '') = '' OR lower(ct.jid) LIKE '%@s.whatsapp.net' THEN $2::text || '@s.whatsapp.net' ELSE ct.jid END,
		email = $3, company = $4
		WHERE ct.id = $5 AND ct."companyId" = $6
		AND COALESCE(ct."isGroup", false) = false`

func UpdateCompanyContact(contactID, companyID int, input ContactUpdateInput) error {
	clearHistory := captureContactHistoryInvalidation(contactID, companyID)
	defer clearHistory()
	defer invalidateAvatarCache(contactID)
	result, err := db.Exec(updateCompanyContactSQL, input.Name, input.Number, input.Email, input.Company, contactID, companyID)
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
	return nil
}

func DeleteCompanyContact(contactID, companyID int) error {
	clearHistory := captureContactHistoryInvalidation(contactID, companyID)
	defer clearHistory()
	defer invalidateAvatarCache(contactID)
	result, err := db.Exec(`DELETE FROM contacts WHERE id = $1 AND "companyId" = $2 AND COALESCE("isGroup", false) = false`, contactID, companyID)
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
	return nil
}

func StartOrReuseConversation(agent Agent, contactID, connectionID int) (ConversationStartResult, error) {
	tx, err := db.Begin()
	if err != nil {
		return ConversationStartResult{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock($1,$2)`, contactID, connectionID); err != nil {
		return ConversationStartResult{}, err
	}
	var contactConnectionID int
	var blocked, group bool
	var connectionStatus string
	err = tx.QueryRow(`SELECT co.id, co.status, COALESCE(ct."isBlocked", false), COALESCE(ct."isGroup", false)
		FROM contacts ct JOIN connections co ON co.id = $2 AND co."companyId" = ct."companyId"
		WHERE ct.id = $1 AND ct."companyId" = $3 FOR UPDATE OF ct, co`,
		contactID, connectionID, agent.CompanyId).Scan(&contactConnectionID, &connectionStatus, &blocked, &group)
	if err != nil {
		return ConversationStartResult{}, err
	}
	if connectionStatus != ConnectionStatusConnected || blocked || group {
		return ConversationStartResult{}, ErrConversationUnavailable
	}
	var conversationID int
	var status string
	var conversationURL uuid.UUID
	var ownerID sql.NullInt64
	err = tx.QueryRow(`SELECT id, status, url, "agentId" FROM conversations
		WHERE "contactId" = $1 AND "connectionId" = $2 ORDER BY id DESC LIMIT 1 FOR UPDATE`,
		contactID, contactConnectionID).Scan(&conversationID, &status, &conversationURL, &ownerID)
	result := ConversationStartResult{}
	switch {
	case errors.Is(err, sql.ErrNoRows):
		conversationURL = uuid.New()
		if err := tx.QueryRow(`INSERT INTO conversations
			(status, "contactId", "connectionId", "agentId", url, "createdAt", "updatedAt")
			VALUES ('open', $1, $2, $3, $4, now(), now()) RETURNING id`, contactID, connectionID, agent.Id, conversationURL).Scan(&conversationID); err != nil {
			return ConversationStartResult{}, err
		}
		result.Created = true
	case err != nil:
		return ConversationStartResult{}, err
	case status == ConversationStatusClosed || status == "resolved":
		conversationURL = uuid.New()
		if err := tx.QueryRow(`INSERT INTO conversations (status,"contactId","connectionId","agentId","departmentId",url,"createdAt","updatedAt") VALUES('open',$1,$2,$3,NULL,$4,now(),now()) RETURNING id`, contactID, connectionID, agent.Id, conversationURL).Scan(&conversationID); err != nil {
			return ConversationStartResult{}, err
		}
		result.Created = true
	case status == ConversationStatusRunning || !ownerID.Valid || ownerID.Int64 == 0 || ownerID.Int64 == int64(agent.Id) || agent.IsAdmin():
		if _, err := tx.Exec(`UPDATE flow_executions SET status = 'failed', "errorMessage" = 'execução cancelada porque a conversa foi iniciada manualmente',
			"completedAt" = now(), "updatedAt" = now() WHERE "conversationId" = $1 AND status IN ('running', 'waiting')`, conversationID); err != nil {
			return ConversationStartResult{}, err
		}
		if _, err := tx.Exec(`UPDATE conversations SET status = 'open', "agentId" = $1, "departmentId" = NULL, "updatedAt" = now() WHERE id = $2`, agent.Id, conversationID); err != nil {
			return ConversationStartResult{}, err
		}
		result.Reused = true
	default:
		return ConversationStartResult{}, ErrConversationUnavailable
	}
	if _, err := tx.Exec(`UPDATE contacts SET "avatarRefreshRequestedAt" = now() WHERE id = $1`, contactID); err != nil {
		return ConversationStartResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ConversationStartResult{}, err
	}
	result.URL = conversationURL
	return result, nil
}

func GetConversationForCompanyByURL(companyID int, conversationURL uuid.UUID) (Conversation, error) {
	var conversation Conversation
	err := db.QueryRow(`SELECT cv.id, cv.status, cv."contactId", cv."connectionId", cv."agentId", cv."departmentId", cv.url,
		cv."createdAt", cv."updatedAt", cv.protocol FROM conversations cv
		JOIN connections co ON co.id = cv."connectionId" AND co."companyId" = $2 WHERE cv.url = $1`,
		conversationURL, companyID).Scan(&conversation.Id, &conversation.Status, &conversation.Contact.Id,
		&conversation.ConnectionID, &conversation.AgentID, &conversation.DepartmentID, &conversation.URL,
		&conversation.CreatedAt, &conversation.UpdatedAt, &conversation.Protocol)
	return conversation, err
}
