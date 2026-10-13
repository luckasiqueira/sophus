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

func TestSupportSessionsIsolationAndRevocationPostgres(t *testing.T) {
	if os.Getenv("ZUBLY_TEST_PGLITE") == "" {
		t.Skip("set ZUBLY_TEST_PGLITE for support session SQL checks")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	migration, err := migrations.ReadFile("migrations/83__create_platform_support_sessions.sql")
	if err != nil {
		t.Fatal(err)
	}
	schema := `CREATE TABLE companies(id integer PRIMARY KEY,name text,"isActive" boolean);
CREATE TABLE platform_admins(id integer PRIMARY KEY,"isActive" boolean,"sessionVersion" integer);
CREATE TABLE agents(id integer PRIMARY KEY,"companyId" integer,preferences jsonb,"sessionVersion" integer DEFAULT 0,"createdAt" timestamptz DEFAULT now(),"updatedAt" timestamptz DEFAULT now());
INSERT INTO companies VALUES(1,'Empresa um',false),(2,'Empresa dois',true);
INSERT INTO platform_admins VALUES(50,true,2),(51,true,2);
INSERT INTO agents(id,"companyId",preferences) VALUES(7,1,'{"customer":"untouched"}'),(8,2,'{"customer":"untouched"}');`
	input, err := json.Marshal(map[string]string{"schema": schema, "migration": strings.Split(string(migration), "-- +goose Down")[0], "lookup": platformSupportSessionSQL, "preferences": updatePlatformSupportPreferencesSQL})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "-e", `
const assert=require('node:assert/strict'),{PGlite}=require(process.env.ZUBLY_TEST_PGLITE);
const sql=JSON.parse(require('node:fs').readFileSync(0,'utf8'));
(async()=>{const db=new PGlite();await db.exec(sql.schema);await db.exec(sql.migration);
 const id='00000000-0000-0000-0000-000000000001';
 await db.query('INSERT INTO platform_support_sessions(id,"tokenHash","adminId","companyId","agentId","sessionVersion",preferences,"expiresAt") VALUES($1,$2,50,1,7,2,$3::jsonb,now()+interval \'1 hour\')',[id,'hash-one',JSON.stringify({scrollAfterSend:true})]);
 assert.equal((await db.query(sql.lookup,['hash-one',50,2])).rows[0].name,'Empresa um','support must work even for inactive companies');
 assert.equal((await db.query(sql.lookup,['hash-one',51,2])).rows.length,0,'another super admin reused the lease');
 assert.equal((await db.query(sql.lookup,['hash-one',50,3])).rows.length,0,'session version binding was bypassed');
 assert.equal((await db.query(sql.lookup,['wrong-token',50,2])).rows.length,0);
 assert.equal((await db.query(sql.preferences,[JSON.stringify({scrollAfterSend:false}),id,50,2,2])).affectedRows,0,'preferences escaped the selected tenant');
 assert.equal((await db.query(sql.preferences,[JSON.stringify({scrollAfterSend:false}),id,50,1,2])).affectedRows,1);
 assert.deepEqual((await db.query('SELECT preferences FROM agents WHERE id=7')).rows[0].preferences,{customer:'untouched'},'support changed customer personal preferences');
 await db.exec('UPDATE platform_admins SET "isActive"=false WHERE id=50;');assert.equal((await db.query(sql.lookup,['hash-one',50,2])).rows.length,0);
 await db.exec('UPDATE platform_admins SET "isActive"=true,"sessionVersion"=3 WHERE id=50;');assert.equal((await db.query(sql.lookup,['hash-one',50,2])).rows.length,0);
 await db.exec('UPDATE platform_admins SET "sessionVersion"=2 WHERE id=50;UPDATE platform_support_sessions SET "expiresAt"=now()-interval \'1 second\';');assert.equal((await db.query(sql.lookup,['hash-one',50,2])).rows.length,0);
 await db.exec('UPDATE platform_support_sessions SET "expiresAt"=now()+interval \'1 hour\',"revokedAt"=now();');assert.equal((await db.query(sql.lookup,['hash-one',50,2])).rows.length,0);
 assert.equal((await db.query(sql.preferences,['{}',id,50,1,2])).affectedRows,0,'revoked session changed preferences');
 await db.exec('UPDATE platform_support_sessions SET "revokedAt"=null,"companyId"=2;');assert.equal((await db.query(sql.lookup,['hash-one',50,2])).rows.length,0,'mismatched company and backing identity accepted');
 console.log('PASS: support tenant/owner binding, inactive companies, private preferences, disabled admins, version revocation, expiry and revoked leases');await db.close();
})().catch(error=>{console.error(error);process.exitCode=1;});`)
	cmd.Stdin = strings.NewReader(string(input))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("support SQL checks: %v\n%s", err, output)
	} else {
		t.Log(string(output))
	}
}
