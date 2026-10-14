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

func TestStressLoadStatementsPostgres(t *testing.T) {
	if os.Getenv("ZUBLY_TEST_PGLITE") == "" {
		t.Skip("set ZUBLY_TEST_PGLITE for isolated PostgreSQL stress load tests")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	schema := `
CREATE TABLE companies (id integer PRIMARY KEY);
CREATE TABLE agents (id integer PRIMARY KEY,"companyId" integer,name text);
CREATE TABLE connections (id integer PRIMARY KEY,"companyId" integer,status text);
CREATE TABLE contacts (id serial PRIMARY KEY,name text NOT NULL,number text NOT NULL,email text NOT NULL DEFAULT '',company text NOT NULL DEFAULT '',
 "companyId" integer,jid text,lid text,"isGroup" boolean,"isBlocked" boolean,"customFields" jsonb NOT NULL DEFAULT '{}');
CREATE TABLE conversations (id serial PRIMARY KEY,status text NOT NULL,"contactId" integer NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
 "connectionId" integer,"agentId" integer,"departmentId" integer,url uuid NOT NULL,"createdAt" timestamptz NOT NULL,"updatedAt" timestamptz NOT NULL);
CREATE TABLE messages (id serial PRIMARY KEY,"messageId" text NOT NULL,text text NOT NULL,"conversationId" integer NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
 "quotedId" text NOT NULL,"mediaType" text NOT NULL,"mediaPath" text,"createdAt" timestamptz NOT NULL,"updatedAt" timestamptz NOT NULL,
 "isFromMe" boolean NOT NULL,"isGroup" boolean NOT NULL,"isRead" boolean NOT NULL,"isDeleted" boolean NOT NULL,sender text NOT NULL DEFAULT '',"displayData" jsonb NOT NULL DEFAULT '{}');
INSERT INTO companies VALUES (1),(2);
INSERT INTO agents VALUES (10,1,'Ana'),(20,2,'Bruno');
INSERT INTO connections VALUES (5,1,'disconnected'),(6,1,'connected'),(7,2,'connected');
INSERT INTO contacts (name,number,"companyId") VALUES ('Cliente real','5511999990000',1);
`
	protocols, err := migrations.ReadFile("migrations/81__add_company_conversation_protocols.sql")
	if err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(map[string]any{
		"schema": schema, "protocols": strings.Split(string(protocols), "-- +goose Down")[0],
		"contact": stressContactInsertSQL, "conversation": stressConversationInsertSQL, "messages": stressMessagesInsertSQL,
		"delete": stressMessagesDeleteSQL, "list": stressContactsListSQL, "texts": []string(stressMessageTexts),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "-e", `
const assert=require('node:assert/strict'),{PGlite}=require(process.env.ZUBLY_TEST_PGLITE);
const sql=JSON.parse(require('node:fs').readFileSync(0,'utf8'));
(async()=>{const db=new PGlite();await db.exec(sql.schema);await db.exec(sql.protocols.replace(/-- \+goose (Up|StatementBegin|StatementEnd)/g,''));
 const contact=(await db.query(sql.contact,['Contato fictício · Estresse 1234','0001234561234',1])).rows[0].id;
 const conversation=(await db.query(sql.conversation,[contact,6,10,'00000000-0000-0000-0000-0000000000aa'])).rows[0];
 assert.equal(Number(conversation.protocol),1,'protocol was not assigned by the company trigger');
 const start=new Date('2026-10-01T00:00:00Z');
 await db.query(sql.messages,[conversation.id,sql.texts,start,'30 seconds',1,7]);
 await db.query(sql.messages,[conversation.id,sql.texts,start,'30 seconds',8,12]);
 const rows=(await db.query('SELECT "messageId",text,"isFromMe","createdAt","isRead","mediaType" FROM messages ORDER BY "createdAt",id')).rows;
 assert.equal(rows.length,12);assert.equal(new Set(rows.map(r=>r.messageId)).size,12,'message ids repeated between batches');
 assert.equal(rows[0].messageId,'stress-'+conversation.id+'-1');assert.equal(rows.at(-1).messageId,'stress-'+conversation.id+'-12');
 assert.ok(rows.every(r=>r.text&&sql.texts.includes(r.text)),'text outside the sample set');
 assert.deepEqual(rows.map(r=>r.isFromMe),rows.map((_,i)=>(i+1)%3===0));
 assert.ok(rows.every(r=>r.isRead&&r.mediaType==='text'));
 assert.equal(rows.at(-1).createdAt-rows[0].createdAt,11*30000,'messages are not 30 seconds apart');
 await assert.rejects(db.query(sql.conversation,[contact,7,20,'00000000-0000-0000-0000-0000000000bb']),/another company/,'cross-company connection accepted');
 const list=(await db.query(sql.list,[1],{rowMode:'array'})).rows;
 assert.equal(list.length,1,'real contacts or other companies leaked into the stress list');
 const [id,company,name,number,url,protocol,agent,count]=list[0];
 assert.deepEqual([id,company,name,number,url,Number(protocol),agent,Number(count)],[contact,1,'Contato fictício · Estresse 1234','0001234561234','00000000-0000-0000-0000-0000000000aa',1,'Ana',12]);
 assert.equal((await db.query(sql.list,[2])).rows.length,0);
 assert.equal((await db.query(sql.delete,['{'+conversation.id+'}',5])).affectedRows,5);
 assert.equal((await db.query(sql.delete,['{'+conversation.id+'}',5])).affectedRows,5);
 assert.equal((await db.query(sql.delete,['{'+conversation.id+'}',5])).affectedRows,2);
 assert.equal((await db.query(sql.delete,['{'+conversation.id+'}',5])).affectedRows,0);
 assert.equal((await db.query('SELECT COUNT(*)::int AS c FROM contacts WHERE number=$1',['5511999990000'])).rows[0].c,1,'real contact touched');
})().catch(error=>{console.error(error);process.exit(1)});`)
	command.Stdin = strings.NewReader(string(input))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}
