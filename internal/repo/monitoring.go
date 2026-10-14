package repo

import (
	"database/sql"
	"log"

	"github.com/lib/pq"
)

type DatabaseStats struct {
	sql.DBStats
	ServerConnections int   `json:"serverConnections"`
	DatabaseBytes     int64 `json:"databaseBytes"`
}

func GetDatabaseStats(includeServer bool) DatabaseStats {
	stats := DatabaseStats{DBStats: db.Stats()}
	if includeServer {
		if err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM pg_stat_activity WHERE datname = current_database()),
			pg_database_size(current_database())`).Scan(&stats.ServerConnections, &stats.DatabaseBytes); err != nil {
			log.Printf("database stats failed: %v", err)
		}
	}
	return stats
}

// Activity counts separate real records from stress-test simulations, so the
// monitoring page never mistakes a load test for real usage.
const (
	countOnlineAgentsSQL = `SELECT COUNT(*) FILTER (WHERE simulated."resourceId" IS NULL), COUNT(*) FILTER (WHERE simulated."resourceId" IS NOT NULL)
		FROM agents agent
		LEFT JOIN stress_test_resources simulated ON simulated."resourceType" = 'agent' AND simulated."resourceId" = agent.id
		WHERE agent.id = ANY($1::integer[])`
	countConnectedInstancesSQL = `SELECT COUNT(*) FILTER (WHERE simulated."resourceId" IS NULL), COUNT(*) FILTER (WHERE simulated."resourceId" IS NOT NULL)
		FROM connections connection
		LEFT JOIN stress_test_resources simulated ON simulated."resourceType" = 'connection' AND simulated."resourceId" = connection.id
		WHERE connection.status = 'connected'`
	companyActivitySQL = `WITH online AS (
			SELECT agent."companyId", COUNT(*) FILTER (WHERE simulated."resourceId" IS NULL) AS agents,
				COUNT(*) FILTER (WHERE simulated."resourceId" IS NOT NULL) AS simulated
			FROM agents agent
			LEFT JOIN stress_test_resources simulated ON simulated."resourceType" = 'agent' AND simulated."resourceId" = agent.id
			WHERE agent.id = ANY($1::integer[]) GROUP BY agent."companyId"
		), instances AS (
			SELECT connection."companyId", COUNT(*) FILTER (WHERE simulated."resourceId" IS NULL) AS connected,
				COUNT(*) FILTER (WHERE simulated."resourceId" IS NOT NULL) AS simulated
			FROM connections connection
			LEFT JOIN stress_test_resources simulated ON simulated."resourceType" = 'connection' AND simulated."resourceId" = connection.id
			WHERE connection.status = 'connected' GROUP BY connection."companyId"
		)
		SELECT company.id, company.name, COALESCE(online.agents, 0), COALESCE(online.simulated, 0),
			COALESCE(instances.connected, 0), COALESCE(instances.simulated, 0)
		FROM companies company
		LEFT JOIN online ON online."companyId" = company.id
		LEFT JOIN instances ON instances."companyId" = company.id
		WHERE online."companyId" IS NOT NULL OR instances."companyId" IS NOT NULL
		ORDER BY COALESCE(online.agents, 0) + COALESCE(online.simulated, 0) DESC, COALESCE(instances.connected, 0) DESC, company.name
		LIMIT 100`
)

func CountOnlineAgents(agentIDs []int) (real, simulated int, err error) {
	if len(agentIDs) == 0 {
		return 0, 0, nil
	}
	err = db.QueryRow(countOnlineAgentsSQL, pq.Array(agentIDs)).Scan(&real, &simulated)
	return real, simulated, err
}

func CountConnectedInstances() (real, simulated int, err error) {
	err = db.QueryRow(countConnectedInstancesSQL).Scan(&real, &simulated)
	return real, simulated, err
}

type CompanyActivity struct {
	CompanyID                   int    `json:"companyId"`
	Name                        string `json:"name"`
	AgentsOnline                int    `json:"agentsOnline"`
	SimulatedAgentsOnline       int    `json:"simulatedAgentsOnline"`
	InstancesConnected          int    `json:"instancesConnected"`
	SimulatedInstancesConnected int    `json:"simulatedInstancesConnected"`
}

// ListCompanyActivity returns the companies with agents online or instances
// connected, busiest first.
func ListCompanyActivity(onlineAgentIDs []int) ([]CompanyActivity, error) {
	rows, err := db.Query(companyActivitySQL, pq.Array(onlineAgentIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	activity := []CompanyActivity{}
	for rows.Next() {
		var company CompanyActivity
		if err := rows.Scan(&company.CompanyID, &company.Name, &company.AgentsOnline, &company.SimulatedAgentsOnline,
			&company.InstancesConnected, &company.SimulatedInstancesConnected); err != nil {
			return nil, err
		}
		activity = append(activity, company)
	}
	return activity, rows.Err()
}
