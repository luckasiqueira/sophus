package repo

import (
	"database/sql"
	"errors"
	"time"
)

const (
	RoleAdmin = "admin"
	RoleAgent = "agent"
)

var (
	ErrAgentEmailInUse        = errors.New("agent email is already in use")
	ErrInvalidAgentRole       = errors.New("invalid agent role")
	ErrLastActiveCompanyAdmin = errors.New("company must have at least one active admin")
	ErrAgentLimitReached      = errors.New("plan agent limit reached")
)

type Agent struct {
	Id             int       `json:"id"`
	Name           string    `json:"name"`
	Email          string    `json:"email"`
	Password       string    `json:"-"`
	Role           string    `json:"role"`
	IsActive       bool      `json:"isActive"`
	CompanyId      int       `json:"companyId"`
	SessionVersion int       `json:"-"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type CreateAgentInput struct {
	Name         string `json:"name"`
	Email        string `json:"email"`
	PasswordHash string `json:"-"`
	Role         string `json:"role"`
	IsActive     bool   `json:"isActive"`
}

type UpdateAgentInput struct {
	Name         string `json:"name"`
	Email        string `json:"email"`
	PasswordHash string `json:"-"`
	Role         string `json:"role"`
	IsActive     bool   `json:"isActive"`
}

func GetAgentByEmail(email string) (Agent, error) {
	query := `SELECT id, name, email, password, role, "isActive", "companyId", "sessionVersion", "createdAt", "updatedAt"
		FROM agents a
		WHERE a.email = $1
			OR (lower(a.email) = lower($1) AND (
				SELECT COUNT(*) FROM agents duplicate WHERE lower(duplicate.email) = lower($1)
			) = 1)
		ORDER BY CASE WHEN a.email = $1 THEN 0 ELSE 1 END
		LIMIT 1`
	return getAgent(query, email)
}

func GetAgentById(id int) (Agent, error) {
	query := `SELECT id, name, email, password, role, "isActive", "companyId", "sessionVersion", "createdAt", "updatedAt" FROM agents WHERE id = $1`
	return getAgent(query, id)
}

func ListAgentsByCompany(companyID int) ([]Agent, error) {
	rows, err := db.Query(`SELECT id, name, email, password, role, "isActive", "companyId", "sessionVersion", "createdAt", "updatedAt"
		FROM agents WHERE "companyId" = $1 ORDER BY name, id`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	agents := []Agent{}
	for rows.Next() {
		agent, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		agents = append(agents, agent)
	}
	return agents, rows.Err()
}

func GetAgentByIdAndCompany(id, companyID int) (Agent, error) {
	query := `SELECT id, name, email, password, role, "isActive", "companyId", "sessionVersion", "createdAt", "updatedAt"
		FROM agents WHERE id = $1 AND "companyId" = $2`
	return getAgent(query, id, companyID)
}

func CreateAgent(companyID int, input CreateAgentInput) (Agent, error) {
	if !validAgentRole(input.Role) {
		return Agent{}, ErrInvalidAgentRole
	}

	tx, err := db.Begin()
	if err != nil {
		return Agent{}, err
	}
	defer tx.Rollback()

	var existingCompanyID int
	if err := tx.QueryRow(`SELECT id FROM companies WHERE id = $1 FOR UPDATE`, companyID).Scan(&existingCompanyID); err != nil {
		return Agent{}, err
	}
	if err := lockAgentEmail(tx, input.Email, 0); err != nil {
		return Agent{}, err
	}
	var agentLimit, agentCount int
	if err := tx.QueryRow(`SELECT p."agentLimit", COUNT(a.id)
		FROM subscriptions s
		JOIN plans p ON p.id = s."planId"
		LEFT JOIN agents a ON a."companyId" = s."companyId"
		WHERE s."companyId" = $1 AND s.status IN ('trialing', 'active', 'past_due')
		GROUP BY p."agentLimit"`, companyID).Scan(&agentLimit, &agentCount); err != nil {
		return Agent{}, err
	}
	if agentCount >= agentLimit {
		return Agent{}, ErrAgentLimitReached
	}

	agent, err := scanAgent(tx.QueryRow(`INSERT INTO agents
		(name, email, password, role, "isActive", "companyId", "createdAt", "updatedAt")
		VALUES ($1, $2, $3, $4, $5, $6, now(), now())
		RETURNING id, name, email, password, role, "isActive", "companyId", "sessionVersion", "createdAt", "updatedAt"`,
		input.Name, input.Email, input.PasswordHash, input.Role, input.IsActive, companyID))
	if err != nil {
		return Agent{}, err
	}
	if err := tx.Commit(); err != nil {
		return Agent{}, err
	}
	return agent, nil
}

func UpdateAgent(id, companyID int, input UpdateAgentInput) (bool, error) {
	if !validAgentRole(input.Role) {
		return false, ErrInvalidAgentRole
	}

	tx, err := db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	if err := lockCompanyForAgentChange(tx, companyID); err != nil {
		return false, err
	}
	existing, err := scanAgent(tx.QueryRow(`SELECT id, name, email, password, role, "isActive", "companyId", "sessionVersion", "createdAt", "updatedAt"
		FROM agents WHERE id = $1 AND "companyId" = $2 FOR UPDATE`, id, companyID))
	if err != nil {
		return false, err
	}
	if err := lockAgentEmail(tx, input.Email, id); err != nil {
		return false, err
	}
	if existing.IsActive && existing.Role == RoleAdmin && (!input.IsActive || input.Role != RoleAdmin) {
		if err := ensureAnotherActiveAdmin(tx, id, companyID); err != nil {
			return false, err
		}
	}

	result, err := tx.Exec(`UPDATE agents SET name = $1, email = $2,
		password = COALESCE(NULLIF($3, ''), password), role = $4, "isActive" = $5,
		"sessionVersion" = "sessionVersion" + CASE
			WHEN lower(email) <> lower($2) OR NULLIF($3, '') IS NOT NULL OR "isActive" <> $5 THEN 1 ELSE 0 END,
		"updatedAt" = now()
		WHERE id = $6 AND "companyId" = $7`, input.Name, input.Email, input.PasswordHash,
		input.Role, input.IsActive, id, companyID)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if rows != 1 {
		return false, sql.ErrNoRows
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func SetAgentActive(id, companyID int, active bool) (bool, error) {
	tx, err := db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	if err := lockCompanyForAgentChange(tx, companyID); err != nil {
		return false, err
	}
	agent, err := scanAgent(tx.QueryRow(`SELECT id, name, email, password, role, "isActive", "companyId", "sessionVersion", "createdAt", "updatedAt"
		FROM agents WHERE id = $1 AND "companyId" = $2 FOR UPDATE`, id, companyID))
	if err != nil {
		return false, err
	}
	if agent.IsActive && agent.Role == RoleAdmin && !active {
		if err := ensureAnotherActiveAdmin(tx, id, companyID); err != nil {
			return false, err
		}
	}

	result, err := tx.Exec(`UPDATE agents SET "isActive" = $1,
		"sessionVersion" = "sessionVersion" + CASE WHEN "isActive" <> $1 THEN 1 ELSE 0 END,
		"updatedAt" = now()
		WHERE id = $2 AND "companyId" = $3`, active, id, companyID)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if rows != 1 {
		return false, sql.ErrNoRows
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func GetAgentByMessage(message string) (Agent, error) {
	query := `SELECT a.id, a.name, a.email, a.password, a.role, a."isActive", a."companyId", a."sessionVersion", a."createdAt", a."updatedAt"
	FROM messages m
	INNER JOIN conversations c
		ON c.id = m."conversationId"
	INNER JOIN agents a
		ON a.id = c."agentId"
	WHERE m."messageId" = $1`
	return getAgent(query, message)
}

func getAgent(query string, args ...interface{}) (Agent, error) {
	stmt, err := db.Prepare(query)
	if err != nil {
		return Agent{}, err
	}
	defer stmt.Close()
	return scanAgent(stmt.QueryRow(args...))
}

func scanAgent(scanner rowScanner) (Agent, error) {
	var agent Agent
	err := scanner.Scan(&agent.Id, &agent.Name, &agent.Email, &agent.Password, &agent.Role, &agent.IsActive,
		&agent.CompanyId, &agent.SessionVersion, &agent.CreatedAt, &agent.UpdatedAt)
	return agent, err
}

func validAgentRole(role string) bool {
	return role == RoleAdmin || role == RoleAgent
}

func lockAgentEmail(tx *sql.Tx, email string, excludeAgentID int) error {
	var lockAcquired interface{}
	if err := tx.QueryRow(`SELECT pg_advisory_xact_lock(hashtextextended(lower($1), 0))`, email).Scan(&lockAcquired); err != nil {
		return err
	}
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(
		SELECT 1 FROM agents WHERE lower(email) = lower($1) AND id <> $2
		UNION ALL
		SELECT 1 FROM platform_admins WHERE lower(email) = lower($1)
	)`, email, excludeAgentID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return ErrAgentEmailInUse
	}
	return nil
}

func lockCompanyForAgentChange(tx *sql.Tx, companyID int) error {
	var id int
	return tx.QueryRow(`SELECT id FROM companies WHERE id = $1 FOR UPDATE`, companyID).Scan(&id)
}

func ensureAnotherActiveAdmin(tx *sql.Tx, agentID, companyID int) error {
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(
		SELECT 1 FROM agents
		WHERE "companyId" = $1 AND id <> $2 AND role = $3 AND "isActive" = true
	)`, companyID, agentID, RoleAdmin).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrLastActiveCompanyAdmin
	}
	return nil
}

func (a Agent) IsAdmin() bool {
	return a.Role == RoleAdmin
}
