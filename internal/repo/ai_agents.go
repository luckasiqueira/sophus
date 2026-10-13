package repo

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrAIAgentConflict = errors.New("ai agent revision conflict")

const aiConversationLockNamespace = 0x4149

type AIAgent struct {
	Id               int       `json:"id"`
	CompanyId        int       `json:"companyId"`
	ConnectionId     *int      `json:"connectionId"`
	ConnectionName   string    `json:"connectionName"`
	Name             string    `json:"name"`
	CredentialId     int       `json:"credentialId"`
	CredentialName   string    `json:"credentialName"`
	Provider         string    `json:"provider"`
	Model            string    `json:"model"`
	APIKeyCiphertext string    `json:"-"`
	HasAPIKey        bool      `json:"hasApiKey"`
	SystemPrompt     string    `json:"systemPrompt"`
	KnowledgeBase    string    `json:"knowledgeBase"`
	Tone             string    `json:"tone"`
	MaxOutputTokens  int       `json:"maxOutputTokens"`
	Temperature      float64   `json:"temperature"`
	HistoryLimit     int       `json:"historyLimit"`
	WelcomeMessage   string    `json:"welcomeMessage"`
	SafeMode         bool      `json:"safeMode"`
	IsActive         bool      `json:"isActive"`
	Revision         int       `json:"revision"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

type AIAgentInput struct {
	ConnectionId    *int
	Name            string
	CredentialId    int
	SystemPrompt    string
	KnowledgeBase   string
	Tone            string
	MaxOutputTokens int
	Temperature     float64
	HistoryLimit    int
	WelcomeMessage  string
	SafeMode        bool
	IsActive        bool
	Revision        int
}

type AIAgentSession struct {
	Id             int64
	CompanyId      int
	ConversationId int
	AIAgentId      int
	SafeMode       bool
	Agent          AIAgent
}

type AIHistoryMessage struct {
	Role    string
	Content string
}

func AcquireAIConversationLock(ctx context.Context, conversationID int) (func(), error) {
	connection, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := connection.ExecContext(ctx, `SELECT pg_advisory_lock($1, $2)`, aiConversationLockNamespace, conversationID); err != nil {
		connection.Close()
		return nil, err
	}
	return func() {
		unlockContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = connection.ExecContext(unlockContext, `SELECT pg_advisory_unlock($1, $2)`, aiConversationLockNamespace, conversationID)
		_ = connection.Close()
	}, nil
}

const aiAgentColumns = `a.id, a."companyId", a."connectionId", COALESCE(c.name, ''), a.name,
	a."credentialId", credential.name, credential.provider, credential.model, credential."apiKeyCiphertext",
	a."systemPrompt", a."knowledgeBase", a.tone, a."maxOutputTokens", a.temperature,
	a."historyLimit", a."welcomeMessage", a."safeMode", a."isActive", a.revision, a."createdAt", a."updatedAt"`

const aiAgentJoins = ` LEFT JOIN connections c ON c.id = a."connectionId"
	JOIN ai_credentials credential ON credential.id = a."credentialId" AND credential."companyId" = a."companyId"`

func ListAIAgents(companyID int) ([]AIAgent, error) {
	rows, err := db.Query(`SELECT `+aiAgentColumns+` FROM ai_agents a`+aiAgentJoins+`
		WHERE a."companyId" = $1 ORDER BY a."createdAt" DESC, a.id DESC`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	agents := []AIAgent{}
	for rows.Next() {
		agent, err := scanAIAgent(rows)
		if err != nil {
			return nil, err
		}
		agents = append(agents, agent)
	}
	return agents, rows.Err()
}

func GetAIAgent(id, companyID int) (AIAgent, error) {
	return scanAIAgent(db.QueryRow(`SELECT `+aiAgentColumns+` FROM ai_agents a`+aiAgentJoins+`
		WHERE a.id = $1 AND a."companyId" = $2`, id, companyID))
}

func GetDirectAIAgent(connectionID, companyID int) (AIAgent, error) {
	return scanAIAgent(db.QueryRow(`SELECT `+aiAgentColumns+` FROM ai_agents a`+aiAgentJoins+`
		WHERE a."connectionId" = $1 AND a."companyId" = $2 AND a."isActive" = true`, connectionID, companyID))
}

func CreateAIAgent(companyID int, input AIAgentInput) (AIAgent, error) {
	return scanAIAgent(db.QueryRow(`WITH inserted AS (
		INSERT INTO ai_agents ("companyId", "connectionId", name, "credentialId", "systemPrompt",
			"knowledgeBase", tone, "maxOutputTokens", temperature, "historyLimit", "welcomeMessage", "safeMode", "isActive")
		SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
		WHERE $2::integer IS NULL OR EXISTS (SELECT 1 FROM connections WHERE id = $2 AND "companyId" = $1)
		AND EXISTS (SELECT 1 FROM ai_credentials WHERE id = $4 AND "companyId" = $1)
		RETURNING *)
		SELECT `+aiAgentColumns+` FROM inserted a`+aiAgentJoins, companyID,
		input.ConnectionId, input.Name, input.CredentialId, input.SystemPrompt, input.KnowledgeBase, input.Tone,
		input.MaxOutputTokens, input.Temperature, input.HistoryLimit, input.WelcomeMessage, input.SafeMode, input.IsActive))
}

func UpdateAIAgent(id, companyID int, input AIAgentInput) (AIAgent, error) {
	agent, err := scanAIAgent(db.QueryRow(`WITH updated AS (
		UPDATE ai_agents SET "connectionId" = $1, name = $2, "credentialId" = $3,
			"systemPrompt" = $4, "knowledgeBase" = $5, tone = $6, "maxOutputTokens" = $7,
			temperature = $8, "historyLimit" = $9, "welcomeMessage" = $10, "safeMode" = $11, "isActive" = $12,
			revision = revision + 1, "updatedAt" = now()
		WHERE id = $13 AND "companyId" = $14 AND revision = $15
		AND ($1::integer IS NULL OR EXISTS (SELECT 1 FROM connections WHERE id = $1 AND "companyId" = $14))
		AND EXISTS (SELECT 1 FROM ai_credentials WHERE id = $3 AND "companyId" = $14)
		RETURNING *)
		SELECT `+aiAgentColumns+` FROM updated a`+aiAgentJoins, input.ConnectionId, input.Name, input.CredentialId,
		input.SystemPrompt, input.KnowledgeBase, input.Tone, input.MaxOutputTokens, input.Temperature,
		input.HistoryLimit, input.WelcomeMessage, input.SafeMode, input.IsActive, id, companyID, input.Revision))
	if errors.Is(err, sql.ErrNoRows) {
		var exists bool
		if existsErr := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM ai_agents WHERE id = $1 AND "companyId" = $2)`, id, companyID).Scan(&exists); existsErr != nil {
			return AIAgent{}, existsErr
		}
		if exists {
			return AIAgent{}, ErrAIAgentConflict
		}
	}
	return agent, err
}

func DeleteAIAgent(id, companyID int) (bool, error) {
	result, err := db.Exec(`DELETE FROM ai_agents WHERE id = $1 AND "companyId" = $2`, id, companyID)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func ActivateAIAgentSession(conversationID, agentID, companyID int, safeMode bool) (AIAgentSession, error) {
	var session AIAgentSession
	err := db.QueryRow(`INSERT INTO ai_agent_sessions ("companyId", "conversationId", "aiAgentId", "safeMode", status)
		SELECT $3, cv.id, a.id, $4, 'active' FROM conversations cv
		JOIN connections c ON c.id = cv."connectionId" AND c."companyId" = $3
		JOIN ai_agents a ON a.id = $2 AND a."companyId" = $3 AND a."isActive" = true
		WHERE cv.id = $1
		ON CONFLICT ("conversationId") DO UPDATE SET "aiAgentId" = EXCLUDED."aiAgentId", "safeMode" = EXCLUDED."safeMode", status = 'active',
			"startedAt" = now(), "updatedAt" = now(), "endedAt" = NULL
		RETURNING id, "companyId", "conversationId", "aiAgentId", "safeMode"`, conversationID, agentID, companyID, safeMode).
		Scan(&session.Id, &session.CompanyId, &session.ConversationId, &session.AIAgentId, &session.SafeMode)
	return session, err
}

func ActivateDirectAIAgentSession(conversationID, agentID, companyID int) (AIAgentSession, error) {
	var session AIAgentSession
	err := db.QueryRow(`WITH released AS (
		UPDATE conversations cv SET "agentId" = NULL, status = 'open', "updatedAt" = now()
		FROM connections connection, ai_agents agent
		WHERE cv.id = $1 AND connection.id = cv."connectionId" AND connection."companyId" = $3
		AND agent.id = $2 AND agent."companyId" = $3 AND agent."connectionId" = connection.id AND agent."isActive" = true
		AND (cv."agentId" IS NULL OR NOT EXISTS (SELECT 1 FROM ai_agent_sessions existing WHERE existing."conversationId" = cv.id))
		RETURNING cv.id
	) INSERT INTO ai_agent_sessions ("companyId", "conversationId", "aiAgentId", "safeMode", status)
		SELECT $3, released.id, $2, NULL, 'active' FROM released
		ON CONFLICT ("conversationId") DO UPDATE SET "aiAgentId" = EXCLUDED."aiAgentId", "safeMode" = NULL, status = 'active',
			"startedAt" = now(), "updatedAt" = now(), "endedAt" = NULL
		RETURNING id, "companyId", "conversationId", "aiAgentId"`, conversationID, agentID, companyID).
		Scan(&session.Id, &session.CompanyId, &session.ConversationId, &session.AIAgentId)
	return session, err
}

func GetActiveAIAgentSession(conversationID, companyID int) (AIAgentSession, error) {
	var session AIAgentSession
	row := db.QueryRow(`SELECT s.id, s."companyId", s."conversationId", s."aiAgentId", COALESCE(s."safeMode", a."safeMode"), `+aiAgentColumns+`
		FROM ai_agent_sessions s JOIN ai_agents a ON a.id = s."aiAgentId"
		`+aiAgentJoins+`
		WHERE s."conversationId" = $1 AND s."companyId" = $2 AND s.status = 'active' AND a."isActive" = true`, conversationID, companyID)
	err := row.Scan(&session.Id, &session.CompanyId, &session.ConversationId, &session.AIAgentId, &session.SafeMode,
		&session.Agent.Id, &session.Agent.CompanyId, &session.Agent.ConnectionId, &session.Agent.ConnectionName,
		&session.Agent.Name, &session.Agent.CredentialId, &session.Agent.CredentialName, &session.Agent.Provider,
		&session.Agent.Model, &session.Agent.APIKeyCiphertext,
		&session.Agent.SystemPrompt, &session.Agent.KnowledgeBase, &session.Agent.Tone, &session.Agent.MaxOutputTokens,
		&session.Agent.Temperature, &session.Agent.HistoryLimit, &session.Agent.WelcomeMessage, &session.Agent.SafeMode, &session.Agent.IsActive,
		&session.Agent.Revision, &session.Agent.CreatedAt, &session.Agent.UpdatedAt)
	session.Agent.HasAPIKey = session.Agent.APIKeyCiphertext != ""
	return session, err
}

func EndAIAgentSession(conversationID int) error {
	_, err := db.Exec(`UPDATE ai_agent_sessions SET status = 'ended', "endedAt" = now(), "updatedAt" = now()
		WHERE "conversationId" = $1 AND status = 'active'`, conversationID)
	return err
}

func StartAIAgentRun(companyID int, sessionID int64, incomingKey string) (int64, bool, error) {
	var id int64
	err := db.QueryRow(`INSERT INTO ai_agent_runs ("companyId", "sessionId", "incomingKey") VALUES ($1, $2, $3)
		ON CONFLICT ("companyId", "incomingKey") DO NOTHING RETURNING id`, companyID, sessionID, incomingKey).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return id, err == nil, err
}

func FinishAIAgentRun(id int64, responseErr error, promptTokens, completionTokens int) error {
	status := "completed"
	var errorMessage interface{}
	if responseErr != nil {
		status = "failed"
		message := responseErr.Error()
		if len(message) > 2000 {
			message = message[:2000]
		}
		errorMessage = message
	}
	_, err := db.Exec(`UPDATE ai_agent_runs SET status = $1, "promptTokens" = $2, "completionTokens" = $3,
		"errorMessage" = $4, "completedAt" = now() WHERE id = $5 AND status = 'processing'`, status,
		promptTokens, completionTokens, errorMessage, id)
	return err
}

func GetAIConversationHistory(conversationID, companyID, limit int) ([]AIHistoryMessage, error) {
	rows, err := db.Query(`SELECT "isFromMe", text FROM (
		SELECT m.id, m."isFromMe", m.text FROM messages m
		JOIN conversations cv ON cv.id = m."conversationId"
		JOIN connections c ON c.id = cv."connectionId"
		WHERE cv.id = $1 AND c."companyId" = $2 AND m."isDeleted" = false AND m."isGroup" = false
		AND NULLIF(BTRIM(m.text), '') IS NOT NULL ORDER BY m.id DESC LIMIT $3
	) history ORDER BY id`, conversationID, companyID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	history := []AIHistoryMessage{}
	for rows.Next() {
		var fromMe bool
		var text string
		if err := rows.Scan(&fromMe, &text); err != nil {
			return nil, err
		}
		role := "user"
		if fromMe {
			role = "assistant"
		}
		history = append(history, AIHistoryMessage{Role: role, Content: text})
	}
	return history, rows.Err()
}

func ActiveFlowReferencesAIAgent(agentID, companyID int) (bool, error) {
	var referenced bool
	err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM chatbot_flows flow,
		LATERAL jsonb_array_elements(COALESCE(flow."flowData"->'nodes', '[]'::jsonb)) node
		WHERE flow."companyId" = $2 AND flow."isActive" = true AND node->>'type' IN ('aiAgent', 'conversationSummary', 'mediaTranscription')
		AND node->'data'->>'aiAgentId' = $1::text)`, agentID, companyID).Scan(&referenced)
	return referenced, err
}

func scanAIAgent(scanner rowScanner) (AIAgent, error) {
	var agent AIAgent
	err := scanner.Scan(&agent.Id, &agent.CompanyId, &agent.ConnectionId, &agent.ConnectionName, &agent.Name,
		&agent.CredentialId, &agent.CredentialName, &agent.Provider, &agent.Model, &agent.APIKeyCiphertext,
		&agent.SystemPrompt, &agent.KnowledgeBase,
		&agent.Tone, &agent.MaxOutputTokens, &agent.Temperature, &agent.HistoryLimit, &agent.WelcomeMessage,
		&agent.SafeMode, &agent.IsActive, &agent.Revision, &agent.CreatedAt, &agent.UpdatedAt)
	agent.HasAPIKey = agent.APIKeyCiphertext != ""
	return agent, err
}
