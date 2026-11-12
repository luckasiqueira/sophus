package repo

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// LockMessageIngestion serializes the ingestion of one WhatsApp message
// across goroutines and app instances. Two deliveries of the same message
// (provider retries, parallel queue consumers, or the synchronous fallback
// used when Redis is down) could otherwise both pass the "already stored?"
// check before either inserts, persisting the message twice.
//
// The lock is a session-level Postgres advisory lock held on a dedicated
// connection; release must be called once the message row is stored. An empty
// message ID cannot be deduplicated, so it returns a no-op release.
func LockMessageIngestion(ctx context.Context, connectionID int, messageID string) (func(), error) {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return func() {}, nil
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	key := fmt.Sprintf("message-ingestion:%d:%s", connectionID, messageID)
	// The single-bigint advisory key space never overlaps the two-int locks
	// used for companies and conversations.
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtextextended($1, 0))`, key); err != nil {
		_ = conn.Close()
		return nil, err
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, key)
			_ = conn.Close()
		})
	}, nil
}
