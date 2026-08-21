package repo

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/lib/pq"
)

var (
	ErrFlowExecutionInactive   = errors.New("flow execution is no longer active")
	ErrFlowHasActiveExecutions = errors.New("flow has active executions")
	ErrFlowHasExecutions       = errors.New("flow has execution history")
	ErrFlowReferenced          = errors.New("flow is referenced by an active flow")
	ErrFlowRevisionConflict    = errors.New("flow was changed by another editor")
)

type ChatbotFlow struct {
	Id           int             `json:"id"`
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	CompanyId    int             `json:"companyId"`
	ConnectionId *int            `json:"connectionId"`
	TriggerType  string          `json:"triggerType"`
	TriggerValue string          `json:"triggerValue"`
	FlowData     json.RawMessage `json:"flowData"`
	IsActive     bool            `json:"isActive"`
	Priority     int             `json:"priority"`
	Revision     int             `json:"revision"`
	CreatedBy    *int            `json:"createdBy"`
	CreatedAt    time.Time       `json:"createdAt"`
	UpdatedAt    time.Time       `json:"updatedAt"`
}

type FlowExecution struct {
	Id               int             `json:"id"`
	FlowId           int             `json:"flowId"`
	ConversationId   int             `json:"conversationId"`
	CompanyId        int             `json:"companyId"`
	Status           string          `json:"status"`
	CurrentNodeId    *string         `json:"currentNodeId"`
	Context          json.RawMessage `json:"context"`
	ErrorMessage     *string         `json:"errorMessage"`
	CreatedAt        time.Time       `json:"createdAt"`
	UpdatedAt        time.Time       `json:"updatedAt"`
	CompletedAt      *time.Time      `json:"completedAt"`
	FlowDataSnapshot json.RawMessage `json:"-"`
	FlowRevision     int             `json:"flowRevision"`
	SnapshotIsLegacy bool            `json:"snapshotIsLegacy"`
	ClaimVersion     int             `json:"claimVersion"`
	ResumeAt         *time.Time      `json:"resumeAt,omitempty"`
	WaitReason       *string         `json:"waitReason,omitempty"`
}

type FlowConditionMetadata struct {
	ContactName      string
	ContactNumber    string
	ContactEmail     string
	ConversationTags []string
	Department       string
	ConnectionType   string
}

type FlowAgentAssignment struct {
	AgentID   int
	AgentName string
}

func GetChatbotFlowsByCompany(companyId int) ([]ChatbotFlow, error) {
	stmt, err := db.Prepare(`SELECT id, name, COALESCE(description,''), "companyId", "connectionId",
		"triggerType", COALESCE("triggerValue",''), "flowData", "isActive", priority, revision, "createdBy", "createdAt", "updatedAt"
		FROM chatbot_flows WHERE "companyId" = $1 ORDER BY "createdAt" DESC`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	rows, err := stmt.Query(companyId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	flows := []ChatbotFlow{}
	for rows.Next() {
		var f ChatbotFlow
		err = rows.Scan(&f.Id, &f.Name, &f.Description, &f.CompanyId, &f.ConnectionId,
			&f.TriggerType, &f.TriggerValue, &f.FlowData, &f.IsActive, &f.Priority, &f.Revision, &f.CreatedBy, &f.CreatedAt, &f.UpdatedAt)
		if err != nil {
			return nil, err
		}
		flows = append(flows, f)
	}
	return flows, nil
}

func GetChatbotFlowById(id, companyId int) (ChatbotFlow, error) {
	var f ChatbotFlow
	stmt, err := db.Prepare(`SELECT id, name, COALESCE(description,''), "companyId", "connectionId",
		"triggerType", COALESCE("triggerValue",''), "flowData", "isActive", priority, revision, "createdBy", "createdAt", "updatedAt"
		FROM chatbot_flows WHERE id = $1 AND "companyId" = $2`)
	if err != nil {
		return f, err
	}
	defer stmt.Close()
	err = stmt.QueryRow(id, companyId).Scan(&f.Id, &f.Name, &f.Description, &f.CompanyId, &f.ConnectionId,
		&f.TriggerType, &f.TriggerValue, &f.FlowData, &f.IsActive, &f.Priority, &f.Revision, &f.CreatedBy, &f.CreatedAt, &f.UpdatedAt)
	return f, err
}

func CreateChatbotFlow(f ChatbotFlow) (int, error) {
	if len(f.FlowData) == 0 {
		f.FlowData = json.RawMessage(`{"nodes":[],"edges":[]}`)
	}
	query := `INSERT INTO chatbot_flows (name, description, "companyId", "connectionId", "triggerType", "triggerValue", "flowData", "isActive", priority, "createdBy")
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id`
	return insertInt(query, f.Name, f.Description, f.CompanyId, f.ConnectionId, f.TriggerType, f.TriggerValue, f.FlowData, f.IsActive, f.Priority, f.CreatedBy)
}

func UpdateChatbotFlow(f *ChatbotFlow) error {
	err := db.QueryRow(`UPDATE chatbot_flows SET name = $1, description = $2, "connectionId" = $3,
		"triggerType" = $4, "triggerValue" = $5, "flowData" = $6, "isActive" = $7, priority = $8,
		revision = revision + 1, "updatedAt" = now()
		WHERE id = $9 AND "companyId" = $10 AND revision = $11 RETURNING revision, "updatedAt"`,
		f.Name, f.Description, f.ConnectionId, f.TriggerType, f.TriggerValue, f.FlowData, f.IsActive, f.Priority, f.Id, f.CompanyId, f.Revision).Scan(&f.Revision, &f.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrFlowRevisionConflict
	}
	return err
}

func DeleteChatbotFlow(id, companyId int) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var lockedID int
	if err := tx.QueryRow(`SELECT id FROM chatbot_flows WHERE id = $1 AND "companyId" = $2 FOR UPDATE`, id, companyId).Scan(&lockedID); err != nil {
		return err
	}
	var active, any bool
	if err := tx.QueryRow(`SELECT
		EXISTS(SELECT 1 FROM flow_executions WHERE "flowId" = $1 AND status IN ('running', 'waiting')),
		EXISTS(SELECT 1 FROM flow_executions WHERE "flowId" = $1)`, id).Scan(&active, &any); err != nil {
		return err
	}
	if active {
		return ErrFlowHasActiveExecutions
	}
	var referenced bool
	if err := tx.QueryRow(`SELECT EXISTS(
		SELECT 1 FROM chatbot_flows parent,
		LATERAL jsonb_array_elements(COALESCE(parent."flowData"->'nodes', '[]'::jsonb)) node
		WHERE parent."companyId" = $2 AND parent.id <> $1 AND parent."isActive" = true
		AND node->>'type' = 'subflow' AND node->'data'->>'flowId' = $1::text
	)`, id, companyId).Scan(&referenced); err != nil {
		return err
	}
	if referenced {
		return ErrFlowReferenced
	}
	if any {
		return ErrFlowHasExecutions
	}
	if _, err := tx.Exec(`DELETE FROM chatbot_flows WHERE id = $1`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func GetActiveFlowsForConnection(companyId, connectionId int) ([]ChatbotFlow, error) {
	stmt, err := db.Prepare(`SELECT id, name, COALESCE(description,''), "companyId", "connectionId",
		"triggerType", COALESCE("triggerValue",''), "flowData", "isActive", priority, revision, "createdBy", "createdAt", "updatedAt"
		FROM chatbot_flows
		WHERE "companyId" = $1 AND "isActive" = true
		AND ("connectionId" IS NULL OR "connectionId" = $2)
		ORDER BY ("connectionId" IS NOT NULL) DESC,
			CASE "triggerType" WHEN 'exact' THEN 0 WHEN 'keyword' THEN 1 WHEN 'always' THEN 2 ELSE 3 END,
			priority DESC, length(COALESCE("triggerValue", '')) DESC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	rows, err := stmt.Query(companyId, connectionId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	flows := []ChatbotFlow{}
	for rows.Next() {
		var f ChatbotFlow
		err = rows.Scan(&f.Id, &f.Name, &f.Description, &f.CompanyId, &f.ConnectionId,
			&f.TriggerType, &f.TriggerValue, &f.FlowData, &f.IsActive, &f.Priority, &f.Revision, &f.CreatedBy, &f.CreatedAt, &f.UpdatedAt)
		if err != nil {
			return nil, err
		}
		flows = append(flows, f)
	}
	return flows, rows.Err()
}

func StartFlowExecution(e FlowExecution) (int, bool, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	if err := tx.QueryRow(`SELECT revision FROM chatbot_flows
		WHERE id = $1 AND "companyId" = $2 AND "isActive" = true AND revision = $3 FOR KEY SHARE`, e.FlowId, e.CompanyId, e.FlowRevision).
		Scan(&e.FlowRevision); err != nil {
		return 0, false, err
	}

	result, err := tx.Exec(`UPDATE conversations conversation SET status = $1, "updatedAt" = now()
		FROM connections connection
		WHERE conversation.id = $2 AND conversation.status = $3
		AND connection.id = conversation."connectionId" AND connection."companyId" = $4`,
		ConversationStatusRunning, e.ConversationId, ConversationStatusOpen, e.CompanyId)
	if err != nil {
		return 0, false, err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return 0, false, err
	}
	if len(e.Context) == 0 {
		e.Context = json.RawMessage(`{}`)
	}
	var id int
	err = tx.QueryRow(`INSERT INTO flow_executions ("flowId", "conversationId", "companyId", status, context, "flowDataSnapshot", "flowRevision", "snapshotIsLegacy")
		VALUES ($1, $2, $3, $4, $5, $6, $7, false) RETURNING id`,
		e.FlowId, e.ConversationId, e.CompanyId, e.Status, e.Context, e.FlowDataSnapshot, e.FlowRevision).Scan(&id)
	if err != nil {
		return 0, false, err
	}
	if err := tx.Commit(); err != nil {
		return 0, false, err
	}
	return id, true, nil
}

func GetFlowExecutionById(id int) (FlowExecution, error) {
	var e FlowExecution
	stmt, err := db.Prepare(`SELECT id, "flowId", "conversationId", "companyId", status,
		"currentNodeId", context, "errorMessage", "createdAt", "updatedAt", "completedAt", "flowDataSnapshot", "flowRevision", "snapshotIsLegacy", "claimVersion", "resumeAt", "waitReason"
		FROM flow_executions WHERE id = $1`)
	if err != nil {
		return e, err
	}
	defer stmt.Close()
	err = stmt.QueryRow(id).Scan(&e.Id, &e.FlowId, &e.ConversationId, &e.CompanyId, &e.Status,
		&e.CurrentNodeId, &e.Context, &e.ErrorMessage, &e.CreatedAt, &e.UpdatedAt, &e.CompletedAt,
		&e.FlowDataSnapshot, &e.FlowRevision, &e.SnapshotIsLegacy, &e.ClaimVersion, &e.ResumeAt, &e.WaitReason)
	return e, err
}

func GetWaitingExecutionByConversation(conversationId int) (FlowExecution, error) {
	var e FlowExecution
	stmt, err := db.Prepare(`SELECT id, "flowId", "conversationId", "companyId", status,
		"currentNodeId", context, "errorMessage", "createdAt", "updatedAt", "completedAt", "flowDataSnapshot", "flowRevision", "snapshotIsLegacy", "claimVersion", "resumeAt", "waitReason"
		FROM flow_executions
		WHERE "conversationId" = $1 AND status = 'waiting'
		ORDER BY id DESC LIMIT 1`)
	if err != nil {
		return e, err
	}
	defer stmt.Close()
	err = stmt.QueryRow(conversationId).Scan(&e.Id, &e.FlowId, &e.ConversationId, &e.CompanyId, &e.Status,
		&e.CurrentNodeId, &e.Context, &e.ErrorMessage, &e.CreatedAt, &e.UpdatedAt, &e.CompletedAt,
		&e.FlowDataSnapshot, &e.FlowRevision, &e.SnapshotIsLegacy, &e.ClaimVersion, &e.ResumeAt, &e.WaitReason)
	return e, err
}

func GetActiveFlowExecutionByConversation(conversationId int) (FlowExecution, error) {
	var e FlowExecution
	err := db.QueryRow(`SELECT id, "flowId", "conversationId", "companyId", status,
		"currentNodeId", context, "errorMessage", "createdAt", "updatedAt", "completedAt", "flowDataSnapshot", "flowRevision", "snapshotIsLegacy", "claimVersion", "resumeAt", "waitReason"
		FROM flow_executions
		WHERE "conversationId" = $1 AND status IN ('running', 'waiting')
		ORDER BY id DESC LIMIT 1`, conversationId).Scan(
		&e.Id, &e.FlowId, &e.ConversationId, &e.CompanyId, &e.Status,
		&e.CurrentNodeId, &e.Context, &e.ErrorMessage, &e.CreatedAt, &e.UpdatedAt, &e.CompletedAt,
		&e.FlowDataSnapshot, &e.FlowRevision, &e.SnapshotIsLegacy, &e.ClaimVersion, &e.ResumeAt, &e.WaitReason,
	)
	return e, err
}

func ClaimFlowExecution(id int) (int, bool, error) {
	var claimVersion int
	err := db.QueryRow(`UPDATE flow_executions SET status = 'running', "resumeAt" = NULL,
		"claimVersion" = "claimVersion" + 1, "updatedAt" = now()
		WHERE id = $1 AND status = 'waiting' RETURNING "claimVersion"`, id).Scan(&claimVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return claimVersion, err == nil, err
}

func UpdateFlowExecution(e FlowExecution) error {
	stmt, err := db.Prepare(`UPDATE flow_executions SET status = $1, "currentNodeId" = $2, context = $3,
		"errorMessage" = $4, "updatedAt" = now(), "completedAt" = $5,
		"resumeAt" = $6, "waitReason" = $7
		WHERE id = $8 AND status IN ('running', 'waiting') AND "claimVersion" = $9`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	result, err := stmt.Exec(e.Status, e.CurrentNodeId, e.Context, e.ErrorMessage, e.CompletedAt, e.ResumeAt, e.WaitReason, e.Id, e.ClaimVersion)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrFlowExecutionInactive
	}
	return nil
}

func SetFlowExecutionWaiting(e FlowExecution, timeoutSeconds int) error {
	result, err := db.Exec(`UPDATE flow_executions SET status = 'waiting', "currentNodeId" = $1, context = $2,
		"errorMessage" = $3, "updatedAt" = now(), "completedAt" = NULL,
		"resumeAt" = CASE WHEN $4 > 0 THEN now() + ($4 * interval '1 second') ELSE NULL END,
		"waitReason" = $5
		WHERE id = $6 AND status IN ('running', 'waiting') AND "claimVersion" = $7`,
		e.CurrentNodeId, e.Context, e.ErrorMessage, timeoutSeconds, e.WaitReason, e.Id, e.ClaimVersion)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrFlowExecutionInactive
	}
	return nil
}

func RecoverExpiredFlowExecutionClaims() error {
	_, err := db.Exec(`UPDATE flow_executions SET status = 'waiting', "resumeAt" = now(), "updatedAt" = now()
		WHERE status = 'running' AND "waitReason" IS NOT NULL
		AND "updatedAt" <= now() - interval '5 minutes'`)
	return err
}

func ReserveDueFlowExecutionIDs(limit int) ([]int, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := db.Query(`WITH due AS (
		SELECT id FROM flow_executions
		WHERE status = 'waiting' AND "resumeAt" IS NOT NULL AND "resumeAt" <= now()
		ORDER BY "resumeAt", id FOR UPDATE SKIP LOCKED LIMIT $1
	)
	UPDATE flow_executions execution
	SET "resumeAt" = now() + interval '30 seconds'
	FROM due WHERE execution.id = due.id
	RETURNING execution.id`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int{}
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func FinalizeFlowExecution(e FlowExecution) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.Exec(`UPDATE flow_executions SET status = $1, "currentNodeId" = $2, context = $3,
		"errorMessage" = $4, "updatedAt" = now(), "completedAt" = $5, "resumeAt" = NULL, "waitReason" = NULL
		WHERE id = $6 AND status IN ('running', 'waiting') AND "claimVersion" = $7`,
		e.Status, e.CurrentNodeId, e.Context, e.ErrorMessage, e.CompletedAt, e.Id, e.ClaimVersion)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrFlowExecutionInactive
	}
	conversationStatus := conversationStatusAfterExecution(e.Status)
	_, err = tx.Exec(`UPDATE conversations
		SET status = $1, "agentId" = NULL, "updatedAt" = now()
		WHERE id = $2 AND status = $3`, conversationStatus, e.ConversationId, ConversationStatusRunning)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func RecoverStaleFlowExecution(e FlowExecution, message string) (bool, error) {
	tx, err := db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	now := time.Now()
	result, err := tx.Exec(`UPDATE flow_executions SET status = 'failed', "errorMessage" = $1,
		"completedAt" = $2, "updatedAt" = now(), "resumeAt" = NULL, "waitReason" = NULL
		WHERE id = $3 AND status = 'running' AND "updatedAt" = $4`, message, now, e.Id, e.UpdatedAt)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return false, err
	}
	if _, err := tx.Exec(`UPDATE conversations SET status = $1, "updatedAt" = now()
		WHERE id = $2 AND status = $3`, ConversationStatusOpen, e.ConversationId, ConversationStatusRunning); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func TouchFlowExecution(id, claimVersion int) error {
	result, err := db.Exec(`UPDATE flow_executions SET "updatedAt" = now()
		WHERE id = $1 AND status = 'running' AND "claimVersion" = $2`, id, claimVersion)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrFlowExecutionInactive
	}
	return nil
}

func conversationStatusAfterExecution(executionStatus string) string {
	return ConversationStatusPending
}

func UpdateConversationStatus(conversationID, executionID int, status string) error {
	stmt, err := db.Prepare(`UPDATE conversations SET status = $1, "updatedAt" = now()
		WHERE id = $2 AND EXISTS (
			SELECT 1 FROM flow_executions fe
			WHERE fe.id = $3 AND fe."conversationId" = $2 AND fe.status IN ('running', 'waiting')
		)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	result, err := stmt.Exec(status, conversationID, executionID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrFlowExecutionInactive
	}
	return nil
}

func ClaimConversationForFlow(conversationId int) (bool, error) {
	result, err := db.Exec(`UPDATE conversations SET status = $1, "updatedAt" = now()
		WHERE id = $2 AND status = $3`, ConversationStatusRunning, conversationId, ConversationStatusOpen)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func AssignConversationAgent(conversationID, agentID, companyID int) error {
	result, err := db.Exec(`UPDATE conversations cv
		SET "agentId" = a.id,
			"departmentId" = CASE WHEN cv."departmentId" IS NULL OR EXISTS (
				SELECT 1 FROM agent_departments ad WHERE ad."agentId" = a.id AND ad."departmentId" = cv."departmentId"
			) THEN cv."departmentId" ELSE NULL END,
			status = $1, "updatedAt" = now()
		FROM agents a, connections co
		WHERE cv.id = $2 AND a.id = $3 AND a."companyId" = $4 AND a."isActive" = true
		AND co.id = cv."connectionId" AND co."companyId" = $4
		AND EXISTS (SELECT 1 FROM flow_executions fe WHERE fe."conversationId" = cv.id AND fe.status IN ('running', 'waiting'))`, ConversationStatusOpen, conversationID, agentID, companyID)
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

func AssignConversationDepartment(conversationID, departmentID, companyID int) error {
	result, err := db.Exec(`UPDATE conversations cv
		SET "departmentId" = d.id, "agentId" = NULL, status = $1, "updatedAt" = now()
		FROM departments d, connections co
		WHERE cv.id = $2 AND d.id = $3 AND d."companyId" = $4 AND d."isActive" = true
		AND co.id = cv."connectionId" AND co."companyId" = $4
		AND EXISTS (SELECT 1 FROM flow_executions fe WHERE fe."conversationId" = cv.id AND fe.status IN ('running', 'waiting'))`, ConversationStatusPending, conversationID, departmentID, companyID)
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

func AssignConversationSmart(conversationID, executionID, companyID int, departmentID *int, strategy string) (FlowAgentAssignment, error) {
	tx, err := db.Begin()
	if err != nil {
		return FlowAgentAssignment{}, err
	}
	defer tx.Rollback()
	lockKey := 0
	if departmentID != nil {
		lockKey = *departmentID
	}
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock($1, $2)`, companyID, lockKey); err != nil {
		return FlowAgentAssignment{}, err
	}
	orderBy := `count(workload.id), agent.id`
	if strategy == "random" {
		orderBy = `random()`
	}
	query := `SELECT agent.id, agent.name
		FROM agents agent
		LEFT JOIN conversations workload ON workload."agentId" = agent.id AND workload.status = 'open'
		WHERE agent."companyId" = $1 AND agent."isActive" = true
		AND ($2::integer IS NULL OR EXISTS (
			SELECT 1 FROM agent_departments membership
			WHERE membership."agentId" = agent.id AND membership."departmentId" = $2
		))
		GROUP BY agent.id, agent.name ORDER BY ` + orderBy + ` LIMIT 1`
	var assignment FlowAgentAssignment
	if err := tx.QueryRow(query, companyID, departmentID).Scan(&assignment.AgentID, &assignment.AgentName); err != nil {
		return FlowAgentAssignment{}, err
	}
	result, err := tx.Exec(`UPDATE conversations conversation
		SET "agentId" = $1, status = 'open', "updatedAt" = now()
		WHERE conversation.id = $2
		AND EXISTS (SELECT 1 FROM connections connection
			WHERE connection.id = conversation."connectionId" AND connection."companyId" = $3)
		AND EXISTS (SELECT 1 FROM flow_executions execution
			WHERE execution.id = $4 AND execution."conversationId" = conversation.id
			AND execution."companyId" = $3 AND execution.status IN ('running', 'waiting'))`,
		assignment.AgentID, conversationID, companyID, executionID)
	if err != nil {
		return FlowAgentAssignment{}, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return FlowAgentAssignment{}, err
	}
	if rows != 1 {
		return FlowAgentAssignment{}, sql.ErrNoRows
	}
	return assignment, tx.Commit()
}

func GetConversationById(id int) (Conversation, error) {
	var c Conversation
	stmt, err := db.Prepare(`SELECT id, status, "contactId", "connectionId", "agentId", "departmentId", url, "createdAt", "updatedAt"
		FROM conversations WHERE id = $1`)
	if err != nil {
		return c, err
	}
	defer stmt.Close()
	err = stmt.QueryRow(id).Scan(&c.Id, &c.Status, &c.Contact.Id, &c.ConnectionID, &c.AgentID, &c.DepartmentID, &c.URL, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

func GetFlowConditionMetadata(conversationID, companyID int) (FlowConditionMetadata, error) {
	var metadata FlowConditionMetadata
	var tags pq.StringArray
	err := db.QueryRow(`SELECT COALESCE(ct.name, ''), COALESCE(ct.number, ''), COALESCE(ct.email, ''),
		COALESCE(cv.tags, ARRAY[]::text[]), COALESCE(d.name, ''), COALESCE(co.type, 'whatsapp_qrcode')
		FROM conversations cv
		INNER JOIN contacts ct ON ct.id = cv."contactId"
		INNER JOIN connections co ON co.id = cv."connectionId" AND co."companyId" = $2
		LEFT JOIN departments d ON d.id = cv."departmentId"
		WHERE cv.id = $1`, conversationID, companyID).Scan(
		&metadata.ContactName,
		&metadata.ContactNumber,
		&metadata.ContactEmail,
		&tags,
		&metadata.Department,
		&metadata.ConnectionType,
	)
	metadata.ConversationTags = []string(tags)
	return metadata, err
}

func UpdateConversationTag(conversationID, executionID, companyID int, operation, tag string) ([]string, error) {
	var expression string
	switch operation {
	case "add":
		expression = `CASE WHEN EXISTS (
			SELECT 1 FROM unnest(conversation.tags) existing_tag WHERE lower(existing_tag) = lower($4)
		) THEN conversation.tags ELSE array_append(conversation.tags, $4) END`
	case "remove":
		expression = `ARRAY(
			SELECT existing_tag FROM unnest(conversation.tags) existing_tag WHERE lower(existing_tag) <> lower($4)
		)`
	default:
		return nil, errors.New("invalid tag operation")
	}
	query := `UPDATE conversations conversation SET tags = ` + expression + `, "updatedAt" = now()
		FROM connections connection
		WHERE conversation.id = $1 AND connection.id = conversation."connectionId" AND connection."companyId" = $2
		AND EXISTS (SELECT 1 FROM flow_executions execution
			WHERE execution.id = $3 AND execution."conversationId" = conversation.id
			AND execution."companyId" = $2 AND execution.status IN ('running', 'waiting'))
		RETURNING conversation.tags`
	var tags pq.StringArray
	if err := db.QueryRow(query, conversationID, companyID, executionID, tag).Scan(&tags); err != nil {
		return nil, err
	}
	return []string(tags), nil
}

func UpdateFlowContact(conversationID, executionID, companyID int, field, value string) error {
	column := ""
	switch field {
	case "name":
		column = "name"
	case "email":
		column = "email"
	default:
		return errors.New("invalid contact field")
	}
	query := `UPDATE contacts contact SET ` + column + ` = $4
		FROM conversations conversation
		JOIN connections connection ON connection.id = conversation."connectionId"
		WHERE conversation.id = $1 AND contact.id = conversation."contactId" AND connection."companyId" = $2
		AND EXISTS (SELECT 1 FROM flow_executions execution
			WHERE execution.id = $3 AND execution."conversationId" = conversation.id
			AND execution."companyId" = $2 AND execution.status IN ('running', 'waiting'))`
	result, err := db.Exec(query, conversationID, companyID, executionID, value)
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

func GetConnectionById(id int) (ConnectionEVO, error) {
	stmt, err := db.Prepare(`SELECT id, name, number, status, "companyId", COALESCE(qrcode,''), "createdAt",
		"instanceId", webhook, COALESCE("apiToken", ''), "connectionKey"
		FROM connections WHERE id = $1`)
	if err != nil {
		return ConnectionEVO{}, err
	}
	defer stmt.Close()
	var c ConnectionEVO
	err = stmt.QueryRow(id).Scan(&c.Id, &c.Name, &c.Number, &c.Status, &c.CompanyID, &c.QRCode, &c.CreatedAt,
		&c.InstanceID, &c.Webhook, &c.APIToken, &c.ConnectionKey)
	return c, err
}
