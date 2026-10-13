package repo

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

const bytesPerGB int64 = 1 << 30

func GetCompanyStorageUsage(companyID int) (StorageUsage, error) {
	var usage StorageUsage
	var scannedAt sql.NullTime
	err := db.QueryRow(`SELECT c.id, COALESCE(su."usedBytes", 0), COALESCE(su."reservedBytes", 0),
		COALESCE(su."fileCount", 0), limits."storageLimitGB", su."scannedAt"
		FROM companies c
		JOIN subscriptions s ON s."companyId" = c.id AND s.status IN ('trialing', 'active', 'past_due')
		JOIN subscription_effective_limits limits ON limits."subscriptionId"=s.id
		LEFT JOIN company_storage_usage su ON su."companyId" = c.id
		WHERE c.id = $1`, companyID).Scan(&usage.CompanyId, &usage.UsedBytes, &usage.ReservedBytes,
		&usage.FileCount, &usage.StorageLimitGB, &scannedAt)
	if err != nil {
		return StorageUsage{}, err
	}
	usage.StorageLimitBytes = int64(usage.StorageLimitGB) * bytesPerGB
	if usage.StorageLimitBytes > 0 {
		usage.PercentUsed = float64(usage.UsedBytes) * 100 / float64(usage.StorageLimitBytes)
	}
	usage.Exceeded = usage.UsedBytes >= usage.StorageLimitBytes
	usage.ScannedAt = nullTimePointer(scannedAt)
	return usage, nil
}

func ReserveCompanyStorage(companyID int, bytes int64) error {
	if bytes <= 0 {
		return errors.New("storage reservation must be positive")
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var limitGB int
	if err := tx.QueryRow(`SELECT limits."storageLimitGB" FROM companies c
		JOIN subscriptions s ON s."companyId" = c.id AND s.status IN ('trialing', 'active', 'past_due')
		JOIN subscription_effective_limits limits ON limits."subscriptionId"=s.id WHERE c.id = $1 FOR UPDATE OF c`, companyID).Scan(&limitGB); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO company_storage_usage ("companyId") VALUES ($1)
		ON CONFLICT ("companyId") DO NOTHING`, companyID); err != nil {
		return err
	}
	var used, reserved int64
	if err := tx.QueryRow(`SELECT "usedBytes", "reservedBytes" FROM company_storage_usage
		WHERE "companyId" = $1 FOR UPDATE`, companyID).Scan(&used, &reserved); err != nil {
		return err
	}
	if used+reserved+bytes > int64(limitGB)*bytesPerGB {
		return ErrStorageLimitReached
	}
	if _, err := tx.Exec(`UPDATE company_storage_usage SET "reservedBytes" = "reservedBytes" + $1,
		"reservationUpdatedAt" = now(), "updatedAt" = now() WHERE "companyId" = $2`, bytes, companyID); err != nil {
		return err
	}
	return tx.Commit()
}

func CompleteCompanyStorageReservation(companyID int, reserved, actual int64) error {
	defer invalidateAccessCache()
	if reserved <= 0 || actual < 0 || actual > reserved {
		return errors.New("invalid storage reservation completion")
	}
	result, err := db.Exec(`UPDATE company_storage_usage SET
		"reservedBytes" = GREATEST(0, "reservedBytes" - $1),
		"reservationUpdatedAt" = CASE WHEN "reservedBytes" <= $1 THEN NULL ELSE "reservationUpdatedAt" END,
		"usedBytes" = "usedBytes" + $2,
		"fileCount" = "fileCount" + 1,
		"updatedAt" = now() WHERE "companyId" = $3`, reserved, actual, companyID)
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

func ReleaseCompanyStorageReservation(companyID int, reserved int64) error {
	if reserved <= 0 {
		return nil
	}
	_, err := db.Exec(`UPDATE company_storage_usage SET "reservedBytes" = GREATEST(0, "reservedBytes" - $1),
		"reservationUpdatedAt" = CASE WHEN "reservedBytes" <= $1 THEN NULL ELSE "reservationUpdatedAt" END,
		"updatedAt" = now() WHERE "companyId" = $2`, reserved, companyID)
	return err
}

func ReconcileCompanyStorage(companyID int, usedBytes int64, fileCount int, scannedAt time.Time) error {
	defer invalidateAccessCache()
	_, err := db.Exec(`INSERT INTO company_storage_usage ("companyId", "usedBytes", "fileCount", "scannedAt")
		VALUES ($1, $2, $3, $4)
		ON CONFLICT ("companyId") DO UPDATE SET "usedBytes" = EXCLUDED."usedBytes",
			"fileCount" = EXCLUDED."fileCount", "scannedAt" = EXCLUDED."scannedAt",
			"reservedBytes" = CASE WHEN company_storage_usage."reservationUpdatedAt" < now() - interval '1 hour' THEN 0 ELSE company_storage_usage."reservedBytes" END,
			"reservationUpdatedAt" = CASE WHEN company_storage_usage."reservationUpdatedAt" < now() - interval '1 hour' THEN NULL ELSE company_storage_usage."reservationUpdatedAt" END,
			"updatedAt" = now()
		WHERE company_storage_usage."reservedBytes" = 0 OR company_storage_usage."reservationUpdatedAt" < now() - interval '1 hour'`,
		companyID, usedBytes, fileCount, scannedAt)
	return err
}

func DecrementCompanyStorage(companyID int, bytes int64) error {
	defer invalidateAccessCache()
	_, err := db.Exec(`UPDATE company_storage_usage SET "usedBytes" = GREATEST(0, "usedBytes" - $1),
		"fileCount" = GREATEST(0, "fileCount" - 1), "updatedAt" = now() WHERE "companyId" = $2`, bytes, companyID)
	return err
}

func ListCompanyStoragePolicies() ([]CompanyStoragePolicy, error) {
	rows, err := db.Query(`SELECT id, "mediaRetentionDays", "operationSettings" FROM companies WHERE "isActive" = true ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	policies := []CompanyStoragePolicy{}
	for rows.Next() {
		var policy CompanyStoragePolicy
		var retention sql.NullInt64
		var operationsJSON []byte
		if err := rows.Scan(&policy.CompanyId, &retention, &operationsJSON); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(operationsJSON, &policy.Operations); err != nil {
			return nil, err
		}
		policy.Operations = normalizeCompanyOperationSettings(policy.Operations)
		if retention.Valid {
			value := int(retention.Int64)
			policy.MediaRetentionDays = &value
		}
		policies = append(policies, policy)
	}
	return policies, rows.Err()
}

func ApplyConversationInactivityPolicy(companyID, minutes int, now time.Time) (int, error) {
	if minutes <= 0 {
		return 0, nil
	}
	cutoff := now.Add(-time.Duration(minutes) * time.Minute)
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT cv.id FROM conversations cv JOIN connections co ON co.id = cv."connectionId"
		WHERE co."companyId" = $1 AND cv.status <> 'closed' AND COALESCE(
			(SELECT MAX(m."createdAt") FROM messages m WHERE m."conversationId" = cv.id), cv."updatedAt") < $2 FOR UPDATE`, companyID, cutoff)
	if err != nil {
		return 0, err
	}
	ids := []int{}
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	for _, id := range ids {
		if _, err := tx.Exec(`UPDATE flow_executions SET status = 'failed', "errorMessage" = 'execução cancelada por inatividade', "completedAt" = now(), "updatedAt" = now() WHERE "conversationId" = $1 AND status IN ('running','waiting')`, id); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`UPDATE ai_agent_sessions SET status = 'ended', "endedAt" = now(), "updatedAt" = now() WHERE "conversationId" = $1 AND status = 'active'`, id); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`UPDATE conversations SET status = 'closed', "updatedAt" = now() WHERE id = $1`, id); err != nil {
			return 0, err
		}
	}
	return len(ids), tx.Commit()
}

func DeleteExpiredConversations(companyID, days int, now time.Time) (int64, error) {
	if days <= 0 {
		return 0, nil
	}
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	cutoff := now.AddDate(0, 0, -days)
	if _, err := tx.Exec(`DELETE FROM messages m USING conversations cv, connections co
		WHERE m."conversationId" = cv.id AND co.id = cv."connectionId" AND co."companyId" = $1
			AND cv.status = 'closed' AND cv."updatedAt" < $2`, companyID, cutoff); err != nil {
		return 0, err
	}
	rows, err := tx.Query(`DELETE FROM conversations cv USING connections co WHERE co.id = cv."connectionId"
		AND co."companyId" = $1 AND cv.status = 'closed' AND cv."updatedAt" < $2 RETURNING cv.id`, companyID, cutoff)
	if err != nil {
		return 0, err
	}
	ids := []int{}
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	for _, id := range ids {
		invalidateConversationHistory(companyID, id)
		invalidateConversationEventHistory(companyID, id)
	}
	return int64(len(ids)), nil
}

func ListExpiredConversationMedia(companyID, days int, now time.Time) ([]string, error) {
	if days <= 0 {
		return nil, nil
	}
	rows, err := db.Query(`SELECT DISTINCT m."mediaPath" FROM messages m
		JOIN conversations cv ON cv.id = m."conversationId" JOIN connections co ON co.id = cv."connectionId"
		WHERE co."companyId" = $1 AND cv.status = 'closed' AND cv."updatedAt" < $2
			AND m."mediaScope" = 'messages' AND m."mediaPath" IS NOT NULL AND m."mediaPath" <> ''`, companyID, now.AddDate(0, 0, -days))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	paths := []string{}
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	return paths, rows.Err()
}

func ListExpiredMessageMedia(companyID, retentionDays int, now time.Time) ([]string, error) {
	cutoff := now.AddDate(0, 0, -retentionDays)
	rows, err := db.Query(`SELECT m."mediaPath" FROM messages m
		JOIN conversations c ON c.id = m."conversationId"
		JOIN connections cn ON cn.id = c."connectionId"
		WHERE cn."companyId" = $1 AND m."mediaScope" = 'messages' AND m."mediaPath" IS NOT NULL AND m."mediaPath" <> ''
		GROUP BY m."mediaPath" HAVING MAX(m."createdAt") < $2`, companyID, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	paths := []string{}
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	return paths, rows.Err()
}

func ClearMessageMediaPath(companyID int, path string) error {
	rows, err := db.Query(`UPDATE messages m SET "mediaPath" = NULL, "updatedAt" = now()
		FROM conversations c, connections cn
		WHERE m."conversationId" = c.id AND c."connectionId" = cn.id
			AND cn."companyId" = $1 AND m."mediaScope" = 'messages' AND m."mediaPath" = $2 RETURNING m."conversationId",m."messageId"`, companyID, path)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var conversationID int
		var messageID string
		if err := rows.Scan(&conversationID, &messageID); err != nil {
			return err
		}
		scheduleConversationHistoryUpdate(conversationID, messageID, companyID)
	}
	return rows.Err()
}

func CountMessageMediaReferences(companyID int, scope, path string) (int, error) {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM messages m
		JOIN conversations cv ON cv.id = m."conversationId"
		JOIN connections co ON co.id = cv."connectionId"
		WHERE co."companyId" = $1 AND m."mediaScope" = $2 AND m."mediaPath" = $3`, companyID, scope, path).Scan(&count)
	return count, err
}

func CountFlowMediaReferences(companyID int, fileName string) (int, error) {
	var count int
	err := db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM chatbot_flows WHERE "companyId" = $1 AND "flowData"::text LIKE $2) +
		(SELECT COUNT(*) FROM flow_executions WHERE "companyId" = $1 AND "flowDataSnapshot"::text LIKE $2)`,
		companyID, "%"+fileName+"%").Scan(&count)
	return count, err
}
