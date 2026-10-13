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

func TestNormalizeImportedContact(t *testing.T) {
	contact, ok := NormalizeImportedContact("  Maria  ", " ACME ", "5511999999999@s.whatsapp.net")
	if !ok {
		t.Fatal("valid WhatsApp contact rejected")
	}
	if contact.Name != "Maria" || contact.Company != "ACME" || contact.Number != "5511999999999" {
		t.Fatalf("unexpected normalized contact: %#v", contact)
	}
}

func TestNormalizeImportedContactRejectsGroupsAndInvalidNumbers(t *testing.T) {
	for _, jid := range []string{"5511999999999@g.us", "abc@s.whatsapp.net", ""} {
		if _, ok := NormalizeImportedContact("Contact", "", jid); ok {
			t.Fatalf("invalid JID accepted: %q", jid)
		}
	}
}

func TestContactImportQueriesWithVarcharColumnsPostgres(t *testing.T) {
	if os.Getenv("ZUBLY_TEST_PGLITE") == "" {
		t.Skip("set ZUBLY_TEST_PGLITE for isolated PostgreSQL contact-import regression")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(map[string]string{"insert": contactImportInsertSQL, "update": contactImportUpdateSQL})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "-e", `
const assert=require('node:assert/strict'),{PGlite}=require(process.env.ZUBLY_TEST_PGLITE);
const sql=JSON.parse(require('node:fs').readFileSync(0,'utf8'));
(async()=>{const db=new PGlite();await db.exec('CREATE TABLE contacts (id serial PRIMARY KEY,name varchar(100),number varchar(100),email varchar(100),company varchar(100),"companyId" integer,jid text,lid text,"isGroup" boolean,"isBlocked" boolean,"avatarRefreshRequestedAt" timestamptz);');
 const contact=['Maria','5511999999999','ACME',8,'5511999999999@s.whatsapp.net'];
 await db.query(sql.insert,contact);await db.query(sql.insert,contact);
 assert.equal((await db.query('SELECT COUNT(*)::integer AS count FROM contacts')).rows[0].count,1,'repeat import duplicated contact');
 await db.query(sql.insert,['Contato salvo automaticamente','5511888888888','',8,'']);
 const id=(await db.query('SELECT id FROM contacts WHERE number=$1',['5511888888888'])).rows[0].id;
 await db.query(sql.update,['João','Empresa','5511888888888@s.whatsapp.net',id]);
 let current=(await db.query('SELECT name,company,jid,"avatarRefreshRequestedAt" FROM contacts WHERE id=$1',[id])).rows[0];
 assert.equal(current.name,'João');assert.equal(current.company,'Empresa');assert.equal(current.jid,'5511888888888@s.whatsapp.net');assert.ok(current.avatarRefreshRequestedAt);
 await db.query(sql.update,['Outro nome','Outra empresa','outra-identidade',id]);
 current=(await db.query('SELECT name,company,jid FROM contacts WHERE id=$1',[id])).rows[0];
 assert.equal(current.name,'João');assert.equal(current.company,'Empresa');assert.equal(current.jid,'5511888888888@s.whatsapp.net','repeat import overwrote curated contact');
 await db.query(sql.insert,['Maria','5511999999999','ACME',9,'5511999999999@s.whatsapp.net']);
 assert.equal((await db.query('SELECT COUNT(*)::integer AS count FROM contacts')).rows[0].count,3,'same number in another company was suppressed');
 await db.close();console.log('PASS: explicit parameter types, insert/update, duplicate protection, curated fields and company isolation');
})().catch(error=>{console.error(error);process.exitCode=1});`)
	command.Stdin = strings.NewReader(string(input))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("contact import regression: %v\n%s", err, output)
	}
	t.Log(string(output))
}
