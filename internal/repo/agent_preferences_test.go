package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/joho/godotenv"
)

func TestAgentPreferenceMigrationPreservesSignatures(t *testing.T) {
	if os.Getenv("ZUBLY_TEST_PGLITE") == "" {
		t.Skip("set ZUBLY_TEST_PGLITE for migration tests")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js required")
	}
	migration, err := migrations.ReadFile("migrations/80__add_agent_preferences.sql")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "-e", `
const assert=require('node:assert/strict'),{PGlite}=require(process.env.ZUBLY_TEST_PGLITE),migration=require('node:fs').readFileSync(0,'utf8');
(async()=>{const db=new PGlite();try {
 await db.exec(`+"`"+`CREATE TABLE companies(id integer PRIMARY KEY,"operationSettings" jsonb);
 CREATE TABLE agents(id integer PRIMARY KEY,"companyId" integer);
 INSERT INTO companies VALUES(3,'{"messageSignatureEnabled":false,"messageSignatureTemplate":"*{agentName}*:"}'),(4,'{"messageSignatureEnabled":true,"messageSignatureTemplate":""}');
 INSERT INTO agents VALUES(7,3),(8,3),(9,4);`+"`"+`);
 await db.exec(migration.split('-- +goose Down')[0]);
 const {rows}=await db.query('SELECT id,preferences FROM agents ORDER BY id');
 assert.equal(rows[0].preferences.messageSignatureEnabled,false);assert.equal(rows[0].preferences.messageSignatureTemplate,'*{agentName}*:');assert.deepEqual(rows[0].preferences,rows[1].preferences);
 assert.equal(rows[2].preferences.messageSignatureEnabled,true);assert.equal(rows[2].preferences.messageSignatureTemplate,'{agentName}');
 await db.exec('INSERT INTO agents VALUES(10,3,DEFAULT)');const added=await db.query('SELECT preferences FROM agents WHERE id=10');assert.equal(added.rows[0].preferences.scrollAfterSend,true);assert.equal(added.rows[0].preferences.notifications.sound,true);
 await db.exec(migration.split('-- +goose Down')[1]);const columns=await db.query("SELECT column_name FROM information_schema.columns WHERE table_name='agents' AND column_name='preferences'");assert.equal(columns.rows.length,0);
 console.log('PASS: signature migration, defaults for new agents and rollback');
}finally{await db.close();}})().catch(error=>{console.error(error);process.exitCode=1});`)
	command.Stdin = strings.NewReader(string(migration))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("preference migration failed: %v\n%s", err, output)
	} else {
		t.Log(string(output))
	}
}

func TestBoldSignatureMigrationKeepsCustomTemplates(t *testing.T) {
	if os.Getenv("ZUBLY_TEST_PGLITE") == "" {
		t.Skip("set ZUBLY_TEST_PGLITE for migration tests")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js required")
	}
	migration, err := migrations.ReadFile("migrations/84__bold_default_message_signature.sql")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "-e", `
const assert=require('node:assert/strict'),{PGlite}=require(process.env.ZUBLY_TEST_PGLITE),migration=require('node:fs').readFileSync(0,'utf8');
(async()=>{const db=new PGlite();try {
 await db.exec(`+"`"+`CREATE TABLE companies(id integer PRIMARY KEY,"operationSettings" jsonb NOT NULL DEFAULT '{}');
 CREATE TABLE agents(id integer PRIMARY KEY,preferences jsonb NOT NULL DEFAULT '{}');
 CREATE TABLE platform_support_sessions(id integer PRIMARY KEY,preferences jsonb NOT NULL);
 INSERT INTO companies VALUES(1,'{"messageSignatureTemplate":"{agentName}","acceptCalls":true}'),(2,'{"messageSignatureTemplate":"Att, {agentName}"}');
 INSERT INTO agents VALUES(1,'{"messageSignatureTemplate":"{agentName}","scrollAfterSend":false}'),(2,'{"messageSignatureTemplate":"_{agentName}_"}');
 INSERT INTO platform_support_sessions VALUES(1,'{"messageSignatureTemplate":"{agentName}"}');`+"`"+`);
 const [up,down]=migration.split('-- +goose Down');
 await db.exec(up);
 const template=async(table,column,id)=>(await db.query('SELECT '+column+'->>\'messageSignatureTemplate\' AS t FROM '+table+' WHERE id=$1',[id])).rows[0].t;
 assert.equal(await template('companies','"operationSettings"',1),'*{agentName}*');assert.equal(await template('companies','"operationSettings"',2),'Att, {agentName}');
 assert.equal(await template('agents','preferences',1),'*{agentName}*');assert.equal(await template('agents','preferences',2),'_{agentName}_');
 assert.equal(await template('platform_support_sessions','preferences',1),'*{agentName}*');
 const kept=(await db.query('SELECT "operationSettings" s FROM companies WHERE id=1')).rows[0].s;assert.equal(kept.acceptCalls,true);
 await db.exec('INSERT INTO companies(id) VALUES(3);INSERT INTO agents(id) VALUES(3)');
 assert.equal(await template('companies','"operationSettings"',3),'*{agentName}*');assert.equal(await template('agents','preferences',3),'*{agentName}*');
 const created=(await db.query('SELECT "operationSettings" s FROM companies WHERE id=3')).rows[0].s;assert.equal(created.maxAttachmentMB,50);
 await db.exec(down);
 assert.equal(await template('companies','"operationSettings"',1),'{agentName}');assert.equal(await template('agents','preferences',1),'{agentName}');assert.equal(await template('agents','preferences',2),'_{agentName}_');
 console.log('PASS: bold default signature migration and rollback');
}finally{await db.close();}})().catch(error=>{console.error(error);process.exitCode=1});`)
	command.Stdin = strings.NewReader(string(migration))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("signature migration failed: %v\n%s", err, output)
	} else {
		t.Log(string(output))
	}
}

func TestAgentSignatureUsesIndividualChoice(t *testing.T) {
	preferences := DefaultAgentPreferences()
	preferences.MessageSignatureTemplate = "*{agentName}*:"
	if actual := preferences.SignMessage("Olá", "Ana"); actual != "*Ana*:\n\nOlá" {
		t.Fatalf("unexpected signature: %q", actual)
	}
	preferences.MessageSignatureEnabled = false
	if actual := preferences.SignMessage("Olá", "Bruno"); actual != "Olá" {
		t.Fatalf("disabled signature changed text: %q", actual)
	}
}

func TestAgentPreferencesIsolationPostgres(t *testing.T) {
	if os.Getenv("ZUBLY_TEST_POSTGRES") == "" {
		t.Skip("set ZUBLY_TEST_POSTGRES for temporary-table preference isolation tests")
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
	if _, err := testDB.Exec(`CREATE TEMP TABLE agents(id integer PRIMARY KEY,"companyId" integer,preferences jsonb NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(DefaultAgentPreferences())
	if _, err := testDB.Exec(`INSERT INTO agents VALUES(7,3,$1::jsonb),(8,3,$1::jsonb),(9,4,$1::jsonb)`, string(raw)); err != nil {
		t.Fatal(err)
	}
	preferences := DefaultAgentPreferences()
	preferences.ScrollAfterSend = false
	preferences.ScrollAfterSendMode = "except_quotes"
	preferences.MessageSignatureEnabled = false
	preferences.Notifications = AgentNotificationPreferences{}
	if err := UpdateAgentPreferences(7, 3, preferences); err != nil {
		t.Fatal(err)
	}
	actual, err := GetAgentPreferences(7, 3)
	if err != nil || actual != preferences {
		t.Fatalf("preferences did not persist: %+v (%v)", actual, err)
	}
	for _, identity := range [][2]int{{8, 3}, {9, 4}} {
		actual, err := GetAgentPreferences(identity[0], identity[1])
		if err != nil || actual != DefaultAgentPreferences() {
			t.Fatalf("another agent's preferences changed: %+v (%v)", actual, err)
		}
	}
	if _, err := GetAgentPreferences(7, 4); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("foreign company could read preferences: %v", err)
	}
	if err := UpdateAgentPreferences(7, 4, DefaultAgentPreferences()); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("foreign company could update preferences: %v", err)
	}
	for _, state := range []AgentOnboarding{{Step: 2}, {Completed: true, Step: 3}, {Step: 1}} {
		if err := SaveAgentOnboarding(7, 3, state); err != nil {
			t.Fatal(err)
		}
	}
	if err := UpdateAgentPreferences(7, 3, DefaultAgentPreferences()); err != nil {
		t.Fatal(err)
	}
	after, err := GetAgentPreferences(7, 3)
	if err != nil || !after.Onboarding.Completed || after.Onboarding.Step != 3 {
		t.Fatalf("guide completion reset by stale progress or preferences: %+v (%v)", after.Onboarding, err)
	}
	forged := DefaultAgentPreferences()
	forged.Onboarding = AgentOnboarding{Completed: true, Step: 30}
	if err := UpdateAgentPreferences(8, 3, forged); err != nil {
		t.Fatal(err)
	}
	other, err := GetAgentPreferences(8, 3)
	if err != nil || other.Onboarding.Completed || other.Onboarding.Step != 0 {
		t.Fatal("preferences input changed onboarding state")
	}
	if err := SaveAgentOnboarding(7, 4, AgentOnboarding{Completed: true}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("foreign company could mark completion: %v", err)
	}
}
