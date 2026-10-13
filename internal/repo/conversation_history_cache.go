package repo

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"
	"sophus/internal/objectcache"
)

const conversationHistoryTTL = 6 * time.Hour
const conversationHistoryKey = "snapshot-v4"

type conversationHistorySnapshot struct {
	Messages []MessageData `json:"messages"`
}

func conversationHistoryScope(companyID, conversationID int) string {
	return fmt.Sprintf("history-%d-%d", companyID, conversationID)
}

func cachedConversationHistory(companyID, conversationID int) (conversationHistorySnapshot, error) {
	var snapshot conversationHistorySnapshot
	err := objectcache.Get(conversationHistoryScope(companyID, conversationID), conversationHistoryKey, conversationHistoryTTL, &snapshot, func() error {
		var err error
		snapshot.Messages, err = GetMessagesByConversation(conversationID, companyID)
		return err
	})
	return snapshot, err
}

func cachedConversationHistoryPage(conversationID, companyID, beforeID int) (ConversationMessagePage, error) {
	snapshot, err := cachedConversationHistory(companyID, conversationID)
	if err != nil {
		return ConversationMessagePage{}, err
	}
	page, err := historyPage(snapshot.Messages, beforeID)
	if errors.Is(err, sql.ErrNoRows) {
		// A just-arrived cursor may precede the incremental updater. Refresh only
		// on this miss, rather than consulting PostgreSQL for every older page.
		objectcache.Delete(conversationHistoryScope(companyID, conversationID), conversationHistoryKey)
		snapshot, err = cachedConversationHistory(companyID, conversationID)
		if err != nil {
			return ConversationMessagePage{}, err
		}
		return historyPage(snapshot.Messages, beforeID)
	}
	return page, err
}

func historyPage(messages []MessageData, beforeID int) (ConversationMessagePage, error) {
	anchor := -1
	for index := range messages {
		if messages[index].Id == beforeID {
			anchor = index
			break
		}
	}
	if anchor < 0 {
		return ConversationMessagePage{}, sql.ErrNoRows
	}
	start := anchor - 50
	if start < 0 {
		start = 0
	}
	upper := messages[anchor].CreatedAt
	page := ConversationMessagePage{Messages: append([]MessageData(nil), messages[start:anchor]...), HasOlder: start > 0, WindowBefore: &upper}
	if len(page.Messages) > 0 {
		page.BeforeID = page.Messages[0].Id
		if page.HasOlder {
			lower := page.Messages[0].CreatedAt
			page.WindowAfter = &lower
		}
	}
	return page, nil
}

func mergeHistoryMessages(messages, updates []MessageData) []MessageData {
	byID := make(map[int]MessageData, len(messages)+len(updates))
	for _, message := range messages {
		byID[message.Id] = message
	}
	for _, message := range updates {
		byID[message.Id] = message
	}
	merged := make([]MessageData, 0, len(byID))
	for _, message := range byID {
		merged = append(merged, message)
	}
	sort.Slice(merged, func(i, j int) bool {
		if merged[i].CreatedAt.Equal(merged[j].CreatedAt) {
			return merged[i].Id < merged[j].Id
		}
		return merged[i].CreatedAt.Before(merged[j].CreatedAt)
	})
	return merged
}

type historyUpdateJob struct {
	companyID int
	pending   map[string]bool
}

var historyUpdateQueue = struct {
	sync.Mutex
	jobs map[int]*historyUpdateJob
}{jobs: make(map[int]*historyUpdateJob)}
var historyUpdateSlots = make(chan struct{}, 2)

// Inserts and receipt/reaction changes share a bounded incremental updater.
// Keeping the recent 50-message buffer in the snapshot prevents omissions when
// many new messages move the window forward at once, including out-of-order arrivals.
func scheduleConversationHistoryUpdate(conversationID int, messageID string, companyIDs ...int) {
	if !objectcache.Enabled() {
		return
	}
	historyUpdateQueue.Lock()
	job, existing := historyUpdateQueue.jobs[conversationID]
	if !existing {
		job = &historyUpdateJob{pending: make(map[string]bool)}
		historyUpdateQueue.jobs[conversationID] = job
	}
	if len(companyIDs) > 0 {
		job.companyID = companyIDs[0]
	}
	if messageID != "" {
		job.pending[messageID] = true
	}
	historyUpdateQueue.Unlock()
	if existing {
		return
	}
	go func() {
		historyUpdateSlots <- struct{}{}
		defer func() { <-historyUpdateSlots }()
		for {
			historyUpdateQueue.Lock()
			ids := make([]string, 0, len(job.pending))
			for id := range job.pending {
				ids = append(ids, id)
			}
			job.pending = make(map[string]bool)
			companyID := job.companyID
			historyUpdateQueue.Unlock()
			if companyID == 0 {
				if err := db.QueryRow(`SELECT ct."companyId" FROM conversations cv JOIN contacts ct ON ct.id=cv."contactId" WHERE cv.id=$1`, conversationID).Scan(&companyID); err != nil {
					companyID = 0
				}
			}
			if companyID > 0 {
				if err := updateConversationHistory(companyID, conversationID, ids); err != nil && !errors.Is(err, objectcache.ErrUnavailable) {
					log.Printf("conversation history cache update failed: conversation=%d error=%v", conversationID, err)
				}
			}
			historyUpdateQueue.Lock()
			if len(job.pending) == 0 {
				delete(historyUpdateQueue.jobs, conversationID)
				historyUpdateQueue.Unlock()
				return
			}
			historyUpdateQueue.Unlock()
		}
	}()
}

func updateConversationHistory(companyID, conversationID int, ids []string) error {
	// Probe the cache before any hydration work. If Redis is unavailable, an
	// incoming message must not trigger a full database history read.
	exists, err := objectcache.Exists(conversationHistoryScope(companyID, conversationID), conversationHistoryKey)
	if err != nil {
		return err
	}
	if !exists {
		var hasOlder bool
		if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM messages WHERE "conversationId"=$1 OFFSET 50 LIMIT 1)`, conversationID).Scan(&hasOlder); err != nil {
			return err
		}
		if !hasOlder {
			return nil
		}
		_, err := cachedConversationHistory(companyID, conversationID)
		return err
	}
	updates := make([]MessageData, 0, len(ids))
	for _, id := range ids {
		message, err := GetMessageForConversation(id, conversationID, companyID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			invalidateConversationHistory(companyID, conversationID)
			return err
		}
		updates = append(updates, message)
	}
	updated, err := objectcache.UpdateJSON(conversationHistoryScope(companyID, conversationID), conversationHistoryKey, conversationHistoryTTL, func(data []byte) ([]byte, error) {
		var snapshot conversationHistorySnapshot
		if err := json.Unmarshal(data, &snapshot); err != nil {
			return nil, err
		}
		snapshot.Messages = mergeHistoryMessages(snapshot.Messages, updates)
		return json.Marshal(snapshot)
	})
	if err != nil {
		return err
	}
	if !updated {
		_, err = cachedConversationHistory(companyID, conversationID)
	}
	return err
}

func invalidateConversationHistory(companyID, conversationID int) {
	objectcache.Delete(conversationHistoryScope(companyID, conversationID), conversationHistoryKey)
}

func captureContactHistoryInvalidation(contactID, companyID int) func() {
	if !objectcache.Enabled() {
		return func() {}
	}
	rows, err := db.Query(`SELECT cv.id FROM conversations cv JOIN contacts owner ON owner.id=cv."contactId"
		JOIN contacts changed ON changed.id=$1 AND changed."companyId"=$2
		WHERE owner."companyId"=$2 AND (cv."contactId"=$1 OR EXISTS(
			SELECT 1 FROM messages m WHERE m."conversationId"=cv.id AND
			((changed.jid<>'' AND m.sender=changed.jid) OR (changed.lid<>'' AND m.sender=changed.lid)
			OR (changed.number<>'' AND split_part(m.sender,'@',1)=changed.number))))`, contactID, companyID)
	if err != nil {
		log.Printf("history invalidation capture failed: contact=%d error=%v", contactID, err)
		return func() {}
	}
	defer rows.Close()
	ids := []int{}
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	return func() {
		for _, id := range ids {
			invalidateConversationHistory(companyID, id)
			invalidateConversationEventHistory(companyID, id)
		}
	}
}

type ConversationSearchMatch struct {
	ConversationID int       `json:"conversationId"`
	ID             int       `json:"id"`
	MessageID      string    `json:"messageId"`
	Summary        string    `json:"summary"`
	CreatedAt      time.Time `json:"createdAt"`
}

type ConversationSearchResults struct {
	Matches []ConversationSearchMatch `json:"matches"`
	Total   int                       `json:"total"`
}

// GetConversationMessageContext returns a bounded preview without loading older
// pages into the browser. The recent window covers incremental-cache lag.
func GetConversationMessageContext(conversationID, companyID, messageID int) ([]MessageData, error) {
	recent, err := GetConversationMessagePage(conversationID, companyID, 0)
	if err != nil {
		return nil, err
	}
	if !recent.HasOlder {
		return messageContext(recent.Messages, messageID)
	}
	snapshot, err := cachedConversationHistory(companyID, conversationID)
	if err != nil {
		return nil, err
	}
	messages, err := messageContext(mergeHistoryMessages(snapshot.Messages, recent.Messages), messageID)
	if !errors.Is(err, sql.ErrNoRows) {
		return messages, err
	}
	objectcache.Delete(conversationHistoryScope(companyID, conversationID), conversationHistoryKey)
	snapshot, err = cachedConversationHistory(companyID, conversationID)
	if err != nil {
		return nil, err
	}
	return messageContext(mergeHistoryMessages(snapshot.Messages, recent.Messages), messageID)
}

func messageContext(messages []MessageData, messageID int) ([]MessageData, error) {
	anchor := -1
	for index, message := range messages {
		if message.Id == messageID && !message.IsDeleted {
			anchor = index
			break
		}
	}
	if anchor < 0 {
		return nil, sql.ErrNoRows
	}
	start, end := anchor, anchor+1
	for count := 0; start > 0 && count < 5; {
		start--
		if !messages[start].IsDeleted {
			count++
		}
	}
	for count := 0; end < len(messages) && count < 5; end++ {
		if !messages[end].IsDeleted {
			count++
		}
	}
	context := make([]MessageData, 0, 11)
	for _, message := range messages[start:end] {
		if !message.IsDeleted {
			context = append(context, message)
		}
	}
	return context, nil
}

func normalizeConversationSearch(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Mn, r) {
			return -1
		}
		return unicode.ToLower(r)
	}, norm.NFD.String(text))
}

func SearchConversationMessages(companyID, conversationID int, query string, offset int) (ConversationSearchResults, error) {
	// The latest window remains a fresh database read; older content comes from
	// Redis, and recently received messages are searchable before the updater runs.
	recent, err := GetConversationMessagePage(conversationID, companyID, 0)
	if err != nil {
		return ConversationSearchResults{}, err
	}
	if !recent.HasOlder {
		return searchHistoryMessages(recent.Messages, query, offset), nil
	}
	snapshot, err := cachedConversationHistory(companyID, conversationID)
	if err != nil {
		return ConversationSearchResults{}, err
	}
	return searchHistoryMessages(mergeHistoryMessages(snapshot.Messages, recent.Messages), query, offset), nil
}

func searchHistoryMessages(messages []MessageData, query string, offset int) ConversationSearchResults {
	query = normalizeConversationSearch(strings.TrimSpace(query))
	result := ConversationSearchResults{Matches: []ConversationSearchMatch{}}
	if query == "" {
		return result
	}
	for _, message := range messages {
		if message.IsDeleted {
			continue
		}
		if !strings.Contains(normalizeConversationSearch(message.Text+" "+message.QuotedText+" "+message.MediaType), query) {
			continue
		}
		position := result.Total
		result.Total++
		if position < offset || len(result.Matches) >= 100 {
			continue
		}
		summary := []rune(message.Text)
		if len(summary) > 160 {
			summary = summary[:160]
		}
		result.Matches = append(result.Matches, ConversationSearchMatch{ID: message.Id, ConversationID: message.ConversationId, MessageID: message.MessageId, Summary: string(summary), CreatedAt: message.CreatedAt})
	}
	return result
}
