package repo

import (
	"database/sql"
	"errors"
	"time"
)

const bytesPerGB int64 = 1 << 30

func GetCompanyStorageUsage(companyID int) (StorageUsage, error) {
	var usage StorageUsage
	var scannedAt sql.NullTime
	err := db.QueryRow(`SELECT c.id, COALESCE(su."usedBytes", 0), COALESCE(su."reservedBytes", 0),
		COALESCE(su."fileCount", 0), p."storageLimitGB", su."scannedAt"
		FROM companies c
		JOIN subscriptions s ON s."companyId" = c.id AND s.status IN ('trialing', 'active', 'past_due')
		JOIN plans p ON p.id = s."planId"
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
	if err := tx.QueryRow(`SELECT p."storageLimitGB" FROM companies c
		JOIN subscriptions s ON s."companyId" = c.id AND s.status IN ('trialing', 'active', 'past_due')
		JOIN plans p ON p.id = s."planId" WHERE c.id = $1 FOR UPDATE OF c`, companyID).Scan(&limitGB); err != nil {
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
	_, err := db.Exec(`UPDATE company_storage_usage SET "usedBytes" = GREATEST(0, "usedBytes" - $1),
		"fileCount" = GREATEST(0, "fileCount" - 1), "updatedAt" = now() WHERE "companyId" = $2`, bytes, companyID)
	return err
}

func ListCompanyStoragePolicies() ([]CompanyStoragePolicy, error) {
	rows, err := db.Query(`SELECT id, "mediaRetentionDays" FROM companies WHERE "isActive" = true ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	policies := []CompanyStoragePolicy{}
	for rows.Next() {
		var policy CompanyStoragePolicy
		var retention sql.NullInt64
		if err := rows.Scan(&policy.CompanyId, &retention); err != nil {
			return nil, err
		}
		if retention.Valid {
			value := int(retention.Int64)
			policy.MediaRetentionDays = &value
		}
		policies = append(policies, policy)
	}
	return policies, rows.Err()
}

func ListExpiredMessageMedia(companyID, retentionDays int, now time.Time) ([]string, error) {
	cutoff := now.AddDate(0, 0, -retentionDays)
	rows, err := db.Query(`SELECT DISTINCT m."mediaPath" FROM messages m
		JOIN conversations c ON c.id = m."conversationId"
		JOIN connections cn ON cn.id = c."connectionId"
		WHERE cn."companyId" = $1 AND m."mediaPath" IS NOT NULL AND m."mediaPath" <> '' AND m."createdAt" < $2`, companyID, cutoff)
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
	_, err := db.Exec(`UPDATE messages m SET "mediaPath" = NULL, "updatedAt" = now()
		FROM conversations c, connections cn
		WHERE m."conversationId" = c.id AND c."connectionId" = cn.id
			AND cn."companyId" = $1 AND m."mediaPath" = $2`, companyID, path)
	return err
}

func CountFlowMediaReferences(companyID int, fileName string) (int, error) {
	var count int
	err := db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM chatbot_flows WHERE "companyId" = $1 AND "flowData"::text LIKE $2) +
		(SELECT COUNT(*) FROM flow_executions WHERE "companyId" = $1 AND "flowDataSnapshot"::text LIKE $2)`,
		companyID, "%"+fileName+"%").Scan(&count)
	return count, err
}
