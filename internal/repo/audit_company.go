package repo

import (
	"database/sql"
	"strconv"
	"strings"
	"time"
)

// CompanyAuditEvent is the company-admin view of an audit row. Platform staff
// identities are reduced to a role label; their emails never leave the platform.
type CompanyAuditEvent struct {
	ID         int64     `json:"id"`
	ActorType  string    `json:"actorType"`
	ActorName  string    `json:"actorName"`
	ActorEmail string    `json:"actorEmail"`
	Action     string    `json:"action"`
	TargetType string    `json:"targetType"`
	TargetID   string    `json:"targetId"`
	TargetName string    `json:"targetName"`
	IPAddress  string    `json:"ipAddress"`
	CreatedAt  time.Time `json:"createdAt"`
}

type CompanyAuditPage struct {
	Data     []CompanyAuditEvent `json:"data"`
	Total    int                 `json:"total"`
	Page     int                 `json:"page"`
	PageSize int                 `json:"pageSize"`
}

type CompanyAuditFilter struct {
	Category string
	Actor    string
	From     *time.Time
	To       *time.Time
}

const companyAuditPageSize = 50

// Categories group raw action/target pairs into the filters shown to admins.
var companyAuditCategories = map[string]string{
	"login":          `e.action IN ('login','login_failed')`,
	"login_failed":   `e.action = 'login_failed'`,
	"agent":          `e."targetType" = 'agent'`,
	"instance":       `e."targetType" = 'instance'`,
	"contact":        `e."targetType" IN ('contact','contacts')`,
	"support":        `e."actorType" = 'platform_support'`,
	"administration": `e."actorType" IN ('platform','provider')`,
}

func ValidCompanyAuditCategory(category string) bool {
	_, ok := companyAuditCategories[category]
	return category == "" || ok
}

func ListCompanyAuditEvents(companyID int, filter CompanyAuditFilter, page int) (CompanyAuditPage, error) {
	if page < 1 {
		page = 1
	}
	result := CompanyAuditPage{Data: []CompanyAuditEvent{}, Page: page, PageSize: companyAuditPageSize}
	category := "true"
	if condition, ok := companyAuditCategories[filter.Category]; ok {
		category = condition
	}
	var from, to any
	if filter.From != nil {
		from = *filter.From
	}
	if filter.To != nil {
		to = *filter.To
	}
	actor := strings.TrimSpace(filter.Actor)
	// Platform emails are not searchable by company admins, matching the masked output.
	where := `e."companyId" = $1 AND ` + category + `
		AND ($2::timestamptz IS NULL OR e."createdAt" >= $2) AND ($3::timestamptz IS NULL OR e."createdAt" < $3)
		AND ($4 = '' OR (e."actorType" NOT IN ('platform','platform_support','provider') AND (e."actorEmail" ILIKE '%' || $4 || '%'
			OR EXISTS (SELECT 1 FROM agents named WHERE named.id = e."actorId" AND named."companyId" = e."companyId" AND named.name ILIKE '%' || $4 || '%'))))`
	if err := db.QueryRow(`SELECT count(*) FROM audit_events e WHERE `+where, companyID, from, to, actor).Scan(&result.Total); err != nil {
		return result, err
	}
	rows, err := db.Query(`WITH page AS (
			SELECT e.*, CASE WHEN e."targetId" ~ '^[0-9]{1,9}$' THEN e."targetId"::integer END AS "targetNumber"
			FROM audit_events e WHERE `+where+`
			ORDER BY e.id DESC LIMIT $5 OFFSET $6)
		SELECT page.id, page."actorType", COALESCE(actor.name, ''), page."actorEmail", page.action, page."targetType", page."targetId",
			COALESCE(NULLIF(CASE page."targetType" WHEN 'agent' THEN target_agent.name WHEN 'contact' THEN target_contact.name
				WHEN 'instance' THEN target_connection.name WHEN 'contacts' THEN target_connection.name END, ''), page."targetName"),
			page."ipAddress", page."createdAt"
		FROM page
		LEFT JOIN agents actor ON page."actorType" = 'agent' AND actor.id = page."actorId" AND actor."companyId" = page."companyId"
		LEFT JOIN agents target_agent ON page."targetType" = 'agent' AND target_agent.id = page."targetNumber" AND target_agent."companyId" = page."companyId"
		LEFT JOIN contacts target_contact ON page."targetType" = 'contact' AND target_contact.id = page."targetNumber" AND target_contact."companyId" = page."companyId"
		LEFT JOIN connections target_connection ON page."targetType" IN ('instance','contacts') AND target_connection.id = page."targetNumber" AND target_connection."companyId" = page."companyId"
		ORDER BY page.id DESC`, companyID, from, to, actor, companyAuditPageSize, (page-1)*companyAuditPageSize)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var event CompanyAuditEvent
		if err := rows.Scan(&event.ID, &event.ActorType, &event.ActorName, &event.ActorEmail, &event.Action, &event.TargetType,
			&event.TargetID, &event.TargetName, &event.IPAddress, &event.CreatedAt); err != nil {
			return result, err
		}
		switch event.ActorType {
		case "platform_support":
			event.ActorName, event.ActorEmail = "Suporte da plataforma", ""
		case "platform":
			event.ActorName, event.ActorEmail = "Administração da plataforma", ""
		case "provider":
			event.ActorName, event.ActorEmail = "Sistema de pagamentos", ""
		}
		result.Data = append(result.Data, event)
	}
	return result, rows.Err()
}

// LoginFailureSummary describes wrong-password attempts against one account
// since its previous successful login.
type LoginFailureSummary struct {
	Count  int       `json:"count"`
	LastAt time.Time `json:"lastAt"`
	LastIP string    `json:"lastIp"`
}

// LoginFailuresSinceLastLogin must run before the current successful login is
// recorded, otherwise it would compare against that new row. Typos made from the
// current IP just before this login are the user's own and are left out.
func LoginFailuresSinceLastLogin(agentID int, currentIP string) (LoginFailureSummary, error) {
	var summary LoginFailureSummary
	var lastAt sql.NullTime
	var lastIP sql.NullString
	err := db.QueryRow(`WITH previous AS (
			SELECT COALESCE(max(id), 0) AS id FROM audit_events
			WHERE "actorType" = 'agent' AND "actorId" = $1 AND action = 'login')
		SELECT count(*), max(failure."createdAt"), (array_agg(failure."ipAddress" ORDER BY failure.id DESC))[1]
		FROM audit_events failure, previous
		WHERE failure."actorType" = 'agent' AND failure."actorId" = $1 AND failure.action = 'login_failed' AND failure.id > previous.id
			AND NOT (failure."ipAddress" = $2 AND failure."createdAt" > now() - interval '15 minutes')`,
		agentID, currentIP).Scan(&summary.Count, &lastAt, &lastIP)
	if err != nil {
		return summary, err
	}
	summary.LastAt, summary.LastIP = lastAt.Time, lastIP.String
	return summary, nil
}

// AuditTargetName resolves a label inside the company before a mutation can
// remove the row it names. Unknown types and missing rows yield "".
func AuditTargetName(companyID int, targetType, targetID string) string {
	table := map[string]string{"agent": "agents", "contact": "contacts", "instance": "connections", "contacts": "connections"}[targetType]
	id, err := strconv.Atoi(targetID)
	if table == "" || err != nil || id <= 0 {
		return ""
	}
	var name string
	if err := db.QueryRow(`SELECT name FROM `+table+` WHERE id = $1 AND "companyId" = $2`, id, companyID).Scan(&name); err != nil {
		return ""
	}
	return name
}
