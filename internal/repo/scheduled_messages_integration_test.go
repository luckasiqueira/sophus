package repo

import (
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

// ZUBLY_TEST_SCHEMA_POSTGRES_URL points at a disposable PostgreSQL (e.g. an
// in-memory PGlite socket). The full migration set is applied to it, so never
// point it at a real database.
func scheduledMessagesTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("ZUBLY_TEST_SCHEMA_POSTGRES_URL")
	if dsn == "" {
		t.Skip("set ZUBLY_TEST_SCHEMA_POSTGRES_URL to a disposable database for scheduled message tests")
	}
	testDB, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	testDB.SetMaxOpenConns(1)
	restore := ReplaceDatabaseForTesting(testDB)
	t.Cleanup(func() { restore(); testDB.Close() })
	if err := RunMigrations(); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	return testDB
}

type scheduleFixture struct {
	company, otherCompany        int
	agent, otherAgent, admin     int
	contact, group, otherContact int
	connection                   int
}

func seedScheduleFixture(t *testing.T, testDB *sql.DB) scheduleFixture {
	t.Helper()
	var f scheduleFixture
	exec := func(target *int, query string, args ...any) {
		t.Helper()
		if err := testDB.QueryRow(query, args...).Scan(target); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	suffix := uuid.NewString()[:8]
	exec(&f.company, `INSERT INTO companies (name, email, "isActive") VALUES ('Empresa', $1, true) RETURNING id`, "empresa-"+suffix+"@teste.local")
	exec(&f.otherCompany, `INSERT INTO companies (name, email, "isActive") VALUES ('Outra', $1, true) RETURNING id`, "outra-"+suffix+"@teste.local")
	agent := `INSERT INTO agents (name, email, password, role, "companyId", "isActive", "createdAt", "updatedAt") VALUES ($1, $2, 'x', $3, $4, true, now(), now()) RETURNING id`
	exec(&f.agent, agent, "Ana", "ana-"+suffix+"@teste.local", "agent", f.company)
	exec(&f.otherAgent, agent, "Bia", "bia-"+suffix+"@teste.local", "agent", f.company)
	exec(&f.admin, agent, "Caio", "caio-"+suffix+"@teste.local", "admin", f.company)
	contact := `INSERT INTO contacts ("companyId", name, number, "isGroup") VALUES ($1, $2, $3, $4) RETURNING id`
	exec(&f.contact, contact, f.company, "Cliente", "5511900000001", false)
	exec(&f.group, contact, f.company, "Grupo", "120363000000", true)
	exec(&f.otherContact, contact, f.otherCompany, "Alheio", "5511900000002", false)
	exec(&f.connection, `INSERT INTO connections ("companyId", name, number, status, webhook) VALUES ($1, 'Comercial', '551130000000', 'connected', $2) RETURNING id`, f.company, uuid.New())
	return f
}

func TestScheduledMessageLifecycleOnMigratedSchema(t *testing.T) {
	testDB := scheduledMessagesTestDB(t)
	f := seedScheduleFixture(t, testDB)
	future := time.Now().Add(2 * time.Hour).Truncate(time.Second)

	// Targets outside the company and groups are rejected.
	for _, contact := range []int{f.otherContact, f.group} {
		if _, err := CreateScheduledMessage(f.company, f.agent, ScheduledMessageInput{ContactId: contact, ConnectionId: f.connection, Body: "Oi", ScheduledFor: future}); !errors.Is(err, ErrScheduledMessageTarget) {
			t.Fatalf("contact %d: err=%v, want ErrScheduledMessageTarget", contact, err)
		}
	}
	// The table itself refuses a schedule with neither text nor attachment.
	if _, err := CreateScheduledMessage(f.company, f.agent, ScheduledMessageInput{ContactId: f.contact, ConnectionId: f.connection, Body: "  ", ScheduledFor: future}); err == nil {
		t.Fatal("empty schedule was accepted")
	}

	attachment := &ScheduledMessageMedia{Kind: "document", Path: "proposta-" + uuid.NewString() + ".pdf", Name: "proposta.pdf", Size: 2048}
	created, err := CreateScheduledMessage(f.company, f.agent, ScheduledMessageInput{ContactId: f.contact, ConnectionId: f.connection, Body: " Segue a proposta ", ScheduledFor: future, Media: attachment})
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != ScheduledMessageStatusPending || created.Body != "Segue a proposta" || created.ContactName != "Cliente" || created.ConnectionName != "Comercial" ||
		created.AgentName != "Ana" || created.MediaName != "proposta.pdf" || !created.ScheduledFor.Equal(future) {
		t.Fatalf("unexpected schedule: %#v", created)
	}
	if count, err := CountScheduledMessageMediaReferences(f.company, attachment.Path); err != nil || count != 1 {
		t.Fatalf("pending attachment must be protected: count=%d err=%v", count, err)
	}

	// Agents only see and change their own schedules; admins see everyone's.
	own, err := ListScheduledMessages(ScheduledMessageFilter{CompanyId: f.company, AgentId: &f.otherAgent})
	if err != nil || own.Total != 0 {
		t.Fatalf("other agent sees %d schedules (err=%v)", own.Total, err)
	}
	all, err := ListScheduledMessages(ScheduledMessageFilter{CompanyId: f.company, Statuses: []string{ScheduledMessageStatusPending}})
	if err != nil || all.Total != 1 || all.Data[0].Id != created.Id {
		t.Fatalf("admin list = %#v err=%v", all, err)
	}
	edit := ScheduledMessageInput{ContactId: f.contact, ConnectionId: f.connection, Body: "Nova versão", ScheduledFor: future.Add(time.Hour)}
	if _, _, err := UpdateScheduledMessage(f.company, created.Id, f.otherAgent, false, edit); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("other agent edit err=%v, want sql.ErrNoRows", err)
	}
	updated, released, err := UpdateScheduledMessage(f.company, created.Id, f.admin, true, edit)
	if err != nil || released != nil || updated.Body != "Nova versão" || updated.MediaPath != attachment.Path {
		t.Fatalf("edit keeping media: updated=%#v released=%#v err=%v", updated, released, err)
	}
	// Removing the only attachment and the text at once is refused.
	if _, _, err := UpdateScheduledMessage(f.company, created.Id, f.agent, false, ScheduledMessageInput{ContactId: f.contact, ConnectionId: f.connection, ScheduledFor: future, RemoveMedia: true}); !errors.Is(err, ErrScheduledMessageEmpty) {
		t.Fatalf("empty edit err=%v", err)
	}
	replacement := &ScheduledMessageMedia{Kind: "image", Path: "foto-" + uuid.NewString() + ".png", Name: "foto.png", Size: 100}
	edit.Media = replacement
	updated, released, err = UpdateScheduledMessage(f.company, created.Id, f.agent, false, edit)
	if err != nil || released == nil || released.Path != attachment.Path || updated.MediaKind != "image" {
		t.Fatalf("replace media: updated=%#v released=%#v err=%v", updated, released, err)
	}

	cancelMe, err := CreateScheduledMessage(f.company, f.agent, ScheduledMessageInput{ContactId: f.contact, ConnectionId: f.connection, ScheduledFor: future, Media: &ScheduledMessageMedia{Kind: "audio", Path: "audio-" + uuid.NewString() + ".ogg", Name: "audio.ogg", Size: 10}})
	if err != nil {
		t.Fatal(err)
	}
	freed, err := CancelScheduledMessage(f.company, cancelMe.Id, f.agent, false)
	if err != nil || freed == nil || freed.Kind != "audio" {
		t.Fatalf("cancel: freed=%#v err=%v", freed, err)
	}
	if _, err := CancelScheduledMessage(f.company, cancelMe.Id, f.agent, false); !errors.Is(err, ErrScheduledMessageLocked) {
		t.Fatalf("second cancel err=%v, want locked", err)
	}
	if count, _ := CountScheduledMessageMediaReferences(f.company, freed.Path); count != 0 {
		t.Fatal("canceled schedule still protects its attachment")
	}
	canceled, err := ListScheduledMessages(ScheduledMessageFilter{CompanyId: f.company, Statuses: []string{ScheduledMessageStatusCanceled}})
	if err != nil || canceled.Total != 1 {
		t.Fatalf("canceled filter total=%d err=%v", canceled.Total, err)
	}
}

func TestScheduledMessageClaimRetryAndFinish(t *testing.T) {
	testDB := scheduledMessagesTestDB(t)
	f := seedScheduleFixture(t, testDB)
	// Earlier tests may leave due rows behind; park them so claims are ours.
	if _, err := testDB.Exec(`UPDATE scheduled_messages SET status = 'canceled' WHERE status IN ('pending', 'processing')`); err != nil {
		t.Fatal(err)
	}
	created, err := CreateScheduledMessage(f.company, f.agent, ScheduledMessageInput{ContactId: f.contact, ConnectionId: f.connection, Body: "Lembrete", ScheduledFor: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := ClaimDueScheduledMessages(10); err != nil || len(claimed) != 0 {
		t.Fatalf("future schedule claimed early: %d err=%v", len(claimed), err)
	}
	makeDue := func() {
		t.Helper()
		if _, err := testDB.Exec(`UPDATE scheduled_messages SET "scheduledFor" = now() - interval '1 second', "retryAt" = NULL WHERE id = $1`, created.Id); err != nil {
			t.Fatal(err)
		}
	}
	makeDue()
	claimed, err := ClaimDueScheduledMessages(10)
	if err != nil || len(claimed) != 1 || claimed[0].Id != created.Id || claimed[0].Attempts != 1 || claimed[0].Status != ScheduledMessageStatusProcessing || claimed[0].CompanyId != f.company {
		t.Fatalf("claim = %#v err=%v", claimed, err)
	}
	if again, _ := ClaimDueScheduledMessages(10); len(again) != 0 {
		t.Fatal("a processing schedule was claimed twice")
	}
	// Editing while it is being sent is refused.
	if _, _, err := UpdateScheduledMessage(f.company, created.Id, f.agent, false, ScheduledMessageInput{ContactId: f.contact, ConnectionId: f.connection, Body: "x", ScheduledFor: time.Now().Add(time.Hour)}); !errors.Is(err, ErrScheduledMessageLocked) {
		t.Fatalf("edit while processing err=%v", err)
	}
	// A stale claim (worker gone) can no longer finish the row.
	if err := FinishScheduledMessage(created.Id, claimed[0].ClaimVersion+1, 0, nil, false); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale claim finish err=%v", err)
	}
	if err := FinishScheduledMessage(created.Id, claimed[0].ClaimVersion, 0, errors.New("conexão desconectada"), true); err != nil {
		t.Fatal(err)
	}
	retry, _ := GetScheduledMessage(f.company, created.Id)
	if retry.Status != ScheduledMessageStatusPending || retry.LastError != "conexão desconectada" || retry.RetryAt == nil || !retry.RetryAt.After(time.Now()) ||
		!retry.ScheduledFor.Before(time.Now()) {
		t.Fatalf("retryable failure must requeue without moving the chosen time: %#v", retry)
	}
	if early, _ := ClaimDueScheduledMessages(10); len(early) != 0 {
		t.Fatal("a retry was claimed before its retry time")
	}
	for attempt := 2; attempt <= 3; attempt++ {
		makeDue()
		claimed, err = ClaimDueScheduledMessages(10)
		if err != nil || len(claimed) != 1 || claimed[0].Attempts != attempt {
			t.Fatalf("attempt %d claim = %#v err=%v", attempt, claimed, err)
		}
		if err := FinishScheduledMessage(created.Id, claimed[0].ClaimVersion, 0, errors.New("conexão desconectada"), true); err != nil {
			t.Fatal(err)
		}
	}
	failed, _ := GetScheduledMessage(f.company, created.Id)
	if failed.Status != ScheduledMessageStatusFailed || failed.Attempts != 3 {
		t.Fatalf("schedule must fail after 3 attempts: %#v", failed)
	}
	// Rescheduling a failed message resets its attempts.
	rescheduled, _, err := UpdateScheduledMessage(f.company, created.Id, f.agent, false, ScheduledMessageInput{ContactId: f.contact, ConnectionId: f.connection, Body: "Lembrete", ScheduledFor: time.Now().Add(time.Minute)})
	if err != nil || rescheduled.Status != ScheduledMessageStatusPending || rescheduled.Attempts != 0 || rescheduled.LastError != "" {
		t.Fatalf("reschedule = %#v err=%v", rescheduled, err)
	}
	makeDue()
	claimed, _ = ClaimDueScheduledMessages(10)
	if len(claimed) != 1 {
		t.Fatal("rescheduled message was not claimed")
	}
	// A worker that died mid-send leaves the row processing; it is requeued.
	if _, err := testDB.Exec(`UPDATE scheduled_messages SET "claimedAt" = now() - interval '10 minutes' WHERE id = $1`, created.Id); err != nil {
		t.Fatal(err)
	}
	recovered, err := ClaimDueScheduledMessages(10)
	if err != nil || len(recovered) != 1 || recovered[0].Attempts != 2 {
		t.Fatalf("stale processing row not recovered: %#v err=%v", recovered, err)
	}
	var conversationID int
	if err := testDB.QueryRow(`INSERT INTO conversations (status, "contactId", "connectionId", url, "createdAt", "updatedAt") VALUES ('open', $1, $2, $3, now(), now()) RETURNING id`,
		f.contact, f.connection, uuid.New()).Scan(&conversationID); err != nil {
		t.Fatal(err)
	}
	if err := FinishScheduledMessage(created.Id, recovered[0].ClaimVersion, conversationID, nil, false); err != nil {
		t.Fatal(err)
	}
	sent, _ := GetScheduledMessage(f.company, created.Id)
	if sent.Status != ScheduledMessageStatusSent || sent.SentAt == nil || sent.ConversationURL == nil || sent.LastError != "" {
		t.Fatalf("sent = %#v", sent)
	}
	if _, err := CancelScheduledMessage(f.company, created.Id, f.agent, false); !errors.Is(err, ErrScheduledMessageLocked) {
		t.Fatalf("cancel after send err=%v", err)
	}
}

func TestScheduledMessageSeriesSendNowAndCounts(t *testing.T) {
	testDB := scheduledMessagesTestDB(t)
	f := seedScheduleFixture(t, testDB)
	if _, err := testDB.Exec(`UPDATE scheduled_messages SET status = 'canceled' WHERE status IN ('pending', 'processing')`); err != nil {
		t.Fatal(err)
	}
	location, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Skip("timezone database unavailable")
	}
	first := time.Now().In(location).Add(48 * time.Hour).Truncate(time.Minute)
	until := first.AddDate(0, 0, 14)
	weekly, err := CreateScheduledMessage(f.company, f.agent, ScheduledMessageInput{ContactId: f.contact, ConnectionId: f.connection, Body: "Relatório semanal",
		ScheduledFor: first, Recurrence: ScheduledRecurrenceWeekly, RecurrenceUntil: &until, Media: &ScheduledMessageMedia{Kind: "document", Path: "semanal-" + uuid.NewString() + ".pdf", Name: "semanal.pdf", Size: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if weekly.Recurrence != ScheduledRecurrenceWeekly || weekly.RecurrenceUntil != until.Format("2006-01-02") || weekly.SeriesId != nil || !weekly.SeriesStart.Equal(first) {
		t.Fatalf("weekly schedule = %#v", weekly)
	}
	// "Send now" puts it at the front of the queue without touching the chosen time.
	if _, err := SendScheduledMessageNow(f.company, weekly.Id, f.otherAgent, false); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("other agent send-now err=%v", err)
	}
	moved, err := SendScheduledMessageNow(f.company, weekly.Id, f.agent, false)
	if err != nil || !moved.ScheduledFor.Equal(first) || moved.RetryAt == nil {
		t.Fatalf("send now = %#v err=%v", moved, err)
	}
	claimed, err := ClaimDueScheduledMessages(10)
	if err != nil || len(claimed) != 1 || claimed[0].Id != weekly.Id {
		t.Fatalf("send-now schedule not claimed: %#v err=%v", claimed, err)
	}
	if err := FinishScheduledMessage(weekly.Id, claimed[0].ClaimVersion, 0, nil, false); err != nil {
		t.Fatal(err)
	}
	next, ok := NextScheduledOccurrence(claimed[0], location, time.Now())
	if !ok || !next.Equal(first.AddDate(0, 0, 7)) {
		t.Fatalf("next weekly occurrence = %v ok=%v, want %v", next, ok, first.AddDate(0, 0, 7))
	}
	for attempt := 0; attempt < 2; attempt++ { // a duplicated worker run creates it once
		if err := CreateNextScheduledOccurrence(claimed[0], next); err != nil {
			t.Fatal(err)
		}
	}
	upcoming, err := ListScheduledMessages(ScheduledMessageFilter{CompanyId: f.company, Statuses: []string{ScheduledMessageStatusPending}})
	if err != nil || upcoming.Total != 1 {
		t.Fatalf("expected exactly one next occurrence, got %d (err=%v)", upcoming.Total, err)
	}
	occurrence := upcoming.Data[0]
	if occurrence.SeriesId == nil || *occurrence.SeriesId != weekly.Id || !occurrence.ScheduledFor.Equal(next) || occurrence.MediaName != "semanal.pdf" ||
		occurrence.Body != "Relatório semanal" || occurrence.AgentName != "Ana" || !occurrence.SeriesStart.Equal(first) {
		t.Fatalf("occurrence = %#v", occurrence)
	}
	if count, _ := CountScheduledMessageMediaReferences(f.company, occurrence.MediaPath); count != 1 {
		t.Fatalf("next occurrence must protect the shared attachment, count=%d", count)
	}
	counts, err := ScheduledMessageCounts(f.company, nil)
	if err != nil || counts[ScheduledMessageStatusSent] < 1 || counts[ScheduledMessageStatusPending] != 1 {
		t.Fatalf("counts = %v err=%v", counts, err)
	}
	mine, err := ScheduledMessageCounts(f.company, &f.otherAgent)
	if err != nil || len(mine) != 0 {
		t.Fatalf("other agent counts = %v err=%v", mine, err)
	}
	// Editing an occurrence detaches it into a series of its own.
	edited, _, err := UpdateScheduledMessage(f.company, occurrence.Id, f.agent, false, ScheduledMessageInput{ContactId: f.contact, ConnectionId: f.connection,
		Body: "Relatório semanal", ScheduledFor: next.Add(time.Hour), Recurrence: ScheduledRecurrenceWeekly})
	if err != nil || edited.SeriesId != nil || !edited.SeriesStart.Equal(next.Add(time.Hour)) || edited.RecurrenceUntil != "" {
		t.Fatalf("edited occurrence = %#v err=%v", edited, err)
	}
}
