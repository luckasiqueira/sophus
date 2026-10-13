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

func TestConversationExportPostgresScopeAndCompleteHistory(t *testing.T) {
	if os.Getenv("ZUBLY_TEST_PGLITE") == "" {
		t.Skip("set ZUBLY_TEST_PGLITE to run the isolated PostgreSQL regression check")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	schema := `
CREATE TABLE companies (id int PRIMARY KEY, name text, timezone text);
CREATE TABLE contacts (id int PRIMARY KEY, "companyId" int, name text, number text, email text, company text, "isGroup" boolean, jid text DEFAULT '', lid text DEFAULT '');
CREATE TABLE connections (id int PRIMARY KEY, "companyId" int, name text, number text);
CREATE TABLE agents (id int PRIMARY KEY, "companyId" int, name text);
CREATE TABLE departments (id int PRIMARY KEY, "companyId" int, name text);
CREATE TABLE conversations (id int PRIMARY KEY, url uuid, "contactId" int, "connectionId" int, "agentId" int, "departmentId" int, status text, "createdAt" timestamptz, "updatedAt" timestamptz);
CREATE TABLE messages (id int PRIMARY KEY, "conversationId" int, "messageId" text, text text, "quotedId" text, "mediaType" text, "mediaPath" text, "senderName" text, sender text, "isFromMe" boolean, "isDeleted" boolean, "createdAt" timestamptz);
CREATE TABLE conversation_events (id bigint PRIMARY KEY, "companyId" int, "conversationId" int, "eventType" text, content text, metadata jsonb, "createdAt" timestamptz);
INSERT INTO companies VALUES (1, 'Empresa A', 'UTC'), (2, 'Empresa B', 'UTC');
INSERT INTO contacts (id, "companyId", name, number, email, company, "isGroup") VALUES (10, 1, 'João', '5511999999999', '', '', false), (20, 1, 'Outro contato', '5511888888888', '', '', false), (30, 2, 'Outra empresa', '5511999999999', '', '', false);
UPDATE contacts SET jid = number || '@s.whatsapp.net', lid = '12345@lid' WHERE id = 10;
INSERT INTO connections VALUES (11, 1, 'Principal', ''), (12, 1, 'Segunda conexão', ''), (21, 2, 'Externa', '');
INSERT INTO conversations VALUES
(101, '00000000-0000-0000-0000-000000000101', 10, 11, NULL, NULL, 'open', '2026-01-01', '2026-01-01'),
(102, '00000000-0000-0000-0000-000000000102', 10, NULL, NULL, NULL, 'closed', '2026-01-02', '2026-01-02'),
(103, '00000000-0000-0000-0000-000000000103', 10, 12, NULL, NULL, 'closed', '2026-01-03', '2026-01-03'),
(201, '00000000-0000-0000-0000-000000000201', 20, 11, NULL, NULL, 'open', '2026-01-01', '2026-01-01'),
(301, '00000000-0000-0000-0000-000000000301', 30, 21, NULL, NULL, 'open', '2026-01-01', '2026-01-01');
INSERT INTO messages SELECT n, 101, n::text, 'Mensagem ' || n, '', 'text', '', '', '', false, false, '2026-01-01'::timestamptz + n * interval '1 second' FROM generate_series(1, 150) n;
INSERT INTO messages VALUES
(151, 101, '151', 'Não expor excluída', '', 'image', 'arquivo-removido.jpg', '', '', false, true, '2026-01-02'),
(201, 102, '201', 'Histórico arquivado', '', 'text', '', '', '', false, false, '2026-01-02'),
(202, 103, '202', 'Outra conexão', '', 'text', '', '', '', true, false, '2026-01-03'),
(203, 201, '203', 'Outro contato não incluir', '', 'text', '', '', '', false, false, '2026-01-02'),
(204, 301, '204', 'Outra empresa não incluir', '', 'text', '', '', '', false, false, '2026-01-02');
UPDATE messages SET sender = '12345@lid' WHERE id = 1;
UPDATE messages SET sender = '5511777777777@s.whatsapp.net' WHERE id = 2;
INSERT INTO conversation_events VALUES (1, 1, 101, 'note', 'Nota interna', '{"authorName":"Ana"}', '2026-01-01'), (2, 2, 101, 'note', 'Nota de outra empresa', '{}', '2026-01-01');
ALTER TABLE conversations ADD COLUMN protocol bigint NOT NULL DEFAULT 1;
ALTER TABLE conversations ADD COLUMN "companyId" integer;
UPDATE conversations cv SET "companyId"=ct."companyId" FROM contacts ct WHERE ct.id=cv."contactId";
`
	input, err := json.Marshal(map[string]string{"schema": schema, "anchor": conversationExportAnchorSQL, "list": conversationExportListSQL, "entries": conversationExportEntriesSQL})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "-e", `
const assert = require('node:assert/strict');
const { PGlite } = require(process.env.ZUBLY_TEST_PGLITE);
const sql = JSON.parse(require('node:fs').readFileSync(0, 'utf8'));
(async () => {
 const db = new PGlite(); await db.exec(sql.schema);
 assert.equal((await db.query(sql.anchor, ['00000000-0000-0000-0000-000000000101', 1])).rows.length, 1);
 assert.equal((await db.query(sql.anchor, ['00000000-0000-0000-0000-000000000101', 2])).rows.length, 0);
 const current = await db.query(sql.list, [1, 101, false, 10]); assert.deepEqual(current.rows.map(row => row.id), [101]);
 const all = await db.query(sql.list, [1, 101, true, 10]); assert.deepEqual(all.rows.map(row => row.id), [101, 102, 103]);
 const entries = await db.query(sql.entries, [1, 101, true, 10]);
 assert.equal(entries.rows.filter(row => row.kind === 'message').length, 153, 'export must include more than 100 messages');
 assert.equal(entries.rows.filter(row => row.kind === 'note').length, 1, 'notes must be scoped to company');
 const originalMessage = entries.rows.find(row => row.id === 1 && row.kind === 'message');
 assert.ok(Object.values(originalMessage).includes('João'), 'sender name must resolve from the contact');
 assert.ok(Object.values(originalMessage).includes('5511999999999'), 'LID must resolve to the contact phone number');
 assert.ok(Object.values(entries.rows.find(row => row.id === 2 && row.kind === 'message')).includes('5511777777777'), 'unknown contact number must derive from sender JID');
 assert.ok(entries.rows.some(row => row.content === 'Histórico arquivado')); assert.ok(entries.rows.some(row => row.content === 'Outra conexão'));
 assert.equal(entries.rows.find(row => row.id === 151).content, '', 'deleted text is redacted');
 assert.ok(!entries.rows.some(row => row.content.includes('não incluir')));
 assert.equal((await db.query(sql.entries, [2, 101, true, 10])).rows.length, 0);
 assert.ok((await db.query(sql.entries, [1, 101, false, 10])).rows.every(row => row.conversationId === 101));
 await db.close();
})().catch(error => { console.error(error); process.exitCode = 1; });
`)
	cmd.Stdin = strings.NewReader(string(input))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("PostgreSQL export regression check: %v\n%s", err, output)
	}
}
