package repo

import (
	"context"
	"net/url"
	"strconv"
	"time"
)

// ReportFilter uses zero for all and -1 for an unassigned dimension.
type ReportFilter struct {
	ConnectionID int
	DepartmentID int
	AgentID      int
	Period       string
	From         time.Time
	To           time.Time // Exclusive upper bound in the company's timezone.
	Timezone     string
}

func (f ReportFilter) FromDate() string {
	if f.From.IsZero() {
		return ""
	}
	return f.From.Format("2006-01-02")
}

func (f ReportFilter) ToDate() string {
	if f.To.IsZero() {
		return ""
	}
	return f.To.AddDate(0, 0, -1).Format("2006-01-02")
}

func (f ReportFilter) PeriodLabel() string {
	if f.From.IsZero() {
		return "Todo o histórico"
	}
	return f.From.Format("02/01/2006") + " a " + f.To.AddDate(0, 0, -1).Format("02/01/2006")
}

func (f ReportFilter) ExportURL() string {
	values := url.Values{"connection": {strconv.Itoa(f.ConnectionID)}, "department": {strconv.Itoa(f.DepartmentID)}, "agent": {strconv.Itoa(f.AgentID)}}
	// Freeze relative presets to the exact dates shown on screen.
	if f.From.IsZero() {
		values.Set("period", "all")
	} else {
		values.Set("period", "custom")
		values.Set("from", f.FromDate())
		values.Set("to", f.ToDate())
	}
	return "/reports/pdf?" + values.Encode()
}

type ReportTrend struct {
	Label          string
	Sent, Received int64
}

// reportScopeSQL is shared by the summary and timeline queries.
const reportScopeSQL = `WITH visible AS (
	SELECT cv.* FROM conversations cv
	JOIN contacts ct ON ct.id = cv."contactId" AND ct."companyId" = $1
	WHERE ($2 OR cv."agentId" = $3
		OR cv."departmentId" IN (SELECT ad."departmentId" FROM agent_departments ad WHERE ad."agentId" = $3)
		OR (cv."agentId" IS NULL AND cv."departmentId" IS NULL AND cv.status <> 'closed'))
	AND ($4 = 0 OR COALESCE(cv."connectionId", -1) = $4)
	AND ($5 = 0 OR COALESCE(cv."departmentId", -1) = $5)
	AND ($6 = 0 OR COALESCE(cv."agentId", -1) = $6)
), period_messages AS (
	SELECT m."conversationId", m."createdAt", m."isFromMe" FROM messages m JOIN visible cv ON cv.id = m."conversationId"
	WHERE m."isDeleted" = false
	AND ($7::timestamptz IS NULL OR m."createdAt" >= $7)
	AND ($8::timestamptz IS NULL OR m."createdAt" < $8)
), message_counts AS (
	SELECT "conversationId", COUNT(*) FILTER (WHERE "isFromMe" = true) AS sent,
	COUNT(*) FILTER (WHERE "isFromMe" = false) AS received
	FROM period_messages GROUP BY "conversationId"
) `

type ReportOption struct {
	ID   int
	Name string
}
type ReportMetrics struct{ Sent, Received, Unattended, Conversations int64 }
type ReportRow struct {
	Connection, Department, Agent string
	ReportMetrics
}
type ConversationReport struct {
	Filter                           ReportFilter
	Connections, Departments, Agents []ReportOption
	Totals                           ReportMetrics
	Rows                             []ReportRow
	Trend                            []ReportTrend
}

func GetConversationReport(ctx context.Context, agent Agent, filter ReportFilter) (ConversationReport, error) {
	report := ConversationReport{Filter: filter, Rows: []ReportRow{}}
	var from, to any
	if !filter.From.IsZero() {
		from = filter.From
		to = filter.To
	}
	args := []any{agent.CompanyId, agent.IsAdmin(), agent.Id, filter.ConnectionID, filter.DepartmentID, filter.AgentID, from, to}
	// Aggregate messages first to count each conversation once, including those
	// without messages and histories retained after deleting a connection.
	rows, err := db.QueryContext(ctx, reportScopeSQL+`
	SELECT COALESCE(co.name, 'Conexão removida'), COALESCE(d.name, 'Sem fila'), COALESCE(a.name, 'Sem agente'),
		COALESCE(SUM(mc.sent), 0)::bigint, COALESCE(SUM(mc.received), 0)::bigint,
		COUNT(*) FILTER (WHERE (cv."agentId" IS NULL OR cv.status = 'running') AND cv.status <> 'closed'), COUNT(*)
	FROM visible cv
	LEFT JOIN connections co ON co.id = cv."connectionId" AND co."companyId" = $1
	LEFT JOIN departments d ON d.id = cv."departmentId" AND d."companyId" = $1
	LEFT JOIN agents a ON a.id = cv."agentId" AND a."companyId" = $1
	LEFT JOIN message_counts mc ON mc."conversationId" = cv.id
	WHERE ($7::timestamptz IS NULL OR mc."conversationId" IS NOT NULL
		OR (cv."createdAt" >= $7 AND cv."createdAt" < $8))
	GROUP BY cv."connectionId", co.name, cv."departmentId", d.name, cv."agentId", a.name
	ORDER BY COUNT(*) DESC, co.name, d.name, a.name`,
		args...)
	if err != nil {
		return report, err
	}
	for rows.Next() {
		var row ReportRow
		if err := rows.Scan(&row.Connection, &row.Department, &row.Agent, &row.Sent, &row.Received, &row.Unattended, &row.Conversations); err != nil {
			rows.Close()
			return report, err
		}
		report.Rows = append(report.Rows, row)
		report.Totals.Sent += row.Sent
		report.Totals.Received += row.Received
		report.Totals.Unattended += row.Unattended
		report.Totals.Conversations += row.Conversations
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return report, err
	}
	bucket, format := "month", "MM/YYYY"
	if !filter.From.IsZero() && filter.To.Sub(filter.From) <= 90*24*time.Hour {
		bucket, format = "day", "DD/MM"
	}
	zone := filter.Timezone
	if zone == "" {
		zone = "UTC"
	}
	trendArgs := append(append([]any{}, args...), bucket, zone, format)
	timeline, err := db.QueryContext(ctx, reportScopeSQL+`SELECT
		to_char(date_trunc($9, "createdAt" AT TIME ZONE $10), $11),
		COUNT(*) FILTER (WHERE "isFromMe" = true), COUNT(*) FILTER (WHERE "isFromMe" = false)
		FROM period_messages GROUP BY date_trunc($9, "createdAt" AT TIME ZONE $10)
		ORDER BY date_trunc($9, "createdAt" AT TIME ZONE $10)`, trendArgs...)
	if err != nil {
		return report, err
	}
	for timeline.Next() {
		var point ReportTrend
		if err := timeline.Scan(&point.Label, &point.Sent, &point.Received); err != nil {
			timeline.Close()
			return report, err
		}
		report.Trend = append(report.Trend, point)
	}
	err = timeline.Err()
	timeline.Close()
	if err != nil {
		return report, err
	}
	// Inactive agents and queues remain available for historical filtering.
	options, err := db.QueryContext(ctx, `SELECT 'connection', id, name FROM connections WHERE "companyId" = $1
		UNION ALL SELECT 'department', id, name FROM departments WHERE "companyId" = $1
		UNION ALL SELECT 'agent', id, name FROM agents WHERE "companyId" = $1
		ORDER BY 1, 3, 2`, agent.CompanyId)
	if err != nil {
		return report, err
	}
	defer options.Close()
	for options.Next() {
		var kind string
		var option ReportOption
		if err := options.Scan(&kind, &option.ID, &option.Name); err != nil {
			return report, err
		}
		switch kind {
		case "connection":
			report.Connections = append(report.Connections, option)
		case "department":
			report.Departments = append(report.Departments, option)
		case "agent":
			report.Agents = append(report.Agents, option)
		}
	}
	return report, options.Err()
}
