package repo

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/joho/godotenv"
	"google.golang.org/protobuf/encoding/protowire"
)

func TestMessageEditMigrationHidesOnlyInternalPlaceholders(t *testing.T) {
	if os.Getenv("ZUBLY_TEST_PGLITE") == "" {
		t.Skip("set ZUBLY_TEST_PGLITE for isolated migration tests")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js required")
	}
	migration, err := migrations.ReadFile("migrations/82__retain_message_edit_source.sql")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "-e", `
const assert=require('node:assert/strict'),{PGlite}=require(process.env.ZUBLY_TEST_PGLITE),migration=require('node:fs').readFileSync(0,'utf8');
(async()=>{const db=new PGlite();try {
 await db.exec(`+"`"+`CREATE TABLE messages(id integer PRIMARY KEY,"mediaType" text,text text,"mediaPath" text,"isRead" boolean,"isDeleted" boolean);
 INSERT INTO messages VALUES(1,'unknown','secretEncryptedMessage','',false,false),(2,'text','secretEncryptedMessage','',false,false),(3,'text','Original','',true,false);`+"`"+`);
 await db.exec(migration.split('-- +goose Down')[0]);
 const {rows}=await db.query('SELECT * FROM messages ORDER BY id');assert.equal(rows[0].isDeleted,true);assert.equal(rows[0].isRead,true);assert.equal(rows[0].mediaType,'system');assert.equal(rows[1].isDeleted,false);assert.equal(rows[2].isDeleted,false);assert.deepEqual(rows[2].editSource,{});
 await db.exec(migration.split('-- +goose Down')[1]);
 console.log('PASS: encrypted edit placeholder cleanup preserves ordinary messages');
 }finally{await db.close();}})().catch(error=>{console.error(error);process.exitCode=1});`)
	command.Stdin = strings.NewReader(string(migration))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("edit migration failed: %v\n%s", err, output)
	} else {
		t.Log(string(output))
	}
}

const encryptedEditFixture = `{"data":{"Info":{"ID":"edit-event","Chat":"5511888888888@s.whatsapp.net","Sender":"5511888888888@s.whatsapp.net","Timestamp":"2026-10-09T12:00:00Z"},"Message":{"secretEncryptedMessage":{"targetMessageKey":{"id":"original","fromMe":true},"secretEncType":"MESSAGE_EDIT","encIV":"AgICAgICAgICAgIC","encPayload":"FDr+GfZdhrHU7DSo40taPGEd0vGqR+3/SH4XzGSGQQ=="}}}}`

func editFixture(t *testing.T, payload string) EventMessageEVO {
	t.Helper()
	var msg EventMessageEVO
	if err := json.Unmarshal([]byte(payload), &msg); err != nil {
		t.Fatal(err)
	}
	msg.FullJSON = []byte(payload)
	NormalizeWebhookMessage(&msg, ConnectionEVO{})
	return msg
}

func TestWebhookEditsIdentifyOriginalMessageAndContent(t *testing.T) {
	for _, payload := range []string{
		`{"data":{"Info":{"ID":"original"},"IsEdit":true,"Message":{"conversation":"Texto editado"}}}`,
		`{"data":{"Info":{"ID":"original","IsEdit":true},"Message":{"extendedTextMessage":{"text":"Texto editado"}}}}`,
		`{"data":{"Info":{"ID":"original"},"Message":{"editedMessage":{"message":{"conversation":"Texto editado"}}}}}`,
		`{"data":{"Info":{"ID":"event"},"Message":{"protocolMessage":{"type":14,"key":{"id":"original"},"timestampMS":1791547200000,"editedMessage":{"conversation":"Texto editado"}}}}}`,
		`{"data":{"Info":{"ID":"event"},"Message":{"deviceSentMessage":{"message":{"protocolMessage":{"type":"MESSAGE_EDIT","key":{"id":"original"},"editedMessage":{"extendedTextMessage":{"text":"Texto editado"}}}}}}}}`,
	} {
		edit, handled := parseWebhookMessageEdit(editFixture(t, payload))
		if !handled || edit.TargetID != "original" || !edit.HasText || edit.Text != "Texto editado" {
			t.Fatalf("edit lost original ID/content: %+v (%s)", edit, payload)
		}
	}
	edit, handled := parseWebhookMessageEdit(editFixture(t, `{"data":{"Info":{"ID":"original"},"IsEdit":true,"Message":{"documentMessage":{"fileName":"arquivo.pdf"}}}}`))
	if !handled || !edit.HasText || edit.Text != "" {
		t.Fatal("removed caption was not represented as an empty edit")
	}
	if _, handled := parseWebhookMessageEdit(editFixture(t, `{"data":{"Message":{"conversation":"Mensagem normal"}}}`)); handled {
		t.Fatal("normal text treated as an edit")
	}
	if !IsInternalWhatsAppEvent(editFixture(t, encryptedEditFixture)) {
		t.Fatal("encrypted control message can leak into transcript as a placeholder")
	}
}

func TestEncryptedEditGoldenVectorAndAuthentication(t *testing.T) {
	// Golden vector generated independently with Node.js hkdfSync/AES-256-GCM,
	// using WhatsApp's Message Edit derivation and a waE2E conversation protobuf.
	edit, handled := parseWebhookMessageEdit(editFixture(t, encryptedEditFixture))
	source := messageEditSecret{Secret: bytes.Repeat([]byte{1}, 32), Senders: []string{"5511888888888:3@s.whatsapp.net"}}
	text, ok := decryptMessageEdit(edit, source, []string{"5511888888888@s.whatsapp.net"})
	if !handled || !ok || text != "Texto editado" {
		t.Fatalf("encrypted edit could not be decoded: %q (%v)", text, ok)
	}
	source.Secret[0] ^= 1
	if _, ok := decryptMessageEdit(edit, source, []string{"5511888888888@s.whatsapp.net"}); ok {
		t.Fatal("wrong secret accepted")
	}
	source.Secret[0] ^= 1
	if _, ok := decryptMessageEdit(edit, source, []string{"5511777777777@s.whatsapp.net"}); ok {
		t.Fatal("wrong author accepted")
	}
	if _, ok := editedProtoText([]byte{0xff}, 0); ok {
		t.Fatal("malformed protobuf accepted")
	}
	document := protowire.AppendTag(nil, 8, protowire.BytesType)
	document = protowire.AppendString(document, "arquivo.pdf")
	message := protowire.AppendTag(nil, 7, protowire.BytesType)
	message = protowire.AppendBytes(message, document)
	if text, ok := editedProtoText(message, 0); !ok || text != "" {
		t.Fatalf("document file name used as edited caption: %q", text)
	}
}

func TestEditSecretsStayServerSideAndLegacyProviderRecovery(t *testing.T) {
	secret := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	payload := `{"data":{"Info":{"Sender":"5511888888888@s.whatsapp.net"},"Message":{"deviceSentMessage":{"message":{"conversation":"Original"}},"messageContextInfo":{"messageSecret":"` + secret + `"}}}}`
	msg := editFixture(t, payload)
	var source messageEditSecret
	if err := json.Unmarshal(messageEditSource(msg, ConnectionEVO{}), &source); err != nil || len(source.Secret) != 32 {
		t.Fatal("normalization lost edit secret")
	}
	public, _ := json.Marshal(MessageData{Text: "Original"})
	if strings.Contains(string(public), "messageSecret") || strings.Contains(string(public), secret) {
		t.Fatal("secret exposed in message DTO")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/message/status" || r.Header.Get("apikey") != "instance-key" {
			t.Error("recovery did not use the correct instance")
		}
		var request map[string]string
		_ = json.NewDecoder(r.Body).Decode(&request)
		if request["id"] != "original" {
			t.Error("wrong recovery target")
		}
		_, _ = w.Write([]byte(`{"data":{"result":{"rawData":` + strconvQuote(payload) + `}}}`))
	}))
	defer server.Close()
	previous := ApiBaseURL
	ApiBaseURL = server.URL
	t.Cleanup(func() { ApiBaseURL = previous })
	legacy, err := loadLegacyEditSecret("original", ConnectionEVO{APIToken: "instance-key"})
	if err != nil || len(legacy.Secret) != 32 {
		t.Fatalf("legacy edit recovery failed: %v", err)
	}
}

func strconvQuote(value string) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func TestMessageEditPreservesAttendanceAndConnectionScopePostgres(t *testing.T) {
	if os.Getenv("ZUBLY_TEST_POSTGRES") == "" {
		t.Skip("set ZUBLY_TEST_POSTGRES for isolated edit lifecycle tests")
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
	previous := db
	db = testDB
	t.Cleanup(func() { db = previous })
	if _, err := testDB.Exec(`CREATE TEMP TABLE connections(id integer PRIMARY KEY,"companyId" integer);
CREATE TEMP TABLE conversations(id integer PRIMARY KEY,"connectionId" integer,status text);
CREATE TEMP TABLE messages(id integer PRIMARY KEY,"messageId" text,"conversationId" integer,text text,"mediaType" text,"isDeleted" boolean,"isFromMe" boolean,"isRead" boolean,"mediaPath" text,"quotedId" text,"createdAt" timestamptz,"updatedAt" timestamptz,"displayData" jsonb,"editSource" jsonb);
INSERT INTO connections VALUES(9,3),(10,3),(11,4);
INSERT INTO conversations VALUES(12,9,'closed'),(13,10,'open'),(14,11,'open');
INSERT INTO messages SELECT n,'original',n+11,'Original','text',false,false,true,'','quote','2026-10-01','2026-10-01','{}','{}' FROM generate_series(1,3) n;`); err != nil {
		t.Fatal(err)
	}
	source, _ := json.Marshal(messageEditSecret{Secret: bytes.Repeat([]byte{1}, 32), Senders: []string{"5511888888888@s.whatsapp.net"}})
	if _, err := testDB.Exec(`UPDATE messages SET "editSource"=$1::jsonb WHERE id=1`, string(source)); err != nil {
		t.Fatal(err)
	}
	msg := editFixture(t, encryptedEditFixture)
	connection := ConnectionEVO{Id: 9, CompanyID: 3}
	if id, handled, err := ApplyWebhookMessageEdit(msg, connection); err != nil || !handled || id != "original" {
		t.Fatalf("edit not applied: id=%s handled=%v err=%v", id, handled, err)
	}
	if id, _, err := ApplyWebhookMessageEdit(msg, connection); err != nil || id != "" {
		t.Fatalf("replayed edit not idempotent: %s (%v)", id, err)
	}
	if id, _, err := ApplyWebhookMessageEdit(msg, ConnectionEVO{Id: 9, CompanyID: 4}); err != nil || id != "" {
		t.Fatalf("another company could edit message: %s (%v)", id, err)
	}
	stale := editFixture(t, `{"data":{"Info":{"ID":"original","Timestamp":"2026-10-08T12:00:00Z"},"IsEdit":true,"Message":{"conversation":"Edição antiga"}}}`)
	if id, _, err := ApplyWebhookMessageEdit(stale, connection); err != nil || id != "" {
		t.Fatalf("older edit overwrote newer content: %s (%v)", id, err)
	}
	var changed, untouched, intact, conversationCount int
	if err := testDB.QueryRow(`SELECT COUNT(*) FILTER(WHERE id=1 AND text='Texto editado' AND "displayData"->>'edited'='true'),
COUNT(*) FILTER(WHERE id<>1 AND text='Original'),COUNT(*) FILTER(WHERE "quotedId"='quote' AND "isRead"=true AND "isFromMe"=false AND "createdAt"="updatedAt" AND "updatedAt"='2026-10-01') FROM messages`).Scan(&changed, &untouched, &intact); err != nil {
		t.Fatal(err)
	}
	if err := testDB.QueryRow(`SELECT COUNT(*) FROM conversations WHERE (id=12 AND status='closed') OR id IN (13,14)`).Scan(&conversationCount); err != nil {
		t.Fatal(err)
	}
	if changed != 1 || untouched != 2 || intact != 3 || conversationCount != 3 {
		t.Fatalf("edit changed original lifecycle or other connections: changed=%d untouched=%d intact=%d conversations=%d", changed, untouched, intact, conversationCount)
	}
}
