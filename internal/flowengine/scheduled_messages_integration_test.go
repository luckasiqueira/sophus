package flowengine

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"sophus/internal/media"
	"sophus/internal/repo"
	"sophus/utils/env"

	"github.com/google/uuid"
)

func init() {
	// The schema test runs against a disposable database; keep the shared
	// object cache off so no key from it ever reaches a configured Redis.
	if os.Getenv("ZUBLY_TEST_SCHEMA_POSTGRES_URL") != "" {
		env.Backend["REDIS_ADDR"] = ""
	}
}

type fakeEvolution struct {
	mu       sync.Mutex
	requests []map[string]any
	paths    []string
	fail     bool
}

func (f *fakeEvolution) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.paths = append(f.paths, r.URL.Path)
	f.requests = append(f.requests, body)
	if f.fail {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	fmt.Fprintf(w, `{"data":{"Info":{"ID":"MSG-%d","Timestamp":%q}}}`, len(f.paths), time.Now().UTC().Format(time.RFC3339Nano))
}

func TestAgentScheduledMessagesAreDeliveredEndToEnd(t *testing.T) {
	dsn := os.Getenv("ZUBLY_TEST_SCHEMA_POSTGRES_URL")
	if dsn == "" {
		t.Skip("set ZUBLY_TEST_SCHEMA_POSTGRES_URL to a disposable database for the scheduled delivery test")
	}
	testDB, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	testDB.SetMaxOpenConns(1)
	restore := repo.ReplaceDatabaseForTesting(testDB)
	t.Cleanup(func() { restore(); testDB.Close() })
	if err := repo.RunMigrations(); err != nil {
		t.Fatal(err)
	}
	evolution := &fakeEvolution{}
	server := httptest.NewServer(evolution)
	t.Cleanup(server.Close)
	previousAPI := repo.ApiBaseURL
	repo.ApiBaseURL = server.URL
	t.Cleanup(func() { repo.ApiBaseURL = previousAPI })
	for key, value := range map[string]string{"MEDIA_DIRECTORY": t.TempDir(), "SALT_JWT": "scheduled-test-secret", "APP_DOMAIN": "https://zubly.test"} {
		previous := env.Backend[key]
		env.Backend[key] = value
		t.Cleanup(func() { env.Backend[key] = previous })
	}

	scan := func(target *int, query string, args ...any) {
		t.Helper()
		if err := testDB.QueryRow(query, args...).Scan(target); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	suffix := uuid.NewString()[:8]
	var company, plan, agent, contact, connection int
	scan(&company, `INSERT INTO companies (name, email, "isActive") VALUES ('Envio', $1, true) RETURNING id`, "envio-"+suffix+"@teste.local")
	scan(&plan, `INSERT INTO plans (name, "priceCents", "durationDays", "agentLimit", "connectionLimit", "monthlyPriceCents", "annualPriceCents") VALUES ($1, 0, 30, 5, 5, 0, 0) RETURNING id`, "Teste "+suffix)
	var subscription int
	scan(&subscription, `INSERT INTO subscriptions ("companyId", "planId", status, "priceCents", "durationDays", "startsAt", "currentPeriodStart", "currentPeriodEnd", "basePeriodEnd")
		VALUES ($1, $2, 'active', 0, 30, now(), now(), now() + interval '30 days', now() + interval '30 days') RETURNING id`, company, plan)
	scan(&agent, `INSERT INTO agents (name, email, password, role, "companyId", "isActive", "createdAt", "updatedAt") VALUES ('Ana', $1, 'x', 'agent', $2, true, now(), now()) RETURNING id`, "ana-"+suffix+"@teste.local", company)
	scan(&contact, `INSERT INTO contacts ("companyId", name, number) VALUES ($1, 'Cliente', '5511988887777') RETURNING id`, company)
	scan(&connection, `INSERT INTO connections ("companyId", name, number, status, webhook, "connectionKey") VALUES ($1, 'Comercial', '551130000000', 'connected', $2, $3) RETURNING id`, company, uuid.New(), uuid.New())
	if allowed, err := repo.CompanyHasAccess(company); err != nil || !allowed {
		t.Fatalf("fixture company must have access: allowed=%v err=%v", allowed, err)
	}
	if _, err := testDB.Exec(`UPDATE scheduled_messages SET status = 'canceled' WHERE status IN ('pending', 'processing')`); err != nil {
		t.Fatal(err)
	}

	due := func(id int64) {
		t.Helper()
		if _, err := testDB.Exec(`UPDATE scheduled_messages SET "scheduledFor" = now() - interval '1 second', "retryAt" = NULL WHERE id = $1`, id); err != nil {
			t.Fatal(err)
		}
	}
	runWorker := func() []repo.ScheduledMessage {
		t.Helper()
		claimed, err := repo.ClaimDueScheduledMessages(10)
		if err != nil {
			t.Fatal(err)
		}
		for _, message := range claimed {
			processAgentScheduledMessage(message)
		}
		return claimed
	}
	future := time.Now().Add(time.Hour)
	// The agent's default preferences sign outgoing messages with their name.
	signed := func(text string) string { return repo.DefaultAgentPreferences().SignMessage(text, "Ana") }

	// 1. Text only: a conversation is opened for the agent and the message saved.
	text, err := repo.CreateScheduledMessage(company, agent, repo.ScheduledMessageInput{ContactId: contact, ConnectionId: connection, Body: "Bom dia! Lembrete da sua consulta.", ScheduledFor: future})
	if err != nil {
		t.Fatal(err)
	}
	due(text.Id)
	if claimed := runWorker(); len(claimed) != 1 {
		t.Fatalf("claimed %d schedules", len(claimed))
	}
	sent, _ := repo.GetScheduledMessage(company, text.Id)
	if sent.Status != repo.ScheduledMessageStatusSent || sent.ConversationURL == nil {
		t.Fatalf("text schedule not sent: %#v", sent)
	}
	if len(evolution.paths) != 1 || evolution.paths[0] != "/send/text" || evolution.requests[0]["text"] != signed("Bom dia! Lembrete da sua consulta.") || evolution.requests[0]["number"] != "5511988887777" {
		t.Fatalf("evolution received %v %v", evolution.paths, evolution.requests)
	}
	var savedText string
	var ownerID int
	if err := testDB.QueryRow(`SELECT m.text, cv."agentId" FROM messages m JOIN conversations cv ON cv.id = m."conversationId"
		WHERE cv.url = $1 AND m."isFromMe" = true ORDER BY m.id DESC LIMIT 1`, *sent.ConversationURL).Scan(&savedText, &ownerID); err != nil {
		t.Fatalf("sent message not saved in the conversation: %v", err)
	}
	if savedText != signed("Bom dia! Lembrete da sua consulta.") || ownerID != agent {
		t.Fatalf("saved text=%q owner=%d", savedText, ownerID)
	}

	// 2. Document with caption: sent through /send/media with a signed URL.
	stored, err := media.SaveScoped(company, media.ScopeMessages, "document", "proposta.pdf", "application/pdf", strings.NewReader("%PDF-1.4\n%test\n"))
	if err != nil {
		t.Fatal(err)
	}
	document, err := repo.CreateScheduledMessage(company, agent, repo.ScheduledMessageInput{ContactId: contact, ConnectionId: connection, Body: "Segue a proposta",
		ScheduledFor: future, Media: &repo.ScheduledMessageMedia{Kind: "document", Path: stored.Path, Name: stored.Name, Size: stored.Size}})
	if err != nil {
		t.Fatal(err)
	}
	due(document.Id)
	runWorker()
	if sent, _ := repo.GetScheduledMessage(company, document.Id); sent.Status != repo.ScheduledMessageStatusSent {
		t.Fatalf("document schedule not sent: %#v", sent)
	}
	last := evolution.requests[len(evolution.requests)-1]
	if evolution.paths[len(evolution.paths)-1] != "/send/media" || last["type"] != "document" || last["caption"] != signed("Segue a proposta") ||
		!strings.HasPrefix(fmt.Sprint(last["url"]), "https://zubly.test/media-public/") || last["filename"] != filepath.Base(stored.Name) {
		t.Fatalf("media payload = %v", last)
	}
	var mediaPath string
	if err := testDB.QueryRow(`SELECT COALESCE("mediaPath", '') FROM messages WHERE "mediaPath" = $1`, stored.Path).Scan(&mediaPath); err != nil {
		t.Fatalf("sent document not linked to the conversation message: %v", err)
	}

	// 3. Audio with text: the text follows in a separate message.
	audio, err := media.SaveScoped(company, media.ScopeMessages, "audio", "recado.ogg", "audio/ogg", strings.NewReader("OggS\x00\x02test-audio"))
	if err != nil {
		t.Fatal(err)
	}
	voice, err := repo.CreateScheduledMessage(company, agent, repo.ScheduledMessageInput{ContactId: contact, ConnectionId: connection, Body: "Ouça o recado",
		ScheduledFor: future, Media: &repo.ScheduledMessageMedia{Kind: "audio", Path: audio.Path, Name: audio.Name, Size: audio.Size}})
	if err != nil {
		t.Fatal(err)
	}
	before := len(evolution.paths)
	due(voice.Id)
	runWorker()
	if sent, _ := repo.GetScheduledMessage(company, voice.Id); sent.Status != repo.ScheduledMessageStatusSent {
		t.Fatalf("audio schedule not sent: %#v", sent)
	}
	if got := evolution.paths[before:]; len(got) != 2 || got[0] != "/send/media" || got[1] != "/send/text" || evolution.requests[before]["caption"] != nil || evolution.requests[before+1]["text"] != signed("Ouça o recado") {
		t.Fatalf("audio send sequence = %v (%v)", got, evolution.requests[before:])
	}

	// 4. Evolution down: the schedule is requeued with the reason recorded.
	evolution.fail = true
	retry, err := repo.CreateScheduledMessage(company, agent, repo.ScheduledMessageInput{ContactId: contact, ConnectionId: connection, Body: "Tentativa", ScheduledFor: future})
	if err != nil {
		t.Fatal(err)
	}
	due(retry.Id)
	runWorker()
	requeued, _ := repo.GetScheduledMessage(company, retry.Id)
	if requeued.Status != repo.ScheduledMessageStatusPending || requeued.Attempts != 1 || requeued.LastError == "" {
		t.Fatalf("failed send must be retried: %#v", requeued)
	}
	evolution.fail = false

	// 5. Disconnected instance at send time: retryable with an explicit reason.
	if _, err := testDB.Exec(`UPDATE connections SET status = 'disconnected' WHERE id = $1`, connection); err != nil {
		t.Fatal(err)
	}
	due(retry.Id)
	runWorker()
	offline, _ := repo.GetScheduledMessage(company, retry.Id)
	if offline.Status != repo.ScheduledMessageStatusPending || !strings.Contains(offline.LastError, "desconectada") {
		t.Fatalf("disconnected send = %#v", offline)
	}
	if _, err := testDB.Exec(`UPDATE connections SET status = 'connected' WHERE id = $1`, connection); err != nil {
		t.Fatal(err)
	}

	// 6. Recurring: after the delivery the worker queues the next occurrence.
	daily, err := repo.CreateScheduledMessage(company, agent, repo.ScheduledMessageInput{ContactId: contact, ConnectionId: connection, Body: "Bom dia!",
		ScheduledFor: future, Recurrence: repo.ScheduledRecurrenceDaily})
	if err != nil {
		t.Fatal(err)
	}
	due(daily.Id)
	runWorker()
	var nextID int64
	var nextAt time.Time
	if err := testDB.QueryRow(`SELECT id, "scheduledFor" FROM scheduled_messages WHERE "seriesId" = $1 AND status = 'pending'`, daily.Id).Scan(&nextID, &nextAt); err != nil {
		t.Fatalf("next daily occurrence not created: %v", err)
	}
	if !nextAt.After(time.Now()) || nextAt.After(time.Now().Add(25*time.Hour)) {
		t.Fatalf("next daily occurrence at %v", nextAt)
	}
	if _, err := repo.CancelScheduledMessage(company, nextID, agent, false); err != nil {
		t.Fatal(err)
	}

	// 7. Agent deactivated: permanent failure, no retry.
	if _, err := testDB.Exec(`UPDATE agents SET "isActive" = false WHERE id = $1`, agent); err != nil {
		t.Fatal(err)
	}
	due(retry.Id)
	runWorker()
	stopped, _ := repo.GetScheduledMessage(company, retry.Id)
	if stopped.Status != repo.ScheduledMessageStatusFailed || !strings.Contains(stopped.LastError, "agente") {
		t.Fatalf("deactivated agent = %#v", stopped)
	}
}
