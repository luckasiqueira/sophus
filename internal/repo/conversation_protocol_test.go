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

func TestConversationProtocolsPerCompanyPostgres(t *testing.T) {
	if os.Getenv("ZUBLY_TEST_PGLITE") == "" {
		t.Skip("set ZUBLY_TEST_PGLITE for isolated PostgreSQL protocol tests")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	migration, err := migrations.ReadFile("migrations/81__add_company_conversation_protocols.sql")
	if err != nil {
		t.Fatal(err)
	}
	schema := `CREATE TABLE companies(id integer PRIMARY KEY);
CREATE TABLE contacts(id integer PRIMARY KEY,"companyId" integer REFERENCES companies(id),name text,number text,email text,jid text,lid text,"isGroup" boolean,"isBlocked" boolean);
CREATE TABLE connections(id integer PRIMARY KEY,"companyId" integer REFERENCES companies(id));
CREATE TABLE agents(id integer PRIMARY KEY,name text);
CREATE TABLE departments(id integer PRIMARY KEY,name text,color text);
CREATE TABLE agent_departments("agentId" integer,"departmentId" integer);
CREATE TABLE conversations(id serial PRIMARY KEY,status text,"contactId" integer REFERENCES contacts(id) ON DELETE CASCADE,"connectionId" integer REFERENCES connections(id) ON DELETE SET NULL,"agentId" integer,"departmentId" integer,url uuid UNIQUE,"createdAt" timestamptz,"updatedAt" timestamptz);
INSERT INTO companies VALUES(1),(2),(3);
INSERT INTO contacts VALUES(1,1,'Empresa um','5511111111111','','','',false,false),(2,2,'Empresa dois','5522222222222','','','',false,false),(3,3,'Empresa três','5533333333333','','','',false,false);
INSERT INTO connections VALUES(1,1),(2,2),(3,3);
INSERT INTO agents VALUES(7,'Agente um'),(8,'Agente dois');
INSERT INTO conversations(id,status,"contactId","connectionId","agentId",url,"createdAt","updatedAt") VALUES
(12,'open',1,1,7,'00000000-0000-0000-0000-000000000012','2026-01-02','2026-01-02'),
(77,'closed',1,1,7,'00000000-0000-0000-0000-000000000077','2026-01-01','2026-01-01'),
(9,'closed',1,null,7,'00000000-0000-0000-0000-000000000009','2026-01-03','2026-01-03'),
(5,'open',2,2,8,'00000000-0000-0000-0000-000000000005','2026-01-01','2026-01-01');
SELECT setval(pg_get_serial_sequence('conversations','id'),77);`
	input, err := json.Marshal(map[string]string{"schema": schema, "migration": strings.Split(string(migration), "-- +goose Down")[0], "lookup": visibleConversationQuery(`cv.protocol = $1 AND cv."companyId" = $2`)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "-e", `
const assert=require('node:assert/strict'),{PGlite}=require(process.env.ZUBLY_TEST_PGLITE);
const sql=JSON.parse(require('node:fs').readFileSync(0,'utf8'));
(async()=>{
 const db=new PGlite();await db.exec(sql.schema);await db.exec('BEGIN;'+sql.migration+'COMMIT;');
 const old=(await db.query('SELECT id,"companyId",protocol FROM conversations ORDER BY id')).rows;
 assert.deepEqual(old.map(row=>[row.id,row.companyId,Number(row.protocol)]),[[5,2,1],[9,1,3],[12,1,2],[77,1,1]],'backfill must follow creation order separately per company, including archives');
 const companyOne=(await db.query(sql.lookup,[1,1,true,7])).rows,companyTwo=(await db.query(sql.lookup,[1,2,true,8])).rows;
 assert.equal(companyOne[0].url,'00000000-0000-0000-0000-000000000077');assert.equal(companyTwo[0].url,'00000000-0000-0000-0000-000000000005');assert.notEqual(companyOne[0].url,companyTwo[0].url,'same protocol across companies must open different global URLs');
 assert.equal((await db.query(sql.lookup,[1,3,true,7])).rows.length,0,'protocol leaked to an unrelated company');
 assert.equal((await db.query(sql.lookup,[1,2,false,7])).rows.length,0,'protocol search bypassed conversation visibility');
 await db.exec('UPDATE company_conversation_protocols SET "lastProtocol"=1001 WHERE "companyId"=1;UPDATE company_conversation_protocols SET "lastProtocol"=639 WHERE "companyId"=2;');
 let uuid=100;
 async function insert(contact,connection,extra=''){
  const url='00000000-0000-0000-0000-'+String(uuid++).padStart(12,'0');
  return (await db.query('INSERT INTO conversations(status,"contactId","connectionId",url,"createdAt","updatedAt"'+extra+') VALUES(\'open\',$1,$2,$3,now(),now()'+(extra?',999':'')+') RETURNING id,"companyId",protocol',[contact,connection,url])).rows[0];
 }
 assert.equal(Number((await insert(1,1)).protocol),1002);assert.equal(Number((await insert(2,2)).protocol),640);assert.equal(Number((await insert(3,3)).protocol),1);
 await db.exec('BEGIN;');assert.equal(Number((await insert(1,1)).protocol),1003);await db.exec('ROLLBACK;');
 const afterRollback=await insert(1,1);assert.equal(Number(afterRollback.protocol),1003,'rollback must not commit a counter increment');
 await db.query('DELETE FROM conversations WHERE id=$1',[afterRollback.id]);
 await db.exec('DELETE FROM connections WHERE id=1;INSERT INTO connections VALUES(1,1);');
 assert.equal(Number((await db.query(sql.lookup,[1,1,true,7])).rows[0].protocol),1,'archived protocol changed when connection was deleted');
 const concurrent=await Promise.all(Array.from({length:20},()=>insert(1,1)));
 const numbers=concurrent.map(row=>Number(row.protocol)).sort((a,b)=>a-b);
 assert.deepEqual(numbers,Array.from({length:20},(_,i)=>1004+i));assert.equal(new Set(numbers).size,20,'concurrent inserts reused a protocol');
 assert.equal(Number((await insert(2,2)).protocol),641,'company one changed company two counter');
 await assert.rejects(()=>db.query('UPDATE conversations SET protocol=999 WHERE id=12'));
 await assert.rejects(()=>db.query('UPDATE conversations SET "contactId"=2 WHERE id=12'));
 await assert.rejects(()=>insert(1,2));await assert.rejects(()=>insert(1,1,',"companyId"'));await assert.rejects(()=>insert(1,1,',protocol'));
 assert.equal(Number((await db.query('SELECT "lastProtocol" FROM company_conversation_protocols WHERE "companyId"=1')).rows[0].lastProtocol),1023,'failed inserts consumed a protocol');
 console.log('PASS: tenant sequences, chronological backfill, archive retention, concurrent submissions, rollback, no reuse, immutable protocols and authorized lookup');await db.close();
})().catch(error=>{console.error(error);process.exitCode=1;});
`)
	command.Stdin = strings.NewReader(string(input))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("protocol PostgreSQL regression: %v\n%s", err, output)
	} else {
		t.Log(string(output))
	}
}
