package repo

import (
	"database/sql"
	"time"
)

type CompanyConnectionSummary struct {
	Id        int       `json:"id"`
	Name      string    `json:"name"`
	Number    string    `json:"number"`
	Status    string    `json:"status"`
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"createdAt"`
}

type CompanyFlowSummary struct {
	Id                  int        `json:"id"`
	Name                string     `json:"name"`
	IsActive            bool       `json:"isActive"`
	ConnectionId        *int       `json:"connectionId"`
	LastExecutionStatus *string    `json:"lastExecutionStatus"`
	LastExecutionAt     *time.Time `json:"lastExecutionAt"`
}

type CompanyUserSummary struct {
	Id        int       `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	IsActive  bool      `json:"isActive"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type CompanyMessageMetrics struct {
	TotalSent      int64 `json:"totalSent"`
	TotalReceived  int64 `json:"totalReceived"`
	PeriodSent     int64 `json:"periodSent"`
	PeriodReceived int64 `json:"periodReceived"`
}

type CompanyOverviewPage struct {
	Page     int `json:"page"`
	PageSize int `json:"pageSize"`
	Total    int `json:"total"`
}

type CompanyOverviewPagination struct {
	Connections CompanyOverviewPage `json:"connections"`
	Flows       CompanyOverviewPage `json:"flows"`
	Users       CompanyOverviewPage `json:"users"`
}

type CompanyOperationalOverview struct {
	Company              Company                    `json:"company"`
	Messages             CompanyMessageMetrics      `json:"messages"`
	Connections          []CompanyConnectionSummary `json:"connections"`
	Flows                []CompanyFlowSummary       `json:"flows"`
	Users                []CompanyUserSummary       `json:"users"`
	ConnectedConnections int                        `json:"connectedConnections"`
	ActiveUsers          int                        `json:"activeUsers"`
	Pagination           CompanyOverviewPagination  `json:"pagination"`
}

func GetCompanyOperationalOverview(companyID int, from, to time.Time, includeMessages, includeTotals bool, connectionPage, flowPage, userPage int) (CompanyOperationalOverview, error) {
	const pageSize = 50
	company, err := GetCompanyById(companyID)
	if err != nil {
		return CompanyOperationalOverview{}, err
	}
	overview := CompanyOperationalOverview{
		Company: company, Connections: []CompanyConnectionSummary{}, Flows: []CompanyFlowSummary{}, Users: []CompanyUserSummary{},
		Pagination: CompanyOverviewPagination{
			Connections: CompanyOverviewPage{Page: connectionPage, PageSize: pageSize},
			Flows:       CompanyOverviewPage{Page: flowPage, PageSize: pageSize},
			Users:       CompanyOverviewPage{Page: userPage, PageSize: pageSize},
		},
	}
	if err := db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM connections WHERE "companyId" = $1),
		(SELECT COUNT(*) FROM connections WHERE "companyId" = $1 AND status = 'connected'),
		(SELECT COUNT(*) FROM chatbot_flows WHERE "companyId" = $1),
		(SELECT COUNT(*) FROM agents WHERE "companyId" = $1),
		(SELECT COUNT(*) FROM agents WHERE "companyId" = $1 AND "isActive" = true)`, companyID).Scan(
		&overview.Pagination.Connections.Total, &overview.ConnectedConnections,
		&overview.Pagination.Flows.Total, &overview.Pagination.Users.Total, &overview.ActiveUsers); err != nil {
		return CompanyOperationalOverview{}, err
	}
	connectionPage = clampOverviewPage(connectionPage, overview.Pagination.Connections.Total, pageSize)
	flowPage = clampOverviewPage(flowPage, overview.Pagination.Flows.Total, pageSize)
	userPage = clampOverviewPage(userPage, overview.Pagination.Users.Total, pageSize)
	overview.Pagination.Connections.Page = connectionPage
	overview.Pagination.Flows.Page = flowPage
	overview.Pagination.Users.Page = userPage
	if includeMessages && includeTotals {
		if err := db.QueryRow(`SELECT
		COUNT(DISTINCT (co.id, m."messageId")) FILTER (WHERE m."isFromMe" = true AND m."isDeleted" = false),
		COUNT(DISTINCT (co.id, m."messageId")) FILTER (WHERE m."isFromMe" = false AND m."isDeleted" = false)
		FROM messages m
		JOIN conversations cv ON cv.id = m."conversationId"
		JOIN connections co ON co.id = cv."connectionId" AND co."companyId" = $1`, companyID).Scan(
			&overview.Messages.TotalSent, &overview.Messages.TotalReceived); err != nil {
			return CompanyOperationalOverview{}, err
		}
	}
	if includeMessages {
		if err := db.QueryRow(`SELECT
		COUNT(DISTINCT (co.id, m."messageId")) FILTER (WHERE m."isFromMe" = true AND m."isDeleted" = false),
		COUNT(DISTINCT (co.id, m."messageId")) FILTER (WHERE m."isFromMe" = false AND m."isDeleted" = false)
		FROM connections co
		JOIN conversations cv ON cv."connectionId" = co.id
		JOIN messages m ON m."conversationId" = cv.id AND m."createdAt" >= $2 AND m."createdAt" < $3
		WHERE co."companyId" = $1`, companyID, from, to).Scan(
			&overview.Messages.PeriodSent, &overview.Messages.PeriodReceived); err != nil {
			return CompanyOperationalOverview{}, err
		}
	}
	connectionRows, err := db.Query(`SELECT id, name, number, status, type, "createdAt"
		FROM connections WHERE "companyId" = $1 ORDER BY "createdAt" DESC, id DESC
		LIMIT $2 OFFSET $3`, companyID, pageSize, (connectionPage-1)*pageSize)
	if err != nil {
		return CompanyOperationalOverview{}, err
	}
	for connectionRows.Next() {
		var connection CompanyConnectionSummary
		if err := connectionRows.Scan(&connection.Id, &connection.Name, &connection.Number, &connection.Status,
			&connection.Type, &connection.CreatedAt); err != nil {
			connectionRows.Close()
			return CompanyOperationalOverview{}, err
		}
		overview.Connections = append(overview.Connections, connection)
	}
	if err := connectionRows.Close(); err != nil {
		return CompanyOperationalOverview{}, err
	}
	if err := connectionRows.Err(); err != nil {
		return CompanyOperationalOverview{}, err
	}
	flowRows, err := db.Query(`SELECT f.id, f.name, f."isActive", f."connectionId",
		latest.status, latest."createdAt"
		FROM chatbot_flows f
		LEFT JOIN LATERAL (
			SELECT e.status, e."createdAt" FROM flow_executions e
			WHERE e."flowId" = f.id AND e."companyId" = $1
			ORDER BY e."createdAt" DESC, e.id DESC LIMIT 1
		) latest ON true
		WHERE f."companyId" = $1 ORDER BY f."createdAt" DESC, f.id DESC
		LIMIT $2 OFFSET $3`, companyID, pageSize, (flowPage-1)*pageSize)
	if err != nil {
		return CompanyOperationalOverview{}, err
	}
	for flowRows.Next() {
		var flow CompanyFlowSummary
		var connectionID sql.NullInt64
		var lastStatus sql.NullString
		var lastExecutionAt sql.NullTime
		if err := flowRows.Scan(&flow.Id, &flow.Name, &flow.IsActive, &connectionID, &lastStatus, &lastExecutionAt); err != nil {
			flowRows.Close()
			return CompanyOperationalOverview{}, err
		}
		flow.ConnectionId = nullIntPointer(connectionID)
		flow.LastExecutionStatus = nullStringPointer(lastStatus)
		flow.LastExecutionAt = nullTimePointer(lastExecutionAt)
		overview.Flows = append(overview.Flows, flow)
	}
	if err := flowRows.Close(); err != nil {
		return CompanyOperationalOverview{}, err
	}
	if err := flowRows.Err(); err != nil {
		return CompanyOperationalOverview{}, err
	}
	userRows, err := db.Query(`SELECT id, name, email, role, "isActive", "createdAt", "updatedAt"
		FROM agents WHERE "companyId" = $1 ORDER BY name, id
		LIMIT $2 OFFSET $3`, companyID, pageSize, (userPage-1)*pageSize)
	if err != nil {
		return CompanyOperationalOverview{}, err
	}
	for userRows.Next() {
		var user CompanyUserSummary
		if err := userRows.Scan(&user.Id, &user.Name, &user.Email, &user.Role, &user.IsActive,
			&user.CreatedAt, &user.UpdatedAt); err != nil {
			userRows.Close()
			return CompanyOperationalOverview{}, err
		}
		overview.Users = append(overview.Users, user)
	}
	if err := userRows.Close(); err != nil {
		return CompanyOperationalOverview{}, err
	}
	if err := userRows.Err(); err != nil {
		return CompanyOperationalOverview{}, err
	}
	return overview, nil
}

func clampOverviewPage(page, total, pageSize int) int {
	lastPage := (total + pageSize - 1) / pageSize
	if lastPage < 1 {
		lastPage = 1
	}
	if page > lastPage {
		return lastPage
	}
	return page
}
