package repo

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
)

const (
	stressResourceContact    = "contact"
	stressResourceAgent      = "agent"
	stressResourceConnection = "connection"

	// Simulated WhatsApp numbers use country code 000, which no real account
	// can have; workers that would query Evolution skip them.
	StressNumberPrefix = stressContactNumberBase
)

type stressExecer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func registerStressResources(execer stressExecer, companyID int, resourceType string, ids ...int) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := execer.Exec(`INSERT INTO stress_test_resources ("resourceType", "resourceId", "companyId")
		SELECT $1, id, $2 FROM unnest($3::integer[]) id ON CONFLICT DO NOTHING`, resourceType, companyID, pq.Array(ids))
	return err
}

func unregisterStressResources(execer stressExecer, resourceType string, ids ...int) error {
	_, err := execer.Exec(`DELETE FROM stress_test_resources WHERE "resourceType" = $1 AND "resourceId" = ANY($2::integer[])`,
		resourceType, pq.Array(ids))
	return err
}

// CreateStressAgents inserts active agents whose password hash belongs to a
// discarded random secret, so they can only act through simulator sessions.
// Plan limits are deliberately bypassed: the test measures capacity.
func CreateStressAgents(companyID, count int) ([]Agent, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	hash, err := bcrypt.GenerateFromPassword(secret, bcrypt.MinCost)
	if err != nil {
		return nil, err
	}
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var existing int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM stress_test_resources WHERE "companyId" = $1 AND "resourceType" = $2`,
		companyID, stressResourceAgent).Scan(&existing); err != nil {
		return nil, err
	}
	agents := make([]Agent, 0, count)
	ids := make([]int, 0, count)
	for index := 1; index <= count; index++ {
		agent, err := scanAgent(tx.QueryRow(`INSERT INTO agents
			(name, email, password, role, "isActive", "companyId", "createdAt", "updatedAt")
			VALUES ($1, $2, $3, $4, true, $5, now(), now())
			RETURNING id, name, email, password, role, "isActive", "companyId", "sessionVersion", "createdAt", "updatedAt"`,
			fmt.Sprintf("Agente simulado %03d", existing+index), "simulado-"+uuid.NewString()[:13]+"@stress.invalid",
			string(hash), RoleAgent, companyID))
		if err != nil {
			return nil, err
		}
		agents = append(agents, agent)
		ids = append(ids, agent.Id)
	}
	if err := registerStressResources(tx, companyID, stressResourceAgent, ids...); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	invalidateSessionCache()
	return agents, nil
}

// DeleteStressAgents removes simulated agents, e.g. when a simulation fails
// to start after they were created.
func DeleteStressAgents(companyID int, agents []Agent) error {
	for _, agent := range agents {
		if err := DeleteCompanyAgent(agent.Id, companyID); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err := unregisterStressResources(db, stressResourceAgent, agent.Id); err != nil {
			return err
		}
	}
	return nil
}

// CreateStressConnections inserts instances already marked as connected. They
// have no Evolution counterpart: traffic is injected through their webhook URL.
func CreateStressConnections(companyID, count int) ([]ConnectionEVO, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var existing int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM stress_test_resources WHERE "companyId" = $1 AND "resourceType" = $2`,
		companyID, stressResourceConnection).Scan(&existing); err != nil {
		return nil, err
	}
	connections := make([]ConnectionEVO, 0, count)
	ids := make([]int, 0, count)
	for index := 1; index <= count; index++ {
		number, err := stressContactNumber()
		if err != nil {
			return nil, err
		}
		connection := ConnectionEVO{
			Name: fmt.Sprintf("Instância simulada %03d", existing+index), Number: number, Status: ConnectionStatusConnected,
			CompanyID: companyID, CreatedAt: time.Now(), InstanceID: uuid.New(), Webhook: uuid.New(),
			APIToken: stressAPIToken(), ConnectionKey: uuid.New(),
		}
		if err := tx.QueryRow(`INSERT INTO connections
			(name, number, status, "companyId", qrcode, "createdAt", "instanceId", webhook, "apiToken", "connectionKey")
			VALUES ($1, $2, $3, $4, '', $5, $6, $7, $8, $9) RETURNING id`,
			connection.Name, connection.Number, connection.Status, companyID, connection.CreatedAt,
			connection.InstanceID, connection.Webhook, connection.APIToken, connection.ConnectionKey).Scan(&connection.Id); err != nil {
			return nil, err
		}
		connections = append(connections, connection)
		ids = append(ids, connection.Id)
	}
	if err := registerStressResources(tx, companyID, stressResourceConnection, ids...); err != nil {
		return nil, err
	}
	return connections, tx.Commit()
}

// stressAPIToken fits connections."apiToken" (varchar 32) like a real token,
// whose 32 hex digits never contain "stress", so the two cannot collide.
func stressAPIToken() string {
	return "stress" + strings.ReplaceAll(uuid.NewString(), "-", "")[:26]
}

func IsStressConnection(connectionID int) bool {
	var simulated bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM stress_test_resources WHERE "resourceType" = $1 AND "resourceId" = $2)`,
		stressResourceConnection, connectionID).Scan(&simulated); err != nil {
		log.Printf("stress connection lookup failed: connection=%d error=%v", connectionID, err)
		return false
	}
	return simulated
}

// WithoutStressConnections drops simulated instances from lists sent to
// Evolution; on lookup failure the list is returned unchanged.
func WithoutStressConnections(connections []ConnectionEVO) []ConnectionEVO {
	rows, err := db.Query(`SELECT "resourceId" FROM stress_test_resources WHERE "resourceType" = $1`, stressResourceConnection)
	if err != nil {
		log.Printf("stress connection list failed: %v", err)
		return connections
	}
	defer rows.Close()
	simulated := map[int]bool{}
	for rows.Next() {
		var id int
		if rows.Scan(&id) == nil {
			simulated[id] = true
		}
	}
	if len(simulated) == 0 {
		return connections
	}
	filtered := make([]ConnectionEVO, 0, len(connections))
	for _, connection := range connections {
		if !simulated[connection.Id] {
			filtered = append(filtered, connection)
		}
	}
	return filtered
}

type StressConversationTarget struct {
	URL     string
	Pending bool
	AgentID int
}

// StressConversationTargets lists only conversations of simulated instances,
// so virtual agents never open (and mark as read) real customer conversations.
func StressConversationTargets(companyID int) ([]StressConversationTarget, error) {
	rows, err := db.Query(`SELECT cv.url::text, cv."agentId" IS NULL, COALESCE(cv."agentId", 0)
		FROM conversations cv
		JOIN stress_test_resources resource ON resource."resourceType" = $2 AND resource."resourceId" = cv."connectionId"
		WHERE cv."companyId" = $1 AND cv.status <> 'closed'
		ORDER BY cv."updatedAt" DESC LIMIT 300`, companyID, stressResourceConnection)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	targets := []StressConversationTarget{}
	for rows.Next() {
		var target StressConversationTarget
		if err := rows.Scan(&target.URL, &target.Pending, &target.AgentID); err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	return targets, rows.Err()
}

type StressSummary struct {
	CompanyID         int   `json:"companyId"`
	Contacts          int   `json:"contacts"`
	SimulatedContacts int   `json:"simulatedContacts"`
	Agents            int   `json:"agents"`
	Connections       int   `json:"connections"`
	Conversations     int   `json:"conversations"`
	Messages          int64 `json:"messages"`
}

// A companyID of 0 addresses every company. Simulated customers are contacts
// with the 000 prefix whose conversations all belong to simulated instances.
const stressScopeSQL = `WITH scope_resources AS (
		SELECT "resourceType", "resourceId" FROM stress_test_resources WHERE $1::integer = 0 OR "companyId" = $1
	), sim_connections AS (
		SELECT "resourceId" AS id FROM scope_resources WHERE "resourceType" = 'connection'
	), sim_customers AS (
		SELECT ct.id FROM contacts ct
		WHERE ct.number LIKE '` + StressNumberPrefix + `%' AND ($1::integer = 0 OR ct."companyId" = $1)
		AND EXISTS (SELECT 1 FROM conversations cv WHERE cv."contactId" = ct.id AND cv."connectionId" IN (SELECT id FROM sim_connections))
		AND NOT EXISTS (SELECT 1 FROM conversations cv WHERE cv."contactId" = ct.id
			AND (cv."connectionId" IS NULL OR cv."connectionId" NOT IN (SELECT id FROM sim_connections)))
	), scope_contacts AS (
		SELECT "resourceId" AS id FROM scope_resources WHERE "resourceType" = 'contact'
		UNION SELECT id FROM sim_customers
	), scope_conversations AS (
		SELECT cv.id FROM conversations cv
		WHERE cv."contactId" IN (SELECT id FROM scope_contacts) OR cv."connectionId" IN (SELECT id FROM sim_connections)
	)`

var (
	stressSummarySQL = stressScopeSQL + `
		SELECT
			(SELECT COUNT(*) FROM scope_resources resource JOIN contacts ct ON ct.id = resource."resourceId" WHERE resource."resourceType" = 'contact'),
			(SELECT COUNT(*) FROM sim_customers),
			(SELECT COUNT(*) FROM scope_resources resource JOIN agents agent ON agent.id = resource."resourceId" WHERE resource."resourceType" = 'agent'),
			(SELECT COUNT(*) FROM sim_connections connection JOIN connections existing ON existing.id = connection.id),
			(SELECT COUNT(*) FROM scope_conversations),
			(SELECT COUNT(*) FROM messages WHERE "conversationId" IN (SELECT id FROM scope_conversations))`
	stressRevertContactsSQL    = stressScopeSQL + ` DELETE FROM contacts WHERE id IN (SELECT id FROM scope_contacts)`
	stressRevertConnectionsSQL = `DELETE FROM connections WHERE id IN (
		SELECT "resourceId" FROM stress_test_resources WHERE "resourceType" = 'connection' AND ($1::integer = 0 OR "companyId" = $1))`
	stressRevertConnectionConversationsSQL = `DELETE FROM conversations WHERE "connectionId" IN (
		SELECT "resourceId" FROM stress_test_resources WHERE "resourceType" = 'connection' AND ($1::integer = 0 OR "companyId" = $1))`
)

func GetStressSummary(companyID int) (StressSummary, error) {
	summary := StressSummary{CompanyID: companyID}
	err := db.QueryRow(stressSummarySQL, companyID).Scan(
		&summary.Contacts, &summary.SimulatedContacts, &summary.Agents, &summary.Connections, &summary.Conversations, &summary.Messages)
	return summary, err
}

// StartStressRevert deletes every record created by stress tests in the scope:
// generated and simulated contacts with their conversations and messages,
// simulated instances and simulated agents. Real records are never selected.
func StartStressRevert(companyID int) (StressJob, error) {
	summary, err := GetStressSummary(companyID)
	if err != nil {
		return StressJob{}, err
	}
	job := &StressJob{Kind: StressJobRevert, CompanyID: companyID, Total: int(summary.Messages),
		ContactName: fmt.Sprintf("%d contatos, %d agentes, %d instâncias", summary.Contacts+summary.SimulatedContacts, summary.Agents, summary.Connections)}
	ctx, err := reserveStressJob(job)
	if err != nil {
		return StressJob{}, err
	}
	snapshot := *CurrentStressJob()
	go func() {
		err := revertStressData(ctx, companyID)
		if err != nil && ctx.Err() == nil {
			log.Printf("stress test revert failed: company=%d error=%v", companyID, err)
		}
		finishStressJob(ctx, err)
	}()
	return snapshot, nil
}

func revertStressData(ctx context.Context, companyID int) error {
	rows, err := db.QueryContext(ctx, stressScopeSQL+` SELECT cv.id, cv."companyId" FROM conversations cv WHERE cv.id IN (SELECT id FROM scope_conversations)`, companyID)
	if err != nil {
		return err
	}
	conversationIDs := pq.Int64Array{}
	companies := map[int64]int{}
	for rows.Next() {
		var id int64
		var company int
		if err := rows.Scan(&id, &company); err != nil {
			rows.Close()
			return err
		}
		conversationIDs = append(conversationIDs, id)
		companies[id] = company
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	done := 0
	for len(conversationIDs) > 0 {
		result, err := db.ExecContext(ctx, stressMessagesDeleteSQL, conversationIDs, stressCleanupBatchSize)
		if err != nil {
			return err
		}
		deleted, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if deleted == 0 {
			break
		}
		done += int(deleted)
		updateStressJob(func(job *StressJob) { job.Done = min(done, job.Total) })
	}
	// Contacts are resolved before the instances go: deleting a connection
	// nulls conversations."connectionId" and would hide simulated customers.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, stressRevertContactsSQL, companyID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, stressRevertConnectionConversationsSQL, companyID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, stressRevertConnectionsSQL, companyID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM stress_test_resources WHERE "resourceType" IN ('contact', 'connection') AND ($1::integer = 0 OR "companyId" = $1)`, companyID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for id, company := range companies {
		invalidateConversationHistory(company, int(id))
	}
	agentRows, err := db.QueryContext(ctx, `SELECT "resourceId", "companyId" FROM stress_test_resources
		WHERE "resourceType" = 'agent' AND ($1::integer = 0 OR "companyId" = $1)`, companyID)
	if err != nil {
		return err
	}
	type stressAgent struct{ id, company int }
	agents := []stressAgent{}
	for agentRows.Next() {
		var agent stressAgent
		if err := agentRows.Scan(&agent.id, &agent.company); err != nil {
			agentRows.Close()
			return err
		}
		agents = append(agents, agent)
	}
	agentRows.Close()
	if err := agentRows.Err(); err != nil {
		return err
	}
	for _, agent := range agents {
		if err := DeleteCompanyAgent(agent.id, agent.company); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err := unregisterStressResources(db, stressResourceAgent, agent.id); err != nil {
			return err
		}
	}
	return nil
}
