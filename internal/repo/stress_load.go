package repo

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"math/big"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// Stress load data is synthetic: the fictitious contact uses a number with
// country code 000 (never routable) and is marked in customFields so cleanup
// can only ever remove records created by this tool.
const (
	StressMessagesMax       = 1000000
	stressMessageBatchSize  = 5000
	stressCleanupBatchSize  = 10000
	stressMessageInterval   = 30 * time.Second
	stressContactNumberBase = "000"
)

const (
	StressJobGenerate = "generate"
	StressJobCleanup  = "cleanup"
	StressJobRevert   = "revert"

	StressJobRunning   = "running"
	StressJobCompleted = "completed"
	StressJobFailed    = "failed"
	StressJobCanceled  = "canceled"
)

var (
	ErrStressJobRunning      = errors.New("já existe um teste de estresse em execução")
	ErrStressAgentNotFound   = errors.New("usuário não encontrado nesta empresa")
	ErrStressContactNotFound = errors.New("contato de teste não encontrado nesta empresa")
)

// SQL is kept in constants so the PGlite test exercises the exact statements.
const (
	stressContactInsertSQL = `INSERT INTO contacts
		(name, number, email, company, "companyId", jid, lid, "isGroup", "isBlocked", "customFields")
		VALUES ($1, $2, '', '', $3, '', '', false, false, jsonb_build_object('stressTest', true))
		RETURNING id`
	stressConversationInsertSQL = `INSERT INTO conversations
		(status, "contactId", "connectionId", "agentId", url, "createdAt", "updatedAt")
		VALUES ('open', $1, $2, $3, $4, now(), now()) RETURNING id, protocol`
	stressMessagesInsertSQL = `INSERT INTO messages
		("messageId", text, "conversationId", "quotedId", "mediaType", "mediaPath",
		 "createdAt", "updatedAt", "isFromMe", "isGroup", "isRead", "isDeleted")
		SELECT 'stress-' || $1::integer || '-' || n, ($2::text[])[1 + n % cardinality($2::text[])],
		$1::integer, '', 'text', '', $3::timestamptz + n * $4::interval, $3::timestamptz + n * $4::interval,
		n % 3 = 0, false, true, false
		FROM generate_series($5::integer, $6::integer) n`
	stressMessagesDeleteSQL = `DELETE FROM messages WHERE id IN (
		SELECT id FROM messages WHERE "conversationId" = ANY($1) LIMIT $2)`
	stressContactsListSQL = `SELECT ct.id, ct."companyId", ct.name, ct.number,
		COALESCE(latest.url::text, ''), COALESCE(latest.protocol, 0), COALESCE(owner.name, ''),
		(SELECT COUNT(*) FROM messages m JOIN conversations cv ON cv.id = m."conversationId" WHERE cv."contactId" = ct.id),
		COALESCE(latest."createdAt", now())
		FROM contacts ct
		LEFT JOIN LATERAL (SELECT url, protocol, "agentId", "createdAt" FROM conversations
		WHERE "contactId" = ct.id ORDER BY id DESC LIMIT 1) latest ON true
		LEFT JOIN agents owner ON owner.id = latest."agentId"
		WHERE ct."companyId" = $1 AND ct."customFields" @> '{"stressTest": true}'
		ORDER BY ct.id DESC`
)

var stressMessageTexts = pq.StringArray{
	"Olá, tudo bem? Gostaria de mais informações sobre o serviço.",
	"Claro! Posso ajudar com o que você precisar.",
	"Qual é o prazo de entrega para a minha região?",
	"O prazo médio é de 3 a 5 dias úteis após a confirmação do pagamento.",
	"Perfeito, obrigado pelo retorno.",
	"Vocês aceitam pagamento via PIX?",
	"Sim, aceitamos PIX, boleto e cartão de crédito em até 12 vezes sem juros.",
	"Preciso alterar o endereço de entrega do meu pedido, é possível?",
	"É possível sim. Me informe o novo endereço completo, com CEP, número e complemento, que eu atualizo o cadastro e confirmo a alteração com a transportadora ainda hoje.",
	"Ok 👍",
	"Recebi o produto, mas a embalagem veio danificada.",
	"Sinto muito pelo ocorrido! Pode enviar uma foto da embalagem para abrirmos a ocorrência?",
	"Mensagem de teste de estresse gerada automaticamente para avaliar a navegação com grande volume de registros.",
	"Bom dia!",
	"Até mais, tenha um ótimo dia.",
}

type StressContact struct {
	ContactID       int       `json:"contactId"`
	CompanyID       int       `json:"companyId"`
	Name            string    `json:"name"`
	Number          string    `json:"number"`
	ConversationURL string    `json:"conversationUrl"`
	Protocol        int64     `json:"protocol"`
	AgentName       string    `json:"agentName"`
	MessageCount    int64     `json:"messageCount"`
	CreatedAt       time.Time `json:"createdAt"`
}

type StressJob struct {
	ID              string     `json:"id"`
	Kind            string     `json:"kind"`
	Status          string     `json:"status"`
	CompanyID       int        `json:"companyId"`
	AgentID         int        `json:"agentId,omitempty"`
	AgentName       string     `json:"agentName,omitempty"`
	ContactID       int        `json:"contactId"`
	ContactName     string     `json:"contactName"`
	ConversationURL string     `json:"conversationUrl,omitempty"`
	Protocol        int64      `json:"protocol,omitempty"`
	Total           int        `json:"total"`
	Done            int        `json:"done"`
	Error           string     `json:"error,omitempty"`
	StartedAt       time.Time  `json:"startedAt"`
	FinishedAt      *time.Time `json:"finishedAt,omitempty"`
}

// Only one stress job runs per process so a test cannot be multiplied by
// accident; progress is kept in memory and is lost if the server restarts.
var stressJobs = struct {
	sync.Mutex
	current *StressJob
	cancel  context.CancelFunc
}{}

func CurrentStressJob() *StressJob {
	stressJobs.Lock()
	defer stressJobs.Unlock()
	if stressJobs.current == nil {
		return nil
	}
	job := *stressJobs.current
	return &job
}

func CancelStressJob() bool {
	stressJobs.Lock()
	defer stressJobs.Unlock()
	if stressJobs.current == nil || stressJobs.current.Status != StressJobRunning || stressJobs.cancel == nil {
		return false
	}
	stressJobs.cancel()
	return true
}

func reserveStressJob(job *StressJob) (context.Context, error) {
	stressJobs.Lock()
	defer stressJobs.Unlock()
	if stressJobs.current != nil && stressJobs.current.Status == StressJobRunning {
		return nil, ErrStressJobRunning
	}
	ctx, cancel := context.WithCancel(context.Background())
	job.ID = uuid.NewString()
	job.Status = StressJobRunning
	job.StartedAt = time.Now()
	stressJobs.current = job
	stressJobs.cancel = cancel
	return ctx, nil
}

func updateStressJob(update func(job *StressJob)) {
	stressJobs.Lock()
	defer stressJobs.Unlock()
	if stressJobs.current != nil {
		update(stressJobs.current)
	}
}

func finishStressJob(ctx context.Context, err error) {
	updateStressJob(func(job *StressJob) {
		now := time.Now()
		job.FinishedAt = &now
		switch {
		case err == nil:
			job.Status = StressJobCompleted
		case ctx.Err() != nil:
			job.Status = StressJobCanceled
		default:
			job.Status = StressJobFailed
			job.Error = err.Error()
		}
	})
	stressJobs.Lock()
	if stressJobs.cancel != nil {
		stressJobs.cancel()
		stressJobs.cancel = nil
	}
	stressJobs.Unlock()
}

// StartStressGeneration creates the fictitious contact and its conversation
// synchronously, so validation errors reach the caller, then inserts the
// messages in committed batches; the conversation is navigable while it grows.
func StartStressGeneration(companyID, agentID, count int) (StressJob, error) {
	if count < 1 || count > StressMessagesMax {
		return StressJob{}, fmt.Errorf("a quantidade deve estar entre 1 e %d", StressMessagesMax)
	}
	job := &StressJob{Kind: StressJobGenerate, CompanyID: companyID, AgentID: agentID, Total: count}
	ctx, err := reserveStressJob(job)
	if err != nil {
		return StressJob{}, err
	}
	conversationID, err := createStressConversation(job)
	if err != nil {
		finishStressJob(ctx, err)
		return StressJob{}, err
	}
	snapshot := *CurrentStressJob()
	go func() {
		err := insertStressMessages(ctx, companyID, conversationID, count)
		if err != nil && ctx.Err() == nil {
			log.Printf("stress test generation failed: company=%d conversation=%d error=%v", companyID, conversationID, err)
		}
		finishStressJob(ctx, err)
	}()
	return snapshot, nil
}

func createStressConversation(job *StressJob) (int, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock($1, $2)`, contactCompanyLockNamespace, job.CompanyID); err != nil {
		return 0, err
	}
	var agentName string
	err = tx.QueryRow(`SELECT name FROM agents WHERE id = $1 AND "companyId" = $2`, job.AgentID, job.CompanyID).Scan(&agentName)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrStressAgentNotFound
	}
	if err != nil {
		return 0, err
	}
	// Prefer a connected instance so the conversation behaves like a live one;
	// without any instance it is stored as connectionless history.
	var connectionID sql.NullInt64
	err = tx.QueryRow(`SELECT id FROM connections WHERE "companyId" = $1
		ORDER BY (status = $2) DESC, id LIMIT 1`, job.CompanyID, ConnectionStatusConnected).Scan(&connectionID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	number := ""
	for attempt := 0; attempt < 5 && number == ""; attempt++ {
		candidate, err := stressContactNumber()
		if err != nil {
			return 0, err
		}
		var exists bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM contacts WHERE "companyId" = $1 AND number = $2)`, job.CompanyID, candidate).Scan(&exists); err != nil {
			return 0, err
		}
		if !exists {
			number = candidate
		}
	}
	if number == "" {
		return 0, errors.New("não foi possível gerar um número fictício único")
	}
	name := "Contato fictício · Estresse " + number[len(number)-4:]
	var contactID, conversationID int
	var protocol int64
	conversationURL := uuid.New()
	if err := tx.QueryRow(stressContactInsertSQL, name, number, job.CompanyID).Scan(&contactID); err != nil {
		return 0, err
	}
	if err := registerStressResources(tx, job.CompanyID, stressResourceContact, contactID); err != nil {
		return 0, err
	}
	if err := tx.QueryRow(stressConversationInsertSQL,
		contactID, connectionID, job.AgentID, conversationURL).Scan(&conversationID, &protocol); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	updateStressJob(func(current *StressJob) {
		current.AgentName = agentName
		current.ContactID = contactID
		current.ContactName = name
		current.ConversationURL = conversationURL.String()
		current.Protocol = protocol
	})
	return conversationID, nil
}

func stressContactNumber() (string, error) {
	value, err := rand.Int(rand.Reader, big.NewInt(10_000_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%010d", stressContactNumberBase, value.Int64()), nil
}

// Messages alternate between contact and agent and are spaced 30 seconds apart,
// ending at the start time, so ordering and pagination match a real history.
func insertStressMessages(ctx context.Context, companyID, conversationID, count int) error {
	start := time.Now().Add(-time.Duration(count) * stressMessageInterval)
	for first := 1; first <= count; first += stressMessageBatchSize {
		last := min(first+stressMessageBatchSize-1, count)
		_, err := db.ExecContext(ctx, stressMessagesInsertSQL,
			conversationID, stressMessageTexts, start, fmt.Sprintf("%d seconds", int(stressMessageInterval.Seconds())), first, last)
		if err != nil {
			return err
		}
		invalidateConversationHistory(companyID, conversationID)
		updateStressJob(func(job *StressJob) { job.Done = last })
	}
	_, err := db.ExecContext(ctx, `UPDATE conversations SET "updatedAt" = now() WHERE id = $1`, conversationID)
	return err
}

// StartStressCleanup removes a fictitious contact created by this tool. The
// messages go first in small batches so the final cascade stays short.
func StartStressCleanup(companyID, contactID int) (StressJob, error) {
	var name string
	var total int
	err := db.QueryRow(`SELECT ct.name, (SELECT COUNT(*) FROM messages m JOIN conversations cv ON cv.id = m."conversationId" WHERE cv."contactId" = ct.id)
		FROM contacts ct WHERE ct.id = $1 AND ct."companyId" = $2 AND ct."customFields" @> '{"stressTest": true}'`,
		contactID, companyID).Scan(&name, &total)
	if errors.Is(err, sql.ErrNoRows) {
		return StressJob{}, ErrStressContactNotFound
	}
	if err != nil {
		return StressJob{}, err
	}
	job := &StressJob{Kind: StressJobCleanup, CompanyID: companyID, ContactID: contactID, ContactName: name, Total: total}
	ctx, err := reserveStressJob(job)
	if err != nil {
		return StressJob{}, err
	}
	snapshot := *CurrentStressJob()
	go func() {
		err := deleteStressContact(ctx, companyID, contactID)
		if err != nil && ctx.Err() == nil {
			log.Printf("stress test cleanup failed: company=%d contact=%d error=%v", companyID, contactID, err)
		}
		finishStressJob(ctx, err)
	}()
	return snapshot, nil
}

func deleteStressContact(ctx context.Context, companyID, contactID int) error {
	rows, err := db.QueryContext(ctx, `SELECT id FROM conversations WHERE "contactId" = $1`, contactID)
	if err != nil {
		return err
	}
	conversationIDs := pq.Int64Array{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		conversationIDs = append(conversationIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	done := 0
	for {
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
	for _, id := range conversationIDs {
		invalidateConversationHistory(companyID, int(id))
	}
	if err := DeleteCompanyContact(contactID, companyID); err != nil {
		return err
	}
	return unregisterStressResources(db, stressResourceContact, contactID)
}

func ListStressContacts(companyID int) ([]StressContact, error) {
	rows, err := db.Query(stressContactsListSQL, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	contacts := []StressContact{}
	for rows.Next() {
		var contact StressContact
		if err := rows.Scan(&contact.ContactID, &contact.CompanyID, &contact.Name, &contact.Number, &contact.ConversationURL,
			&contact.Protocol, &contact.AgentName, &contact.MessageCount, &contact.CreatedAt); err != nil {
			return nil, err
		}
		contacts = append(contacts, contact)
	}
	return contacts, rows.Err()
}
