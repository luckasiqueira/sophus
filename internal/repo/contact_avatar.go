package repo

import (
	"context"
	"database/sql"
	"time"
)

type ContactAvatarRefresh struct {
	ContactID    int
	Number       string
	ConnectionID int
	RequestedAt  time.Time
	CompanyID    int
}

func NextContactAvatarRefresh() (ContactAvatarRefresh, error) {
	var refresh ContactAvatarRefresh
	err := db.QueryRow(`SELECT contact.id, COALESCE(NULLIF(contact.jid, ''), NULLIF(contact.number, ''), contact.lid), connection.id, contact."avatarRefreshRequestedAt", contact."companyId"
		FROM contacts contact
		JOIN LATERAL (
			SELECT connection.id FROM connections connection
			WHERE connection."companyId" = contact."companyId" AND connection.status = 'connected'
			ORDER BY EXISTS(SELECT 1 FROM conversations conversation
				WHERE conversation."contactId" = contact.id AND conversation."connectionId" = connection.id) DESC, connection.id
			LIMIT 1
		) connection ON true
		WHERE contact."avatarRefreshRequestedAt" IS NOT NULL
		AND contact."avatarRefreshRequestedAt" <= now()
		AND (contact."avatarUpdatedAt" IS NULL OR contact."avatarUpdatedAt" < contact."avatarRefreshRequestedAt")
		AND COALESCE(contact."isGroup", false) = false
		AND COALESCE(NULLIF(contact.jid, ''), NULLIF(contact.number, ''), NULLIF(contact.lid, '')) IS NOT NULL
		ORDER BY contact."avatarRefreshRequestedAt", contact.id LIMIT 1`).Scan(
		&refresh.ContactID, &refresh.Number, &refresh.ConnectionID, &refresh.RequestedAt, &refresh.CompanyID)
	return refresh, err
}

func RetryContactAvatarRefresh(contactID int, requestedAt time.Time, delay time.Duration) error {
	result, err := db.Exec(`UPDATE contacts SET "avatarRefreshRequestedAt" = now() + ($1 * interval '1 second')
		WHERE id = $2 AND "avatarRefreshRequestedAt" = $3`, delay.Seconds(), contactID, requestedAt)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

const completeContactAvatarSQL = `UPDATE contacts SET "avatarUrl" = $1::text, "avatarUpdatedAt" = now()
	WHERE id = $2 AND "avatarRefreshRequestedAt" = $3`

func CompleteContactAvatarRefresh(contactID int, requestedAt time.Time, avatarURL string) error {
	defer invalidateAvatarCache(contactID)
	result, err := db.Exec(completeContactAvatarSQL, avatarURL, contactID, requestedAt)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

const contactPictureChangeSQL = `UPDATE contacts SET "avatarUrl" = '',
	"avatarRefreshRequestedAt" = CASE WHEN $5::boolean THEN NULL ELSE clock_timestamp() END,
	"avatarUpdatedAt" = CASE WHEN $5::boolean THEN now() ELSE "avatarUpdatedAt" END
	WHERE "companyId" = $1 AND COALESCE("isGroup", false) = false
	AND (($2::text <> '' AND number = $2) OR ($3::text <> '' AND jid = $3) OR ($4::text <> '' AND lid = $4))
	RETURNING id`

func ApplyContactPictureChange(ctx context.Context, companyID int, identity string, removed bool) ([]int, error) {
	number, jid, lid := canonicalWhatsAppIdentity(identity)
	ids := []int{}
	if number == "" && jid == "" && lid == "" {
		return ids, nil
	}
	rows, err := db.QueryContext(ctx, contactPictureChangeSQL, companyID, number, jid, lid, removed)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
		invalidateAvatarCache(id)
	}
	return ids, rows.Err()
}

// Broken-photo reports preserve a pending retry instead of resetting its timer
// for every image of the same contact on the page.
const invalidateContactAvatarSQL = `UPDATE contacts SET "avatarUrl" = '',
	"avatarRefreshRequestedAt" = CASE
		WHEN "avatarRefreshRequestedAt" IS NULL OR "avatarUpdatedAt" >= "avatarRefreshRequestedAt" THEN clock_timestamp()
		ELSE "avatarRefreshRequestedAt" END
	WHERE id = $1 AND "companyId" = $2 AND COALESCE("isGroup", false) = false
	AND (COALESCE("avatarUrl", '') <> '' OR
		(("avatarRefreshRequestedAt" IS NULL OR "avatarUpdatedAt" >= "avatarRefreshRequestedAt")
		AND ("avatarUpdatedAt" IS NULL OR "avatarUpdatedAt" < now() - interval '1 minute')))`

func InvalidateContactAvatar(ctx context.Context, companyID, contactID int) (bool, error) {
	defer invalidateAvatarCache(contactID)
	result, err := db.ExecContext(ctx, invalidateContactAvatarSQL, contactID, companyID)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count > 0, err
}

func RequestContactAvatarRefresh(contactID int) error {
	_, err := db.Exec(`UPDATE contacts SET "avatarRefreshRequestedAt" = now() WHERE id = $1`, contactID)
	return err
}
