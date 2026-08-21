package repo

import (
	"database/sql"
	"time"
)

type ScheduledFlowMessage struct {
	ID             int64
	CompanyID      int
	ConversationID int
	ConnectionID   int
	Message        string
	Attempts       int
	ClaimVersion   int
	DueAt          time.Time
}

func ScheduleFlowMessage(conversationID, executionID, companyID int, nodeID, message string, delayMinutes int) (time.Time, error) {
	var dueAt time.Time
	err := db.QueryRow(`INSERT INTO scheduled_flow_messages
		("companyId", "conversationId", "connectionId", "executionId", "nodeId", message, "dueAt")
		SELECT $3, conversation.id, conversation."connectionId", execution.id, $4, $5,
			now() + ($6 * interval '1 minute')
		FROM conversations conversation
		JOIN connections connection ON connection.id = conversation."connectionId" AND connection."companyId" = $3
		JOIN flow_executions execution ON execution.id = $2 AND execution."conversationId" = conversation.id
			AND execution."companyId" = $3 AND execution.status IN ('running', 'waiting')
		WHERE conversation.id = $1
		ON CONFLICT ("executionId", "nodeId") WHERE "executionId" IS NOT NULL
		DO UPDATE SET message = scheduled_flow_messages.message
		RETURNING "dueAt"`, conversationID, executionID, companyID, nodeID, message, delayMinutes).Scan(&dueAt)
	return dueAt, err
}

func ClaimDueScheduledFlowMessages(limit int) ([]ScheduledFlowMessage, error) {
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE scheduled_flow_messages
		SET status = 'pending', "claimedAt" = NULL, "updatedAt" = now()
		WHERE status = 'processing' AND "claimedAt" <= now() - interval '5 minutes' AND attempts < 3`); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE scheduled_flow_messages
		SET status = 'failed', "claimedAt" = NULL, "lastError" = COALESCE("lastError", 'claim expirado'), "updatedAt" = now()
		WHERE status = 'processing' AND "claimedAt" <= now() - interval '5 minutes' AND attempts >= 3`); err != nil {
		return nil, err
	}
	rows, err := tx.Query(`WITH due AS (
		SELECT id FROM scheduled_flow_messages
		WHERE status = 'pending' AND "dueAt" <= now() AND attempts < 3
		ORDER BY "dueAt", id FOR UPDATE SKIP LOCKED LIMIT $1
	)
	UPDATE scheduled_flow_messages message
	SET status = 'processing', attempts = attempts + 1, "claimVersion" = "claimVersion" + 1,
		"claimedAt" = now(), "updatedAt" = now()
	FROM due WHERE message.id = due.id
	RETURNING message.id, message."companyId", message."conversationId", message."connectionId",
		message.message, message.attempts, message."claimVersion", message."dueAt"`, limit)
	if err != nil {
		return nil, err
	}
	messages := []ScheduledFlowMessage{}
	for rows.Next() {
		var message ScheduledFlowMessage
		if err := rows.Scan(&message.ID, &message.CompanyID, &message.ConversationID, &message.ConnectionID,
			&message.Message, &message.Attempts, &message.ClaimVersion, &message.DueAt); err != nil {
			rows.Close()
			return nil, err
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return messages, nil
}

func FinishScheduledFlowMessage(id int64, claimVersion int, sendErr error) error {
	if sendErr == nil {
		result, err := db.Exec(`UPDATE scheduled_flow_messages SET status = 'sent', "sentAt" = now(),
			"claimedAt" = NULL, "lastError" = NULL, "updatedAt" = now()
			WHERE id = $1 AND status = 'processing' AND "claimVersion" = $2`, id, claimVersion)
		return scheduledMessageUpdateResult(result, err)
	}
	result, err := db.Exec(`UPDATE scheduled_flow_messages SET
		status = CASE WHEN attempts >= 3 THEN 'failed' ELSE 'pending' END,
		"dueAt" = CASE WHEN attempts >= 3 THEN "dueAt" ELSE now() + (attempts * interval '1 minute') END,
		"claimedAt" = NULL, "lastError" = $2, "updatedAt" = now()
		WHERE id = $1 AND status = 'processing' AND "claimVersion" = $3`, id, sendErr.Error(), claimVersion)
	return scheduledMessageUpdateResult(result, err)
}

func scheduledMessageUpdateResult(result sql.Result, err error) error {
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
