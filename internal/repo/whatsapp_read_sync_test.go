package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestWhatsAppReadBatchUsesIncomingIDsAndConnectionSession(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/message/markread" || r.Method != "POST" || r.Header.Get("apikey") != "instance-key" {
			t.Errorf("unexpected read sync request: %s %s", r.Method, r.URL.Path)
		}
		var payload struct {
			Number string   `json:"number"`
			IDs    []string `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload.Number != "5511999999999@s.whatsapp.net" || len(payload.IDs) != 2 || payload.IDs[0] != "incoming-1" {
			t.Errorf("incorrect read receipt payload: %+v", payload)
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"message":"success"}`))
	}))
	defer server.Close()
	previous := ApiBaseURL
	ApiBaseURL = server.URL
	t.Cleanup(func() { ApiBaseURL = previous })
	if err := SendWhatsAppReadBatch(WhatsAppReadBatch{Connection: ConnectionEVO{APIToken: "instance-key"}, Chat: "5511999999999@s.whatsapp.net", IDs: []string{"incoming-1", "incoming-2"}}); err != nil {
		t.Fatal(err)
	}
	if err := SendWhatsAppReadBatch(WhatsAppReadBatch{}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("empty read batch called provider")
	}
}

func TestWhatsAppReadSyncPostgresScopeAndPhoneReceipts(t *testing.T) {
	if os.Getenv("ZUBLY_TEST_POSTGRES") == "" {
		t.Skip("set ZUBLY_TEST_POSTGRES for temporary-table read-sync tests")
	}
	config, err := godotenv.Read("../../.env")
	if err != nil {
		t.Fatal("workspace database configuration required")
	}
	dsn := &url.URL{Scheme: "postgres", Host: config["POSTGRES_HOST"] + ":" + config["POSTGRES_PORT"], Path: "/" + config["POSTGRES_DB"], User: url.UserPassword(config["POSTGRES_USER"], config["POSTGRES_PASSWORD"])}
	dsn.RawQuery = url.Values{"sslmode": []string{"disable"}, "options": []string{"-csearch_path=pg_temp"}}.Encode()
	testDB, err := sql.Open("postgres", dsn.String())
	if err != nil {
		t.Fatal("cannot open scoped test connection")
	}
	defer testDB.Close()
	testDB.SetMaxOpenConns(1)
	testDB.SetMaxIdleConns(1)
	previous := db
	db = testDB
	t.Cleanup(func() { db = previous })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture := `CREATE TEMP TABLE contacts(id integer PRIMARY KEY,"companyId" integer,jid text,lid text,number text,"isGroup" boolean);
CREATE TEMP TABLE connections(id integer PRIMARY KEY,"companyId" integer,"apiToken" text,"connectionKey" uuid,status text);
CREATE TEMP TABLE conversations(id integer PRIMARY KEY,"contactId" integer,"connectionId" integer,status text);
CREATE TEMP TABLE messages(id integer PRIMARY KEY,"conversationId" integer,"messageId" text,"isFromMe" boolean,"isRead" boolean,"isDeleted" boolean);
INSERT INTO contacts VALUES(1,1,'5511999999999@s.whatsapp.net','','5511999999999',false),(2,2,'5511888888888@s.whatsapp.net','','5511888888888',false);
INSERT INTO connections VALUES(9,1,'session','00000000-0000-0000-0000-000000000009','connected'),(10,2,'other','00000000-0000-0000-0000-000000000010','connected');
INSERT INTO conversations VALUES(1,1,9,'open'),(2,1,9,'closed'),(3,2,10,'open');
INSERT INTO messages VALUES(1,1,'incoming',false,true,false),(2,1,'outgoing',true,true,false),(3,2,'historical-incoming',false,true,false),(4,3,'foreign',false,true,false),(5,1,'phone-read',false,false,false);`
	if _, err = testDB.ExecContext(ctx, fixture); err != nil {
		t.Fatal(err)
	}
	migration, err := migrations.ReadFile("migrations/79__synchronize_whatsapp_read_state.sql")
	if err != nil {
		t.Fatal(err)
	}
	migrationSQL := strings.ReplaceAll(strings.Split(string(migration), "-- +goose Down")[0], "CREATE TABLE", "CREATE TEMP TABLE")
	if _, err = testDB.ExecContext(ctx, migrationSQL); err != nil {
		t.Fatal(err)
	}
	batch, err := GetPendingWhatsAppReads(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.IDs) != 2 || batch.IDs[0] != "incoming" || batch.IDs[1] != "historical-incoming" {
		t.Fatalf("invalid read-sync scope: %+v", batch.IDs)
	}
	if _, err = GetPendingWhatsAppReads(1, 2); err == nil {
		t.Fatal("cross-company read access allowed")
	}
	connection := ConnectionEVO{Id: 9, CompanyID: 1, ConnectionKey: uuid.MustParse("00000000-0000-0000-0000-000000000009")}
	if _, err = RecordWhatsAppReads(connection, batch.IDs); err != nil {
		t.Fatal(err)
	}
	batch, err = GetPendingWhatsAppReads(1, 1)
	if err != nil || len(batch.IDs) != 0 {
		t.Fatalf("already synchronized messages were repeated: %v", err)
	}
	if _, err = RecordWhatsAppReads(connection, []string{"phone-read", "foreign", "outgoing", "not-yet-saved"}); err != nil {
		t.Fatal(err)
	}
	var phoneRead, foreignSynced, outgoingSynced bool
	if err = testDB.QueryRow(`SELECT "isRead" FROM messages WHERE "messageId"='phone-read'`).Scan(&phoneRead); err != nil {
		t.Fatal(err)
	}
	_ = testDB.QueryRow(`SELECT "whatsappReadSynced" FROM messages WHERE "messageId"='foreign'`).Scan(&foreignSynced)
	_ = testDB.QueryRow(`SELECT "whatsappReadSynced" FROM messages WHERE "messageId"='outgoing'`).Scan(&outgoingSynced)
	if !phoneRead || foreignSynced || outgoingSynced {
		t.Fatal("phone receipt changed another company or an outgoing recipient status")
	}
	var retained bool
	_ = testDB.QueryRow(`SELECT EXISTS(SELECT 1 FROM message_read_receipts WHERE "connectionId"=9 AND "messageId"='not-yet-saved')`).Scan(&retained)
	if !retained {
		t.Fatal("receipt arriving before its message was lost")
	}
}

func TestPhoneSelfReadReceiptDoesNotRepresentRecipientRead(t *testing.T) {
	body := []byte(`{"data":{"type":"read-self","messageIDs":["incoming-1"]}}`)
	ids, self := ParseSelfReadReceipt(body)
	if !self || len(ids) != 1 {
		t.Fatal("phone read-self event ignored")
	}
	_, status, err := ParseMessageReceipt(body)
	if err != nil || status != 0 {
		t.Fatal("phone read-self was treated as recipient reading an outgoing message")
	}
	if _, self := ParseSelfReadReceipt([]byte(`{"data":{"type":"read","messageIDs":["outgoing-1"]}}`)); self {
		t.Fatal("recipient receipt treated as phone-local read")
	}
}
