package repo

import (
	"database/sql"
	"errors"
	"strings"
)

const contactCompanyLockNamespace = 0x4349

// Parameters reused in varchar assignments and text comparisons must have an
// explicit type; otherwise PostgreSQL can reject the import with 42P08.
const contactImportUpdateSQL = `UPDATE contacts SET
	name = CASE WHEN BTRIM(name) = '' OR name = 'Contato salvo automaticamente' THEN $1::text ELSE name END,
	company = CASE WHEN BTRIM(company) = '' THEN $2::text ELSE company END,
	jid = CASE WHEN COALESCE(BTRIM(jid), '') = '' THEN $3::text ELSE jid END,
	"avatarRefreshRequestedAt" = CASE WHEN
		((BTRIM(name) = '' OR name = 'Contato salvo automaticamente') AND name <> $1::text)
		OR (BTRIM(company) = '' AND $2::text <> '')
		OR (COALESCE(BTRIM(jid), '') = '' AND $3::text <> '')
		THEN now() ELSE "avatarRefreshRequestedAt" END
	WHERE id = $4`

const contactImportInsertSQL = `INSERT INTO contacts (name, number, email, company, "companyId", jid, lid, "isGroup", "isBlocked", "avatarRefreshRequestedAt")
	SELECT $1::text, $2::text, '', $3::text, $4::integer, $5::text, '', false, false, now()
	WHERE NOT EXISTS (SELECT 1 FROM contacts WHERE "companyId" = $4::integer AND (number = $2::text OR jid = $5::text))
	ON CONFLICT DO NOTHING`

type ContactImport struct {
	Name    string
	Number  string
	JID     string
	Company string
}

type ContactImportResult struct {
	Received int `json:"received"`
	Imported int `json:"imported"`
	Updated  int `json:"updated"`
	Skipped  int `json:"skipped"`
	Invalid  int `json:"invalid"`
	Excluded int `json:"excluded"`
}

func ImportConnectionContacts(companyID, connectionID int, contacts []ContactImport, invalid int) (ContactImportResult, error) {
	result := ContactImportResult{Received: len(contacts) + invalid, Invalid: invalid}
	tx, err := db.Begin()
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock($1, $2)`, contactCompanyLockNamespace, companyID); err != nil {
		return result, err
	}
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM connections WHERE id = $1 AND "companyId" = $2)`, connectionID, companyID).Scan(&exists); err != nil {
		return result, err
	}
	if !exists {
		return result, sql.ErrNoRows
	}
	for _, contact := range contacts {
		var currentID int
		var currentName, currentCompany, currentJID string
		err := tx.QueryRow(`SELECT id, name, company, COALESCE(jid, '') FROM contacts
			WHERE "companyId" = $1 AND (number = $2 OR jid = $3) ORDER BY id DESC LIMIT 1 FOR UPDATE`,
			companyID, contact.Number, contact.JID).Scan(&currentID, &currentName, &currentCompany, &currentJID)
		if err == nil {
			changed := ((strings.TrimSpace(currentName) == "" || currentName == "Contato salvo automaticamente") && currentName != contact.Name) ||
				(strings.TrimSpace(currentCompany) == "" && contact.Company != "") || (strings.TrimSpace(currentJID) == "" && contact.JID != "")
			if _, err := tx.Exec(contactImportUpdateSQL, contact.Name, contact.Company, contact.JID, currentID); err != nil {
				return result, err
			}
			if changed {
				result.Updated++
			} else {
				result.Skipped++
			}
			continue
		}
		switch {
		case !errors.Is(err, sql.ErrNoRows):
			return result, err
		}
		insert, err := tx.Exec(contactImportInsertSQL,
			contact.Name, contact.Number, contact.Company, companyID, contact.JID)
		if err != nil {
			return result, err
		}
		rows, err := insert.RowsAffected()
		if err != nil {
			return result, err
		}
		if rows == 1 {
			result.Imported++
		} else {
			result.Skipped++
		}
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}

func NormalizeImportedContact(name, company, jid string) (ContactImport, bool) {
	jid = strings.TrimSpace(jid)
	if !strings.HasSuffix(strings.ToLower(jid), "@s.whatsapp.net") {
		return ContactImport{}, false
	}
	number := strings.TrimSpace(strings.SplitN(jid, "@", 2)[0])
	if number == "" {
		return ContactImport{}, false
	}
	for _, character := range number {
		if character < '0' || character > '9' {
			return ContactImport{}, false
		}
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = number
	}
	return ContactImport{Name: name, Number: number, JID: jid, Company: strings.TrimSpace(company)}, true
}
