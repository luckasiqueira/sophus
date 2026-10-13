package repo

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestContactHistoryScopeAndCursorPostgres(t *testing.T) {
	if os.Getenv("ZUBLY_TEST_PGLITE") == "" {
		t.Skip("set ZUBLY_TEST_PGLITE for isolated PostgreSQL scope tests")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	schema := `
CREATE TABLE contacts (id integer PRIMARY KEY,"companyId" integer);
CREATE TABLE connections (id integer PRIMARY KEY,"companyId" integer);
CREATE TABLE agents (id integer PRIMARY KEY,"companyId" integer,name text);
CREATE TABLE departments (id integer PRIMARY KEY,"companyId" integer,name text);
CREATE TABLE conversations (id integer PRIMARY KEY,url uuid,status text,"contactId" integer,"connectionId" integer,"agentId" integer,"departmentId" integer,"createdAt" timestamptz,"updatedAt" timestamptz);
CREATE TABLE messages (id integer PRIMARY KEY,"conversationId" integer,"createdAt" timestamptz,"mediaType" text NOT NULL DEFAULT 'text',text text NOT NULL DEFAULT '',"mediaPath" text,"isRead" boolean NOT NULL DEFAULT false,"isDeleted" boolean NOT NULL DEFAULT false);
CREATE TABLE conversation_events (id integer PRIMARY KEY,"companyId" integer,"conversationId" integer,"executionId" integer,"nodeId" text,"eventType" text,content text,metadata jsonb,"createdAt" timestamptz);
INSERT INTO contacts VALUES (1,1),(2,1),(3,2);
INSERT INTO connections VALUES (1,1),(2,1),(3,2);
INSERT INTO agents VALUES (1,1,'Atual'),(2,1,'Outro operador');
INSERT INTO departments VALUES (1,1,'Suporte');
INSERT INTO conversations VALUES
(1,'00000000-0000-0000-0000-000000000001','open',1,1,1,1,'2026-10-01','2026-10-01'),
(2,'00000000-0000-0000-0000-000000000002','closed',1,1,2,1,'2026-09-01','2026-09-01'),
(3,'00000000-0000-0000-0000-000000000003','closed',1,2,2,1,'2026-09-01','2026-09-01'),
(4,'00000000-0000-0000-0000-000000000004','closed',2,1,2,1,'2026-09-01','2026-09-01'),
(5,'00000000-0000-0000-0000-000000000005','closed',3,3,null,null,'2026-09-01','2026-09-01'),
(6,'00000000-0000-0000-0000-000000000006','open',1,1,2,1,'2026-09-01','2026-09-01'),
(7,'00000000-0000-0000-0000-000000000007','closed',1,null,2,1,'2026-09-01','2026-09-01');
INSERT INTO messages (id,"conversationId","createdAt") SELECT n,CASE WHEN n<=75 THEN 2 ELSE 1 END,'2026-10-01'::timestamptz + (n/2)*interval '1 second' FROM generate_series(1,150)n;
INSERT INTO messages (id,"conversationId","createdAt") SELECT 1000+n,n,'2026-10-01' FROM generate_series(3,7)n;
INSERT INTO conversation_events SELECT n,CASE WHEN n=5 THEN 2 ELSE 1 END,n,null,null,'note','Nota', '{}','2026-10-01' FROM generate_series(1,7)n;
ALTER TABLE conversations ADD COLUMN protocol bigint NOT NULL DEFAULT 1;
ALTER TABLE conversations ADD COLUMN "companyId" integer;
UPDATE conversations cv SET "companyId"=ct."companyId" FROM contacts ct WHERE ct.id=cv."contactId";
`
	index, err := migrations.ReadFile("migrations/76__index_contact_connection_history.sql")
	if err != nil {
		t.Fatal(err)
	}
	mediaMigration, err := migrations.ReadFile("migrations/77__retain_message_media_source.sql")
	if err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(map[string]string{"schema": schema, "index": strings.Split(string(index), "-- +goose Down")[0], "mediaMigration": strings.Split(string(mediaMigration), "-- +goose Down")[0], "scope": contactHistoryConversationsSQL, "page": contactHistoryPageIDsSQL + `SELECT m.id FROM messages m JOIN page_ids page ON page.id=m.id ORDER BY m."createdAt" DESC,m.id DESC`, "events": contactHistoryEventsSQL})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "-e", `
const assert=require('node:assert/strict'),{PGlite}=require(process.env.ZUBLY_TEST_PGLITE);
const sql=JSON.parse(require('node:fs').readFileSync(0,'utf8'));
(async()=>{const db=new PGlite();await db.exec(sql.schema);await db.exec(sql.index);
 assert.deepEqual((await db.query(sql.scope,[1,1])).rows.map(r=>r.id),[2,1],'scope leaked another contact, connection or open appointment');
 assert.equal((await db.query(sql.scope,[1,2])).rows.length,0,'cross-company anchor accepted');
 assert.equal((await db.query(sql.scope,[7,1])).rows.length,0,'removed connections were merged');
 const all=[];let before=0;
 for(let page=0;page<4;page++){
  const rows=(await db.query(sql.page,[1,1,before,51])).rows;const shown=rows.slice(0,50);all.push(...shown.map(r=>r.id));if(rows.length<=50)break;before=shown.at(-1).id;
 }
 assert.equal(all.length,150);assert.equal(new Set(all).size,150,'equal timestamps caused cursor overlap');assert.ok(all.every(id=>id<=150));
 assert.equal((await db.query(sql.page,[1,1,1003,51])).rows.length,0,'foreign cursor entered the scope');
 const notes=(await db.query(sql.events,[1,1,null,null])).rows;assert.deepEqual(notes.map(r=>r.id),[1,2]);
 assert.equal((await db.query(sql.events,[1,1,null,'2026-10-01'])).rows.length,0,'note duplicated at upper boundary');
 await db.exec(`+"`"+`INSERT INTO messages (id,"conversationId","createdAt","mediaType",text) VALUES (2000,1,'2026-10-02','unknown','protocolMessage'),(2001,1,'2026-10-02','text','protocolMessage');`+"`"+`);
 await db.exec(sql.mediaMigration);
 const technical=(await db.query('SELECT "mediaType","isDeleted","mediaSource" FROM messages WHERE id=2000')).rows[0];assert.equal(technical.mediaType,'system');assert.equal(technical.isDeleted,true);assert.deepEqual(technical.mediaSource,{});
 assert.equal((await db.query('SELECT "isDeleted" FROM messages WHERE id=2001')).rows[0].isDeleted,false,'real user text was hidden');
 assert.ok(!(await db.query(sql.page,[1,1,0,51])).rows.some(row=>row.id===2000),'technical event leaked into history pagination');
 console.log('PASS: company/contact/connection isolation, closed-only history, removed connection handling, equal-timestamp pagination and notes');await db.close();
})().catch(error=>{console.error(error);process.exitCode=1});`)
	command.Stdin = strings.NewReader(string(input))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("contact history SQL regression: %v\n%s", err, output)
	}
	t.Log(string(output))
}
