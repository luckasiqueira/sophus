package repo

// UpdateOwnAgentProfile never changes company, permissions, or account status.
// The session version guards against concurrent password changes.
func UpdateOwnAgentProfile(agent Agent, name, passwordHash string) (Agent, error) {
	defer invalidateSessionCache()
	return scanAgent(db.QueryRow(`UPDATE agents SET name = $1,
		password = CASE WHEN $2 <> '' THEN $2 ELSE password END,
		"sessionVersion" = "sessionVersion" + CASE WHEN $2 <> '' THEN 1 ELSE 0 END,
		"updatedAt" = now()
		WHERE id = $3 AND "companyId" = $4 AND "sessionVersion" = $5 AND "isActive" = true
		RETURNING id, name, email, password, role, "isActive", "companyId", "sessionVersion", "createdAt", "updatedAt"`,
		name, passwordHash, agent.Id, agent.CompanyId, agent.SessionVersion))
}
