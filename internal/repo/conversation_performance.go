package repo

import "context"

// ExplainConversationList is a read-only diagnostic for the exact sidebar
// query, using the same scope and permissions as the application.
func ExplainConversationList(ctx context.Context, agent Agent, tab string, groups bool) (string, error) {
	var plan string
	err := db.QueryRowContext(ctx, "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+conversationListQuery(tab),
		agent.CompanyId, agent.IsAdmin(), agent.Id, groups).Scan(&plan)
	return plan, err
}
