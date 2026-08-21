package repo

import (
	"database/sql"
	"errors"
	"time"
)

var ErrFlowTemplateConflict = errors.New("flow message template was changed")

type FlowMessageTemplate struct {
	ID        int       `json:"id"`
	CompanyID int       `json:"companyId"`
	Name      string    `json:"name"`
	Content   string    `json:"content"`
	Revision  int       `json:"revision"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func ListFlowMessageTemplates(companyID int) ([]FlowMessageTemplate, error) {
	rows, err := db.Query(`SELECT id, "companyId", name, content, revision, "createdAt", "updatedAt"
		FROM flow_message_templates WHERE "companyId" = $1 ORDER BY lower(name), id`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	templates := []FlowMessageTemplate{}
	for rows.Next() {
		var template FlowMessageTemplate
		if err := rows.Scan(&template.ID, &template.CompanyID, &template.Name, &template.Content,
			&template.Revision, &template.CreatedAt, &template.UpdatedAt); err != nil {
			return nil, err
		}
		templates = append(templates, template)
	}
	return templates, rows.Err()
}

func GetFlowMessageTemplate(id, companyID int) (FlowMessageTemplate, error) {
	var template FlowMessageTemplate
	err := db.QueryRow(`SELECT id, "companyId", name, content, revision, "createdAt", "updatedAt"
		FROM flow_message_templates WHERE id = $1 AND "companyId" = $2`, id, companyID).Scan(
		&template.ID, &template.CompanyID, &template.Name, &template.Content,
		&template.Revision, &template.CreatedAt, &template.UpdatedAt,
	)
	return template, err
}

func CreateFlowMessageTemplate(template FlowMessageTemplate) (FlowMessageTemplate, error) {
	err := db.QueryRow(`INSERT INTO flow_message_templates ("companyId", name, content)
		VALUES ($1, $2, $3) RETURNING id, revision, "createdAt", "updatedAt"`,
		template.CompanyID, template.Name, template.Content).Scan(
		&template.ID, &template.Revision, &template.CreatedAt, &template.UpdatedAt,
	)
	return template, err
}

func UpdateFlowMessageTemplate(template *FlowMessageTemplate) error {
	err := db.QueryRow(`UPDATE flow_message_templates SET name = $1, content = $2,
		revision = revision + 1, "updatedAt" = now()
		WHERE id = $3 AND "companyId" = $4 AND revision = $5
		RETURNING revision, "updatedAt"`, template.Name, template.Content, template.ID,
		template.CompanyID, template.Revision).Scan(&template.Revision, &template.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrFlowTemplateConflict
	}
	return err
}

func DeleteFlowMessageTemplate(id, companyID int) error {
	result, err := db.Exec(`DELETE FROM flow_message_templates WHERE id = $1 AND "companyId" = $2`, id, companyID)
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
