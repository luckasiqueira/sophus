package repo

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestDuplicateMessagesMigrationKeepsOneRowWithMedia(t *testing.T) {
	if os.Getenv("ZUBLY_TEST_PGLITE") == "" {
		t.Skip("set ZUBLY_TEST_PGLITE for migration tests")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js required")
	}
	migration, err := migrations.ReadFile("migrations/98__remove_duplicate_messages.sql")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "-e", `
const assert=require('node:assert/strict'),{PGlite}=require(process.env.ZUBLY_TEST_PGLITE),migration=require('node:fs').readFileSync(0,'utf8');
(async()=>{const db=new PGlite();try {
 await db.exec(`+"`"+`CREATE TABLE messages(id serial PRIMARY KEY,"messageId" varchar,"conversationId" integer,"mediaType" varchar,"mediaPath" varchar,"isDeleted" boolean);
 CREATE TABLE message_reactions(id bigserial PRIMARY KEY,"messageId" integer NOT NULL REFERENCES messages(id) ON DELETE CASCADE,sender varchar NOT NULL,emoji varchar NOT NULL,"isFromMe" boolean NOT NULL DEFAULT false,"eventId" varchar NOT NULL DEFAULT '',"updatedAt" timestamptz NOT NULL DEFAULT now(),UNIQUE("messageId",sender));
 INSERT INTO messages("messageId","conversationId","mediaType","mediaPath","isDeleted") VALUES
  ('IMG1',1,'image','',false),      -- 1: duplicate without the file
  ('IMG1',1,'image','a.jpg',false), -- 2: keeps the file -> survivor
  ('IMG1',1,'image','b.jpg',false), -- 3: second copy with a file
  ('TXT1',1,'text','',false),       -- 4: unique
  ('IMG1',2,'image','c.jpg',false), -- 5: same id in another conversation stays
  ('',1,'text','',false),('',1,'text','',false); -- no id: never touched
 INSERT INTO message_reactions("messageId",sender,emoji) VALUES(1,'contact','👍'),(3,'self','❤');`+"`"+`);
 await db.exec('BEGIN');await db.exec(migration.split('-- +goose Down')[0]);await db.exec('COMMIT');
 const {rows}=await db.query('SELECT id FROM messages ORDER BY id');
 assert.deepEqual(rows.map(r=>r.id),[2,4,5,6,7]);
 const reactions=await db.query('SELECT "messageId",sender FROM message_reactions ORDER BY sender');
 assert.deepEqual(reactions.rows.map(r=>[r.messageId,r.sender]),[[2,'contact'],[2,'self']]);
 await db.exec(migration.split('-- +goose Down')[1]);
 console.log('PASS: duplicates removed, media row kept, reactions moved');
}finally{await db.close();}})().catch(error=>{console.error(error);process.exitCode=1});`)
	command.Stdin = strings.NewReader(string(migration))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("duplicate cleanup migration failed: %v\n%s", err, output)
	} else {
		t.Log(string(output))
	}
}
