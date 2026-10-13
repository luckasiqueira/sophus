package repo

import (
	"database/sql"
	"strings"
	"time"
)

type AuditEvent struct {
	ID         int64     `json:"id"`
	CompanyID  *int      `json:"companyId"`
	ActorID    *int      `json:"actorId"`
	ActorEmail string    `json:"actorEmail"`
	ActorType  string    `json:"actorType"`
	Action     string    `json:"action"`
	TargetType string    `json:"targetType"`
	TargetID   string    `json:"targetId"`
	IPAddress  string    `json:"ipAddress"`
	CreatedAt  time.Time `json:"createdAt"`
}

type AuditPage struct {
	Data  []AuditEvent `json:"data"`
	Total int          `json:"total"`
	Page  int          `json:"page"`
}

func RecordAuditEvent(event AuditEvent) error {
	_, err := db.Exec(`INSERT INTO audit_events ("companyId", "actorId", "actorEmail", "actorType", action, "targetType", "targetId", "ipAddress")
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, event.CompanyID, event.ActorID, event.ActorEmail,
		event.ActorType, event.Action, event.TargetType, event.TargetID, event.IPAddress)
	return err
}

func ListAuditEvents(companyID, actorID *int, actorEmail string, page int) (AuditPage, error) {
	if page < 1 {
		page = 1
	}
	result := AuditPage{Data: []AuditEvent{}, Page: page}
	var company, actor any
	if companyID != nil {
		company = *companyID
	}
	if actorID != nil {
		actor = *actorID
	}
	email := strings.TrimSpace(actorEmail)
	err := db.QueryRow(`SELECT count(*) FROM audit_events WHERE ($1::integer IS NULL OR "companyId" = $1)
		AND ($2::integer IS NULL OR "actorId" = $2) AND ($3 = '' OR "actorEmail" ILIKE '%' || $3 || '%')`, company, actor, email).Scan(&result.Total)
	if err != nil {
		return result, err
	}
	rows, err := db.Query(`SELECT id, "companyId", "actorId", "actorEmail", "actorType", action, "targetType", "targetId", "ipAddress", "createdAt"
		FROM audit_events WHERE ($1::integer IS NULL OR "companyId" = $1)
		AND ($2::integer IS NULL OR "actorId" = $2) AND ($3 = '' OR "actorEmail" ILIKE '%' || $3 || '%')
		ORDER BY id DESC LIMIT 50 OFFSET $4`, company, actor, email, (page-1)*50)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var event AuditEvent
		var company, actor sql.NullInt64
		if err := rows.Scan(&event.ID, &company, &actor, &event.ActorEmail, &event.ActorType, &event.Action, &event.TargetType, &event.TargetID, &event.IPAddress, &event.CreatedAt); err != nil {
			return result, err
		}
		if company.Valid {
			id := int(company.Int64)
			event.CompanyID = &id
		}
		if actor.Valid {
			id := int(actor.Int64)
			event.ActorID = &id
		}
		result.Data = append(result.Data, event)
	}
	return result, rows.Err()
}
