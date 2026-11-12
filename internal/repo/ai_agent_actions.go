package repo

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
)

// Actions an AI agent runs on its own conversation. Each one only applies
// while the session that requested it is still active, so a human takeover
// in the meantime wins.

var ErrAIAgentSessionInactive = errors.New("sessão do agente de IA não está mais ativa")

const aiSessionGuard = `SELECT 1 FROM ai_agent_sessions ai_session WHERE ai_session.id = $1 AND ai_session."conversationId" = $2
	AND ai_session."companyId" = $3 AND ai_session.status = 'active'`

func lockAIAgentSession(tx *sql.Tx, sessionID int64, conversationID, companyID int) error {
	var exists int
	err := tx.QueryRow(aiSessionGuard+` FOR UPDATE`, sessionID, conversationID, companyID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAIAgentSessionInactive
	}
	return err
}

// ValidateAIAgentTransferTarget reports whether an active agent or department
// of the company can receive a conversation.
func ValidateAIAgentTransferTarget(companyID int, assignType string, assignID int) error {
	var exists bool
	var err error
	switch assignType {
	case "agent":
		err = db.QueryRow(`SELECT EXISTS(SELECT 1 FROM agents WHERE id = $1 AND "companyId" = $2 AND "isActive" = true)`, assignID, companyID).Scan(&exists)
	case "department":
		err = db.QueryRow(`SELECT EXISTS(SELECT 1 FROM departments WHERE id = $1 AND "companyId" = $2 AND "isActive" = true)`, assignID, companyID).Scan(&exists)
	default:
		return errors.New("tipo de transferência inválido")
	}
	if err != nil {
		return err
	}
	if !exists {
		return errors.New("destino da transferência não existe ou está inativo")
	}
	return nil
}

// addAIAgentHistory records what the AI agent did in the conversation timeline.
func addAIAgentHistory(tx *sql.Tx, conversationID, companyID int, content string) error {
	content = strings.ToValidUTF8(strings.TrimSpace(content), "")
	if len(content) > 9000 {
		content = strings.ToValidUTF8(content[:9000], "")
	}
	_, err := tx.Exec(`INSERT INTO conversation_events ("companyId", "conversationId", "eventType", content) VALUES ($1, $2, 'history', $3)`,
		companyID, conversationID, content)
	return err
}

// TransferAIAgentConversation hands the conversation to an agent (open) or a
// department queue (pending), records the AI's summary and ends the session.
func TransferAIAgentConversation(sessionID int64, conversationID, companyID int, assignType string, assignID int, summary string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockAIAgentSession(tx, sessionID, conversationID, companyID); err != nil {
		return err
	}
	var result sql.Result
	switch assignType {
	case "agent":
		result, err = tx.Exec(`UPDATE conversations cv
			SET "agentId" = a.id,
				"departmentId" = CASE WHEN cv."departmentId" IS NULL OR EXISTS (
					SELECT 1 FROM agent_departments ad WHERE ad."agentId" = a.id AND ad."departmentId" = cv."departmentId"
				) THEN cv."departmentId" ELSE NULL END,
				status = $1, "updatedAt" = now()
			FROM agents a, connections co
			WHERE cv.id = $2 AND a.id = $3 AND a."companyId" = $4 AND a."isActive" = true
			AND co.id = cv."connectionId" AND co."companyId" = $4`, ConversationStatusOpen, conversationID, assignID, companyID)
	case "department":
		result, err = tx.Exec(`UPDATE conversations cv
			SET "departmentId" = d.id, "agentId" = NULL, status = $1, "updatedAt" = now()
			FROM departments d, connections co
			WHERE cv.id = $2 AND d.id = $3 AND d."companyId" = $4 AND d."isActive" = true
			AND co.id = cv."connectionId" AND co."companyId" = $4`, ConversationStatusPending, conversationID, assignID, companyID)
	default:
		return errors.New("tipo de transferência inválido")
	}
	if err != nil {
		return err
	}
	if rows, err := result.RowsAffected(); err != nil {
		return err
	} else if rows != 1 {
		return errors.New("destino da transferência não existe ou está inativo")
	}
	if _, err := tx.Exec(`UPDATE ai_agent_sessions SET status = 'ended', "endedAt" = now(), "updatedAt" = now() WHERE id = $1`, sessionID); err != nil {
		return err
	}
	history := "Agente de IA transferiu o atendimento."
	if summary = strings.TrimSpace(summary); summary != "" {
		history += " Resumo: " + summary
	}
	if err := addAIAgentHistory(tx, conversationID, companyID, history); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	invalidateConversationEventHistory(companyID, conversationID)
	return nil
}

// CloseAIAgentConversation closes the conversation and ends the AI session.
func CloseAIAgentConversation(sessionID int64, conversationID, companyID int, summary string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockAIAgentSession(tx, sessionID, conversationID, companyID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE ai_agent_sessions SET status = 'ended', "endedAt" = now(), "updatedAt" = now() WHERE id = $1`, sessionID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE conversations SET status = $1, "updatedAt" = now() WHERE id = $2`, ConversationStatusClosed, conversationID); err != nil {
		return err
	}
	history := "Agente de IA encerrou o atendimento."
	if summary = strings.TrimSpace(summary); summary != "" {
		history += " Resumo: " + summary
	}
	if err := addAIAgentHistory(tx, conversationID, companyID, history); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	invalidateConversationEventHistory(companyID, conversationID)
	return nil
}

// PauseAIAgentConversation silences the AI agent without changing who owns the
// conversation. minutes <= 0 pauses until a person transfers, accepts or
// closes it; otherwise the agent answers again after that time.
func PauseAIAgentConversation(sessionID int64, conversationID, companyID, minutes int, reason string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockAIAgentSession(tx, sessionID, conversationID, companyID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE ai_agent_sessions SET status = 'paused',
		"pausedUntil" = CASE WHEN $2 > 0 THEN now() + make_interval(mins => $2) ELSE NULL END, "updatedAt" = now()
		WHERE id = $1`, sessionID, minutes); err != nil {
		return err
	}
	history := "Agente de IA pausado até um atendente assumir."
	if minutes > 0 {
		history = "Agente de IA pausado por " + pauseDurationLabel(minutes) + "."
	}
	if reason = strings.TrimSpace(reason); reason != "" {
		history += " Motivo: " + reason
	}
	if err := addAIAgentHistory(tx, conversationID, companyID, history); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	invalidateConversationEventHistory(companyID, conversationID)
	return nil
}

func pauseDurationLabel(minutes int) string {
	switch {
	case minutes%1440 == 0:
		return strconv.Itoa(minutes/1440) + plural(minutes/1440, " dia", " dias")
	case minutes%60 == 0:
		return strconv.Itoa(minutes/60) + plural(minutes/60, " hora", " horas")
	}
	return strconv.Itoa(minutes) + plural(minutes, " minuto", " minutos")
}

func plural(count int, one, many string) string {
	if count == 1 {
		return one
	}
	return many
}

// AIAgentSessionPaused reports whether the conversation's AI agent is paused,
// first reactivating a pause whose time is over. While it is paused, incoming
// messages start neither the agent nor a flow.
func AIAgentSessionPaused(conversationID, companyID int) (bool, error) {
	if _, err := db.Exec(`UPDATE ai_agent_sessions SET status = 'active', "pausedUntil" = NULL, "updatedAt" = now()
		WHERE "conversationId" = $1 AND "companyId" = $2 AND status = 'paused' AND "pausedUntil" <= now()`, conversationID, companyID); err != nil {
		return false, err
	}
	var paused bool
	err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM ai_agent_sessions WHERE "conversationId" = $1 AND "companyId" = $2 AND status = 'paused')`,
		conversationID, companyID).Scan(&paused)
	return paused, err
}

// AddAIAgentNote posts an internal note, visible only to the team, written by
// the AI agent in the conversation.
func AddAIAgentNote(sessionID int64, conversationID, companyID int, agentName, content string) (ConversationEvent, error) {
	var event ConversationEvent
	content = strings.ToValidUTF8(strings.TrimSpace(content), "")
	if content == "" || len(content) > 10000 {
		return event, errors.New("a nota deve possuir entre 1 e 10000 caracteres")
	}
	metadata, err := json.Marshal(map[string]any{"authorName": "Agente de IA · " + agentName, "aiAgentSessionId": sessionID})
	if err != nil {
		return event, err
	}
	err = db.QueryRow(`INSERT INTO conversation_events ("companyId", "conversationId", "eventType", content, metadata)
		SELECT $3, conversation.id, 'note', $4, $5::jsonb FROM conversations conversation
		JOIN connections connection ON connection.id = conversation."connectionId" AND connection."companyId" = $3
		WHERE conversation.id = $2 AND EXISTS (`+aiSessionGuard+`)
		RETURNING id, "companyId", "conversationId", "executionId", "nodeId", "eventType", content, metadata, "createdAt"`,
		sessionID, conversationID, companyID, content, string(metadata)).Scan(
		&event.ID, &event.CompanyID, &event.ConversationID, &event.ExecutionID, &event.NodeID,
		&event.EventType, &event.Content, &event.Metadata, &event.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return event, ErrAIAgentSessionInactive
	}
	if err == nil {
		invalidateConversationEventHistory(companyID, conversationID)
	}
	return event, err
}

// AddAIAgentConversationTag tags the conversation's contact and returns the
// contact's tags.
func AddAIAgentConversationTag(sessionID int64, conversationID, companyID int, tag string) ([]string, error) {
	tag = strings.TrimSpace(tag)
	if tag == "" || len(tag) > 80 {
		return nil, errors.New("tag deve possuir entre 1 e 80 caracteres")
	}
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := lockAIAgentSession(tx, sessionID, conversationID, companyID); err != nil {
		return nil, err
	}
	var contactID int
	if err := tx.QueryRow(`SELECT conversation."contactId" FROM conversations conversation
		JOIN connections connection ON connection.id = conversation."connectionId"
		WHERE conversation.id = $1 AND connection."companyId" = $2 FOR UPDATE OF conversation`, conversationID, companyID).Scan(&contactID); err != nil {
		return nil, err
	}
	var tagID int64
	if err := tx.QueryRow(`INSERT INTO company_tags ("companyId", name) VALUES ($1, $2) ON CONFLICT ("companyId", (lower(btrim(name)))) DO UPDATE SET name = company_tags.name RETURNING id`, companyID, tag).Scan(&tagID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`INSERT INTO contact_tag_assignments ("contactId", "tagId") VALUES ($1, $2) ON CONFLICT DO NOTHING`, contactID, tagID); err != nil {
		return nil, err
	}
	var tags pq.StringArray
	if err := tx.QueryRow(`SELECT COALESCE(array_agg(catalog.name ORDER BY lower(catalog.name)), ARRAY[]::text[]) FROM contact_tag_assignments assignment JOIN company_tags catalog ON catalog.id = assignment."tagId" WHERE assignment."contactId" = $1 AND catalog."companyId" = $2`, contactID, companyID).Scan(&tags); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE conversations SET tags = $2, "updatedAt" = now() WHERE "contactId" = $1`, contactID, tags); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return []string(tags), nil
}

// UpdateAIAgentContact saves the contact's name or email.
func UpdateAIAgentContact(sessionID int64, conversationID, companyID int, field, value string) error {
	column := ""
	switch field {
	case "name":
		column = "name"
	case "email":
		column = "email"
	default:
		return errors.New("campo de contato inválido")
	}
	return expectOneRow(db.Exec(`UPDATE contacts contact SET `+column+` = $4
		FROM conversations conversation JOIN connections connection ON connection.id = conversation."connectionId"
		WHERE conversation.id = $2 AND contact.id = conversation."contactId" AND connection."companyId" = $3
		AND EXISTS (`+aiSessionGuard+`)`, sessionID, conversationID, companyID, value))
}

// SetAIAgentContactCustomField saves a text custom field on the contact.
func SetAIAgentContactCustomField(sessionID int64, conversationID, companyID int, key, value string) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return expectOneRow(db.Exec(`UPDATE contacts contact SET "customFields" = jsonb_set(contact."customFields", ARRAY[$4], $5::jsonb, true)
		FROM conversations conversation JOIN connections connection ON connection.id = conversation."connectionId"
		WHERE conversation.id = $2 AND contact.id = conversation."contactId" AND connection."companyId" = $3
		AND EXISTS (`+aiSessionGuard+`)`, sessionID, conversationID, companyID, key, string(encoded)))
}

func expectOneRow(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrAIAgentSessionInactive
	}
	return nil
}

// ScheduleAIAgentMessage queues a message the AI agent promised to send
// later, delivered by the scheduled flow messages worker.
func ScheduleAIAgentMessage(sessionID int64, conversationID, companyID int, actionID, message string, dueAt time.Time) error {
	message = strings.TrimSpace(message)
	if message == "" || len([]rune(message)) > 10000 {
		return errors.New("a mensagem agendada deve possuir entre 1 e 10000 caracteres")
	}
	nodeID := "ai:" + strconv.FormatInt(sessionID, 10) + ":" + actionID + ":" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if len(nodeID) > 100 {
		nodeID = nodeID[:100]
	}
	return expectOneRow(db.Exec(`INSERT INTO scheduled_flow_messages
		("companyId", "conversationId", "connectionId", "nodeId", message, "dueAt")
		SELECT $3, conversation.id, conversation."connectionId", $4, $5, $6
		FROM conversations conversation
		JOIN connections connection ON connection.id = conversation."connectionId" AND connection."companyId" = $3
		WHERE conversation.id = $2 AND EXISTS (`+aiSessionGuard+`)`, sessionID, conversationID, companyID, nodeID, message, dueAt))
}

// AIAgentSmartTransferAvailable reports whether an active agent (of the
// department, when given) can receive the conversation.
func AIAgentSmartTransferAvailable(companyID int, departmentID *int) (bool, error) {
	var available bool
	err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM agents agent WHERE agent."companyId" = $1 AND agent."isActive" = true
		AND ($2::integer IS NULL OR EXISTS (SELECT 1 FROM agent_departments membership
			WHERE membership."agentId" = agent.id AND membership."departmentId" = $2)))`, companyID, departmentID).Scan(&available)
	return available, err
}

// SmartTransferAIAgentConversation hands the conversation to the active agent
// with the fewest open conversations (or a random one), within the department
// when given, and ends the AI session. Without an available agent it falls
// back to the department queue; without a department it fails.
func SmartTransferAIAgentConversation(sessionID int64, conversationID, companyID int, departmentID *int, strategy, summary string) (string, error) {
	tx, err := db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if err := lockAIAgentSession(tx, sessionID, conversationID, companyID); err != nil {
		return "", err
	}
	lockKey := 0
	if departmentID != nil {
		lockKey = *departmentID
	}
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock($1, $2)`, companyID, lockKey); err != nil {
		return "", err
	}
	orderBy := `count(workload.id), agent.id`
	if strategy == "random" {
		orderBy = `random()`
	}
	var agentID int
	var agentName string
	err = tx.QueryRow(`SELECT agent.id, agent.name FROM agents agent
		LEFT JOIN conversations workload ON workload."agentId" = agent.id AND workload.status = 'open'
		WHERE agent."companyId" = $1 AND agent."isActive" = true
		AND ($2::integer IS NULL OR EXISTS (SELECT 1 FROM agent_departments membership
			WHERE membership."agentId" = agent.id AND membership."departmentId" = $2))
		GROUP BY agent.id, agent.name ORDER BY `+orderBy+` LIMIT 1`, companyID, departmentID).Scan(&agentID, &agentName)
	var result sql.Result
	switch {
	case err == nil:
		result, err = tx.Exec(`UPDATE conversations cv SET "agentId" = $1, "departmentId" = COALESCE($2, cv."departmentId"),
			status = $3, "updatedAt" = now()
			FROM connections co WHERE cv.id = $4 AND co.id = cv."connectionId" AND co."companyId" = $5`,
			agentID, departmentID, ConversationStatusOpen, conversationID, companyID)
	case errors.Is(err, sql.ErrNoRows) && departmentID != nil:
		agentName = ""
		result, err = tx.Exec(`UPDATE conversations cv SET "departmentId" = $1, "agentId" = NULL, status = $2, "updatedAt" = now()
			FROM connections co WHERE cv.id = $3 AND co.id = cv."connectionId" AND co."companyId" = $4`,
			*departmentID, ConversationStatusPending, conversationID, companyID)
	case errors.Is(err, sql.ErrNoRows):
		return "", errors.New("nenhum atendente ativo disponível")
	}
	if err != nil {
		return "", err
	}
	if rows, err := result.RowsAffected(); err != nil {
		return "", err
	} else if rows != 1 {
		return "", errors.New("conversa não encontrada")
	}
	if _, err := tx.Exec(`UPDATE ai_agent_sessions SET status = 'ended', "endedAt" = now(), "updatedAt" = now() WHERE id = $1`, sessionID); err != nil {
		return "", err
	}
	history := "Agente de IA transferiu o atendimento para a fila do setor."
	if agentName != "" {
		history = "Agente de IA transferiu o atendimento para " + agentName + " (distribuição automática)."
	}
	if summary = strings.TrimSpace(summary); summary != "" {
		history += " Resumo: " + summary
	}
	if err := addAIAgentHistory(tx, conversationID, companyID, history); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	invalidateConversationEventHistory(companyID, conversationID)
	return agentName, nil
}
