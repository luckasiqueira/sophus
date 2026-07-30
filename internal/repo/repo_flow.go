package repo

import (
	"encoding/json"
	"time"
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
	CreatedBy    *int            `json:"createdBy"`
	CreatedAt    time.Time       `json:"createdAt"`
	UpdatedAt    time.Time       `json:"updatedAt"`
}

type FlowExecution struct {
	Id             int             `json:"id"`
	FlowId         int             `json:"flowId"`
	ConversationId int             `json:"conversationId"`
	CompanyId      int             `json:"companyId"`
	Status         string          `json:"status"`
	CurrentNodeId  *string         `json:"currentNodeId"`
	Context        json.RawMessage `json:"context"`
	ErrorMessage   *string         `json:"errorMessage"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
	CompletedAt    *time.Time      `json:"completedAt"`
}

func GetChatbotFlowsByCompany(companyId int) ([]ChatbotFlow, error) {
	stmt, err := db.Prepare(`SELECT id, name, COALESCE(description,''), "companyId", "connectionId",
		"triggerType", COALESCE("triggerValue",''), "flowData", "isActive", "createdBy", "createdAt", "updatedAt"
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
			&f.TriggerType, &f.TriggerValue, &f.FlowData, &f.IsActive, &f.CreatedBy, &f.CreatedAt, &f.UpdatedAt)
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
		"triggerType", COALESCE("triggerValue",''), "flowData", "isActive", "createdBy", "createdAt", "updatedAt"
		FROM chatbot_flows WHERE id = $1 AND "companyId" = $2`)
	if err != nil {
		return f, err
	}
	defer stmt.Close()
	err = stmt.QueryRow(id, companyId).Scan(&f.Id, &f.Name, &f.Description, &f.CompanyId, &f.ConnectionId,
		&f.TriggerType, &f.TriggerValue, &f.FlowData, &f.IsActive, &f.CreatedBy, &f.CreatedAt, &f.UpdatedAt)
	return f, err
}

func CreateChatbotFlow(f ChatbotFlow) (int, error) {
	if len(f.FlowData) == 0 {
		f.FlowData = json.RawMessage(`{"nodes":[],"edges":[]}`)
	}
	query := `INSERT INTO chatbot_flows (name, description, "companyId", "connectionId", "triggerType", "triggerValue", "flowData", "isActive", "createdBy")
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`
	return insertInt(query, f.Name, f.Description, f.CompanyId, f.ConnectionId, f.TriggerType, f.TriggerValue, f.FlowData, f.IsActive, f.CreatedBy)
}

func UpdateChatbotFlow(f ChatbotFlow) error {
	stmt, err := db.Prepare(`UPDATE chatbot_flows SET name = $1, description = $2, "connectionId" = $3,
		"triggerType" = $4, "triggerValue" = $5, "flowData" = $6, "isActive" = $7, "updatedAt" = now()
		WHERE id = $8 AND "companyId" = $9`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	_, err = stmt.Exec(f.Name, f.Description, f.ConnectionId, f.TriggerType, f.TriggerValue, f.FlowData, f.IsActive, f.Id, f.CompanyId)
	return err
}

func DeleteChatbotFlow(id, companyId int) error {
	stmt, err := db.Prepare(`DELETE FROM chatbot_flows WHERE id = $1 AND "companyId" = $2`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	_, err = stmt.Exec(id, companyId)
	return err
}

func GetActiveFlowsForConnection(companyId, connectionId int) ([]ChatbotFlow, error) {
	stmt, err := db.Prepare(`SELECT id, name, COALESCE(description,''), "companyId", "connectionId",
		"triggerType", COALESCE("triggerValue",''), "flowData", "isActive", "createdBy", "createdAt", "updatedAt"
		FROM chatbot_flows
		WHERE "companyId" = $1 AND "isActive" = true
		AND ("connectionId" IS NULL OR "connectionId" = $2)`)
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
			&f.TriggerType, &f.TriggerValue, &f.FlowData, &f.IsActive, &f.CreatedBy, &f.CreatedAt, &f.UpdatedAt)
		if err != nil {
			return nil, err
		}
		flows = append(flows, f)
	}
	return flows, nil
}

func CreateFlowExecution(e FlowExecution) (int, error) {
	if len(e.Context) == 0 {
		e.Context = json.RawMessage(`{}`)
	}
	query := `INSERT INTO flow_executions ("flowId", "conversationId", "companyId", status, context)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`
	return insertInt(query, e.FlowId, e.ConversationId, e.CompanyId, e.Status, e.Context)
}

func GetFlowExecutionById(id int) (FlowExecution, error) {
	var e FlowExecution
	stmt, err := db.Prepare(`SELECT id, "flowId", "conversationId", "companyId", status,
		"currentNodeId", context, "errorMessage", "createdAt", "updatedAt", "completedAt"
		FROM flow_executions WHERE id = $1`)
	if err != nil {
		return e, err
	}
	defer stmt.Close()
	err = stmt.QueryRow(id).Scan(&e.Id, &e.FlowId, &e.ConversationId, &e.CompanyId, &e.Status,
		&e.CurrentNodeId, &e.Context, &e.ErrorMessage, &e.CreatedAt, &e.UpdatedAt, &e.CompletedAt)
	return e, err
}

func GetWaitingExecutionByConversation(conversationId int) (FlowExecution, error) {
	var e FlowExecution
	stmt, err := db.Prepare(`SELECT id, "flowId", "conversationId", "companyId", status,
		"currentNodeId", context, "errorMessage", "createdAt", "updatedAt", "completedAt"
		FROM flow_executions
		WHERE "conversationId" = $1 AND status = 'waiting'
		ORDER BY id DESC LIMIT 1`)
	if err != nil {
		return e, err
	}
	defer stmt.Close()
	err = stmt.QueryRow(conversationId).Scan(&e.Id, &e.FlowId, &e.ConversationId, &e.CompanyId, &e.Status,
		&e.CurrentNodeId, &e.Context, &e.ErrorMessage, &e.CreatedAt, &e.UpdatedAt, &e.CompletedAt)
	return e, err
}

func ClaimFlowExecution(id int) (bool, error) {
	result, err := db.Exec(`UPDATE flow_executions SET status = 'running', "updatedAt" = now()
		WHERE id = $1 AND status = 'waiting'`, id)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func UpdateFlowExecution(e FlowExecution) error {
	stmt, err := db.Prepare(`UPDATE flow_executions SET status = $1, "currentNodeId" = $2, context = $3,
		"errorMessage" = $4, "updatedAt" = now(), "completedAt" = $5
		WHERE id = $6`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	_, err = stmt.Exec(e.Status, e.CurrentNodeId, e.Context, e.ErrorMessage, e.CompletedAt, e.Id)
	return err
}

func UpdateConversationStatus(conversationId int, status string) error {
	stmt, err := db.Prepare(`UPDATE conversations SET status = $1, "updatedAt" = now() WHERE id = $2`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	_, err = stmt.Exec(status, conversationId)
	return err
}

func AssignConversationAgent(conversationId, agentId int) error {
	stmt, err := db.Prepare(`UPDATE conversations SET "agentId" = $1, "updatedAt" = now() WHERE id = $2`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	_, err = stmt.Exec(agentId, conversationId)
	return err
}

func GetConversationById(id int) (Conversation, error) {
	var c Conversation
	stmt, err := db.Prepare(`SELECT id, status, "contactId", "connectionId", "agentId", url, "createdAt", "updatedAt"
		FROM conversations WHERE id = $1`)
	if err != nil {
		return c, err
	}
	defer stmt.Close()
	err = stmt.QueryRow(id).Scan(&c.Id, &c.Status, &c.Contact.Id, &c.ConnectionID, &c.AgentID, &c.URL, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

func GetConnectionById(id int) (ConnectionEVO, error) {
	stmt, err := db.Prepare(`SELECT id, name, number, status, "companyId", COALESCE(qrcode,''), "createdAt",
		"instanceId", webhook, "apiToken", "connectionKey"
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
