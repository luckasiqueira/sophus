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

func TestMonitoringActivityQueriesPostgres(t *testing.T) {
	if os.Getenv("ZUBLY_TEST_PGLITE") == "" {
		t.Skip("set ZUBLY_TEST_PGLITE for isolated PostgreSQL monitoring tests")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	schema := `
CREATE TABLE companies (id integer PRIMARY KEY,name text NOT NULL);
CREATE TABLE agents (id integer PRIMARY KEY,"companyId" integer);
CREATE TABLE connections (id integer PRIMARY KEY,"companyId" integer,status text);
CREATE TABLE stress_test_resources ("resourceType" text,"resourceId" integer,"companyId" integer,PRIMARY KEY ("resourceType","resourceId"));
INSERT INTO companies VALUES (1,'Alfa'),(2,'Beta'),(3,'Gama'),(4,'Delta');
INSERT INTO agents VALUES (10,1),(11,1),(12,1),(20,2),(21,2),(30,3);
INSERT INTO connections VALUES (5,1,'connected'),(6,1,'connected'),(7,2,'disconnected'),(8,3,'connected'),(9,4,'connecting');
INSERT INTO stress_test_resources VALUES ('agent',12,1),('agent',21,2),('connection',6,1);
`
	input, err := json.Marshal(map[string]string{"schema": schema, "online": countOnlineAgentsSQL, "instances": countConnectedInstancesSQL, "companies": companyActivitySQL})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "-e", `
const assert=require('node:assert/strict'),{PGlite}=require(process.env.ZUBLY_TEST_PGLITE);
const sql=JSON.parse(require('node:fs').readFileSync(0,'utf8'));
(async()=>{const db=new PGlite();await db.exec(sql.schema);
 const row=async(query,params)=>(await db.query(query,params||[],{rowMode:'array'})).rows[0].map(Number);
 // Agents 10, 11, 20 are real and 12, 21 simulated; 999 no longer exists.
 assert.deepEqual(await row(sql.online,['{10,11,12,20,21,999}']),[3,2],'online agents: real, simulated');
 assert.deepEqual(await row(sql.online,['{}']),[0,0]);
 assert.deepEqual(await row(sql.instances),[2,1],'connected instances: real (5, 8), simulated (6)');
 const companies=(await db.query(sql.companies,['{10,11,12,20,21}'],{rowMode:'array'})).rows.map(r=>r.map(v=>typeof v==='string'?v:Number(v)));
 assert.deepEqual(companies,[[1,'Alfa',2,1,1,1],[2,'Beta',1,1,0,0],[3,'Gama',0,0,1,0]],'activity by company, busiest first, idle companies left out');
})().catch(error=>{console.error(error);process.exit(1)});`)
	command.Stdin = strings.NewReader(string(input))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}
