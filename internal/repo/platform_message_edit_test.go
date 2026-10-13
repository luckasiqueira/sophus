package repo

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
)

func TestMessageEditPolicyAndEligibility(t *testing.T) {
	owner := 7
	conversation := Conversation{ConnectionID: 9, Status: ConversationStatusOpen, AgentID: &owner}
	for _, scenario := range []struct {
		policy string
		role   string
		id     int
		want   bool
	}{
		{MessageEditingDisabled, RoleAdmin, 8, false},
		{MessageEditingDisabled, RoleAgent, 7, false},
		{MessageEditingAdmins, RoleAdmin, 8, true},
		{MessageEditingAdmins, RoleAgent, 7, false},
		{MessageEditingEveryone, RoleAdmin, 8, true},
		{MessageEditingEveryone, RoleAgent, 7, true},
		{MessageEditingEveryone, RoleAgent, 8, false},
		{"invalid", RoleAdmin, 8, false},
	} {
		if got := AgentCanEditMessages(scenario.policy, Agent{Id: scenario.id, Role: scenario.role}, conversation); got != scenario.want {
			t.Fatalf("policy=%s role=%s agent=%d: allowed=%v", scenario.policy, scenario.role, scenario.id, got)
		}
	}
	conversation.Status = ConversationStatusClosed
	if AgentCanEditMessages(MessageEditingEveryone, Agent{Role: RoleAdmin}, conversation) {
		t.Fatal("closed attendance editable")
	}
	now := time.Now()
	message := MessageData{MessageId: "REAL-ID", IsFromMe: true, MediaType: "text", CreatedAt: now.Add(-time.Minute)}
	if !MessageCanBeEdited(message, now) {
		t.Fatal("recent outgoing text cannot be edited")
	}
	for _, change := range []func(*MessageData){
		func(m *MessageData) { m.IsFromMe = false }, func(m *MessageData) { m.IsDeleted = true },
		func(m *MessageData) { m.CreatedAt = now.Add(-15 * time.Minute) }, func(m *MessageData) { m.CreatedAt = now.Add(time.Minute) },
		func(m *MessageData) { m.MediaType = "image" }, func(m *MessageData) { m.MessageId = "flow-synthetic" },
	} {
		copy := message
		change(&copy)
		if MessageCanBeEdited(copy, now) {
			t.Fatalf("invalid edit accepted: %+v", copy)
		}
	}
}

func TestPlatformEditUsesEvolutionGOContract(t *testing.T) {
	status := http.StatusOK
	bodyResponse := `{"message":"success","data":{"messageId":"EDIT-EVENT","timestamp":"2026-10-09T12:00:00Z"}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/message/edit" || r.Header.Get("apikey") != "session-key" {
			t.Errorf("wrong edit request: %s %s", r.Method, r.URL.Path)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["chat"] != "120363123456@g.us" || body["messageId"] != "ORIGINAL" || body["message"] != "Texto alterado" || len(body) != 3 {
			t.Errorf("wrong edit payload: %v", body)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(bodyResponse))
	}))
	defer server.Close()
	previous := ApiBaseURL
	ApiBaseURL = server.URL
	t.Cleanup(func() { ApiBaseURL = previous })
	connection := ConnectionEVO{APIToken: "session-key"}
	if err := sendWhatsAppMessageEdit(connection, "120363123456@g.us", "ORIGINAL", "Texto alterado"); err != nil {
		t.Fatal(err)
	}
	status = http.StatusBadRequest
	if err := sendWhatsAppMessageEdit(connection, "120363123456@g.us", "ORIGINAL", "Texto alterado"); !errors.Is(err, ErrWhatsAppEditRejected) {
		t.Fatalf("WhatsApp rejection not reported: %v", err)
	}
	status = http.StatusOK
	bodyResponse = `{"message":"error"}`
	if err := sendWhatsAppMessageEdit(connection, "120363123456@g.us", "ORIGINAL", "Texto alterado"); !errors.Is(err, ErrWhatsAppEditRejected) {
		t.Fatal("HTTP 200 without provider confirmation accepted")
	}
}

func TestPlatformEditPermissionsAndRemoteFailurePostgres(t *testing.T) {
	if os.Getenv("ZUBLY_TEST_POSTGRES") == "" {
		t.Skip("set ZUBLY_TEST_POSTGRES for isolated platform edit tests")
	}
	config, err := godotenv.Read("../../.env")
	if err != nil {
		t.Fatal("workspace database configuration required")
	}
	dsn := &url.URL{Scheme: "postgres", Host: config["POSTGRES_HOST"] + ":" + config["POSTGRES_PORT"], Path: "/" + config["POSTGRES_DB"], User: url.UserPassword(config["POSTGRES_USER"], config["POSTGRES_PASSWORD"])}
	dsn.RawQuery = url.Values{"sslmode": {"disable"}, "options": {"-csearch_path=pg_temp"}}.Encode()
	testDB, err := sql.Open("postgres", dsn.String())
	if err != nil {
		t.Fatal(err)
	}
	defer testDB.Close()
	testDB.SetMaxOpenConns(1)
	testDB.SetMaxIdleConns(1)
	previousDB := db
	db = testDB
	t.Cleanup(func() { db = previousDB })
	if _, err := testDB.Exec(`CREATE TEMP TABLE companies(id integer PRIMARY KEY,"operationSettings" jsonb);
CREATE TEMP TABLE contacts(id integer PRIMARY KEY,"companyId" integer,jid text,number text,"isGroup" boolean);
CREATE TEMP TABLE connections(id integer PRIMARY KEY,"companyId" integer,status text,number text,"apiToken" text,"connectionKey" uuid);
CREATE TEMP TABLE conversations(id integer PRIMARY KEY,url uuid,"contactId" integer,"connectionId" integer,status text,"agentId" integer);
CREATE TEMP TABLE messages(id integer PRIMARY KEY,"messageId" text,"conversationId" integer,text text,"mediaType" text,"isFromMe" boolean,"isDeleted" boolean,"isRead" boolean,"createdAt" timestamptz,"updatedAt" timestamptz,"displayData" jsonb);
INSERT INTO companies VALUES(3,'{}');
INSERT INTO contacts VALUES(5,3,'5511888888888@s.whatsapp.net','5511888888888',false);
INSERT INTO connections VALUES(9,3,'connected','5511999999999','session-key',NULL);
INSERT INTO conversations VALUES(12,'00000000-0000-0000-0000-000000000012',5,9,'open',7);
INSERT INTO messages VALUES(1,'ORIGINAL',12,'*Ana*:

Original','text',true,false,true,now()-interval '1 minute',now()-interval '1 minute','{}');`); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	fail := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if fail {
			w.WriteHeader(500)
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["messageId"] != "ORIGINAL" || body["message"] != "*Ana*:\n\nAlterado" {
			t.Error("edit changed signature or original ID")
		}
		_, _ = w.Write([]byte(`{"message":"success","data":{"messageId":"EDIT-EVENT"}}`))
	}))
	defer server.Close()
	previousURL := ApiBaseURL
	ApiBaseURL = server.URL
	t.Cleanup(func() { ApiBaseURL = previousURL })
	u := uuid.MustParse("00000000-0000-0000-0000-000000000012")
	original := "*Ana*:\n\nOriginal"
	edited := "*Ana*:\n\nAlterado"
	for _, scenario := range []struct {
		name, policy, role string
		id, company        int
		want               error
	}{
		{"disabled admin", "disabled", RoleAdmin, 8, 3, ErrMessageEditForbidden},
		{"admins denies agent", "admins", RoleAgent, 7, 3, ErrMessageEditForbidden},
		{"admins allows admin", "admins", RoleAdmin, 8, 3, nil},
		{"all allows owner", "all", RoleAgent, 7, 3, nil},
		{"all denies another agent", "all", RoleAgent, 8, 3, ErrMessageEditForbidden},
		{"foreign company", "all", RoleAdmin, 8, 4, sql.ErrNoRows},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if _, err := testDB.Exec(`UPDATE companies SET "operationSettings"=jsonb_build_object('messageEditPolicy',$1::text)`, scenario.policy); err != nil {
				t.Fatal(err)
			}
			if _, err := testDB.Exec(`UPDATE messages SET text=$1,"displayData"='{}'`, original); err != nil {
				t.Fatal(err)
			}
			before := calls.Load()
			id, err := EditPlatformMessage(Agent{Id: scenario.id, CompanyId: scenario.company, Role: scenario.role}, u, "ORIGINAL", edited, original)
			if !errors.Is(err, scenario.want) {
				t.Fatalf("unexpected edit result: %v", err)
			}
			var actual string
			var count int
			if err := testDB.QueryRow(`SELECT text FROM messages WHERE id=1`).Scan(&actual); err != nil {
				t.Fatal(err)
			}
			if scenario.want == nil {
				if actual != edited || id != 12 || calls.Load() != before+1 {
					t.Fatalf("edit not synchronized: %q id=%d calls=%d", actual, id, calls.Load())
				}
			} else if actual != original || calls.Load() != before {
				t.Fatal("denied edit changed content or called WhatsApp")
			}
			if err := testDB.QueryRow(`SELECT COUNT(*) FROM messages WHERE "isRead"=true AND "createdAt"="updatedAt"`).Scan(&count); err != nil || count != 1 {
				t.Fatal("edit altered identity, read state or original timestamps")
			}
		})
	}
	if _, err := testDB.Exec(`UPDATE companies SET "operationSettings"='{"messageEditPolicy":"all"}'; UPDATE messages SET "displayData"='{}'`); err != nil {
		t.Fatal(err)
	}
	if _, err := testDB.Exec(`UPDATE messages SET text=$1`, original); err != nil {
		t.Fatal(err)
	}
	fail = true
	if _, err := EditPlatformMessage(Agent{Id: 7, CompanyId: 3, Role: RoleAgent}, u, "ORIGINAL", edited, original); !errors.Is(err, ErrWhatsAppEditRejected) {
		t.Fatalf("remote failure accepted: %v", err)
	}
	var actual string
	var display json.RawMessage
	if err := testDB.QueryRow(`SELECT text,"displayData" FROM messages WHERE id=1`).Scan(&actual, &display); err != nil {
		t.Fatal(err)
	}
	if actual != original || string(display) != "{}" {
		t.Fatal("local content changed after WhatsApp rejection")
	}
	before := calls.Load()
	if _, err := EditPlatformMessage(Agent{Id: 7, CompanyId: 3, Role: RoleAgent}, u, "ORIGINAL", edited, "outdated text"); !errors.Is(err, ErrMessageEditConflict) || calls.Load() != before {
		t.Fatalf("stale edit accepted: %v", err)
	}
}
