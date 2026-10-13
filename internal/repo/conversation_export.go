package repo

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
)

type ConversationExport struct {
	Contact          Contact
	CompanyName      string
	Timezone         string
	AllConversations bool
	GeneratedAt      time.Time
	Conversations    []ExportedConversation
}

type ExportedConversation struct {
	ID               int
	Protocol         int64
	URL              uuid.UUID
	Status           string
	ConnectionName   string
	ConnectionNumber string
	AgentName        string
	DepartmentName   string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	Entries          []ConversationExportEntry
}

type ConversationExportEntry struct {
	ID           int64
	Kind         string
	Text         string
	MessageID    string
	QuotedID     string
	MediaType    string
	MediaPath    string
	SenderName   string
	SenderNumber string
	IsFromMe     bool
	IsDeleted    bool
	CreatedAt    time.Time
}

const conversationExportAnchorSQL = `SELECT cv.id, ct.id, ct.name, ct.number, ct.email,
	ct.company, COALESCE(ct."isGroup", false), company.name, company.timezone
	FROM conversations cv JOIN contacts ct ON ct.id = cv."contactId"
	JOIN companies company ON company.id = ct."companyId"
	WHERE cv.url = $1 AND cv."companyId" = $2 AND ct."companyId" = $2`

// Contact ownership keeps archived conversations accessible after a connection
// has been removed, while both export modes stay scoped to the company.
const conversationExportScopeSQL = `WITH scoped AS (
	SELECT cv.* FROM conversations cv JOIN contacts ct ON ct.id = cv."contactId"
	WHERE cv."companyId" = $1 AND ct."companyId" = $1 AND (($3 AND cv."contactId" = $4) OR cv.id = $2)
) `

const conversationExportListSQL = conversationExportScopeSQL + `SELECT cv.id, cv.url, cv.status,
	COALESCE(co.name, 'Conexão removida'), COALESCE(co.number, ''), COALESCE(a.name, 'Sem agente'),
	COALESCE(d.name, 'Sem fila'), cv."createdAt", cv."updatedAt", cv.protocol
	FROM scoped cv
	LEFT JOIN connections co ON co.id = cv."connectionId" AND co."companyId" = $1
	LEFT JOIN agents a ON a.id = cv."agentId" AND a."companyId" = $1
	LEFT JOIN departments d ON d.id = cv."departmentId" AND d."companyId" = $1
	ORDER BY cv."createdAt", cv.id`

const conversationExportEntriesSQL = conversationExportScopeSQL + `SELECT entries.* FROM (
	SELECT m."conversationId", m.id::bigint, 'message'::text AS kind,
		CASE WHEN m."isDeleted" THEN '' ELSE COALESCE(m.text, '') END AS content,
		COALESCE(m."messageId", ''), COALESCE(m."quotedId", ''), COALESCE(m."mediaType", ''),
		CASE WHEN m."isDeleted" THEN '' ELSE COALESCE(m."mediaPath", '') END,
		COALESCE(NULLIF(sender_contact.name, ''), m."senderName", '') AS "senderName",
		COALESCE(NULLIF(sender_contact.number, ''), CASE WHEN lower(m.sender) LIKE '%@s.whatsapp.net' THEN split_part(m.sender, '@', 1) ELSE '' END) AS "senderNumber",
		m."isFromMe", m."isDeleted", m."createdAt"
	FROM messages m JOIN scoped cv ON cv.id = m."conversationId"
	LEFT JOIN LATERAL (
		SELECT participant.name, participant.number FROM contacts participant
		WHERE participant."companyId" = $1 AND COALESCE(participant."isGroup", false) = false
		AND (participant.jid = m.sender OR participant.lid = m.sender
			OR (participant.number <> '' AND participant.number = split_part(m.sender, '@', 1)))
		ORDER BY CASE WHEN participant.jid = m.sender OR participant.lid = m.sender THEN 0 ELSE 1 END, participant.id
		LIMIT 1
	) sender_contact ON COALESCE(m.sender, '') <> '' AND m."isFromMe" = false
	UNION ALL
	SELECT e."conversationId", e.id, 'note'::text, e.content, '', '', '', '',
		COALESCE(e.metadata->>'authorName', ''), '', false, false, e."createdAt"
	FROM conversation_events e JOIN scoped cv ON cv.id = e."conversationId"
	WHERE e."companyId" = $1 AND e."eventType" = 'note'
) entries ORDER BY entries."conversationId", entries."createdAt", entries.kind, entries.id`

func GetConversationExport(ctx context.Context, companyID int, conversationURL uuid.UUID, all bool) (ConversationExport, error) {
	export := ConversationExport{AllConversations: all, GeneratedAt: time.Now(), Conversations: []ExportedConversation{}}
	// Read one snapshot so new messages and transfers cannot change the selected
	// histories between the metadata and transcript queries.
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return export, err
	}
	defer tx.Rollback()
	var anchorID int
	err = tx.QueryRowContext(ctx, conversationExportAnchorSQL, conversationURL, companyID).Scan(
		&anchorID, &export.Contact.Id, &export.Contact.Name, &export.Contact.Number, &export.Contact.Email,
		&export.Contact.Company, &export.Contact.IsGroup, &export.CompanyName, &export.Timezone)
	if err != nil {
		return export, err
	}
	export.Contact.CompanyId = companyID
	args := []any{companyID, anchorID, all, export.Contact.Id}
	rows, err := tx.QueryContext(ctx, conversationExportListSQL, args...)
	if err != nil {
		return export, err
	}
	indices := map[int]int{}
	for rows.Next() {
		var conversation ExportedConversation
		if err := rows.Scan(&conversation.ID, &conversation.URL, &conversation.Status, &conversation.ConnectionName,
			&conversation.ConnectionNumber, &conversation.AgentName, &conversation.DepartmentName, &conversation.CreatedAt, &conversation.UpdatedAt, &conversation.Protocol); err != nil {
			rows.Close()
			return export, err
		}
		indices[conversation.ID] = len(export.Conversations)
		export.Conversations = append(export.Conversations, conversation)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return export, err
	}
	entries, err := tx.QueryContext(ctx, conversationExportEntriesSQL, args...)
	if err != nil {
		return export, err
	}
	for entries.Next() {
		var conversationID int
		var entry ConversationExportEntry
		if err := entries.Scan(&conversationID, &entry.ID, &entry.Kind, &entry.Text, &entry.MessageID, &entry.QuotedID,
			&entry.MediaType, &entry.MediaPath, &entry.SenderName, &entry.SenderNumber, &entry.IsFromMe, &entry.IsDeleted, &entry.CreatedAt); err != nil {
			entries.Close()
			return export, err
		}
		if index, ok := indices[conversationID]; ok {
			export.Conversations[index].Entries = append(export.Conversations[index].Entries, entry)
		}
	}
	err = entries.Err()
	entries.Close()
	if err != nil {
		return export, err
	}
	return export, tx.Commit()
}
