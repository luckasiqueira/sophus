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

func TestStressRevertAndIsolationPostgres(t *testing.T) {
	if os.Getenv("ZUBLY_TEST_PGLITE") == "" {
		t.Skip("set ZUBLY_TEST_PGLITE for isolated PostgreSQL stress revert tests")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	// Company 1 mixes real records with simulated ones: contact 103 is a real
	// contact whose number happens to start with 000, and contact 104 talks
	// through both a real and a simulated instance. Neither may be deleted.
	schema := `
CREATE TABLE companies (id integer PRIMARY KEY);
CREATE TABLE agents (id integer PRIMARY KEY,"companyId" integer,name text,"isActive" boolean NOT NULL DEFAULT true);
CREATE TABLE connections (id integer PRIMARY KEY,"companyId" integer,status text);
CREATE TABLE contacts (id integer PRIMARY KEY,name text NOT NULL DEFAULT '',number text NOT NULL,"companyId" integer,"customFields" jsonb NOT NULL DEFAULT '{}');
CREATE TABLE conversations (id integer PRIMARY KEY,status text NOT NULL,"contactId" integer NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
 "connectionId" integer REFERENCES connections(id) ON DELETE SET NULL,"agentId" integer,"companyId" integer,"updatedAt" timestamptz NOT NULL DEFAULT now());
CREATE TABLE messages (id serial PRIMARY KEY,"conversationId" integer NOT NULL REFERENCES conversations(id) ON DELETE CASCADE);
INSERT INTO companies VALUES (1),(2);
INSERT INTO agents (id,"companyId",name) VALUES (10,1,'Real'),(11,1,'Agente simulado 001'),(21,2,'Agente simulado 001');
INSERT INTO connections VALUES (5,1,'connected'),(6,1,'connected'),(7,2,'connected');
INSERT INTO contacts (id,number,"companyId","customFields") VALUES
 (100,'5511999990000',1,'{}'),(101,'0009000000001',1,'{}'),(102,'0001234560000',1,'{"stressTest":true}'),
 (103,'000123',1,'{}'),(104,'0009000000002',1,'{}'),(201,'0009000000003',2,'{}');
INSERT INTO conversations (id,status,"contactId","connectionId","agentId","companyId") VALUES
 (1,'open',100,5,10,1),(2,'open',100,5,10,1),(3,'pending',100,5,NULL,1),(4,'pending',101,6,NULL,1),
 (5,'open',102,5,10,1),(6,'open',103,5,10,1),(7,'open',104,5,10,1),(8,'open',104,6,NULL,1),(9,'open',201,7,NULL,2);
INSERT INTO messages ("conversationId") SELECT c FROM generate_series(1,9) c, generate_series(1,3);
`
	resources, err := migrations.ReadFile("migrations/90__create_stress_test_resources.sql")
	if err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(map[string]string{
		"schema": schema, "resources": strings.Split(string(resources), "-- +goose Down")[0],
		"assign": autoAssignConversationSQL(`count(workload.id), agent.id`), "summary": stressSummarySQL,
		"contacts": stressRevertContactsSQL, "connectionConversations": stressRevertConnectionConversationsSQL, "connections": stressRevertConnectionsSQL,
		"targets": `SELECT cv.url::text FROM conversations cv WHERE false`,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "-e", `
const assert=require('node:assert/strict'),{PGlite}=require(process.env.ZUBLY_TEST_PGLITE);
const sql=JSON.parse(require('node:fs').readFileSync(0,'utf8'));
(async()=>{const db=new PGlite();await db.exec(sql.schema);await db.exec(sql.resources.replace(/-- \+goose Up/g,''));
 assert.deepEqual((await db.query('SELECT "resourceType","resourceId" FROM stress_test_resources')).rows,[{resourceType:'contact',resourceId:102}],'marked contact was not backfilled');
 await db.exec("INSERT INTO stress_test_resources (\"resourceType\",\"resourceId\",\"companyId\") VALUES ('agent',11,1),('connection',6,1),('agent',21,2),('connection',7,2)");
 const owner=async id=>(await db.query('SELECT "agentId" FROM conversations WHERE id=$1',[id])).rows[0].agentId;
 await db.query(sql.assign,[1,3]);assert.equal(await owner(3),10,'real conversation was given to an idle simulated agent');
 await db.query(sql.assign,[1,4]);assert.equal(await owner(4),11,'simulated conversation went to a real agent');
 await db.exec('UPDATE agents SET "isActive"=false WHERE id=11');await db.query(sql.assign,[1,8]);assert.equal(await owner(8),null,'simulated conversation fell back to a real agent');
 const summary=async company=>(await db.query(sql.summary,[company],{rowMode:'array'})).rows[0].map(Number);
 assert.deepEqual(await summary(1),[1,1,1,1,3,9],'company summary: contacts, simulated customers, agents, instances, conversations, messages');
 assert.deepEqual(await summary(2),[0,1,1,1,1,3]);
 assert.deepEqual(await summary(0),[1,2,2,2,4,12]);
 await db.query(sql.contacts,[1]);await db.query(sql.connectionConversations,[1]);await db.query(sql.connections,[1]);
 assert.deepEqual((await db.query('SELECT id FROM contacts ORDER BY id')).rows.map(r=>r.id),[100,103,104,201],'revert deleted a real contact or another company');
 assert.deepEqual((await db.query('SELECT id FROM conversations ORDER BY id')).rows.map(r=>r.id),[1,2,3,6,7,9]);
 assert.deepEqual((await db.query('SELECT id FROM connections ORDER BY id')).rows.map(r=>r.id),[5,7]);
 assert.equal((await db.query('SELECT COUNT(*)::int AS c FROM messages')).rows[0].c,18);
 await db.query(sql.contacts,[0]);await db.query(sql.connectionConversations,[0]);await db.query(sql.connections,[0]);
 assert.deepEqual((await db.query('SELECT id FROM contacts ORDER BY id')).rows.map(r=>r.id),[100,103,104],'global revert missed company 2');
 assert.deepEqual((await db.query('SELECT id FROM connections ORDER BY id')).rows.map(r=>r.id),[5]);
})().catch(error=>{console.error(error);process.exit(1)});`)
	command.Stdin = strings.NewReader(string(input))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}

func TestStressAPITokenFitsConnectionColumn(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		token := stressAPIToken()
		// connections."apiToken" is character varying(32).
		if len(token) != 32 || !strings.HasPrefix(token, "stress") || seen[token] {
			t.Fatalf("invalid simulated API token %q (len %d)", token, len(token))
		}
		seen[token] = true
	}
}
