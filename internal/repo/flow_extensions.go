package repo

import (
	"database/sql"
	"encoding/json"
	"time"
)

func SetFlowContactCustomField(conversationID, executionID, companyID int, key string, value interface{}) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	result, err := db.Exec(`UPDATE contacts contact SET "customFields" = jsonb_set(contact."customFields", ARRAY[$4], $5::jsonb, true)
		FROM conversations conversation JOIN connections connection ON connection.id = conversation."connectionId"
		WHERE conversation.id = $1 AND contact.id = conversation."contactId" AND connection."companyId" = $2
		AND EXISTS (SELECT 1 FROM flow_executions execution WHERE execution.id = $3 AND execution."conversationId" = conversation.id AND execution."companyId" = $2 AND execution.status IN ('running','waiting'))`,
		conversationID, companyID, executionID, key, string(encoded))
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

func StartFlowNPSEvaluation(conversationID, executionID, companyID int, nodeID string) error {
	result, err := db.Exec(`INSERT INTO nps_evaluations ("companyId", "conversationId", "executionId", "nodeId", status)
		SELECT $3, conversation.id, execution.id, $4, 'waiting'
		FROM conversations conversation
		JOIN connections connection ON connection.id = conversation."connectionId" AND connection."companyId" = $3
		JOIN flow_executions execution ON execution.id = $2 AND execution."conversationId" = conversation.id
			AND execution."companyId" = $3 AND execution.status IN ('running', 'waiting')
		WHERE conversation.id = $1
		ON CONFLICT ("executionId", "nodeId") DO NOTHING`, conversationID, executionID, companyID, nodeID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows == 1 {
		return err
	}
	var exists bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM nps_evaluations WHERE "companyId" = $1 AND "conversationId" = $2 AND "executionId" = $3 AND "nodeId" = $4)`, companyID, conversationID, executionID, nodeID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return sql.ErrNoRows
	}
	return nil
}

func CompleteFlowNPSEvaluation(executionID int, nodeID string, score int, category, response string, answeredAt time.Time) error {
	result, err := db.Exec(`UPDATE nps_evaluations SET score = $1, category = $2, response = $3, status = 'answered', "answeredAt" = $4, "updatedAt" = now()
		WHERE "executionId" = $5 AND "nodeId" = $6 AND status = 'waiting'`, score, category, response, answeredAt, executionID, nodeID)
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

func InvalidateFlowNPSEvaluation(executionID int, nodeID, response string, answeredAt time.Time) error {
	result, err := db.Exec(`UPDATE nps_evaluations SET response = $1, status = 'invalid', "answeredAt" = $2, "updatedAt" = now()
		WHERE "executionId" = $3 AND "nodeId" = $4 AND status = 'waiting'`, response, answeredAt, executionID, nodeID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows == 1 {
		return err
	}
	return sql.ErrNoRows
}

func TimeoutFlowNPSEvaluation(executionID int, nodeID string) error {
	result, err := db.Exec(`UPDATE nps_evaluations SET status = 'timed_out', "updatedAt" = now() WHERE "executionId" = $1 AND "nodeId" = $2 AND status = 'waiting'`, executionID, nodeID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows == 1 {
		return err
	}
	return sql.ErrNoRows
}
