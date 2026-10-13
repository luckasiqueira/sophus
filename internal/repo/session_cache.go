package repo

import (
	"strconv"
	"time"

	"sophus/internal/objectcache"
)

type SessionAgent struct {
	Agent          Agent `json:"agent"`
	CompanyActive  bool  `json:"companyActive"`
	SessionVersion int   `json:"sessionVersion"`
}

// Session reads never cache password hashes. Password-changing endpoints fetch
// their credentials freshly using GetAgentById.
func GetSessionAgent(id int) (SessionAgent, error) {
	var session SessionAgent
	err := objectcache.Get("session", "agent-v2:"+strconv.Itoa(id), 20*time.Second, &session, func() error {
		return db.QueryRow(`SELECT a.id, a.name, a.email, a.role, a."isActive", a."companyId", a."sessionVersion",
			a."createdAt", a."updatedAt", c."isActive"
			FROM agents a JOIN companies c ON c.id = a."companyId" WHERE a.id = $1`, id).Scan(
			&session.Agent.Id, &session.Agent.Name, &session.Agent.Email, &session.Agent.Role, &session.Agent.IsActive,
			&session.Agent.CompanyId, &session.SessionVersion, &session.Agent.CreatedAt, &session.Agent.UpdatedAt, &session.CompanyActive)
	})
	// Agent hides sessionVersion from public JSON responses, so the private
	// cache DTO preserves it explicitly before restoring the authenticated model.
	session.Agent.SessionVersion = session.SessionVersion
	return session, err
}
