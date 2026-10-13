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

// PGlite executes the production query in an isolated PostgreSQL engine. Point
// ZUBLY_TEST_PGLITE at its Node module to run without touching application data.
func TestContactUpdatePostgresParameterTypesAndScope(t *testing.T) {
	if os.Getenv("ZUBLY_TEST_PGLITE") == "" {
		t.Skip("set ZUBLY_TEST_PGLITE to run the isolated PostgreSQL regression check")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(map[string]string{"sql": updateCompanyContactSQL})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "-e", `
const assert = require('node:assert/strict');
const { PGlite } = require(process.env.ZUBLY_TEST_PGLITE);
const { sql } = JSON.parse(require('node:fs').readFileSync(0, 'utf8'));
(async () => {
 const db = new PGlite();
 await db.exec('CREATE TABLE contacts (id integer PRIMARY KEY, "companyId" integer, name varchar NOT NULL, number varchar NOT NULL, jid varchar, email varchar(320), company varchar(160), "isGroup" boolean); CREATE UNIQUE INDEX contact_phone ON contacts ("companyId", number);');
 await db.query('INSERT INTO contacts VALUES (1, 3, $1, $2, $3, $4, $5, false)', ['Antigo', '5511999999999', '5511999999999@s.whatsapp.net', '', '']);
 const updated = await db.query(sql, ['João', '5511888888888', 'joao@example.com', 'Empresa São José', 1, 3]);
 assert.equal(updated.affectedRows, 1, 'varchar assignment and text concatenation must prepare successfully');
 const get = async id => (await db.query('SELECT * FROM contacts WHERE id = $1', [id])).rows[0];
 let contact = await get(1);
 assert.equal(contact.name, 'João'); assert.equal(contact.number, '5511888888888'); assert.equal(contact.jid, '5511888888888@s.whatsapp.net'); assert.equal(contact.company, 'Empresa São José');
 const unrelated = await db.query(sql, ['Não alterar', '5511777777777', '', '', 1, 4]); assert.equal(unrelated.affectedRows, 0); assert.equal((await get(1)).name, 'João');
 await db.query('INSERT INTO contacts VALUES (2, 3, $1, $2, $3, $4, $5, false)', ['LID', '5511666666666', '123456@lid', '', '']);
 await db.query(sql, ['Com LID', '5511555555555', '', '', 2, 3]); assert.equal((await get(2)).jid, '123456@lid', 'preserve LID identities');
 await assert.rejects(() => db.query(sql, ['Conflito', '5511555555555', '', '', 1, 3]), error => error.code === '23505');
 assert.equal((await get(1)).name, 'João', 'failed update must preserve data');
 await db.close();
})().catch(error => { console.error(error); process.exitCode = 1; });
`)
	cmd.Stdin = strings.NewReader(string(input))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("PostgreSQL regression check: %v\n%s", err, output)
	}
}
