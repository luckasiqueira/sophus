package repo

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	ScheduledMessageStatusPending    = "pending"
	ScheduledMessageStatusProcessing = "processing"
	ScheduledMessageStatusSent       = "sent"
	ScheduledMessageStatusFailed     = "failed"
	ScheduledMessageStatusCanceled   = "canceled"

	ScheduledRecurrenceNone    = "none"
	ScheduledRecurrenceDaily   = "daily"
	ScheduledRecurrenceWeekly  = "weekly"
	ScheduledRecurrenceMonthly = "monthly"

	ScheduledMessageBodyLimit = 4096
	scheduledMessageAttempts  = 3
)

// ErrScheduledMessageTarget means the contact or connection does not belong to
// the company, or the contact cannot receive scheduled messages (group/blocked).
var ErrScheduledMessageTarget = errors.New("scheduled message target unavailable")

// ErrScheduledMessageLocked means the schedule is no longer pending or failed
// (it is being sent, was sent or was canceled) and cannot be changed.
var ErrScheduledMessageLocked = errors.New("scheduled message cannot be changed")

var ErrScheduledMessageEmpty = errors.New("scheduled message needs text or media")

func ValidScheduledRecurrence(value string) bool {
	switch value {
	case ScheduledRecurrenceNone, ScheduledRecurrenceDaily, ScheduledRecurrenceWeekly, ScheduledRecurrenceMonthly:
		return true
	}
	return false
}

type ScheduledMessage struct {
	Id              int64      `json:"id"`
	CompanyId       int        `json:"-"`
	ContactId       int        `json:"contactId"`
	ContactName     string     `json:"contactName"`
	ContactNumber   string     `json:"contactNumber"`
	ConnectionId    int        `json:"connectionId"`
	ConnectionName  string     `json:"connectionName"`
	AgentId         *int       `json:"agentId"`
	AgentName       string     `json:"agentName"`
	Body            string     `json:"body"`
	MediaKind       string     `json:"mediaKind"`
	MediaPath       string     `json:"mediaPath"`
	MediaName       string     `json:"mediaName"`
	MediaSize       int64      `json:"mediaSize"`
	ScheduledFor    time.Time  `json:"scheduledFor"`
	RetryAt         *time.Time `json:"retryAt"`
	Recurrence      string     `json:"recurrence"`
	RecurrenceUntil string     `json:"recurrenceUntil"`
	SeriesId        *int64     `json:"seriesId"`
	// SeriesStart is the first occurrence's time; recurring dates are counted
	// from it so a short month never shifts the following ones.
	SeriesStart     time.Time  `json:"-"`
	Status          string     `json:"status"`
	Attempts        int        `json:"attempts"`
	ClaimVersion    int        `json:"-"`
	SentAt          *time.Time `json:"sentAt"`
	LastError       string     `json:"lastError"`
	ConversationURL *uuid.UUID `json:"conversationURL"`
	CreatedAt       time.Time  `json:"createdAt"`
}

type ScheduledMessageInput struct {
	ContactId    int
	ConnectionId int
	Body         string
	ScheduledFor time.Time
	// Recurrence is one of the ScheduledRecurrence values ("" means none);
	// RecurrenceUntil is the last day (company calendar) it may repeat on.
	Recurrence      string
	RecurrenceUntil *time.Time
	// Media replaces the attachment when set; RemoveMedia drops it.
	Media       *ScheduledMessageMedia
	RemoveMedia bool
}

type ScheduledMessageMedia struct {
	Kind string
	Path string
	Name string
	Size int64
}

type ScheduledMessageFilter struct {
	CompanyId int
	// AgentId limits the list to one agent's schedules (non-admin agents).
	AgentId  *int
	Statuses []string
	Page     int
	PageSize int
}

type ScheduledMessagePage struct {
	Data     []ScheduledMessage `json:"data"`
	Total    int                `json:"total"`
	Page     int                `json:"page"`
	PageSize int                `json:"pageSize"`
}

const scheduledMessageColumns = `sm.id, sm."companyId", sm."contactId", ct.name, ct.number, sm."connectionId", co.name,
	sm."agentId", COALESCE(ag.name, ''), sm.body, COALESCE(sm."mediaKind", ''), COALESCE(sm."mediaPath", ''),
	COALESCE(sm."mediaName", ''), COALESCE(sm."mediaSize", 0), sm."scheduledFor", sm."retryAt", sm.recurrence,
	COALESCE(to_char(sm."recurrenceUntil", 'YYYY-MM-DD'), ''), sm."seriesId", COALESCE(root."scheduledFor", sm."scheduledFor"),
	sm.status, sm.attempts, sm."claimVersion", sm."sentAt", COALESCE(sm."lastError", ''), cv.url, sm."createdAt"`

const scheduledMessageJoins = `FROM scheduled_messages sm
	JOIN contacts ct ON ct.id = sm."contactId"
	JOIN connections co ON co.id = sm."connectionId"
	LEFT JOIN agents ag ON ag.id = sm."agentId"
	LEFT JOIN conversations cv ON cv.id = sm."conversationId"
	LEFT JOIN scheduled_messages root ON root.id = sm."seriesId"`

func scanScheduledMessage(scanner rowScanner) (ScheduledMessage, error) {
	var message ScheduledMessage
	var agentID, seriesID sql.NullInt64
	var sentAt, retryAt sql.NullTime
	var conversationURL uuid.NullUUID
	err := scanner.Scan(&message.Id, &message.CompanyId, &message.ContactId, &message.ContactName, &message.ContactNumber,
		&message.ConnectionId, &message.ConnectionName, &agentID, &message.AgentName, &message.Body, &message.MediaKind,
		&message.MediaPath, &message.MediaName, &message.MediaSize, &message.ScheduledFor, &retryAt, &message.Recurrence,
		&message.RecurrenceUntil, &seriesID, &message.SeriesStart, &message.Status, &message.Attempts,
		&message.ClaimVersion, &sentAt, &message.LastError, &conversationURL, &message.CreatedAt)
	if err != nil {
		return ScheduledMessage{}, err
	}
	if agentID.Valid {
		value := int(agentID.Int64)
		message.AgentId = &value
	}
	if seriesID.Valid {
		value := seriesID.Int64
		message.SeriesId = &value
	}
	if sentAt.Valid {
		value := sentAt.Time
		message.SentAt = &value
	}
	if retryAt.Valid {
		value := retryAt.Time
		message.RetryAt = &value
	}
	if conversationURL.Valid {
		value := conversationURL.UUID
		message.ConversationURL = &value
	}
	return message, nil
}

// scheduledTargetSQL validates that contact and connection belong to the
// company and that the contact can receive a one-to-one message.
const scheduledTargetSQL = `SELECT 1 FROM contacts ct JOIN connections co ON co.id = $3 AND co."companyId" = ct."companyId"
	WHERE ct.id = $2 AND ct."companyId" = $1 AND COALESCE(ct."isGroup", false) = false AND COALESCE(ct."isBlocked", false) = false`

func normalizedRecurrence(input ScheduledMessageInput) (string, any) {
	recurrence := input.Recurrence
	if recurrence == "" {
		recurrence = ScheduledRecurrenceNone
	}
	if recurrence == ScheduledRecurrenceNone || input.RecurrenceUntil == nil {
		return recurrence, nil
	}
	return recurrence, input.RecurrenceUntil.Format("2006-01-02")
}

func checkScheduledTarget(tx *sql.Tx, companyID int, input ScheduledMessageInput) error {
	var ok int
	err := tx.QueryRow(scheduledTargetSQL, companyID, input.ContactId, input.ConnectionId).Scan(&ok)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrScheduledMessageTarget
	}
	return err
}

func CreateScheduledMessage(companyID, agentID int, input ScheduledMessageInput) (ScheduledMessage, error) {
	tx, err := db.Begin()
	if err != nil {
		return ScheduledMessage{}, err
	}
	defer tx.Rollback()
	if err := checkScheduledTarget(tx, companyID, input); err != nil {
		return ScheduledMessage{}, err
	}
	media := input.Media
	if media == nil {
		media = &ScheduledMessageMedia{}
	}
	recurrence, until := normalizedRecurrence(input)
	var id int64
	if err := tx.QueryRow(`INSERT INTO scheduled_messages ("companyId", "contactId", "connectionId", "agentId", body,
		"mediaKind", "mediaPath", "mediaName", "mediaSize", "scheduledFor", recurrence, "recurrenceUntil")
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), NULLIF($7, ''), NULLIF($8, ''), NULLIF($9, 0), $10, $11, $12::date) RETURNING id`,
		companyID, input.ContactId, input.ConnectionId, agentID, strings.TrimSpace(input.Body),
		media.Kind, media.Path, media.Name, media.Size, input.ScheduledFor, recurrence, until).Scan(&id); err != nil {
		return ScheduledMessage{}, err
	}
	if err := tx.Commit(); err != nil {
		return ScheduledMessage{}, err
	}
	return GetScheduledMessage(companyID, id)
}

func GetScheduledMessage(companyID int, id int64) (ScheduledMessage, error) {
	return scanScheduledMessage(db.QueryRow(`SELECT `+scheduledMessageColumns+` `+scheduledMessageJoins+`
		WHERE sm.id = $1 AND sm."companyId" = $2`, id, companyID))
}

func lockEditableScheduledMessage(tx *sql.Tx, companyID int, id int64, actorID int, admin bool) (*ScheduledMessageMedia, string, error) {
	var status string
	var kind, path, name sql.NullString
	var size sql.NullInt64
	err := tx.QueryRow(`SELECT status, "mediaKind", "mediaPath", "mediaName", "mediaSize" FROM scheduled_messages
		WHERE id = $1 AND "companyId" = $2 AND ($3 OR "agentId" = $4) FOR UPDATE`, id, companyID, admin, actorID).
		Scan(&status, &kind, &path, &name, &size)
	if err != nil {
		return nil, "", err
	}
	if status != ScheduledMessageStatusPending && status != ScheduledMessageStatusFailed {
		return nil, status, ErrScheduledMessageLocked
	}
	return &ScheduledMessageMedia{Kind: kind.String, Path: path.String, Name: name.String, Size: size.Int64}, status, nil
}

// UpdateScheduledMessage edits a pending or failed schedule and puts it back in
// the queue. It returns the attachment that is no longer referenced (replaced
// or removed) so the caller can delete the file and release its storage. An
// edited occurrence leaves its series and starts a new one from its own time.
func UpdateScheduledMessage(companyID int, id int64, actorID int, admin bool, input ScheduledMessageInput) (ScheduledMessage, *ScheduledMessageMedia, error) {
	tx, err := db.Begin()
	if err != nil {
		return ScheduledMessage{}, nil, err
	}
	defer tx.Rollback()
	current, _, err := lockEditableScheduledMessage(tx, companyID, id, actorID, admin)
	if err != nil {
		return ScheduledMessage{}, nil, err
	}
	if err := checkScheduledTarget(tx, companyID, input); err != nil {
		return ScheduledMessage{}, nil, err
	}
	next, released := current, (*ScheduledMessageMedia)(nil)
	if input.Media != nil || input.RemoveMedia {
		next = input.Media
		if next == nil {
			next = &ScheduledMessageMedia{}
		}
		if current.Path != "" && current.Path != next.Path {
			released = current
		}
	}
	if strings.TrimSpace(input.Body) == "" && next.Path == "" {
		return ScheduledMessage{}, nil, ErrScheduledMessageEmpty
	}
	recurrence, until := normalizedRecurrence(input)
	if _, err := tx.Exec(`UPDATE scheduled_messages SET "contactId" = $3, "connectionId" = $4, body = $5,
		"mediaKind" = NULLIF($6, ''), "mediaPath" = NULLIF($7, ''), "mediaName" = NULLIF($8, ''), "mediaSize" = NULLIF($9, 0),
		"scheduledFor" = $10, recurrence = $11, "recurrenceUntil" = $12::date, "seriesId" = NULL, "retryAt" = NULL,
		status = 'pending', attempts = 0, "claimedAt" = NULL, "lastError" = NULL, "updatedAt" = now()
		WHERE id = $1 AND "companyId" = $2`, id, companyID, input.ContactId, input.ConnectionId, strings.TrimSpace(input.Body),
		next.Kind, next.Path, next.Name, next.Size, input.ScheduledFor, recurrence, until); err != nil {
		return ScheduledMessage{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return ScheduledMessage{}, nil, err
	}
	message, err := GetScheduledMessage(companyID, id)
	return message, released, err
}

// SendScheduledMessageNow moves a pending or failed schedule to the front of
// the queue. A recurring series keeps its calendar: the next occurrence is
// still counted from the original time.
func SendScheduledMessageNow(companyID int, id int64, actorID int, admin bool) (ScheduledMessage, error) {
	tx, err := db.Begin()
	if err != nil {
		return ScheduledMessage{}, err
	}
	defer tx.Rollback()
	if _, _, err := lockEditableScheduledMessage(tx, companyID, id, actorID, admin); err != nil {
		return ScheduledMessage{}, err
	}
	if _, err := tx.Exec(`UPDATE scheduled_messages SET "retryAt" = now(), status = 'pending', attempts = 0,
		"claimedAt" = NULL, "lastError" = NULL, "updatedAt" = now() WHERE id = $1`, id); err != nil {
		return ScheduledMessage{}, err
	}
	if err := tx.Commit(); err != nil {
		return ScheduledMessage{}, err
	}
	return GetScheduledMessage(companyID, id)
}

// CancelScheduledMessage stops a pending or failed schedule (and with it the
// rest of its recurring series). The returned attachment was never sent and
// can be deleted by the caller once nothing else references it.
func CancelScheduledMessage(companyID int, id int64, actorID int, admin bool) (*ScheduledMessageMedia, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	media, _, err := lockEditableScheduledMessage(tx, companyID, id, actorID, admin)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE scheduled_messages SET status = 'canceled', "claimedAt" = NULL, "retryAt" = NULL, "updatedAt" = now()
		WHERE id = $1`, id); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if media.Path == "" {
		return nil, nil
	}
	return media, nil
}

func ListScheduledMessages(filter ScheduledMessageFilter) (ScheduledMessagePage, error) {
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize <= 0 || filter.PageSize > 100 {
		filter.PageSize = 30
	}
	page := ScheduledMessagePage{Data: []ScheduledMessage{}, Page: filter.Page, PageSize: filter.PageSize}
	var agentID any
	if filter.AgentId != nil {
		agentID = *filter.AgentId
	}
	statuses := "{}"
	if len(filter.Statuses) > 0 {
		statuses = "{" + strings.Join(filter.Statuses, ",") + "}"
	}
	// Upcoming schedules first (soonest on top); history newest first.
	rows, err := db.Query(`SELECT `+scheduledMessageColumns+`, COUNT(*) OVER() `+scheduledMessageJoins+`
		WHERE sm."companyId" = $1 AND ($2::integer IS NULL OR sm."agentId" = $2)
			AND (cardinality($3::text[]) = 0 OR sm.status = ANY($3::text[]))
		ORDER BY CASE WHEN sm.status IN ('pending', 'processing') THEN 0 ELSE 1 END,
			CASE WHEN sm.status IN ('pending', 'processing') THEN COALESCE(sm."retryAt", sm."scheduledFor") END ASC,
			COALESCE(sm."sentAt", sm."scheduledFor") DESC, sm.id DESC
		LIMIT $4 OFFSET $5`, filter.CompanyId, agentID, statuses, filter.PageSize, (filter.Page-1)*filter.PageSize)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var total int
		message, err := scanScheduledMessage(scannerWithTail{rows, &total})
		if err != nil {
			return page, err
		}
		page.Data = append(page.Data, message)
		page.Total = total
	}
	return page, rows.Err()
}

// ScheduledMessageCounts returns how many schedules each status has, for the
// tab badges. AgentId limits it to one agent like the list does.
func ScheduledMessageCounts(companyID int, agentID *int) (map[string]int, error) {
	var agent any
	if agentID != nil {
		agent = *agentID
	}
	rows, err := db.Query(`SELECT status, COUNT(*) FROM scheduled_messages
		WHERE "companyId" = $1 AND ($2::integer IS NULL OR "agentId" = $2) GROUP BY status`, companyID, agent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		counts[status] = count
	}
	return counts, rows.Err()
}

// scannerWithTail appends trailing destinations (window counts) to a row scan.
type scannerWithTail struct {
	rows interface{ Scan(...any) error }
	tail *int
}

func (scanner scannerWithTail) Scan(dest ...any) error {
	return scanner.rows.Scan(append(dest, scanner.tail)...)
}

// CountScheduledMessageMediaReferences counts schedules that still need a
// stored file, so the media library keeps it until the message is sent.
func CountScheduledMessageMediaReferences(companyID int, path string) (int, error) {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM scheduled_messages
		WHERE "companyId" = $1 AND "mediaPath" = $2 AND status IN ('pending', 'processing', 'failed')`, companyID, path).Scan(&count)
	return count, err
}

func ClaimDueScheduledMessages(limit int) ([]ScheduledMessage, error) {
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// A worker that died mid-send leaves the row processing; requeue it, or
	// fail it once it used every attempt.
	if _, err := tx.Exec(`UPDATE scheduled_messages SET status = CASE WHEN attempts >= $1 THEN 'failed' ELSE 'pending' END,
		"lastError" = CASE WHEN attempts >= $1 THEN COALESCE("lastError", 'o envio foi interrompido') ELSE "lastError" END,
		"claimedAt" = NULL, "updatedAt" = now()
		WHERE status = 'processing' AND "claimedAt" <= now() - interval '5 minutes'`, scheduledMessageAttempts); err != nil {
		return nil, err
	}
	rows, err := tx.Query(`WITH due AS (
		SELECT id FROM scheduled_messages
		WHERE status = 'pending' AND COALESCE("retryAt", "scheduledFor") <= now() AND attempts < $2
		ORDER BY COALESCE("retryAt", "scheduledFor"), id FOR UPDATE SKIP LOCKED LIMIT $1
	)
	UPDATE scheduled_messages message
	SET status = 'processing', attempts = attempts + 1, "claimVersion" = "claimVersion" + 1, "claimedAt" = now(), "updatedAt" = now()
	FROM due WHERE message.id = due.id
	RETURNING message.id`, limit, scheduledMessageAttempts)
	if err != nil {
		return nil, err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	messages := make([]ScheduledMessage, 0, len(ids))
	for _, id := range ids {
		message, err := scanScheduledMessage(db.QueryRow(`SELECT `+scheduledMessageColumns+` `+scheduledMessageJoins+` WHERE sm.id = $1`, id))
		if err != nil {
			return messages, err
		}
		messages = append(messages, message)
	}
	return messages, nil
}

// FinishScheduledMessage records the send outcome. A retryable error requeues
// the schedule (without moving the chosen time) until it runs out of attempts.
func FinishScheduledMessage(id int64, claimVersion, conversationID int, sendErr error, retryable bool) error {
	var conversation any
	if conversationID > 0 {
		conversation = conversationID
	}
	if sendErr == nil {
		result, err := db.Exec(`UPDATE scheduled_messages SET status = 'sent', "sentAt" = now(), "claimedAt" = NULL, "retryAt" = NULL,
			"lastError" = NULL, "conversationId" = COALESCE($3, "conversationId"), "updatedAt" = now()
			WHERE id = $1 AND status = 'processing' AND "claimVersion" = $2`, id, claimVersion, conversation)
		return scheduledMessageUpdateResult(result, err)
	}
	result, err := db.Exec(`UPDATE scheduled_messages SET
		status = CASE WHEN $4 AND attempts < $5 THEN 'pending' ELSE 'failed' END,
		"retryAt" = CASE WHEN $4 AND attempts < $5 THEN now() + (attempts * interval '1 minute') ELSE NULL END,
		"claimedAt" = NULL, "lastError" = $3, "conversationId" = COALESCE($6, "conversationId"), "updatedAt" = now()
		WHERE id = $1 AND status = 'processing' AND "claimVersion" = $2`, id, claimVersion, sendErr.Error(), retryable, scheduledMessageAttempts, conversation)
	return scheduledMessageUpdateResult(result, err)
}

// NextScheduledOccurrence returns the first occurrence of a recurring series
// after both the occurrence just sent and now, counted from the series start in
// the company timezone. Monthly dates fall on the series day, or on the last
// day of shorter months. It reports false once the series has ended.
func NextScheduledOccurrence(message ScheduledMessage, location *time.Location, now time.Time) (time.Time, bool) {
	if message.Recurrence == "" || message.Recurrence == ScheduledRecurrenceNone {
		return time.Time{}, false
	}
	start := message.SeriesStart
	if start.IsZero() {
		start = message.ScheduledFor
	}
	start = start.In(location)
	var until time.Time
	if message.RecurrenceUntil != "" {
		parsed, err := time.ParseInLocation("2006-01-02", message.RecurrenceUntil, location)
		if err != nil {
			return time.Time{}, false
		}
		until = parsed.AddDate(0, 0, 1) // the whole last day is included
	}
	after := message.ScheduledFor
	if now.After(after) {
		after = now
	}
	for step := 1; step <= 4000; step++ {
		var candidate time.Time
		switch message.Recurrence {
		case ScheduledRecurrenceDaily:
			candidate = start.AddDate(0, 0, step)
		case ScheduledRecurrenceWeekly:
			candidate = start.AddDate(0, 0, 7*step)
		case ScheduledRecurrenceMonthly:
			first := time.Date(start.Year(), start.Month()+time.Month(step), 1, start.Hour(), start.Minute(), start.Second(), 0, location)
			lastDay := first.AddDate(0, 1, -1).Day()
			candidate = time.Date(first.Year(), first.Month(), min(start.Day(), lastDay), start.Hour(), start.Minute(), start.Second(), 0, location)
		default:
			return time.Time{}, false
		}
		if !until.IsZero() && !candidate.Before(until) {
			return time.Time{}, false
		}
		if candidate.After(after) {
			return candidate, true
		}
	}
	return time.Time{}, false
}

// CreateNextScheduledOccurrence queues the next occurrence of a recurring
// series with the same content. Creating it twice is a no-op.
func CreateNextScheduledOccurrence(source ScheduledMessage, scheduledFor time.Time) error {
	series := source.Id
	if source.SeriesId != nil {
		series = *source.SeriesId
	}
	_, err := db.Exec(`INSERT INTO scheduled_messages ("companyId", "contactId", "connectionId", "agentId", body,
		"mediaKind", "mediaPath", "mediaName", "mediaSize", "scheduledFor", recurrence, "recurrenceUntil", "seriesId")
		SELECT "companyId", "contactId", "connectionId", "agentId", body, "mediaKind", "mediaPath", "mediaName", "mediaSize",
			$2, recurrence, "recurrenceUntil", $3
		FROM scheduled_messages WHERE id = $1
		ON CONFLICT ("seriesId", "scheduledFor") WHERE "seriesId" IS NOT NULL DO NOTHING`, source.Id, scheduledFor, series)
	return err
}
