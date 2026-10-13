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

func TestEvolutionPicturePayload(t *testing.T) {
	var event EventPictureEVO
	if err := json.Unmarshal([]byte(`{"event":"Picture","data":{"JID":"5511999999999:2@s.whatsapp.net","Author":"5511888888888@s.whatsapp.net","Remove":true,"PictureID":"123"}}`), &event); err != nil {
		t.Fatal(err)
	}
	number, jid, _ := canonicalWhatsAppIdentity(event.Data.JID)
	if !event.Data.Remove || number != "5511999999999" || jid != "5511999999999@s.whatsapp.net" {
		t.Fatalf("picture event = %+v", event)
	}
}

func TestContactPicturePostgresRefreshAndRemoval(t *testing.T) {
	if os.Getenv("ZUBLY_TEST_PGLITE") == "" {
		t.Skip("set ZUBLY_TEST_PGLITE to run the isolated PostgreSQL regression check")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(map[string]string{"change": contactPictureChangeSQL, "complete": completeContactAvatarSQL, "invalidate": invalidateContactAvatarSQL})
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
 const db = new PGlite();
 await db.exec('CREATE TABLE contacts (id int PRIMARY KEY, "companyId" int, number text, jid text, lid text, "isGroup" boolean, "avatarUrl" text, "avatarUpdatedAt" timestamptz, "avatarRefreshRequestedAt" timestamptz);');
 await db.query('INSERT INTO contacts VALUES (1, 1, $1, $2, $3, false, $4, $5, NULL), (2, 2, $1, $2, $3, false, $4, $5, NULL)', ['5511999999999', '5511999999999@s.whatsapp.net', '123456789@lid', 'https://pps.whatsapp.net/old.jpg', '2020-01-01']);
 const read = async id => (await db.query('SELECT "avatarUrl" AS url, "avatarRefreshRequestedAt"::text AS requested FROM contacts WHERE id = $1', [id])).rows[0];
 const ids = await db.query(sql.change, [1, '', '', '123456789@lid', false]); assert.deepEqual(ids.rows.map(row => row.id), [1]);
 let contact = await read(1); assert.equal(contact.url, ''); assert.ok(contact.requested); assert.equal((await read(2)).url, 'https://pps.whatsapp.net/old.jpg');
 let saved = await db.query(sql.complete, ['https://pps.whatsapp.net/new.jpg', 1, contact.requested]); assert.equal(saved.affectedRows, 1); assert.equal((await read(1)).url, 'https://pps.whatsapp.net/new.jpg');
 await db.query(sql.change, [1, '5511999999999', '5511999999999@s.whatsapp.net', '', false]);
 contact = await read(1); const inFlight = contact.requested;
 await db.query(sql.change, [1, '5511999999999', '5511999999999@s.whatsapp.net', '', true]);
 saved = await db.query(sql.complete, ['https://pps.whatsapp.net/stale.jpg', 1, inFlight]); assert.equal(saved.affectedRows, 0, 'stale worker must not restore removed photo');
 assert.equal((await read(1)).url, ''); assert.equal((await read(1)).requested, null);
 await db.query('UPDATE contacts SET "avatarUrl" = $1, "avatarUpdatedAt" = $2, "avatarRefreshRequestedAt" = NULL WHERE id = 1', ['https://pps.whatsapp.net/expired.jpg', '2020-01-01']);
 assert.equal((await db.query(sql.invalidate, [1, 1])).affectedRows, 1);
 contact = await read(1); assert.equal(contact.url, ''); assert.ok(contact.requested);
 assert.equal((await db.query(sql.invalidate, [1, 1])).affectedRows, 0, 'repeated broken-image reports must not reset pending refresh');
 assert.equal((await db.query(sql.invalidate, [2, 1])).affectedRows, 0, 'company isolation');
 await db.query(sql.complete, ['', 1, contact.requested]); assert.equal((await read(1)).url, '', 'confirmed no-photo clears URL');
 await db.close();
})().catch(error => { console.error(error); process.exitCode = 1; });
`)
	cmd.Stdin = strings.NewReader(string(input))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("PostgreSQL picture check: %v\n%s", err, output)
	}
}
