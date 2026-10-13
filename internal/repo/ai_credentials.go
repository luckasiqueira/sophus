package repo

import (
	"database/sql"
	"errors"
	"time"
)

var (
	ErrAICredentialConflict = errors.New("ai credential revision conflict")
	ErrAICredentialInUse    = errors.New("ai credential is in use")
)

type AICredential struct {
	Id               int       `json:"id"`
	CompanyId        int       `json:"companyId"`
	Name             string    `json:"name"`
	Provider         string    `json:"provider"`
	Model            string    `json:"model"`
	APIKeyCiphertext string    `json:"-"`
	HasAPIKey        bool      `json:"hasApiKey"`
	Revision         int       `json:"revision"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

type AICredentialInput struct {
	Name             string
	Provider         string
	Model            string
	APIKeyCiphertext string
	Revision         int
}

const aiCredentialColumns = `id, "companyId", name, provider, model, "apiKeyCiphertext", revision, "createdAt", "updatedAt"`

func ListAICredentials(companyID int) ([]AICredential, error) {
	rows, err := db.Query(`SELECT `+aiCredentialColumns+` FROM ai_credentials WHERE "companyId" = $1 ORDER BY lower(name), id`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	credentials := []AICredential{}
	for rows.Next() {
		credential, err := scanAICredential(rows)
		if err != nil {
			return nil, err
		}
		credentials = append(credentials, credential)
	}
	return credentials, rows.Err()
}

func GetAICredential(id, companyID int) (AICredential, error) {
	return scanAICredential(db.QueryRow(`SELECT `+aiCredentialColumns+` FROM ai_credentials WHERE id = $1 AND "companyId" = $2`, id, companyID))
}

func CreateAICredential(companyID int, input AICredentialInput) (AICredential, error) {
	return scanAICredential(db.QueryRow(`INSERT INTO ai_credentials ("companyId", name, provider, model, "apiKeyCiphertext")
		VALUES ($1, $2, $3, $4, $5) RETURNING `+aiCredentialColumns, companyID, input.Name, input.Provider, input.Model, input.APIKeyCiphertext))
}

func UpdateAICredential(id, companyID int, input AICredentialInput) (AICredential, error) {
	credential, err := scanAICredential(db.QueryRow(`UPDATE ai_credentials SET name = $1, provider = $2, model = $3,
		"apiKeyCiphertext" = CASE WHEN $4 = '' THEN "apiKeyCiphertext" ELSE $4 END,
		revision = revision + 1, "updatedAt" = now()
		WHERE id = $5 AND "companyId" = $6 AND revision = $7 RETURNING `+aiCredentialColumns,
		input.Name, input.Provider, input.Model, input.APIKeyCiphertext, id, companyID, input.Revision))
	if errors.Is(err, sql.ErrNoRows) {
		var exists bool
		if existsErr := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM ai_credentials WHERE id = $1 AND "companyId" = $2)`, id, companyID).Scan(&exists); existsErr != nil {
			return AICredential{}, existsErr
		}
		if exists {
			return AICredential{}, ErrAICredentialConflict
		}
	}
	return credential, err
}

func AICredentialReferences(id, companyID int) (agentReferenced, flowReferenced bool, err error) {
	err = db.QueryRow(`SELECT
		EXISTS(SELECT 1 FROM ai_agents WHERE "credentialId" = $1 AND "companyId" = $2),
		EXISTS(SELECT 1 FROM chatbot_flows flow,
			LATERAL jsonb_array_elements(COALESCE(flow."flowData"->'nodes', '[]'::jsonb)) node
			WHERE flow."companyId" = $2 AND flow."isActive" = true
			AND node->>'type' IN ('ai', 'conversationSummary', 'mediaTranscription')
			AND node->'data'->>'credentialId' = $1::text)`, id, companyID).
		Scan(&agentReferenced, &flowReferenced)
	return
}

func DeleteAICredential(id, companyID int) (bool, error) {
	agentReferenced, flowReferenced, err := AICredentialReferences(id, companyID)
	if err != nil {
		return false, err
	}
	if agentReferenced || flowReferenced {
		return false, ErrAICredentialInUse
	}
	result, err := db.Exec(`DELETE FROM ai_credentials WHERE id = $1 AND "companyId" = $2`, id, companyID)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func scanAICredential(scanner rowScanner) (AICredential, error) {
	var credential AICredential
	err := scanner.Scan(&credential.Id, &credential.CompanyId, &credential.Name, &credential.Provider, &credential.Model,
		&credential.APIKeyCiphertext, &credential.Revision, &credential.CreatedAt, &credential.UpdatedAt)
	credential.HasAPIKey = credential.APIKeyCiphertext != ""
	return credential, err
}
