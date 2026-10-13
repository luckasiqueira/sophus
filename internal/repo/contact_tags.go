package repo

import (
	"database/sql"
	"errors"
	"strings"
)

var ErrTagInUseByFlow = errors.New("tag is used by a flow")

type CompanyTag struct {
	Id           int64  `json:"id"`
	Name         string `json:"name"`
	ContactCount int    `json:"contactCount"`
}

func ListCompanyTags(companyID int) ([]CompanyTag, error) {
	rows, err := db.Query(`SELECT tag.id, tag.name, COUNT(DISTINCT assignment."contactId")
		FROM company_tags tag LEFT JOIN contact_tag_assignments assignment ON assignment."tagId" = tag.id
		WHERE tag."companyId" = $1 GROUP BY tag.id, tag.name ORDER BY lower(tag.name), tag.id`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tags := []CompanyTag{}
	for rows.Next() {
		var tag CompanyTag
		if err := rows.Scan(&tag.Id, &tag.Name, &tag.ContactCount); err != nil {
			return nil, err
		}
		tags = append(tags, tag)
	}
	return tags, rows.Err()
}

func CreateCompanyTag(companyID int, name string) (CompanyTag, error) {
	name = strings.TrimSpace(name)
	var tag CompanyTag
	err := db.QueryRow(`INSERT INTO company_tags ("companyId", name) VALUES ($1, $2)
		RETURNING id, name, 0`, companyID, name).Scan(&tag.Id, &tag.Name, &tag.ContactCount)
	return tag, err
}

func RenameCompanyTag(companyID int, tagID int64, name string) (CompanyTag, error) {
	name = strings.TrimSpace(name)
	tx, err := db.Begin()
	if err != nil {
		return CompanyTag{}, err
	}
	defer tx.Rollback()
	var oldName string
	if err := tx.QueryRow(`SELECT name FROM company_tags WHERE id = $1 AND "companyId" = $2 FOR UPDATE`, tagID, companyID).Scan(&oldName); err != nil {
		return CompanyTag{}, err
	}
	var used bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM chatbot_flows WHERE "companyId" = $1 AND "flowData"::text ILIKE '%' || $2 || '%')`, companyID, oldName).Scan(&used); err != nil {
		return CompanyTag{}, err
	}
	if used {
		return CompanyTag{}, ErrTagInUseByFlow
	}
	if _, err := tx.Exec(`UPDATE company_tags SET name = $1, "updatedAt" = now() WHERE id = $2`, name, tagID); err != nil {
		return CompanyTag{}, err
	}
	if _, err := tx.Exec(`UPDATE conversations cv SET tags = ARRAY(SELECT CASE WHEN lower(item) = lower($1) THEN $2 ELSE item END FROM unnest(cv.tags) item)
		FROM connections co WHERE co.id = cv."connectionId" AND co."companyId" = $3`, oldName, name, companyID); err != nil {
		return CompanyTag{}, err
	}
	var tag CompanyTag
	if err := tx.QueryRow(`SELECT tag.id, tag.name, COUNT(assignment."contactId") FROM company_tags tag
		LEFT JOIN contact_tag_assignments assignment ON assignment."tagId" = tag.id WHERE tag.id = $1 GROUP BY tag.id`, tagID).Scan(&tag.Id, &tag.Name, &tag.ContactCount); err != nil {
		return CompanyTag{}, err
	}
	return tag, tx.Commit()
}

func DeleteCompanyTag(companyID int, tagID int64) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var name string
	if err := tx.QueryRow(`SELECT name FROM company_tags WHERE id = $1 AND "companyId" = $2 FOR UPDATE`, tagID, companyID).Scan(&name); err != nil {
		return err
	}
	var used bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM chatbot_flows WHERE "companyId" = $1 AND "flowData"::text ILIKE '%' || $2 || '%')`, companyID, name).Scan(&used); err != nil {
		return err
	}
	if used {
		return ErrTagInUseByFlow
	}
	if _, err := tx.Exec(`UPDATE conversations cv SET tags = ARRAY(SELECT item FROM unnest(cv.tags) item WHERE lower(item) <> lower($1))
		FROM connections co WHERE co.id = cv."connectionId" AND co."companyId" = $2`, name, companyID); err != nil {
		return err
	}
	result, err := tx.Exec(`DELETE FROM company_tags WHERE id = $1 AND "companyId" = $2`, tagID, companyID)
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
	return tx.Commit()
}

func SetContactTags(companyID, contactID int, tagIDs []int64) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM contacts WHERE id = $1 AND "companyId" = $2)`, contactID, companyID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return sql.ErrNoRows
	}
	if _, err := tx.Exec(`DELETE FROM contact_tag_assignments WHERE "contactId" = $1`, contactID); err != nil {
		return err
	}
	for _, tagID := range tagIDs {
		result, err := tx.Exec(`INSERT INTO contact_tag_assignments ("contactId", "tagId") SELECT $1, id FROM company_tags WHERE id = $2 AND "companyId" = $3`, contactID, tagID, companyID)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil || rows != 1 {
			if err != nil {
				return err
			}
			return sql.ErrNoRows
		}
	}
	if _, err := tx.Exec(`UPDATE conversations SET tags = ARRAY(SELECT tag.name FROM contact_tag_assignments assignment JOIN company_tags tag ON tag.id = assignment."tagId" WHERE assignment."contactId" = $1 ORDER BY lower(tag.name)), "updatedAt" = now() WHERE "contactId" = $1`, contactID); err != nil {
		return err
	}
	return tx.Commit()
}
