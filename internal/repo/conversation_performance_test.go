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

func TestConversationSidebarIndexAndHistoryCursorPostgres(t *testing.T) {
	if os.Getenv("ZUBLY_TEST_PGLITE") == "" {
		t.Skip("set ZUBLY_TEST_PGLITE for isolated PostgreSQL plan and cursor tests")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	schema := `
CREATE TABLE contacts (id integer PRIMARY KEY, name text, number text, email text, company text, "companyId" integer, jid text, lid text, "isGroup" boolean, "isBlocked" boolean);
CREATE TABLE agents (id integer PRIMARY KEY, name text);
CREATE TABLE departments (id integer PRIMARY KEY, name text, color text);
CREATE TABLE agent_departments ("agentId" integer, "departmentId" integer);
CREATE TABLE conversations (id integer PRIMARY KEY, status text, "contactId" integer, "connectionId" integer, "agentId" integer, "departmentId" integer, url uuid, "createdAt" timestamptz, "updatedAt" timestamptz);
CREATE TABLE messages (id integer PRIMARY KEY, "conversationId" integer, text text, "mediaType" text, "createdAt" timestamptz, "isDeleted" boolean, "isRead" boolean, "isFromMe" boolean);
INSERT INTO agents VALUES (7, 'Operador');
INSERT INTO contacts SELECT n, 'Contato', '5511999999999', '', '', 1, '', '', false, false FROM generate_series(1,80) n;
INSERT INTO conversations SELECT n, 'open', n, 1, 7, NULL, ('00000000-0000-0000-0000-' || lpad(n::text,12,'0'))::uuid, '2026-10-01', '2026-10-01' FROM generate_series(1,80) n;
ALTER TABLE conversations ADD COLUMN protocol bigint NOT NULL DEFAULT 1, ADD COLUMN "companyId" integer NOT NULL DEFAULT 1;
INSERT INTO messages SELECT n, n % 80 + 1, 'Mensagem', 'text', '2026-10-01'::timestamptz + n * interval '1 second', false, true, n % 2 = 0 FROM generate_series(1,16000) n;
CREATE INDEX messages_latest_contact_idx ON messages ("conversationId", "createdAt" DESC, id DESC) WHERE "isFromMe" = false AND "isDeleted" = false;
CREATE INDEX messages_unread_conversation_idx ON messages ("conversationId") WHERE "isFromMe" = false AND "isRead" = false AND "isDeleted" = false;
ANALYZE;
`
	input, err := json.Marshal(map[string]string{"schema": schema, "list": conversationListQuery("active"), "page": conversationMessagePageIDsSQL})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "-e", `
const assert=require('node:assert/strict'); const {PGlite}=require(process.env.ZUBLY_TEST_PGLITE);
const sql=JSON.parse(require('node:fs').readFileSync(0,'utf8'));
(async()=>{
 const db=new PGlite(); await db.exec(sql.schema);
 const explain='EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) '+sql.list;
 const before=(await db.query(explain,[1,true,7,false])).rows[0]['QUERY PLAN'][0];
 await db.exec('CREATE INDEX messages_latest_conversation_idx ON messages ("conversationId","createdAt" DESC,id DESC) INCLUDE ("mediaType") WHERE "isDeleted" = false; CREATE INDEX messages_conversation_history_idx ON messages ("conversationId","createdAt" DESC,id DESC); ANALYZE messages;');
 const after=(await db.query(explain,[1,true,7,false])).rows[0]['QUERY PLAN'][0];
 assert.equal((await db.query(sql.list,[1,true,7,false])).rows.length,80);
 assert.equal((await db.query(sql.list,[2,true,7,false])).rows.length,0,'another tenant received conversation data');
 function indices(node){return [node['Index Name'],...(node.Plans||[]).flatMap(indices)].filter(Boolean)}
 assert.ok(indices(after.Plan).some(name=>name==='messages_latest_conversation_idx'||name==='messages_conversation_history_idx'),'new index not used by sidebar query');
 const page=sql.page+'SELECT m.id,$2::integer AS company FROM messages m JOIN page_ids p ON p.id=m.id ORDER BY m."createdAt" DESC,m.id DESC';
 const first=(await db.query(page,[1,1,0,51])).rows; assert.equal(first.length,51); const shown=first.slice(0,50);
 const next=(await db.query(page,[1,1,shown.at(-1).id,51])).rows;
 assert.ok(next.length>0); assert.ok(next.every(row=>!shown.some(previous=>previous.id===row.id)),'history pages overlap');
 assert.equal(next[0].id,first[50].id,'sentinel message was skipped');
 console.log('EXPLAIN ANALYZE synthetic: before='+before['Execution Time']+'ms after='+after['Execution Time']+'ms indexes='+indices(after.Plan).join(','));
 await db.close();
})().catch(error=>{console.error(error);process.exitCode=1});
`)
	command.Stdin = strings.NewReader(string(input))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("PostgreSQL performance regression: %v\n%s", err, output)
	}
	t.Log(string(output))
}
